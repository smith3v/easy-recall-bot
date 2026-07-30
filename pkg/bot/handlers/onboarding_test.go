package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/smith3v/tg-word-reminder/pkg/bot/onboarding"
	"github.com/smith3v/tg-word-reminder/pkg/db"
	"github.com/smith3v/tg-word-reminder/pkg/internal/testutil"
	"github.com/smith3v/tg-word-reminder/pkg/logger"
	"gorm.io/gorm"
)

func TestHandleOnboardingCallbackCancelResetClearsState(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)

	if err := db.DB.Create(&db.OnboardingState{UserID: 301, AwaitingResetPhrase: true}).Error; err != nil {
		t.Fatalf("failed to seed onboarding state: %v", err)
	}

	client := newMockClient()
	b := newTestTelegramBot(t, client)
	update := newTestCallbackUpdate(onboarding.BuildCancelResetCallback(), 301, 301, 88)

	HandleOnboardingCallback(context.Background(), b, update)

	var state db.OnboardingState
	err := db.DB.Where("user_id = ?", 301).First(&state).Error
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected onboarding state to be cleared, got err=%v state=%+v", err, state)
	}

	sawEdit := false
	for _, req := range client.requests {
		if strings.Contains(req.path, "editMessageText") && strings.Contains(string(req.body), "Reset canceled. Your data is unchanged.") {
			sawEdit = true
			break
		}
	}
	if !sawEdit {
		t.Fatalf("expected editMessageText with reset canceled text")
	}
}

func TestHandleOnboardingCallbackStopsWizardWhenInitVocabularyMissing(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)

	if err := db.DB.Create(&db.OnboardingState{
		UserID:       302,
		Step:         onboarding.StepChooseKnown,
		LearningLang: "en",
	}).Error; err != nil {
		t.Fatalf("failed to seed onboarding state: %v", err)
	}

	client := newMockClient()
	b := newTestTelegramBot(t, client)
	update := newTestCallbackUpdate(onboarding.BuildKnownCallback("ru"), 302, 302, 89)

	HandleOnboardingCallback(context.Background(), b, update)

	var state db.OnboardingState
	err := db.DB.Where("user_id = ?", 302).First(&state).Error
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected onboarding state to be cleared, got err=%v state=%+v", err, state)
	}

	sawUnavailable := false
	for _, req := range client.requests {
		if strings.Contains(req.path, "editMessageText") && strings.Contains(string(req.body), "unavailable") {
			sawUnavailable = true
			break
		}
	}
	if !sawUnavailable {
		t.Fatalf("expected unavailable onboarding message")
	}
}

func TestHandleOnboardingCallbackAtomicallyCompletesPendingReset(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)

	const userID int64 = 303
	now := time.Now().UTC()
	if err := db.DB.Create(&db.InitVocabulary{EN: "hello", RU: "привет"}).Error; err != nil {
		t.Fatalf("failed to seed init vocabulary: %v", err)
	}
	if err := db.DB.Create(&db.WordPair{
		UserID: userID, Word1: "old", Word2: "card", SrsState: "review", SrsDueAt: now,
	}).Error; err != nil {
		t.Fatalf("failed to seed old pair: %v", err)
	}
	if err := db.DB.Create(&db.UserSettings{UserID: userID, PairsToSend: 2}).Error; err != nil {
		t.Fatalf("failed to seed old settings: %v", err)
	}
	if err := db.DB.Create(&db.OnboardingState{
		UserID: userID, Step: onboarding.StepConfirmImport,
		LearningLang: "en", KnownLang: "ru", ResetPending: true,
	}).Error; err != nil {
		t.Fatalf("failed to seed onboarding state: %v", err)
	}

	client := newMockClient()
	b := newTestTelegramBot(t, client)
	update := newTestCallbackUpdate(onboarding.BuildConfirmCallback(), userID, userID, 90)

	HandleOnboardingCallback(context.Background(), b, update)

	var pairs []db.WordPair
	if err := db.DB.Where("user_id = ?", userID).Find(&pairs).Error; err != nil {
		t.Fatalf("failed to load user pairs: %v", err)
	}
	if len(pairs) != 1 || pairs[0].Word1 != "hello" || pairs[0].Word2 != "привет" {
		t.Fatalf("expected only replacement pair, got %+v", pairs)
	}

	var settings db.UserSettings
	if err := db.DB.Where("user_id = ?", userID).First(&settings).Error; err != nil {
		t.Fatalf("failed to load replacement settings: %v", err)
	}
	if settings.PairsToSend != 5 {
		t.Fatalf("expected default settings, got %+v", settings)
	}

	var state db.OnboardingState
	if err := db.DB.Where("user_id = ?", userID).First(&state).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected onboarding state to be cleared, got err=%v state=%+v", err, state)
	}
}
