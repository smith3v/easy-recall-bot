package reminders

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	telegram "github.com/go-telegram/bot"
	"github.com/smith3v/tg-word-reminder/pkg/bot/training"
	"github.com/smith3v/tg-word-reminder/pkg/db"
	"github.com/smith3v/tg-word-reminder/pkg/internal/testutil"
	"github.com/smith3v/tg-word-reminder/pkg/logger"
	"gorm.io/datatypes"
)

type recordedRequest struct {
	path        string
	method      string
	contentType string
	body        []byte
}

type mockClient struct {
	requests []recordedRequest
	response string
}

func newMockClient() *mockClient {
	return &mockClient{
		response: `{"ok":true,"result":{}}`,
	}
}

func (m *mockClient) Do(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}
	if err := req.Body.Close(); err != nil {
		return nil, fmt.Errorf("failed to close request body: %w", err)
	}
	m.requests = append(m.requests, recordedRequest{
		path:        req.URL.Path,
		method:      req.Method,
		contentType: req.Header.Get("Content-Type"),
		body:        body,
	})

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(m.response)),
		Header:     make(http.Header),
	}
	return resp, nil
}

func (m *mockClient) lastMessageText(t *testing.T) string {
	t.Helper()
	if len(m.requests) == 0 {
		t.Fatalf("expected at least one recorded request")
	}
	req := m.requests[len(m.requests)-1]

	mediaType, params, err := mime.ParseMediaType(req.contentType)
	if err != nil {
		t.Fatalf("failed to parse media type: %v", err)
	}
	if !strings.HasPrefix(mediaType, "multipart/") {
		t.Fatalf("unexpected media type: %s", mediaType)
	}

	reader := multipart.NewReader(bytes.NewReader(req.body), params["boundary"])
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("failed to read multipart part: %v", err)
		}
		if part.FormName() == "text" {
			data, err := io.ReadAll(part)
			if err != nil {
				t.Fatalf("failed to read text part: %v", err)
			}
			return string(data)
		}
	}
	t.Fatalf("text field not found in request")
	return ""
}

func (m *mockClient) lastRequestBody(t *testing.T) string {
	t.Helper()
	if len(m.requests) == 0 {
		t.Fatalf("expected at least one recorded request")
	}
	req := m.requests[len(m.requests)-1]
	return string(req.body)
}

func newTestTelegramBot(t *testing.T, client *mockClient) *telegram.Bot {
	t.Helper()
	b, err := telegram.New("test-token",
		telegram.WithSkipGetMe(),
		telegram.WithHTTPClient(time.Second, client),
	)
	if err != nil {
		t.Fatalf("failed to create test bot: %v", err)
	}
	return b
}

func TestLatestDueSlotSelectsMostRecent(t *testing.T) {
	now := time.Date(2025, 1, 2, 14, 30, 0, 0, time.UTC)
	user := db.UserSettings{
		UserID:              1,
		ReminderMorning:     true,
		ReminderAfternoon:   true,
		ReminderEvening:     true,
		TimezoneOffsetHours: 0,
	}

	slot, ok := latestDueSlot(now, user)
	if !ok {
		t.Fatalf("expected due slot")
	}
	expected := time.Date(2025, 1, 2, 13, 0, 0, 0, time.UTC)
	if !slot.Equal(expected) {
		t.Fatalf("expected slot %v, got %v", expected, slot)
	}
}

func TestLatestDueSlotRespectsLastSent(t *testing.T) {
	now := time.Date(2025, 1, 2, 21, 0, 0, 0, time.UTC)
	lastSent := time.Date(2025, 1, 2, 20, 30, 0, 0, time.UTC)
	user := db.UserSettings{
		UserID:              1,
		ReminderMorning:     true,
		ReminderAfternoon:   true,
		ReminderEvening:     true,
		TimezoneOffsetHours: 0,
		LastTrainingSentAt:  &lastSent,
	}

	_, ok := latestDueSlot(now, user)
	if ok {
		t.Fatalf("expected no due slot after evening send")
	}
}

func TestComputeMissedCount(t *testing.T) {
	lastSent := time.Date(2025, 1, 2, 9, 0, 0, 0, time.UTC)
	lastEngaged := time.Date(2025, 1, 2, 10, 0, 0, 0, time.UTC)

	user := db.UserSettings{
		MissedTrainingSessions: 1,
		LastTrainingSentAt:     &lastSent,
	}
	if got := computeMissedCount(user); got != 2 {
		t.Fatalf("expected missed count 2, got %d", got)
	}

	user.LastTrainingEngagedAt = &lastEngaged
	if got := computeMissedCount(user); got != 0 {
		t.Fatalf("expected missed reset to 0, got %d", got)
	}
}

func TestReminderDoesNotPauseBelowThreshold(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)
	training.ResetDefaultManager(time.Now)
	training.ResetOverdueManager(time.Now)

	now := time.Date(2025, 1, 2, 13, 30, 0, 0, time.UTC)
	lastSent := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	user := db.UserSettings{
		UserID:                 9001,
		PairsToSend:            1,
		ReminderAfternoon:      true,
		TimezoneOffsetHours:    0,
		MissedTrainingSessions: 7,
		LastTrainingSentAt:     &lastSent,
	}
	if err := db.DB.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed user settings: %v", err)
	}

	client := newMockClient()
	b := newTestTelegramBot(t, client)
	handleUserReminder(context.Background(), b, user, now)

	var updated db.UserSettings
	if err := db.DB.Where("user_id = ?", user.UserID).First(&updated).Error; err != nil {
		t.Fatalf("failed to load updated user settings: %v", err)
	}
	if updated.TrainingPaused {
		t.Fatalf("expected reminders to remain active below threshold")
	}
	if updated.MissedTrainingSessions != 7 {
		t.Fatalf("expected missed count to remain unchanged when no session is sent, got %d", updated.MissedTrainingSessions)
	}

	for _, req := range client.requests {
		if strings.Contains(req.path, "sendMessage") && strings.Contains(string(req.body), "Paused reminders due to inactivity.") {
			t.Fatalf("did not expect pause message below threshold")
		}
	}
}

func TestReminderPausesAtThreshold(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)
	training.ResetDefaultManager(time.Now)
	training.ResetOverdueManager(time.Now)

	now := time.Date(2025, 1, 2, 13, 30, 0, 0, time.UTC)
	lastSent := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	user := db.UserSettings{
		UserID:                 9002,
		PairsToSend:            1,
		ReminderAfternoon:      true,
		TimezoneOffsetHours:    0,
		MissedTrainingSessions: 8,
		LastTrainingSentAt:     &lastSent,
	}
	if err := db.DB.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed user settings: %v", err)
	}

	client := newMockClient()
	b := newTestTelegramBot(t, client)
	handleUserReminder(context.Background(), b, user, now)

	var updated db.UserSettings
	if err := db.DB.Where("user_id = ?", user.UserID).First(&updated).Error; err != nil {
		t.Fatalf("failed to load updated user settings: %v", err)
	}
	if !updated.TrainingPaused {
		t.Fatalf("expected reminders to pause at threshold")
	}
	if updated.MissedTrainingSessions != 9 {
		t.Fatalf("expected missed count to be set to 9 at pause threshold, got %d", updated.MissedTrainingSessions)
	}

	foundPauseMessage := false
	for _, req := range client.requests {
		if strings.Contains(req.path, "sendMessage") && strings.Contains(string(req.body), "Paused reminders due to inactivity.") {
			foundPauseMessage = true
			break
		}
	}
	if !foundPauseMessage {
		t.Fatalf("expected pause notification message")
	}
}

func TestSendTrainingSessionNoPairs(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)
	training.ResetDefaultManager(time.Now)

	client := newMockClient()
	b := newTestTelegramBot(t, client)
	user := db.UserSettings{UserID: 10, PairsToSend: 1}

	sent, err := sendTrainingSession(context.Background(), b, user, time.Now().UTC())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sent {
		t.Fatalf("expected no session to send")
	}
	if len(client.requests) != 0 {
		t.Fatalf("expected no message to be sent")
	}
}

func TestSendTrainingSessionSendsPrompt(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)
	training.ResetDefaultManager(time.Now)

	if err := db.DB.Create(&db.WordPair{
		UserID:   11,
		Word1:    "Hola",
		Word2:    "Adios",
		SrsState: "new",
		SrsDueAt: time.Now().Add(-time.Hour),
	}).Error; err != nil {
		t.Fatalf("failed to seed word pair: %v", err)
	}

	client := newMockClient()
	b := newTestTelegramBot(t, client)
	user := db.UserSettings{UserID: 11, PairsToSend: 1}

	sent, err := sendTrainingSession(context.Background(), b, user, time.Now().UTC())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sent {
		t.Fatalf("expected session to be sent")
	}

	got := client.lastMessageText(t)
	if !strings.Contains(got, "||") {
		t.Fatalf("expected spoiler in prompt, got %q", got)
	}
}

func TestOverduePromptTriggers(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)
	training.ResetDefaultManager(time.Now)

	now := time.Date(2025, 1, 2, 13, 30, 0, 0, time.UTC)
	user := db.UserSettings{
		UserID:              20,
		PairsToSend:         1,
		ReminderAfternoon:   true,
		TimezoneOffsetHours: 0,
	}
	if err := db.DB.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed user settings: %v", err)
	}
	if err := db.DB.Create(&[]db.WordPair{
		{UserID: 20, Word1: "a", Word2: "b", SrsState: "review", SrsDueAt: now.Add(-time.Hour)},
		{UserID: 20, Word1: "c", Word2: "d", SrsState: "review", SrsDueAt: now.Add(-2 * time.Hour)},
	}).Error; err != nil {
		t.Fatalf("failed to seed pairs: %v", err)
	}

	client := newMockClient()
	b := newTestTelegramBot(t, client)

	handleUserReminder(context.Background(), b, user, now)

	got := client.lastMessageText(t)
	if !strings.Contains(got, "||") {
		t.Fatalf("expected review prompt, got %q", got)
	}

	body := client.lastRequestBody(t)
	if !strings.Contains(body, "snooze1d") || !strings.Contains(body, "snooze1w") {
		t.Fatalf("expected snooze actions in keyboard, got %q", body)
	}
	if !strings.Contains(body, "t:grade") {
		t.Fatalf("expected review grade buttons in keyboard, got %q", body)
	}
}

func TestReminderExpiresActiveSession(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)
	training.ResetDefaultManager(time.Now)
	training.ResetOverdueManager(time.Now)

	now := time.Date(2025, 1, 2, 13, 30, 0, 0, time.UTC)
	user := db.UserSettings{
		UserID:              30,
		PairsToSend:         1,
		ReminderAfternoon:   true,
		TimezoneOffsetHours: 0,
	}
	if err := db.DB.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed user settings: %v", err)
	}
	pair := db.WordPair{
		UserID:   30,
		Word1:    "a",
		Word2:    "b",
		SrsState: "review",
		SrsDueAt: now.Add(-time.Hour),
	}
	if err := db.DB.Create(&pair).Error; err != nil {
		t.Fatalf("failed to seed word pair: %v", err)
	}

	ids, err := json.Marshal([]uint{pair.ID})
	if err != nil {
		t.Fatalf("failed to marshal ids: %v", err)
	}
	if err := db.DB.Create(&db.TrainingSession{
		ChatID:           30,
		UserID:           30,
		PairIDs:          datatypes.JSON(ids),
		CurrentIndex:     0,
		CurrentToken:     "tok",
		CurrentMessageID: 55,
		LastActivityAt:   now.Add(-activeSessionGrace - time.Minute),
		ExpiresAt:        now.Add(time.Hour),
	}).Error; err != nil {
		t.Fatalf("failed to seed training session: %v", err)
	}

	client := newMockClient()
	client.response = `{"ok":true,"result":{"message_id":77}}`
	b := newTestTelegramBot(t, client)

	handleUserReminder(context.Background(), b, user, now)

	edited := false
	for _, req := range client.requests {
		if strings.Contains(req.path, "editMessageText") && strings.Contains(string(req.body), "The session is expired\\.") {
			if !strings.Contains(string(req.body), "||") {
				t.Fatalf("expected expired message to include prompt text, got %q", string(req.body))
			}
			edited = true
			break
		}
	}
	if !edited {
		t.Fatalf("expected expired session message to be edited")
	}

	var stored db.TrainingSession
	if err := db.DB.Where("chat_id = ? AND user_id = ?", 30, 30).First(&stored).Error; err != nil {
		t.Fatalf("failed to load training session: %v", err)
	}
	if stored.CurrentMessageID != 77 {
		t.Fatalf("expected new session message id 77, got %d", stored.CurrentMessageID)
	}
}

func TestReminderSkipsRecentActiveSession(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)
	training.ResetDefaultManager(time.Now)
	training.ResetOverdueManager(time.Now)

	now := time.Date(2025, 1, 2, 13, 30, 0, 0, time.UTC)
	user := db.UserSettings{
		UserID:              40,
		PairsToSend:         1,
		ReminderAfternoon:   true,
		TimezoneOffsetHours: 0,
	}
	if err := db.DB.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed user settings: %v", err)
	}
	pair := db.WordPair{
		UserID:   40,
		Word1:    "a",
		Word2:    "b",
		SrsState: "review",
		SrsDueAt: now.Add(-time.Hour),
	}
	if err := db.DB.Create(&pair).Error; err != nil {
		t.Fatalf("failed to seed word pair: %v", err)
	}

	ids, err := json.Marshal([]uint{pair.ID})
	if err != nil {
		t.Fatalf("failed to marshal ids: %v", err)
	}
	if err := db.DB.Create(&db.TrainingSession{
		ChatID:           40,
		UserID:           40,
		PairIDs:          datatypes.JSON(ids),
		CurrentIndex:     0,
		CurrentToken:     "tok",
		CurrentMessageID: 55,
		LastActivityAt:   now.Add(-activeSessionGrace + time.Minute),
		ExpiresAt:        now.Add(time.Hour),
	}).Error; err != nil {
		t.Fatalf("failed to seed training session: %v", err)
	}

	client := newMockClient()
	b := newTestTelegramBot(t, client)

	handleUserReminder(context.Background(), b, user, now)

	for _, req := range client.requests {
		if strings.Contains(req.path, "editMessageText") || strings.Contains(req.path, "sendMessage") {
			t.Fatalf("expected no reminder actions, got %s", req.path)
		}
	}
}

func TestReminderSkipsOverduePromptWhileSnoozed(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)
	training.ResetDefaultManager(time.Now)
	training.ResetOverdueManager(time.Now)

	now := time.Date(2025, 1, 2, 13, 30, 0, 0, time.UTC)
	snoozedUntil := now.Add(6 * 24 * time.Hour)
	user := db.UserSettings{
		UserID:                50,
		PairsToSend:           1,
		ReminderAfternoon:     true,
		TimezoneOffsetHours:   0,
		ReminderSnoozedUntil:  &snoozedUntil,
	}
	if err := db.DB.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed user settings: %v", err)
	}
	if err := db.DB.Create(&[]db.WordPair{
		{UserID: 50, Word1: "a", Word2: "b", SrsState: "review", SrsDueAt: now.Add(-time.Hour)},
		{UserID: 50, Word1: "c", Word2: "d", SrsState: "review", SrsDueAt: now.Add(-2 * time.Hour)},
	}).Error; err != nil {
		t.Fatalf("failed to seed pairs: %v", err)
	}

	client := newMockClient()
	b := newTestTelegramBot(t, client)

	handleUserReminder(context.Background(), b, user, now)

	for _, req := range client.requests {
		if strings.Contains(req.path, "editMessageText") || strings.Contains(req.path, "sendMessage") {
			t.Fatalf("expected no reminder actions while snoozed, got %s", req.path)
		}
	}
}

func TestReminderSkipsNormalSessionWhileSnoozed(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)
	training.ResetDefaultManager(time.Now)
	training.ResetOverdueManager(time.Now)

	now := time.Date(2025, 1, 2, 13, 30, 0, 0, time.UTC)
	snoozedUntil := now.Add(24 * time.Hour)
	user := db.UserSettings{
		UserID:                60,
		PairsToSend:           1,
		ReminderAfternoon:     true,
		TimezoneOffsetHours:   0,
		ReminderSnoozedUntil:  &snoozedUntil,
	}
	if err := db.DB.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed user settings: %v", err)
	}
	if err := db.DB.Create(&db.WordPair{
		UserID:   60,
		Word1:    "alpha",
		Word2:    "beta",
		SrsState: "review",
		SrsDueAt: now.Add(-30 * time.Minute),
	}).Error; err != nil {
		t.Fatalf("failed to seed word pair: %v", err)
	}

	client := newMockClient()
	b := newTestTelegramBot(t, client)

	handleUserReminder(context.Background(), b, user, now)

	for _, req := range client.requests {
		if strings.Contains(req.path, "editMessageText") || strings.Contains(req.path, "sendMessage") {
			t.Fatalf("expected no reminder actions while snoozed, got %s", req.path)
		}
	}
}

func TestReminderResumesAfterSnoozeExpires(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)
	training.ResetDefaultManager(time.Now)
	training.ResetOverdueManager(time.Now)

	now := time.Date(2025, 1, 2, 13, 30, 0, 0, time.UTC)
	snoozedUntil := now.Add(-time.Minute)
	user := db.UserSettings{
		UserID:                70,
		PairsToSend:           1,
		ReminderAfternoon:     true,
		TimezoneOffsetHours:   0,
		ReminderSnoozedUntil:  &snoozedUntil,
	}
	if err := db.DB.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed user settings: %v", err)
	}
	if err := db.DB.Create(&db.WordPair{
		UserID:   70,
		Word1:    "hola",
		Word2:    "hello",
		SrsState: "review",
		SrsDueAt: now.Add(-15 * time.Minute),
	}).Error; err != nil {
		t.Fatalf("failed to seed word pair: %v", err)
	}

	client := newMockClient()
	client.response = `{"ok":true,"result":{"message_id":88}}`
	b := newTestTelegramBot(t, client)

	handleUserReminder(context.Background(), b, user, now)

	got := client.lastMessageText(t)
	if !strings.Contains(got, "||") {
		t.Fatalf("expected reminder prompt after snooze expiry, got %q", got)
	}
}
