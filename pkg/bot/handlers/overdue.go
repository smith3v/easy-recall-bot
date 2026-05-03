package handlers

import (
	"context"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/smith3v/tg-word-reminder/pkg/bot/training"
	"github.com/smith3v/tg-word-reminder/pkg/db"
	"github.com/smith3v/tg-word-reminder/pkg/logger"
)

func HandleOverdueCallback(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update == nil || update.CallbackQuery == nil {
		logger.Error("invalid update in HandleOverdueCallback")
		return
	}

	callbackID := update.CallbackQuery.ID
	answerCallback := func(text string) {
		if callbackID == "" {
			return
		}
		if _, err := b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: callbackID,
			Text:            text,
		}); err != nil {
			logger.Error("failed to answer overdue callback query", "error", err)
		}
	}

	message := update.CallbackQuery.Message
	if message.Type != models.MaybeInaccessibleMessageTypeMessage || message.Message == nil {
		answerCallback("Message missing")
		return
	}
	msg := message.Message
	if msg.Chat.ID == 0 {
		answerCallback("Message missing")
		return
	}

	token, action := parseOverdueCallback(update.CallbackQuery.Data)
	if token == "" || action == "" {
		answerCallback("Not active")
		return
	}
	if !training.DefaultOverdue.Validate(msg.Chat.ID, update.CallbackQuery.From.ID, token, msg.ID) {
		answerCallback("Not active")
		return
	}

	now := time.Now().UTC()
	responseText := "Got it."
	switch action {
	case "catch":
		if err := startCatchUp(ctx, b, update.CallbackQuery.From.ID, now); err != nil {
			logger.Error("failed to start catch-up", "user_id", update.CallbackQuery.From.ID, "error", err)
			answerCallback("Failed to start")
			return
		}
		responseText = "Let's catch up right away."
	case "snooze1d":
		if err := snoozeReminders(update.CallbackQuery.From.ID, now.Add(24*time.Hour)); err != nil {
			logger.Error("failed to snooze overdue", "user_id", update.CallbackQuery.From.ID, "error", err)
			answerCallback("Failed to snooze")
			return
		}
		training.DefaultManager.End(msg.Chat.ID, update.CallbackQuery.From.ID)
		responseText = "Snoozed reminders for 1 day."
	case "snooze1w":
		if err := snoozeReminders(update.CallbackQuery.From.ID, now.Add(7*24*time.Hour)); err != nil {
			logger.Error("failed to snooze overdue", "user_id", update.CallbackQuery.From.ID, "error", err)
			answerCallback("Failed to snooze")
			return
		}
		training.DefaultManager.End(msg.Chat.ID, update.CallbackQuery.From.ID)
		responseText = "Snoozed reminders for 1 week."
	default:
		answerCallback("Not active")
		return
	}

	if _, err := b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:    msg.Chat.ID,
		MessageID: msg.ID,
		Text:      responseText,
		ReplyMarkup: &models.InlineKeyboardMarkup{
			InlineKeyboard: [][]models.InlineKeyboardButton{},
		},
	}); err != nil {
		logger.Error("failed to edit overdue prompt", "user_id", update.CallbackQuery.From.ID, "error", err)
	}

	markTrainingEngaged(update.CallbackQuery.From.ID, now)
	answerCallback("")
}

func parseOverdueCallback(data string) (string, string) {
	if !strings.HasPrefix(data, training.OverdueCallbackPrefix) {
		return "", ""
	}
	parts := strings.Split(data, ":")
	if len(parts) != 4 || parts[0] != "t" || parts[1] != "overdue" {
		return "", ""
	}
	token := parts[2]
	action := parts[3]
	switch action {
	case "catch", "snooze1d", "snooze1w":
		return token, action
	default:
		return "", ""
	}
}

func snoozeReminders(userID int64, snoozedUntil time.Time) error {
	result := db.DB.Model(&db.UserSettings{}).
		Where("user_id = ?", userID).
		Update("reminder_snoozed_until", snoozedUntil)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}

	settings := db.UserSettings{
		UserID:               userID,
		ReminderSnoozedUntil: &snoozedUntil,
	}
	return db.DB.Create(&settings).Error
}

func startCatchUp(ctx context.Context, b *bot.Bot, userID int64, now time.Time) error {
	var settings db.UserSettings
	if err := db.DB.Where("user_id = ?", userID).First(&settings).Error; err != nil {
		return err
	}

	size := settings.PairsToSend
	if size <= 0 {
		return nil
	}
	pairs, err := training.SelectSessionPairs(userID, size, now)
	if err != nil {
		return err
	}
	if len(pairs) == 0 {
		_, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: userID,
			Text:   "Nothing to review right now.",
		})
		return err
	}

	session := training.DefaultManager.StartOrRestart(userID, userID, pairs)
	card := session.CurrentPair()
	if card == nil {
		return nil
	}
	prompt := training.BuildPrompt(*card)
	msg, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      userID,
		Text:        prompt,
		ParseMode:   models.ParseModeMarkdown,
		ReplyMarkup: training.BuildKeyboard(session.CurrentToken()),
	})
	if err != nil {
		return err
	}

	training.DefaultManager.SetCurrentMessageID(session, msg.ID)
	training.DefaultManager.SetCurrentPromptText(session, prompt)
	training.DefaultManager.Touch(userID, userID)
	return nil
}
