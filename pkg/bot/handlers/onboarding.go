package handlers

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/smith3v/tg-word-reminder/pkg/bot/game"
	"github.com/smith3v/tg-word-reminder/pkg/bot/onboarding"
	"github.com/smith3v/tg-word-reminder/pkg/bot/training"
	"github.com/smith3v/tg-word-reminder/pkg/logger"
)

func HandleOnboardingCallback(ctx context.Context, b *bot.Bot, update *models.Update) {
	if update == nil || update.CallbackQuery == nil {
		logger.Error("invalid update in HandleOnboardingCallback")
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
			logger.Error("failed to answer onboarding callback query", "error", err)
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

	action, err := onboarding.ParseCallbackData(update.CallbackQuery.Data)
	if err != nil {
		answerCallback("Unknown action")
		return
	}

	userID := update.CallbackQuery.From.ID
	state, err := onboarding.GetState(userID)
	if err != nil {
		logger.Error("failed to load onboarding state", "user_id", userID, "error", err)
		answerCallback("Failed")
		return
	}
	if action.Kind == onboarding.ActionCancelReset {
		if state == nil || !state.AwaitingResetPhrase {
			answerCallback("Nothing to cancel")
			return
		}
		if err := onboarding.ClearState(userID); err != nil {
			logger.Error("failed to clear onboarding reset state", "user_id", userID, "error", err)
			answerCallback("Failed")
			return
		}
		if err := editOnboardingMessage(ctx, b, msg.Chat.ID, msg.ID, "Reset canceled. Your data is unchanged.", &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{}}); err != nil {
			logger.Error("failed to edit reset cancellation message", "user_id", userID, "error", err)
			answerCallback("Failed")
			return
		}
		answerCallback("")
		return
	}
	hasInitData, err := onboarding.HasInitVocabularyData()
	if err != nil {
		logger.Error("failed to check init vocabulary availability", "user_id", userID, "error", err)
		answerCallback("Failed")
		return
	}
	if !hasInitData {
		if state != nil && !state.AwaitingResetPhrase {
			if clearErr := onboarding.ClearState(userID); clearErr != nil {
				logger.Error("failed to clear onboarding state while init vocabulary is unavailable", "user_id", userID, "error", clearErr)
			}
		}
		if err := editOnboardingMessage(ctx, b, msg.Chat.ID, msg.ID, "Built-in onboarding vocabulary is unavailable right now.\nYou can still upload your own CSV file with cards to get started.", &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{}}); err != nil {
			logger.Error("failed to edit onboarding unavailable message", "user_id", userID, "error", err)
			answerCallback("Failed")
			return
		}
		answerCallback("")
		return
	}
	if state == nil || state.AwaitingResetPhrase {
		answerCallback("Send /start")
		return
	}

	switch action.Kind {
	case onboarding.ActionSelectLearning:
		if _, err := onboarding.SetLearningLanguage(userID, action.Code); err != nil {
			logger.Error("failed to set onboarding learning language", "user_id", userID, "error", err)
			answerCallback("Failed")
			return
		}
		text, keyboard := onboarding.RenderKnownLanguagePrompt(action.Code)
		if err := editOnboardingMessage(ctx, b, msg.Chat.ID, msg.ID, text, keyboard); err != nil {
			logger.Error("failed to edit known language prompt", "user_id", userID, "error", err)
			answerCallback("Failed")
			return
		}
		answerCallback("")
	case onboarding.ActionSelectKnown:
		nextState, err := onboarding.SetKnownLanguage(userID, action.Code)
		if err != nil {
			answerCallback("Choose a different language")
			return
		}
		count, err := onboarding.CountEligiblePairs(nextState.LearningLang, nextState.KnownLang)
		if err != nil {
			logger.Error("failed to count onboarding pairs", "user_id", userID, "error", err)
			answerCallback("Failed")
			return
		}
		text, keyboard := onboarding.RenderConfirmationPrompt(nextState.LearningLang, nextState.KnownLang, count)
		if err := editOnboardingMessage(ctx, b, msg.Chat.ID, msg.ID, text, keyboard); err != nil {
			logger.Error("failed to edit onboarding confirmation", "user_id", userID, "error", err)
			answerCallback("Failed")
			return
		}
		answerCallback("")
	case onboarding.ActionBackLearning:
		if _, err := onboarding.BackToLearning(userID); err != nil {
			logger.Error("failed to reset onboarding state to learning", "user_id", userID, "error", err)
			answerCallback("Failed")
			return
		}
		text, keyboard := onboarding.RenderLearningLanguagePrompt()
		if err := editOnboardingMessage(ctx, b, msg.Chat.ID, msg.ID, text, keyboard); err != nil {
			logger.Error("failed to edit onboarding learning prompt", "user_id", userID, "error", err)
			answerCallback("Failed")
			return
		}
		answerCallback("")
	case onboarding.ActionBackKnown:
		nextState, err := onboarding.BackToKnown(userID)
		if err != nil {
			answerCallback("Send /start")
			return
		}
		text, keyboard := onboarding.RenderKnownLanguagePrompt(nextState.LearningLang)
		if err := editOnboardingMessage(ctx, b, msg.Chat.ID, msg.ID, text, keyboard); err != nil {
			logger.Error("failed to edit onboarding known prompt", "user_id", userID, "error", err)
			answerCallback("Failed")
			return
		}
		answerCallback("")
	case onboarding.ActionConfirm:
		if state.Step != onboarding.StepConfirmImport || state.LearningLang == "" || state.KnownLang == "" {
			answerCallback("Send /start")
			return
		}
		inserted, err := onboarding.ProvisionUserVocabularyAndDefaults(userID, state.LearningLang, state.KnownLang)
		if err != nil {
			if errors.Is(err, onboarding.ErrNoEligiblePairs) {
				answerCallback("No cards for this language combination")
				if nextState, backErr := onboarding.BackToKnown(userID); backErr == nil {
					text, keyboard := onboarding.RenderKnownLanguagePrompt(nextState.LearningLang)
					_ = editOnboardingMessage(ctx, b, msg.Chat.ID, msg.ID, text, keyboard)
				}
				return
			}
			logger.Error("failed to provision onboarding vocabulary", "user_id", userID, "error", err)
			answerCallback("Failed")
			return
		}

		if state.ResetPending {
			training.DefaultManager.EndAllForUser(userID)
			game.DefaultManager.EndAllForUser(userID, "reset")
		}

		if err := editOnboardingMessage(ctx, b, msg.Chat.ID, msg.ID, "Onboarding completed ✅", &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{}}); err != nil {
			logger.Error("failed to edit onboarding completion", "user_id", userID, "error", err)
		}

		completion := fmt.Sprintf(
			"Imported %d cards: %s -> %s\n\nDuring /review, rate how easy it was to recall each word:\n- Again: could not recall it\n- Hard: recalled with effort\n- Good: recalled correctly\n- Easy: instant recall\n\nI will bring harder cards back sooner and easy ones later.\n\nDefault settings enabled:\n- 3 sessions: morning, afternoon, evening\n- 5 words per session\n\nUse /review to start training, /game for quiz mode, or /settings to adjust preferences.",
			inserted,
			onboarding.LabelForLanguage(state.LearningLang),
			onboarding.LabelForLanguage(state.KnownLang),
		)
		if _, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: msg.Chat.ID, Text: completion}); err != nil {
			logger.Error("failed to send onboarding completion message", "user_id", userID, "error", err)
		}
		answerCallback("")
	default:
		answerCallback("Unknown action")
	}
}

func sendOnboardingLearningPrompt(ctx context.Context, b *bot.Bot, chatID, userID int64) error {
	if _, err := onboarding.Begin(userID); err != nil {
		return err
	}
	text, keyboard := onboarding.RenderLearningLanguagePrompt()
	_, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        text,
		ReplyMarkup: keyboard,
	})
	return err
}

func tryHandleOnboardingResetPhrase(ctx context.Context, b *bot.Bot, update *models.Update) bool {
	if update == nil || update.Message == nil || update.Message.From == nil || update.Message.Chat.ID == 0 {
		return false
	}
	if update.Message.Text == "" {
		return false
	}

	state, err := onboarding.GetState(update.Message.From.ID)
	if err != nil {
		logger.Error("failed to load onboarding state for reset phrase", "user_id", update.Message.From.ID, "error", err)
		return false
	}
	if state == nil || !state.AwaitingResetPhrase {
		return false
	}

	if update.Message.Text != onboarding.ResetPhrase {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "That phrase does not match. To continue, type the exact phrase:\n" + onboarding.ResetPhrase,
		})
		return true
	}
	hasInitData, err := onboarding.HasInitVocabularyData()
	if err != nil {
		logger.Error("failed to check init vocabulary availability for reset", "user_id", update.Message.From.ID, "error", err)
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Failed to verify onboarding data availability. Please try again later.",
		})
		return true
	}
	if !hasInitData {
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text: "Built-in onboarding vocabulary is unavailable right now, so reset cannot continue.\n" +
				"Your existing data is unchanged.",
		})
		return true
	}

	if _, err := onboarding.BeginReset(update.Message.From.ID); err != nil {
		logger.Error("failed to start reset onboarding", "user_id", update.Message.From.ID, "error", err)
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Failed to start onboarding. Your existing data is unchanged.",
		})
		return true
	}

	text, keyboard := onboarding.RenderLearningLanguagePrompt()
	if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      update.Message.Chat.ID,
		Text:        text,
		ReplyMarkup: keyboard,
	}); err != nil {
		logger.Error("failed to restart onboarding after reset", "user_id", update.Message.From.ID, "error", err)
		b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Failed to show onboarding. Your existing data is unchanged; send /start to retry.",
		})
	}
	return true
}

func editOnboardingMessage(ctx context.Context, b *bot.Bot, chatID int64, messageID int, text string, keyboard *models.InlineKeyboardMarkup) error {
	_, err := b.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID:      chatID,
		MessageID:   messageID,
		Text:        text,
		ReplyMarkup: keyboard,
	})
	return err
}
