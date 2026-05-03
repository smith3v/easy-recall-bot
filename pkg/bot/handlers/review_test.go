package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/smith3v/tg-word-reminder/pkg/bot/training"
	"github.com/smith3v/tg-word-reminder/pkg/db"
	"github.com/smith3v/tg-word-reminder/pkg/internal/testutil"
	"github.com/smith3v/tg-word-reminder/pkg/logger"
	"gorm.io/datatypes"
)

func TestHandleReviewNoPairs(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)
	training.ResetDefaultManager(time.Now)
	training.ResetOverdueManager(time.Now)

	client := newMockClient()
	b := newTestTelegramBot(t, client)
	update := newTestUpdate("/review", 3001)
	update.Message.Chat.Type = models.ChatTypePrivate

	HandleReview(context.Background(), b, update)

	got := client.lastMessageText(t)
	if !strings.Contains(got, "Nothing to review") {
		t.Fatalf("expected empty review message, got %q", got)
	}
}

func TestHandleReviewCallbackUpdatesPair(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)
	training.ResetDefaultManager(time.Now)
	training.ResetOverdueManager(time.Now)

	if err := db.DB.Create(&db.UserSettings{
		UserID:                 3002,
		MissedTrainingSessions: 2,
		TrainingPaused:         true,
		PairsToSend:            2,
	}).Error; err != nil {
		t.Fatalf("failed to seed user settings: %v", err)
	}

	now := time.Now().UTC().Add(-time.Hour)
	pairs := []db.WordPair{
		{UserID: 3002, Word1: "hola", Word2: "adios", SrsState: "new", SrsDueAt: now},
		{UserID: 3002, Word1: "uno", Word2: "one", SrsState: "new", SrsDueAt: now},
	}
	for _, pair := range pairs {
		if err := db.DB.Create(&pair).Error; err != nil {
			t.Fatalf("failed to seed pair: %v", err)
		}
	}

	client := newMockClient()
	client.response = `{"ok":true,"result":{"message_id":55}}`
	b := newTestTelegramBot(t, client)
	update := newTestUpdate("/review", 3002)
	update.Message.Chat.Type = models.ChatTypePrivate

	HandleReview(context.Background(), b, update)

	snapshot, ok := training.DefaultManager.Snapshot(3002, 3002)
	if !ok {
		t.Fatalf("expected active review session")
	}

	callback := newTestCallbackUpdate(fmt.Sprintf("t:grade:%s:good", snapshot.Token), 3002, 3002, snapshot.MessageID)
	HandleReviewCallback(context.Background(), b, callback)

	var updated db.WordPair
	if err := db.DB.Where("id = ?", snapshot.Pair.ID).First(&updated).Error; err != nil {
		t.Fatalf("failed to load updated pair: %v", err)
	}
	if updated.SrsState != "learning" || updated.SrsStep != 1 {
		t.Fatalf("expected learning step 1 after good grade, got %+v", updated)
	}
	if updated.SrsLastReviewedAt == nil {
		t.Fatalf("expected last reviewed timestamp to be set")
	}

	var settings db.UserSettings
	if err := db.DB.Where("user_id = ?", 3002).First(&settings).Error; err != nil {
		t.Fatalf("failed to load settings: %v", err)
	}
	if settings.MissedTrainingSessions != 0 || settings.TrainingPaused {
		t.Fatalf("expected engagement reset, got %+v", settings)
	}
	if settings.LastTrainingEngagedAt == nil {
		t.Fatalf("expected engagement timestamp to be set")
	}

	sendCount := 0
	editCount := 0
	for _, req := range client.requests {
		if strings.Contains(req.path, "sendMessage") {
			sendCount++
		}
		if strings.Contains(req.path, "editMessageText") {
			editCount++
		}
	}
	if sendCount < 2 {
		t.Fatalf("expected at least two sendMessage calls, got %d", sendCount)
	}
	if editCount < 1 {
		t.Fatalf("expected editMessageText call, got %d", editCount)
	}
}

func TestHandleReviewCallbackResumesPersistedSession(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)
	training.ResetDefaultManager(time.Now)
	training.ResetOverdueManager(time.Now)

	now := time.Now().UTC().Add(-time.Hour)
	pair := db.WordPair{UserID: 3010, Word1: "flow", Word2: "de stroom", SrsState: "new", SrsDueAt: now}
	if err := db.DB.Create(&pair).Error; err != nil {
		t.Fatalf("failed to seed pair: %v", err)
	}

	prompt := training.BuildPrompt(pair)
	ids, err := json.Marshal([]uint{pair.ID})
	if err != nil {
		t.Fatalf("failed to marshal ids: %v", err)
	}
	if err := db.DB.Create(&db.TrainingSession{
		ChatID:            3010,
		UserID:            3010,
		PairIDs:           datatypes.JSON(ids),
		CurrentIndex:      0,
		CurrentToken:      "tok",
		CurrentMessageID:  88,
		CurrentPromptText: prompt,
		LastActivityAt:    time.Now().UTC(),
		ExpiresAt:         time.Now().UTC().Add(24 * time.Hour),
	}).Error; err != nil {
		t.Fatalf("failed to seed training session: %v", err)
	}

	training.ResetDefaultManager(time.Now)

	client := newMockClient()
	b := newTestTelegramBot(t, client)
	callback := newTestCallbackUpdate("t:grade:tok:good", 3010, 3010, 88)
	HandleReviewCallback(context.Background(), b, callback)

	var updated db.WordPair
	if err := db.DB.Where("id = ?", pair.ID).First(&updated).Error; err != nil {
		t.Fatalf("failed to load updated pair: %v", err)
	}
	if updated.SrsState != "learning" || updated.SrsStep != 1 {
		t.Fatalf("expected learning step 1 after good grade, got %+v", updated)
	}
}

func TestHandleReviewMarksEngagementOnStart(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)
	training.ResetDefaultManager(time.Now)
	training.ResetOverdueManager(time.Now)

	if err := db.DB.Create(&db.UserSettings{
		UserID:                 3003,
		MissedTrainingSessions: 1,
		TrainingPaused:         true,
		PairsToSend:            1,
	}).Error; err != nil {
		t.Fatalf("failed to seed user settings: %v", err)
	}
	if err := db.DB.Create(&db.WordPair{
		UserID:   3003,
		Word1:    "hola",
		Word2:    "adios",
		SrsState: "new",
		SrsDueAt: time.Now().Add(-time.Minute),
	}).Error; err != nil {
		t.Fatalf("failed to seed word pair: %v", err)
	}

	client := newMockClient()
	client.response = `{"ok":true,"result":{"message_id":99}}`
	b := newTestTelegramBot(t, client)
	update := newTestUpdate("/review", 3003)
	update.Message.Chat.Type = models.ChatTypePrivate

	HandleReview(context.Background(), b, update)

	var settings db.UserSettings
	if err := db.DB.Where("user_id = ?", 3003).First(&settings).Error; err != nil {
		t.Fatalf("failed to load settings: %v", err)
	}
	if settings.MissedTrainingSessions != 0 || settings.TrainingPaused {
		t.Fatalf("expected engagement reset on start, got %+v", settings)
	}
	if settings.LastTrainingEngagedAt == nil {
		t.Fatalf("expected engagement timestamp to be set")
	}
}

func TestHandleReviewCompletionMessage(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)
	training.ResetDefaultManager(time.Now)
	training.ResetOverdueManager(time.Now)

	if err := db.DB.Create(&db.WordPair{
		UserID:   3004,
		Word1:    "hola",
		Word2:    "adios",
		SrsState: "new",
		SrsDueAt: time.Now().Add(-time.Minute),
	}).Error; err != nil {
		t.Fatalf("failed to seed word pair: %v", err)
	}
	if err := db.DB.Create(&db.UserSettings{
		UserID:      3004,
		PairsToSend: 1,
	}).Error; err != nil {
		t.Fatalf("failed to seed user settings: %v", err)
	}

	client := newMockClient()
	client.response = `{"ok":true,"result":{"message_id":101}}`
	b := newTestTelegramBot(t, client)
	update := newTestUpdate("/review", 3004)
	update.Message.Chat.Type = models.ChatTypePrivate

	HandleReview(context.Background(), b, update)

	snapshot, ok := training.DefaultManager.Snapshot(3004, 3004)
	if !ok {
		t.Fatalf("expected active review session")
	}

	callback := newTestCallbackUpdate(fmt.Sprintf("t:grade:%s:good", snapshot.Token), 3004, 3004, snapshot.MessageID)
	HandleReviewCallback(context.Background(), b, callback)

	sendCount := 0
	for _, req := range client.requests {
		if strings.Contains(req.path, "sendMessage") {
			sendCount++
		}
	}
	if sendCount != 1 {
		t.Fatalf("expected only the initial prompt, got %d sendMessage calls", sendCount)
	}
}

func TestHandleOverdueCallbackCatchUp(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)
	training.ResetDefaultManager(time.Now)
	training.ResetOverdueManager(time.Now)

	if err := db.DB.Create(&db.UserSettings{
		UserID:      4001,
		PairsToSend: 1,
	}).Error; err != nil {
		t.Fatalf("failed to seed settings: %v", err)
	}
	if err := db.DB.Create(&db.WordPair{
		UserID:   4001,
		Word1:    "hola",
		Word2:    "adios",
		SrsState: "new",
		SrsDueAt: time.Now().Add(-time.Minute),
	}).Error; err != nil {
		t.Fatalf("failed to seed word pair: %v", err)
	}

	client := newMockClient()
	client.response = `{"ok":true,"result":{"message_id":77}}`
	b := newTestTelegramBot(t, client)

	token := training.DefaultOverdue.Start(4001, 4001)
	training.DefaultOverdue.BindMessage(4001, 4001, token, 10)
	update := newTestCallbackUpdate("t:overdue:"+token+":catch", 4001, 4001, 10)

	HandleOverdueCallback(context.Background(), b, update)

	sendCount := 0
	for _, req := range client.requests {
		if strings.Contains(req.path, "sendMessage") {
			sendCount++
		}
	}
	if sendCount < 1 {
		t.Fatalf("expected sendMessage for catch-up")
	}
}

func TestHandleOverdueCallbackSnoozeEndsSession(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)
	training.ResetDefaultManager(time.Now)
	training.ResetOverdueManager(time.Now)

	originalDue := time.Now().Add(-time.Minute).UTC()
	if err := db.DB.Create(&db.UserSettings{
		UserID:      4002,
		PairsToSend: 1,
	}).Error; err != nil {
		t.Fatalf("failed to seed settings: %v", err)
	}
	pair := db.WordPair{
		UserID:   4002,
		Word1:    "hola",
		Word2:    "adios",
		SrsState: "new",
		SrsDueAt: originalDue,
	}
	if err := db.DB.Create(&pair).Error; err != nil {
		t.Fatalf("failed to seed word pair: %v", err)
	}

	session := training.DefaultManager.StartOrRestart(4002, 4002, []db.WordPair{
		{UserID: 4002, Word1: "hola", Word2: "adios", SrsState: "new"},
	})
	training.DefaultManager.SetCurrentMessageID(session, 22)

	client := newMockClient()
	b := newTestTelegramBot(t, client)

	token := training.DefaultOverdue.Start(4002, 4002)
	training.DefaultOverdue.BindMessage(4002, 4002, token, 22)
	update := newTestCallbackUpdate("t:overdue:"+token+":snooze1d", 4002, 4002, 22)

	before := time.Now().UTC()
	HandleOverdueCallback(context.Background(), b, update)
	after := time.Now().UTC()

	if session := training.DefaultManager.GetSession(4002, 4002); session != nil {
		t.Fatalf("expected session to end after snooze")
	}

	var settings db.UserSettings
	if err := db.DB.Where("user_id = ?", 4002).First(&settings).Error; err != nil {
		t.Fatalf("failed to reload settings: %v", err)
	}
	if settings.ReminderSnoozedUntil == nil {
		t.Fatalf("expected reminder snooze to be set")
	}
	if settings.ReminderSnoozedUntil.Before(before.Add(24 * time.Hour)) || settings.ReminderSnoozedUntil.After(after.Add(24*time.Hour)) {
		t.Fatalf("expected snooze to be about 24h ahead, got %v", settings.ReminderSnoozedUntil)
	}

	var reloaded db.WordPair
	if err := db.DB.First(&reloaded, pair.ID).Error; err != nil {
		t.Fatalf("failed to reload word pair: %v", err)
	}
	if !reloaded.SrsDueAt.Equal(originalDue) {
		t.Fatalf("expected word pair due time to stay unchanged, got %v want %v", reloaded.SrsDueAt, originalDue)
	}
}

func TestHandleOverdueCallbackSnoozeWeekSetsReminderWindow(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)
	training.ResetDefaultManager(time.Now)
	training.ResetOverdueManager(time.Now)

	originalDue := time.Now().Add(-2 * time.Minute).UTC()
	if err := db.DB.Create(&db.UserSettings{
		UserID:      4003,
		PairsToSend: 1,
	}).Error; err != nil {
		t.Fatalf("failed to seed settings: %v", err)
	}
	pair := db.WordPair{
		UserID:   4003,
		Word1:    "bonjour",
		Word2:    "hello",
		SrsState: "review",
		SrsDueAt: originalDue,
	}
	if err := db.DB.Create(&pair).Error; err != nil {
		t.Fatalf("failed to seed word pair: %v", err)
	}

	session := training.DefaultManager.StartOrRestart(4003, 4003, []db.WordPair{
		{UserID: 4003, Word1: "bonjour", Word2: "hello", SrsState: "review"},
	})
	training.DefaultManager.SetCurrentMessageID(session, 23)

	client := newMockClient()
	b := newTestTelegramBot(t, client)

	token := training.DefaultOverdue.Start(4003, 4003)
	training.DefaultOverdue.BindMessage(4003, 4003, token, 23)
	update := newTestCallbackUpdate("t:overdue:"+token+":snooze1w", 4003, 4003, 23)

	before := time.Now().UTC()
	HandleOverdueCallback(context.Background(), b, update)
	after := time.Now().UTC()

	if session := training.DefaultManager.GetSession(4003, 4003); session != nil {
		t.Fatalf("expected session to end after snooze")
	}

	var settings db.UserSettings
	if err := db.DB.Where("user_id = ?", 4003).First(&settings).Error; err != nil {
		t.Fatalf("failed to reload settings: %v", err)
	}
	if settings.ReminderSnoozedUntil == nil {
		t.Fatalf("expected reminder snooze to be set")
	}
	if settings.ReminderSnoozedUntil.Before(before.Add(7 * 24 * time.Hour)) || settings.ReminderSnoozedUntil.After(after.Add(7*24*time.Hour)) {
		t.Fatalf("expected snooze to be about 7d ahead, got %v", settings.ReminderSnoozedUntil)
	}

	var reloaded db.WordPair
	if err := db.DB.First(&reloaded, pair.ID).Error; err != nil {
		t.Fatalf("failed to reload word pair: %v", err)
	}
	if !reloaded.SrsDueAt.Equal(originalDue) {
		t.Fatalf("expected word pair due time to stay unchanged, got %v want %v", reloaded.SrsDueAt, originalDue)
	}
}

func TestHandleReviewResumesPersistedSession(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)
	training.ResetDefaultManager(time.Now)
	training.ResetOverdueManager(time.Now)

	now := time.Date(2025, 1, 5, 10, 0, 0, 0, time.UTC)
	pairs := []db.WordPair{
		{UserID: 5001, Word1: "hola", Word2: "adios", SrsState: "new", SrsDueAt: now},
		{UserID: 5001, Word1: "uno", Word2: "one", SrsState: "new", SrsDueAt: now},
	}
	if err := db.DB.Create(&pairs).Error; err != nil {
		t.Fatalf("failed to seed pairs: %v", err)
	}

	session := training.DefaultManager.StartOrRestart(5001, 5001, pairs)
	if session == nil || session.CurrentPair() == nil {
		t.Fatalf("expected in-memory session")
	}

	client := newMockClient()
	client.response = `{"ok":true,"result":{"message_id":77}}`
	b := newTestTelegramBot(t, client)

	update := newTestUpdate("/review", 5001)
	update.Message.Chat.Type = models.ChatTypePrivate

	HandleReview(context.Background(), b, update)

	sendCount := 0
	for _, req := range client.requests {
		if strings.Contains(req.path, "sendMessage") {
			sendCount++
		}
	}
	if sendCount != 1 {
		t.Fatalf("expected one sendMessage for resumed session, got %d", sendCount)
	}

	got := client.lastMessageText(t)
	if !strings.Contains(got, "||") {
		t.Fatalf("expected resumed prompt, got %q", got)
	}
}
