// cmd/yourbot/main.go
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/go-telegram/bot"
	"github.com/smith3v/tg-word-reminder/pkg/bot/feedback"
	"github.com/smith3v/tg-word-reminder/pkg/bot/game"
	"github.com/smith3v/tg-word-reminder/pkg/bot/handlers"
	"github.com/smith3v/tg-word-reminder/pkg/bot/onboarding"
	"github.com/smith3v/tg-word-reminder/pkg/bot/reminders"
	"github.com/smith3v/tg-word-reminder/pkg/bot/stats"
	"github.com/smith3v/tg-word-reminder/pkg/bot/training"
	"github.com/smith3v/tg-word-reminder/pkg/config"
	"github.com/smith3v/tg-word-reminder/pkg/db"
	"github.com/smith3v/tg-word-reminder/pkg/logger"
)

type botSender struct {
	b *bot.Bot
}

func (s botSender) SendMessage(ctx context.Context, chatID int64, text string) error {
	_, err := s.b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   text,
	})
	return err
}

func main() {
	if err := config.LoadConfig("config.json"); err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	if err := logger.Configure(logger.Options{
		Level: config.AppConfig.Logging.Level,
		File:  config.AppConfig.Logging.File,
	}); err != nil {
		logger.Error("failed to configure logger", "error", err)
	}

	if err := db.InitDB(config.AppConfig.Database); err != nil {
		logger.Error("failed to initialize database", "error", err)
		os.Exit(1)
	}
	if err := onboarding.RefreshInitVocabularyFromConfig(); err != nil {
		logger.Error("failed to refresh init vocabulary", "path", config.AppConfig.Onboarding.InitVocabularyPath, "error", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	tracker := handlers.NewActivityTracker()
	opts := []bot.Option{
		bot.WithDefaultHandler(handlers.DefaultHandler),
		bot.WithMiddlewares(handlers.ActivityMiddleware(tracker)),
	}
	b, err := bot.New(config.AppConfig.Telegram.Token, opts...)
	if err != nil {
		logger.Error("failed to create bot", "error", err)
		os.Exit(1)
	}

	b.RegisterHandler(bot.HandlerTypeMessageText, "/start", bot.MatchTypeExact, handlers.HandleStart)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/settings", bot.MatchTypeExact, handlers.HandleSettings)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/clear", bot.MatchTypeExact, handlers.HandleClear)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/getpair", bot.MatchTypeExact, handlers.HandleGetPair)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/export", bot.MatchTypeExact, handlers.HandleExport)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/review", bot.MatchTypeExact, handlers.HandleReview)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/game", bot.MatchTypeExact, handlers.HandleGameStart)
	statHandler := handlers.NewStatHandler(stats.NewPostgresStarter(db.DB))
	b.RegisterHandler(bot.HandlerTypeMessageText, "/stat", bot.MatchTypeExact, statHandler.Handle)
	b.RegisterHandler(bot.HandlerTypeMessageText, "/feedback", bot.MatchTypeExact, handlers.HandleFeedback)
	b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "s:", bot.MatchTypePrefix, handlers.HandleSettingsCallback)
	b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "g:r:", bot.MatchTypePrefix, handlers.HandleGameCallback)
	b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "t:grade:", bot.MatchTypePrefix, handlers.HandleReviewCallback)
	b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "t:overdue:", bot.MatchTypePrefix, handlers.HandleOverdueCallback)
	b.RegisterHandler(bot.HandlerTypeCallbackQueryData, "o:", bot.MatchTypePrefix, handlers.HandleOnboardingCallback)

	go reminders.StartPeriodicMessages(ctx, b)
	go game.StartGameSweeper(ctx, botSender{b: b})
	go training.StartTrainingSweeper(ctx)
	go db.StartSessionCleanup(ctx, 0)
	go feedback.DefaultManager.StartSweeper(ctx)
	go handlers.StartActivityFlusher(ctx, tracker)

	logger.Info("Starting bot...")
	b.Start(ctx)
}
