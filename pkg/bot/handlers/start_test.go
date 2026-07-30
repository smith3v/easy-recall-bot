package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/smith3v/tg-word-reminder/pkg/bot/onboarding"
	"github.com/smith3v/tg-word-reminder/pkg/db"
	"github.com/smith3v/tg-word-reminder/pkg/internal/testutil"
	"github.com/smith3v/tg-word-reminder/pkg/logger"
	"gorm.io/gorm"
)

func seedInitVocabulary(t *testing.T) {
	t.Helper()
	row := db.InitVocabulary{
		EN: "hello",
		RU: "привет",
		NL: "hallo",
		ES: "hola",
		DE: "hallo",
		FR: "bonjour",
	}
	if err := db.DB.Create(&row).Error; err != nil {
		t.Fatalf("failed to seed init vocabulary: %v", err)
	}
}

func TestHandleStartBeginsWizardForNewUser(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)
	seedInitVocabulary(t)

	client := newMockClient()
	b := newTestTelegramBot(t, client)
	update := newTestUpdate("/start", 202)

	HandleStart(context.Background(), b, update)

	var state db.OnboardingState
	if err := db.DB.Where("user_id = ?", 202).First(&state).Error; err != nil {
		t.Fatalf("failed to load onboarding state: %v", err)
	}
	if state.Step != "choose_learning" {
		t.Fatalf("expected choose_learning step, got %+v", state)
	}
	if state.AwaitingResetPhrase {
		t.Fatalf("did not expect reset phrase mode for new user")
	}

	var settings db.UserSettings
	if err := db.DB.Where("user_id = ?", 202).First(&settings).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected settings to be absent before onboarding completion, got err=%v settings=%+v", err, settings)
	}
	got := client.lastMessageText(t)
	if !strings.Contains(got, "Choose the language you are learning") {
		t.Fatalf("expected onboarding prompt, got %q", got)
	}
}

func TestHandleStartRequestsResetPhraseForExistingUser(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)
	seedInitVocabulary(t)

	seed := db.UserSettings{
		UserID:              203,
		PairsToSend:         3,
		ReminderMorning:     true,
		ReminderAfternoon:   true,
		ReminderEvening:     true,
		TimezoneOffsetHours: 2,
	}
	if err := db.DB.Create(&seed).Error; err != nil {
		t.Fatalf("failed to seed settings: %v", err)
	}

	client := newMockClient()
	b := newTestTelegramBot(t, client)
	update := newTestUpdate("/start", 203)

	HandleStart(context.Background(), b, update)

	var state db.OnboardingState
	if err := db.DB.Where("user_id = ?", 203).First(&state).Error; err != nil {
		t.Fatalf("failed to load onboarding state: %v", err)
	}
	if !state.AwaitingResetPhrase {
		t.Fatalf("expected awaiting reset phrase, got %+v", state)
	}
	if state.ResetPending {
		t.Fatalf("did not expect reset to be pending before phrase confirmation")
	}

	var settings db.UserSettings
	if err := db.DB.Where("user_id = ?", 203).First(&settings).Error; err != nil {
		t.Fatalf("failed to load settings: %v", err)
	}
	if settings.PairsToSend != 3 {
		t.Fatalf("expected settings to remain unchanged, got %+v", settings)
	}

	got := client.lastMessageText(t)
	if !strings.Contains(got, "RESET MY DATA") {
		t.Fatalf("expected reset phrase warning, got %q", got)
	}

	body := client.lastRequestBody(t)
	if !strings.Contains(body, onboarding.BuildCancelResetCallback()) {
		t.Fatalf("expected Keep my data callback, got %q", body)
	}
}

func TestHandleStartFallsBackWhenInitVocabularyMissingForNewUser(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)

	client := newMockClient()
	b := newTestTelegramBot(t, client)
	update := newTestUpdate("/start", 204)

	HandleStart(context.Background(), b, update)

	var state db.OnboardingState
	if err := db.DB.Where("user_id = ?", 204).First(&state).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected no onboarding state, got err=%v state=%+v", err, state)
	}

	var settings db.UserSettings
	if err := db.DB.Where("user_id = ?", 204).First(&settings).Error; err != nil {
		t.Fatalf("expected default settings to be created, got err=%v", err)
	}
	if settings.PairsToSend != 5 || !settings.ReminderMorning || !settings.ReminderAfternoon || !settings.ReminderEvening {
		t.Fatalf("unexpected default settings: %+v", settings)
	}

	got := client.lastMessageText(t)
	if !strings.Contains(got, "unavailable") || !strings.Contains(got, "upload your own CSV") {
		t.Fatalf("expected init unavailable fallback message, got %q", got)
	}
}

func TestHandleStartDoesNotEnterResetFlowWhenInitVocabularyMissingForExistingUser(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)

	seed := db.UserSettings{
		UserID:              205,
		PairsToSend:         3,
		ReminderMorning:     true,
		ReminderAfternoon:   true,
		ReminderEvening:     true,
		TimezoneOffsetHours: 2,
	}
	if err := db.DB.Create(&seed).Error; err != nil {
		t.Fatalf("failed to seed settings: %v", err)
	}

	client := newMockClient()
	b := newTestTelegramBot(t, client)
	update := newTestUpdate("/start", 205)

	HandleStart(context.Background(), b, update)

	var state db.OnboardingState
	if err := db.DB.Where("user_id = ?", 205).First(&state).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected no onboarding reset state, got err=%v state=%+v", err, state)
	}

	got := client.lastMessageText(t)
	if !strings.Contains(got, "unavailable") || !strings.Contains(got, "unchanged") {
		t.Fatalf("expected unavailable+unchanged warning, got %q", got)
	}
}
