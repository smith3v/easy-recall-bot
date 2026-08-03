package handlers

import (
	"context"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/smith3v/tg-word-reminder/pkg/bot/importexport"
	"github.com/smith3v/tg-word-reminder/pkg/logger"
)

func DefaultHandler(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update == nil || update.Message == nil {
		logger.Error("received invalid update in defaultHandler")
		return
	}

	// Check if Chat is zero value
	if update.Message.Chat.ID == 0 {
		logger.Error("chat ID is zero in defaultHandler")
		return
	}

	if tryHandleFeedbackCapture(ctx, b, update) {
		return
	}

	// Check if the message contains a document (file)
	if update.Message.Document == nil {
		if update.Message.Text != "" {
			if handleGameTextAttempt(ctx, b, update) {
				return
			}
			if tryHandleOnboardingResetPhrase(ctx, b, update) {
				return
			}
		}
		_, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text: "This bot helps you remember words with repeated review cards and reminders at smart intervals\\.\n\n" +
				"Commands:\n" +
				"\\* /start: \\(re\\-\\)initialize your account\\.\n\n" +
				"\\* /review: start a review session\\.\n" +
				"\\* /game: start a quiz session\\.\n" +
				"\\* /stat: show your personal learning statistics\\.\n" +
				"\\* /getpair: get a random card\\.\n\n" +
				"\\* /settings: configure bot settings\\.\n" +
				"\\* /feedback: send feedback to the bot admins\\.\n\n" +
				"\\* /clear: remove all uploaded words\\.\n" +
				"\\* /export: download your current vocabulary as CSV\\, edit it\\, then send it back here to import updates\\.\n" +
				"\\* CSV upload: attach a CSV file here to import the cards\\.",
			ParseMode: models.ParseModeMarkdown,
		})
		if err != nil {
			logger.Error("failed to send message in defaultHandler", "error", err)
		}
		return
	}

	importexport.HandleDocumentImport(ctx, b, update)
}
