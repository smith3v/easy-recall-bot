package db

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMigrateNewRanks(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite database: %v", err)
	}
	if err := gdb.AutoMigrate(&WordPair{}, &InitVocabulary{}, &UserSettings{}, &OnboardingState{}, &GameSessionStatistics{}, &TrainingSession{}, &GameSession{}); err != nil {
		t.Fatalf("failed to migrate schema: %v", err)
	}

	DB = gdb

	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("failed to access underlying DB: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Fatalf("failed to close database: %v", err)
		}
		DB = nil
	})

	pair := WordPair{
		UserID:   12,
		Word1:    "alpha",
		Word2:    "beta",
		SrsState: "new",
		SrsDueAt: time.Now().UTC(),
	}
	if err := DB.Create(&pair).Error; err != nil {
		t.Fatalf("failed to create word pair: %v", err)
	}

	if err := migrateNewRanks(DB); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	var updated WordPair
	if err := DB.First(&updated, pair.ID).Error; err != nil {
		t.Fatalf("failed to reload word pair: %v", err)
	}
	if updated.SrsNewRank == 0 {
		t.Fatalf("expected srs_new_rank to be set, got %+v", updated)
	}
}

func TestMigrateSessionTables(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite database: %v", err)
	}
	if err := gdb.AutoMigrate(&WordPair{}, &InitVocabulary{}, &UserSettings{}, &OnboardingState{}, &GameSessionStatistics{}, &TrainingSession{}, &GameSession{}); err != nil {
		t.Fatalf("failed to migrate schema: %v", err)
	}

	if !gdb.Migrator().HasTable(&TrainingSession{}) {
		t.Fatalf("expected training_sessions table to exist")
	}
	if !gdb.Migrator().HasTable(&GameSession{}) {
		t.Fatalf("expected game_sessions table to exist")
	}
}

func TestMigrateUserSettingsSnoozeColumn(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite database: %v", err)
	}
	if err := gdb.AutoMigrate(&WordPair{}, &InitVocabulary{}, &UserSettings{}, &OnboardingState{}, &GameSessionStatistics{}, &TrainingSession{}, &GameSession{}); err != nil {
		t.Fatalf("failed to migrate schema: %v", err)
	}

	if !gdb.Migrator().HasColumn(&UserSettings{}, "reminder_snoozed_until") {
		t.Fatalf("expected reminder_snoozed_until column to exist")
	}
}

func TestMigrateOnboardingResetPendingColumn(t *testing.T) {
	gdb, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite database: %v", err)
	}
	if err := gdb.AutoMigrate(&OnboardingState{}); err != nil {
		t.Fatalf("failed to migrate onboarding state: %v", err)
	}

	if !gdb.Migrator().HasColumn(&OnboardingState{}, "reset_pending") {
		t.Fatalf("expected reset_pending column to exist")
	}
}
