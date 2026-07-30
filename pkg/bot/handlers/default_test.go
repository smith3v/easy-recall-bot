package handlers

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/smith3v/tg-word-reminder/pkg/bot/game"
	"github.com/smith3v/tg-word-reminder/pkg/db"
	"github.com/smith3v/tg-word-reminder/pkg/internal/testutil"
	"github.com/smith3v/tg-word-reminder/pkg/logger"
)

func TestDefaultHandlerSendsHelpForText(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)

	client := newMockClient()
	b := newTestTelegramBot(t, client)
	update := newTestUpdate("hello", 100)

	DefaultHandler(context.Background(), b, update)

	got := client.lastMessageText(t)
	if !strings.Contains(got, "Commands:") {
		t.Fatalf("expected commands message, got %q", got)
	}
}

func TestDefaultHandlerConsumesResetPhraseFlow(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)
	if err := db.DB.Create(&db.InitVocabulary{EN: "hello", RU: "привет", NL: "hallo", ES: "hola", DE: "hallo", FR: "bonjour"}).Error; err != nil {
		t.Fatalf("failed to seed init vocabulary: %v", err)
	}

	if err := db.DB.Create(&db.WordPair{UserID: 200, Word1: "hola", Word2: "hello"}).Error; err != nil {
		t.Fatalf("failed to seed pair: %v", err)
	}
	if err := db.DB.Create(&db.OnboardingState{UserID: 200, AwaitingResetPhrase: true}).Error; err != nil {
		t.Fatalf("failed to seed onboarding state: %v", err)
	}

	client := newMockClient()
	b := newTestTelegramBot(t, client)
	update := newTestUpdate("RESET MY DATA", 200)

	DefaultHandler(context.Background(), b, update)

	var count int64
	if err := db.DB.Model(&db.WordPair{}).Where("user_id = ?", 200).Count(&count).Error; err != nil {
		t.Fatalf("failed to count pairs: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected pairs to remain until initialization, got %d", count)
	}

	var state db.OnboardingState
	if err := db.DB.Where("user_id = ?", 200).First(&state).Error; err != nil {
		t.Fatalf("expected onboarding state to exist after restart: %v", err)
	}
	if state.Step != "choose_learning" || state.AwaitingResetPhrase || !state.ResetPending {
		t.Fatalf("expected onboarding wizard step after reset, got %+v", state)
	}

	got := client.lastMessageText(t)
	if !strings.Contains(got, "Choose the language you are learning") {
		t.Fatalf("expected onboarding prompt after reset, got %q", got)
	}
}

func TestDefaultHandlerRequiresExactResetPhrase(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)

	if err := db.DB.Create(&db.WordPair{UserID: 201, Word1: "hola", Word2: "hello"}).Error; err != nil {
		t.Fatalf("failed to seed pair: %v", err)
	}
	if err := db.DB.Create(&db.OnboardingState{UserID: 201, AwaitingResetPhrase: true}).Error; err != nil {
		t.Fatalf("failed to seed onboarding state: %v", err)
	}

	client := newMockClient()
	b := newTestTelegramBot(t, client)
	update := newTestUpdate(" RESET MY DATA ", 201)

	DefaultHandler(context.Background(), b, update)

	var count int64
	if err := db.DB.Model(&db.WordPair{}).Where("user_id = ?", 201).Count(&count).Error; err != nil {
		t.Fatalf("failed to count pairs: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected pairs to remain when phrase is not exact, got %d", count)
	}

	var state db.OnboardingState
	if err := db.DB.Where("user_id = ?", 201).First(&state).Error; err != nil {
		t.Fatalf("expected onboarding state to remain: %v", err)
	}
	if !state.AwaitingResetPhrase {
		t.Fatalf("expected still awaiting phrase, got %+v", state)
	}

	got := client.lastMessageText(t)
	if !strings.Contains(got, "does not match") {
		t.Fatalf("expected mismatch message, got %q", got)
	}
}

func TestDefaultHandlerDoesNotResetWhenInitVocabularyMissing(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)

	if err := db.DB.Create(&db.WordPair{UserID: 203, Word1: "hola", Word2: "hello"}).Error; err != nil {
		t.Fatalf("failed to seed pair: %v", err)
	}
	if err := db.DB.Create(&db.OnboardingState{UserID: 203, AwaitingResetPhrase: true}).Error; err != nil {
		t.Fatalf("failed to seed onboarding state: %v", err)
	}

	client := newMockClient()
	b := newTestTelegramBot(t, client)
	update := newTestUpdate("RESET MY DATA", 203)

	DefaultHandler(context.Background(), b, update)

	var count int64
	if err := db.DB.Model(&db.WordPair{}).Where("user_id = ?", 203).Count(&count).Error; err != nil {
		t.Fatalf("failed to count pairs: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected pairs to remain when init vocabulary is missing, got %d", count)
	}

	var state db.OnboardingState
	if err := db.DB.Where("user_id = ?", 203).First(&state).Error; err != nil {
		t.Fatalf("expected onboarding state to remain: %v", err)
	}
	if !state.AwaitingResetPhrase {
		t.Fatalf("expected still awaiting phrase, got %+v", state)
	}

	got := client.lastMessageText(t)
	if !strings.Contains(got, "unavailable") || !strings.Contains(got, "unchanged") {
		t.Fatalf("expected unavailable warning, got %q", got)
	}
}

func TestDefaultHandlerPrioritizesGameTextOverResetPhraseInterception(t *testing.T) {
	testutil.SetupTestDB(t)
	logger.SetLogLevel(logger.ERROR)

	game.ResetDefaultManager(func() time.Time { return time.Now().UTC() })
	t.Cleanup(func() { game.ResetDefaultManager(nil) })

	pair := db.WordPair{
		UserID:   202,
		Word1:    "hola",
		Word2:    "hello",
		SrsState: "new",
		SrsDueAt: time.Now().UTC(),
	}
	if err := db.DB.Create(&pair).Error; err != nil {
		t.Fatalf("failed to seed pair: %v", err)
	}
	if err := db.DB.Create(&db.OnboardingState{UserID: 202, AwaitingResetPhrase: true}).Error; err != nil {
		t.Fatalf("failed to seed onboarding state: %v", err)
	}

	session := game.DefaultManager.StartOrRestart(202, 202, []db.WordPair{pair})
	if session == nil || session.CurrentCard() == nil {
		t.Fatalf("expected active game session")
	}
	game.DefaultManager.SetCurrentMessageID(session, 123)

	client := newMockClient()
	b := newTestTelegramBot(t, client)
	update := newTestUpdate(session.CurrentCard().Expected, 202)

	DefaultHandler(context.Background(), b, update)

	for _, req := range client.requests {
		if strings.Contains(string(req.body), "does not match") {
			t.Fatalf("expected no reset mismatch message while game session is active")
		}
	}

	sawEditMessage := false
	for _, req := range client.requests {
		if strings.HasSuffix(req.path, "/editMessageText") {
			sawEditMessage = true
			break
		}
	}
	if !sawEditMessage {
		t.Fatalf("expected game flow to edit prompt message")
	}
}
