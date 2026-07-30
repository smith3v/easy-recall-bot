package onboarding

import (
	"errors"
	"testing"
	"time"

	"github.com/smith3v/tg-word-reminder/pkg/db"
	"github.com/smith3v/tg-word-reminder/pkg/internal/testutil"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func TestProvisionUserVocabularyAndDefaults(t *testing.T) {
	testutil.SetupTestDB(t)

	rows := []db.InitVocabulary{
		{EN: "hello", RU: "привет", NL: "hallo", ES: "hola", DE: "hallo", FR: "salut"},
		{EN: "bye", RU: "", NL: "dag", ES: "adios", DE: "tschuss", FR: "au revoir"},
	}
	if err := db.DB.Create(&rows).Error; err != nil {
		t.Fatalf("failed to seed init rows: %v", err)
	}
	if _, err := Begin(5001); err != nil {
		t.Fatalf("failed to create onboarding state: %v", err)
	}
	if _, err := SetLearningLanguage(5001, "en"); err != nil {
		t.Fatalf("failed to select learning language: %v", err)
	}
	if _, err := SetKnownLanguage(5001, "ru"); err != nil {
		t.Fatalf("failed to select known language: %v", err)
	}

	inserted, err := ProvisionUserVocabularyAndDefaults(5001, "en", "ru")
	if err != nil {
		t.Fatalf("provision failed: %v", err)
	}
	if inserted != 1 {
		t.Fatalf("expected 1 inserted pair, got %d", inserted)
	}

	var pairCount int64
	if err := db.DB.Model(&db.WordPair{}).Where("user_id = ?", 5001).Count(&pairCount).Error; err != nil {
		t.Fatalf("failed to count pairs: %v", err)
	}
	if pairCount != 1 {
		t.Fatalf("expected 1 pair, got %d", pairCount)
	}

	var settings db.UserSettings
	if err := db.DB.Where("user_id = ?", 5001).First(&settings).Error; err != nil {
		t.Fatalf("failed to load settings: %v", err)
	}
	if settings.PairsToSend != 5 || !settings.ReminderMorning || !settings.ReminderAfternoon || !settings.ReminderEvening {
		t.Fatalf("unexpected settings defaults: %+v", settings)
	}
}

func TestProvisionReplacesPendingResetDataAndPreservesGameStatistics(t *testing.T) {
	testutil.SetupTestDB(t)

	userID := int64(7001)
	now := time.Now().UTC()
	if err := db.DB.Create(&db.InitVocabulary{EN: "hello", RU: "привет"}).Error; err != nil {
		t.Fatalf("failed to seed init vocabulary: %v", err)
	}
	if err := db.DB.Create(&db.WordPair{UserID: userID, Word1: "old", Word2: "card", SrsState: "review", SrsDueAt: now}).Error; err != nil {
		t.Fatalf("failed to seed pair: %v", err)
	}
	if err := db.DB.Create(&db.UserSettings{UserID: userID, PairsToSend: 3}).Error; err != nil {
		t.Fatalf("failed to seed settings: %v", err)
	}
	if err := db.DB.Create(&db.TrainingSession{UserID: userID, ChatID: userID, PairIDs: datatypes.JSON("[]"), LastActivityAt: now, ExpiresAt: now.Add(time.Hour)}).Error; err != nil {
		t.Fatalf("failed to seed training session: %v", err)
	}
	if err := db.DB.Create(&db.GameSession{UserID: userID, ChatID: userID, PairIDs: datatypes.JSON("[]"), LastActivityAt: now, ExpiresAt: now.Add(time.Hour)}).Error; err != nil {
		t.Fatalf("failed to seed game session: %v", err)
	}
	if err := db.DB.Create(&db.GameSessionStatistics{UserID: userID, SessionDate: now, StartedAt: now}).Error; err != nil {
		t.Fatalf("failed to seed game stats: %v", err)
	}
	if _, err := BeginReset(userID); err != nil {
		t.Fatalf("failed to begin reset: %v", err)
	}
	if _, err := SetLearningLanguage(userID, "en"); err != nil {
		t.Fatalf("failed to select learning language: %v", err)
	}
	if _, err := SetKnownLanguage(userID, "ru"); err != nil {
		t.Fatalf("failed to select known language: %v", err)
	}

	inserted, err := ProvisionUserVocabularyAndDefaults(userID, "en", "ru")
	if err != nil {
		t.Fatalf("replacement failed: %v", err)
	}
	if inserted != 1 {
		t.Fatalf("expected one replacement pair, got %d", inserted)
	}

	assertZeroRows(t, &db.TrainingSession{}, userID)
	assertZeroRows(t, &db.GameSession{}, userID)
	assertZeroRows(t, &db.OnboardingState{}, userID)

	var pairs []db.WordPair
	if err := db.DB.Where("user_id = ?", userID).Find(&pairs).Error; err != nil {
		t.Fatalf("failed to load replacement pairs: %v", err)
	}
	if len(pairs) != 1 || pairs[0].Word1 != "hello" || pairs[0].Word2 != "привет" {
		t.Fatalf("expected only the starter pair, got %+v", pairs)
	}

	var settings db.UserSettings
	if err := db.DB.Where("user_id = ?", userID).First(&settings).Error; err != nil {
		t.Fatalf("failed to load replacement settings: %v", err)
	}
	if settings.PairsToSend != 5 || !settings.ReminderMorning || !settings.ReminderAfternoon || !settings.ReminderEvening {
		t.Fatalf("unexpected replacement settings: %+v", settings)
	}

	var statsCount int64
	if err := db.DB.Model(&db.GameSessionStatistics{}).Where("user_id = ?", userID).Count(&statsCount).Error; err != nil {
		t.Fatalf("failed to count stats: %v", err)
	}
	if statsCount != 1 {
		t.Fatalf("expected game stats to remain, got %d", statsCount)
	}
}

func TestProvisionPendingResetRollsBackDeletionOnInsertFailure(t *testing.T) {
	testutil.SetupTestDB(t)

	userID := int64(7002)
	now := time.Now().UTC()
	oldPair := db.WordPair{UserID: userID, Word1: "old", Word2: "card", SrsState: "review", SrsDueAt: now}
	oldSettings := db.UserSettings{UserID: userID, PairsToSend: 3, ReminderMorning: true}
	oldSession := db.TrainingSession{
		UserID: userID, ChatID: userID, PairIDs: datatypes.JSON("[]"),
		LastActivityAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := db.DB.Create(&db.InitVocabulary{EN: "hello", RU: "привет"}).Error; err != nil {
		t.Fatalf("failed to seed init vocabulary: %v", err)
	}
	if err := db.DB.Create(&oldPair).Error; err != nil {
		t.Fatalf("failed to seed pair: %v", err)
	}
	if err := db.DB.Create(&oldSettings).Error; err != nil {
		t.Fatalf("failed to seed settings: %v", err)
	}
	if err := db.DB.Create(&oldSession).Error; err != nil {
		t.Fatalf("failed to seed training session: %v", err)
	}
	if _, err := BeginReset(userID); err != nil {
		t.Fatalf("failed to begin reset: %v", err)
	}
	if _, err := SetLearningLanguage(userID, "en"); err != nil {
		t.Fatalf("failed to select learning language: %v", err)
	}
	if _, err := SetKnownLanguage(userID, "ru"); err != nil {
		t.Fatalf("failed to select known language: %v", err)
	}

	forcedErr := errors.New("forced starter-card insert failure")
	if err := db.DB.Callback().Create().Before("gorm:create").Register("test:fail_starter_card", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "word_pairs" {
			tx.AddError(forcedErr)
		}
	}); err != nil {
		t.Fatalf("failed to register create callback: %v", err)
	}

	if _, err := ProvisionUserVocabularyAndDefaults(userID, "en", "ru"); !errors.Is(err, forcedErr) {
		t.Fatalf("expected forced insert failure, got %v", err)
	}

	var pair db.WordPair
	if err := db.DB.Where("user_id = ?", userID).First(&pair).Error; err != nil {
		t.Fatalf("expected old pair after rollback: %v", err)
	}
	if pair.Word1 != oldPair.Word1 || pair.Word2 != oldPair.Word2 {
		t.Fatalf("old pair changed after rollback: %+v", pair)
	}

	var settings db.UserSettings
	if err := db.DB.Where("user_id = ?", userID).First(&settings).Error; err != nil {
		t.Fatalf("expected old settings after rollback: %v", err)
	}
	if settings.PairsToSend != oldSettings.PairsToSend {
		t.Fatalf("old settings changed after rollback: %+v", settings)
	}

	var sessionCount int64
	if err := db.DB.Model(&db.TrainingSession{}).Where("user_id = ?", userID).Count(&sessionCount).Error; err != nil {
		t.Fatalf("failed to count sessions after rollback: %v", err)
	}
	if sessionCount != 1 {
		t.Fatalf("expected old session after rollback, got %d", sessionCount)
	}

	state, err := GetState(userID)
	if err != nil {
		t.Fatalf("failed to load onboarding state after rollback: %v", err)
	}
	if state == nil || !state.ResetPending {
		t.Fatalf("expected pending reset state after rollback, got %+v", state)
	}
}

func TestResetPendingSurvivesWizardNavigation(t *testing.T) {
	testutil.SetupTestDB(t)

	const userID int64 = 7003
	if _, err := BeginReset(userID); err != nil {
		t.Fatalf("failed to begin reset: %v", err)
	}
	if _, err := SetLearningLanguage(userID, "en"); err != nil {
		t.Fatalf("failed to select learning language: %v", err)
	}
	if _, err := BackToLearning(userID); err != nil {
		t.Fatalf("failed to return to learning language: %v", err)
	}
	if _, err := SetLearningLanguage(userID, "en"); err != nil {
		t.Fatalf("failed to reselect learning language: %v", err)
	}
	if _, err := SetKnownLanguage(userID, "ru"); err != nil {
		t.Fatalf("failed to select known language: %v", err)
	}
	state, err := BackToKnown(userID)
	if err != nil {
		t.Fatalf("failed to return to known language: %v", err)
	}
	if !state.ResetPending {
		t.Fatalf("expected reset marker to survive navigation, got %+v", state)
	}
}

func TestHasInitVocabularyData(t *testing.T) {
	testutil.SetupTestDB(t)

	hasData, err := HasInitVocabularyData()
	if err != nil {
		t.Fatalf("failed to check init vocabulary data: %v", err)
	}
	if hasData {
		t.Fatalf("expected no init vocabulary data")
	}

	if err := db.DB.Create(&db.InitVocabulary{EN: "hello", RU: "привет", NL: "hallo", ES: "hola", DE: "hallo", FR: "bonjour"}).Error; err != nil {
		t.Fatalf("failed to seed init vocabulary: %v", err)
	}

	hasData, err = HasInitVocabularyData()
	if err != nil {
		t.Fatalf("failed to re-check init vocabulary data: %v", err)
	}
	if !hasData {
		t.Fatalf("expected init vocabulary data to be present")
	}
}

func TestEnsureDefaultSettings(t *testing.T) {
	testutil.SetupTestDB(t)

	if err := EnsureDefaultSettings(9001); err != nil {
		t.Fatalf("failed to ensure settings: %v", err)
	}

	var settings db.UserSettings
	if err := db.DB.Where("user_id = ?", 9001).First(&settings).Error; err != nil {
		t.Fatalf("failed to load settings: %v", err)
	}
	if settings.PairsToSend != 5 || !settings.ReminderMorning || !settings.ReminderAfternoon || !settings.ReminderEvening {
		t.Fatalf("unexpected ensured settings: %+v", settings)
	}

	if err := EnsureDefaultSettings(9001); err != nil {
		t.Fatalf("failed to ensure settings second time: %v", err)
	}
	var count int64
	if err := db.DB.Model(&db.UserSettings{}).Where("user_id = ?", 9001).Count(&count).Error; err != nil {
		t.Fatalf("failed to count settings rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one settings row, got %d", count)
	}
}

func assertZeroRows(t *testing.T, model any, userID int64) {
	t.Helper()
	var count int64
	if err := db.DB.Model(model).Where("user_id = ?", userID).Count(&count).Error; err != nil {
		t.Fatalf("failed to count rows for %T: %v", model, err)
	}
	if count != 0 {
		t.Fatalf("expected no rows for %T, got %d", model, count)
	}
}
