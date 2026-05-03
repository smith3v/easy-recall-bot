package reminders

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/smith3v/tg-word-reminder/pkg/bot/training"
	"github.com/smith3v/tg-word-reminder/pkg/db"
	"github.com/smith3v/tg-word-reminder/pkg/logger"
)

const (
	slotMorningHour    = 8
	slotAfternoonHour  = 13
	slotEveningHour    = 20
	activeSessionGrace = 15 * time.Minute
	pauseAfterMisses   = 9
)

func StartPeriodicMessages(ctx context.Context, b *bot.Bot) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			processReminders(ctx, b, now.UTC())
		}
	}
}

func processReminders(ctx context.Context, b *bot.Bot, now time.Time) {
	training.DefaultOverdue.SweepExpired(now)
	var users []db.UserSettings
	if err := db.DB.Find(&users).Error; err != nil {
		logger.Error("failed to fetch users for reminders", "error", err)
		return
	}

	for _, user := range users {
		handleUserReminder(ctx, b, user, now)
	}
}

func handleUserReminder(ctx context.Context, b *bot.Bot, user db.UserSettings, now time.Time) {
	if user.TrainingPaused {
		return
	}
	if user.ReminderSnoozedUntil != nil && now.Before(*user.ReminderSnoozedUntil) {
		return
	}
	_, ok := latestDueSlot(now, user)
	if !ok {
		return
	}

	sessionRow, err := training.LoadTrainingSession(user.UserID, user.UserID, now)
	if err != nil {
		logger.Error("failed to load active session", "user_id", user.UserID, "error", err)
	}
	if sessionRow != nil && now.Sub(sessionRow.LastActivityAt) <= activeSessionGrace {
		return
	}

	missed := computeMissedCount(user)
	if missed >= pauseAfterMisses {
		if !user.TrainingPaused {
			user.TrainingPaused = true
			user.MissedTrainingSessions = missed
			if err := db.DB.Save(&user).Error; err != nil {
				logger.Error("failed to pause reminders", "user_id", user.UserID, "error", err)
				return
			}
			_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: user.UserID,
				Text:   "Paused reminders due to inactivity.",
			})
		}
		return
	}

	expireActiveSession(ctx, b, user, sessionRow)

	overdueCount, err := countOverdue(user.UserID, now)
	if err != nil {
		logger.Error("failed to count overdue cards", "user_id", user.UserID, "error", err)
		return
	}
	sessionSize := user.PairsToSend
	capacity := sessionSize * countEnabledSlots(user)
	if sessionSize > 0 && (overdueCount > sessionSize || (capacity > 0 && overdueCount > capacity)) {
		sent, err := sendOverdueSession(ctx, b, user, now)
		if err != nil {
			logger.Error("failed to send overdue session", "user_id", user.UserID, "error", err)
			return
		}
		if sent {
			user.LastTrainingSentAt = &now
			user.MissedTrainingSessions = missed
			if err := db.DB.Save(&user).Error; err != nil {
				logger.Error("failed to update reminder state", "user_id", user.UserID, "error", err)
			}
		}
		return
	}

	sent, err := sendTrainingSession(ctx, b, user, now)
	if err != nil {
		logger.Error("failed to send training session", "user_id", user.UserID, "error", err)
		return
	}
	if !sent {
		return
	}

	user.LastTrainingSentAt = &now
	user.MissedTrainingSessions = missed
	if err := db.DB.Save(&user).Error; err != nil {
		logger.Error("failed to update reminder state", "user_id", user.UserID, "error", err)
	}
}

func sendTrainingSession(ctx context.Context, b *bot.Bot, user db.UserSettings, now time.Time) (bool, error) {
	if user.PairsToSend <= 0 {
		return false, nil
	}
	pairs, err := training.SelectSessionPairs(user.UserID, user.PairsToSend, now)
	if err != nil {
		return false, err
	}
	if len(pairs) == 0 {
		return false, nil
	}

	session := training.DefaultManager.StartOrRestart(user.UserID, user.UserID, pairs)
	card := session.CurrentPair()
	if card == nil {
		return false, nil
	}

	prompt := training.BuildPrompt(*card)
	msg, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      user.UserID,
		Text:        prompt,
		ParseMode:   models.ParseModeMarkdown,
		ReplyMarkup: training.BuildKeyboard(session.CurrentToken()),
	})
	if err != nil {
		return false, err
	}

	training.DefaultManager.SetCurrentMessageID(session, msg.ID)
	training.DefaultManager.SetCurrentPromptText(session, prompt)
	return true, nil
}

func expireActiveSession(ctx context.Context, b *bot.Bot, user db.UserSettings, sessionRow *db.TrainingSession) {
	if sessionRow == nil {
		return
	}

	if sessionRow.CurrentMessageID != 0 {
		expiredText := buildExpiredSessionText(user.UserID, sessionRow)
		if _, err := b.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    user.UserID,
			MessageID: sessionRow.CurrentMessageID,
			Text:      expiredText,
			ParseMode: models.ParseModeMarkdown,
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: [][]models.InlineKeyboardButton{},
			},
		}); err != nil {
			logger.Error("failed to edit expired session message", "user_id", user.UserID, "error", err)
		}
	}

	training.DefaultManager.End(user.UserID, user.UserID)
}

func buildExpiredSessionText(userID int64, sessionRow *db.TrainingSession) string {
	expiredNotice := bot.EscapeMarkdown("The session is expired.")
	base := ""
	if snapshot, ok := training.DefaultManager.Snapshot(userID, userID); ok {
		base = snapshot.PromptText
		if base == "" {
			base = training.BuildPrompt(snapshot.Pair)
		}
	}

	if base == "" && sessionRow != nil && sessionRow.CurrentPromptText != "" {
		base = sessionRow.CurrentPromptText
	}

	if base == "" && sessionRow != nil && len(sessionRow.PairIDs) > 0 {
		var ids []uint
		if err := json.Unmarshal(sessionRow.PairIDs, &ids); err == nil {
			if sessionRow.CurrentIndex >= 0 && sessionRow.CurrentIndex < len(ids) {
				var pair db.WordPair
				if err := db.DB.First(&pair, ids[sessionRow.CurrentIndex]).Error; err == nil && pair.ID != 0 {
					base = training.BuildPrompt(pair)
				}
			}
		}
	}

	if base == "" {
		return expiredNotice
	}
	return fmt.Sprintf("%s\n\n%s", base, expiredNotice)
}

func latestDueSlot(now time.Time, user db.UserSettings) (time.Time, bool) {
	offset := time.Duration(user.TimezoneOffsetHours) * time.Hour
	localNow := now.Add(offset)
	year, month, day := localNow.Date()

	var latest time.Time
	consider := func(enabled bool, hour int) {
		if !enabled {
			return
		}
		localSlot := time.Date(year, month, day, hour, 0, 0, 0, time.UTC)
		slotUTC := localSlot.Add(-offset)
		if now.Before(slotUTC) {
			return
		}
		if user.LastTrainingSentAt != nil && !user.LastTrainingSentAt.Before(slotUTC) {
			return
		}
		if latest.IsZero() || slotUTC.After(latest) {
			latest = slotUTC
		}
	}

	consider(user.ReminderMorning, slotMorningHour)
	consider(user.ReminderAfternoon, slotAfternoonHour)
	consider(user.ReminderEvening, slotEveningHour)

	if latest.IsZero() {
		return time.Time{}, false
	}
	return latest, true
}

func computeMissedCount(user db.UserSettings) int {
	missed := user.MissedTrainingSessions
	if user.LastTrainingSentAt == nil {
		return missed
	}
	if user.LastTrainingEngagedAt == nil || user.LastTrainingEngagedAt.Before(*user.LastTrainingSentAt) {
		return missed + 1
	}
	return 0
}

func countEnabledSlots(user db.UserSettings) int {
	count := 0
	if user.ReminderMorning {
		count++
	}
	if user.ReminderAfternoon {
		count++
	}
	if user.ReminderEvening {
		count++
	}
	return count
}

func countOverdue(userID int64, now time.Time) (int, error) {
	var count int64
	if err := db.DB.Model(&db.WordPair{}).
		Where("user_id = ? AND srs_due_at <= ?", userID, now).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return int(count), nil
}

func sendOverdueSession(ctx context.Context, b *bot.Bot, user db.UserSettings, now time.Time) (bool, error) {
	if user.PairsToSend <= 0 {
		return false, nil
	}
	pairs, err := training.SelectSessionPairs(user.UserID, user.PairsToSend, now)
	if err != nil {
		return false, err
	}
	if len(pairs) == 0 {
		return false, nil
	}

	session := training.DefaultManager.StartOrRestart(user.UserID, user.UserID, pairs)
	card := session.CurrentPair()
	if card == nil {
		return false, nil
	}

	overdueToken := training.DefaultOverdue.Start(user.UserID, user.UserID)
	prompt := training.BuildPrompt(*card)
	msg, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      user.UserID,
		Text:        prompt,
		ParseMode:   models.ParseModeMarkdown,
		ReplyMarkup: buildOverdueKeyboard(session.CurrentToken(), overdueToken),
	})
	if err != nil {
		return false, err
	}
	training.DefaultManager.SetCurrentMessageID(session, msg.ID)
	training.DefaultManager.SetCurrentPromptText(session, prompt)
	training.DefaultOverdue.BindMessage(user.UserID, user.UserID, overdueToken, msg.ID)
	return true, nil
}

func buildOverdueKeyboard(reviewToken, overdueToken string) *models.InlineKeyboardMarkup {
	keyboard := training.BuildKeyboard(reviewToken)
	if keyboard == nil {
		keyboard = &models.InlineKeyboardMarkup{}
	}
	keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, []models.InlineKeyboardButton{
		{Text: "Snooze 1 day", CallbackData: training.OverdueCallbackPrefix + overdueToken + ":snooze1d"},
		{Text: "Snooze 1 week", CallbackData: training.OverdueCallbackPrefix + overdueToken + ":snooze1w"},
	})
	return keyboard
}
