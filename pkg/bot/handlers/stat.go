package handlers

import (
	"context"
	"errors"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/smith3v/tg-word-reminder/pkg/bot/stats"
	"github.com/smith3v/tg-word-reminder/pkg/logger"
)

const statCollectionTimeout = 30 * time.Second

type StatHandler struct {
	starter stats.Starter
	timeout time.Duration
}

func NewStatHandler(starter stats.Starter) *StatHandler {
	return &StatHandler{
		starter: starter,
		timeout: statCollectionTimeout,
	}
}

func (h *StatHandler) Handle(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update == nil || update.Message == nil || update.Message.From == nil || update.Message.Chat.ID == 0 {
		logger.Error("invalid update in StatHandler")
		return
	}
	if tryHandleFeedbackCapture(ctx, b, update) {
		return
	}
	if update.Message.Chat.Type != models.ChatTypePrivate {
		h.send(ctx, b, update.Message.Chat.ID, "The /stat command works only in private chat.")
		return
	}

	baseContext := context.WithoutCancel(ctx)
	go h.collect(baseContext, b, update.Message.Chat.ID, update.Message.From.ID)
}

func (h *StatHandler) collect(ctx context.Context, b *bot.Bot, chatID, userID int64) {
	startedAt := time.Now()
	if h == nil || h.starter == nil {
		h.handleFailure(
			ctx,
			b,
			chatID,
			userID,
			startedAt,
			errors.New("statistics handler is not configured"),
		)
		return
	}
	timeout := h.timeout
	if timeout <= 0 {
		timeout = statCollectionTimeout
	}
	collectionContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	job, err := h.starter.Start(collectionContext, userID)
	if errors.Is(err, stats.ErrBusy) {
		h.send(ctx, b, chatID, "Statistics are busy right now. Please try /stat again in a moment.")
		return
	}
	if err != nil {
		h.handleFailure(ctx, b, chatID, userID, startedAt, err)
		return
	}

	h.send(
		collectionContext,
		b,
		chatID,
		"Collecting your stats… I’ll send a new message when it’s ready.",
	)

	report, err := job.Collect(collectionContext)
	if err != nil {
		h.handleFailure(ctx, b, chatID, userID, startedAt, err)
		return
	}
	h.send(ctx, b, chatID, stats.Format(report))
}

func (h *StatHandler) handleFailure(
	ctx context.Context,
	b *bot.Bot,
	chatID int64,
	userID int64,
	startedAt time.Time,
	err error,
) {
	logger.Error(
		"failed to collect user statistics",
		"user_id", userID,
		"duration", time.Since(startedAt),
		"error", err,
	)
	h.send(ctx, b, chatID, "I couldn’t collect your stats right now. Please try again later.")
}

func (h *StatHandler) send(ctx context.Context, b *bot.Bot, chatID int64, text string) {
	if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID: chatID,
		Text:   text,
	}); err != nil {
		logger.Error("failed to send statistics message", "chat_id", chatID, "error", err)
	}
}
