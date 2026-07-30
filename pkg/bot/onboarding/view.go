package onboarding

import (
	"fmt"

	"github.com/go-telegram/bot/models"
)

func RenderLearningLanguagePrompt() (string, *models.InlineKeyboardMarkup) {
	text := "Welcome!\n\nI will help you remember words with short review cards and repeating reminders at smart intervals.\n\nChoose the language you are learning."
	return text, renderLanguageKeyboard(ActionSelectLearning, "", "")
}

func RenderResetWarningPrompt() (string, *models.InlineKeyboardMarkup) {
	text := "Re-initialization will replace your vocabulary and training progress data (review sessions and game sessions).\n" +
		"Your current data will remain unchanged until you finish setup and tap Initialize.\n" +
		"To continue, type this exact phrase:\n" +
		ResetPhrase
	return text, &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{
			{
				{Text: "Keep my data", CallbackData: BuildCancelResetCallback()},
			},
		},
	}
}

func RenderKnownLanguagePrompt(learningCode string) (string, *models.InlineKeyboardMarkup) {
	text := fmt.Sprintf(
		"Learning language: %s\n\nChoose the language you already know.",
		LabelForLanguage(learningCode),
	)
	return text, renderLanguageKeyboard(ActionSelectKnown, learningCode, BuildBackLearningCallback())
}

func RenderConfirmationPrompt(learningCode, knownCode string, eligibleCount int) (string, *models.InlineKeyboardMarkup) {
	text := fmt.Sprintf(
		"Initialize vocabulary\n\nLearning language: %s\nKnown language: %s\nEligible cards: %d",
		LabelForLanguage(learningCode),
		LabelForLanguage(knownCode),
		eligibleCount,
	)
	if eligibleCount == 0 {
		text += "\n\nSorry, we don't have a training set for the chosen languages. Please try to choose another language combination."
	}

	rows := [][]models.InlineKeyboardButton{}
	if eligibleCount > 0 {
		rows = append(rows, []models.InlineKeyboardButton{{Text: "Initialize", CallbackData: BuildConfirmCallback()}})
	}
	rows = append(rows, []models.InlineKeyboardButton{{Text: "Back", CallbackData: BuildBackKnownCallback()}})
	return text, &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func renderLanguageKeyboard(action CallbackActionKind, excludeCode, backCallback string) *models.InlineKeyboardMarkup {
	rows := make([][]models.InlineKeyboardButton, 0, (len(LanguageOptions)/2)+2)
	currentRow := make([]models.InlineKeyboardButton, 0, 2)

	for _, option := range LanguageOptions {
		if option.Code == excludeCode {
			continue
		}

		callback := ""
		switch action {
		case ActionSelectLearning:
			callback = BuildLearningCallback(option.Code)
		case ActionSelectKnown:
			callback = BuildKnownCallback(option.Code)
		default:
			continue
		}

		currentRow = append(currentRow, models.InlineKeyboardButton{Text: option.Label, CallbackData: callback})
		if len(currentRow) == 2 {
			rows = append(rows, currentRow)
			currentRow = make([]models.InlineKeyboardButton, 0, 2)
		}
	}

	if len(currentRow) > 0 {
		rows = append(rows, currentRow)
	}
	if backCallback != "" {
		rows = append(rows, []models.InlineKeyboardButton{{Text: "Back", CallbackData: backCallback}})
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}
