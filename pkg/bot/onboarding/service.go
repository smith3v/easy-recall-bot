package onboarding

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/smith3v/tg-word-reminder/pkg/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ResetPhrase = "RESET MY DATA"

	StepChooseLearning = "choose_learning"
	StepChooseKnown    = "choose_known"
	StepConfirmImport  = "confirm_import"
)

var (
	errInvalidLanguagePair = errors.New("invalid language pair")
	errInvalidState        = errors.New("invalid onboarding state")
	ErrNoEligiblePairs     = errors.New("no eligible pairs found")

	srsNewRankRand = rand.New(rand.NewSource(time.Now().UnixNano()))
	srsNewRankMu   sync.Mutex
)

func GetState(userID int64) (*db.OnboardingState, error) {
	var state db.OnboardingState
	if err := db.DB.Where("user_id = ?", userID).First(&state).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &state, nil
}

func Begin(userID int64) (*db.OnboardingState, error) {
	return begin(userID, false)
}

func BeginReset(userID int64) (*db.OnboardingState, error) {
	return begin(userID, true)
}

func begin(userID int64, resetPending bool) (*db.OnboardingState, error) {
	state, err := ensureState(userID)
	if err != nil {
		return nil, err
	}
	state.Step = StepChooseLearning
	state.LearningLang = ""
	state.KnownLang = ""
	state.AwaitingResetPhrase = false
	state.ResetPending = resetPending
	if err := db.DB.Save(state).Error; err != nil {
		return nil, err
	}
	return state, nil
}

func SetAwaitingResetPhrase(userID int64) error {
	state, err := ensureState(userID)
	if err != nil {
		return err
	}
	state.Step = ""
	state.LearningLang = ""
	state.KnownLang = ""
	state.AwaitingResetPhrase = true
	state.ResetPending = false
	return db.DB.Save(state).Error
}

func SetLearningLanguage(userID int64, code string) (*db.OnboardingState, error) {
	if !IsSupportedLanguage(code) {
		return nil, errInvalidLanguagePair
	}
	state, err := ensureState(userID)
	if err != nil {
		return nil, err
	}
	state.Step = StepChooseKnown
	state.LearningLang = code
	state.KnownLang = ""
	state.AwaitingResetPhrase = false
	if err := db.DB.Save(state).Error; err != nil {
		return nil, err
	}
	return state, nil
}

func SetKnownLanguage(userID int64, code string) (*db.OnboardingState, error) {
	if !IsSupportedLanguage(code) {
		return nil, errInvalidLanguagePair
	}
	state, err := ensureState(userID)
	if err != nil {
		return nil, err
	}
	if state.LearningLang == "" || state.LearningLang == code {
		return nil, errInvalidLanguagePair
	}
	state.Step = StepConfirmImport
	state.KnownLang = code
	state.AwaitingResetPhrase = false
	if err := db.DB.Save(state).Error; err != nil {
		return nil, err
	}
	return state, nil
}

func BackToKnown(userID int64) (*db.OnboardingState, error) {
	state, err := ensureState(userID)
	if err != nil {
		return nil, err
	}
	if state.LearningLang == "" {
		return nil, errInvalidLanguagePair
	}
	state.Step = StepChooseKnown
	state.KnownLang = ""
	state.AwaitingResetPhrase = false
	if err := db.DB.Save(state).Error; err != nil {
		return nil, err
	}
	return state, nil
}

func BackToLearning(userID int64) (*db.OnboardingState, error) {
	state, err := ensureState(userID)
	if err != nil {
		return nil, err
	}
	state.Step = StepChooseLearning
	state.LearningLang = ""
	state.KnownLang = ""
	state.AwaitingResetPhrase = false
	if err := db.DB.Save(state).Error; err != nil {
		return nil, err
	}
	return state, nil
}

func ClearState(userID int64) error {
	return db.DB.Where("user_id = ?", userID).Delete(&db.OnboardingState{}).Error
}

func HasExistingUserData(userID int64) (bool, error) {
	checks := []any{
		&db.WordPair{},
		&db.UserSettings{},
		&db.TrainingSession{},
		&db.GameSession{},
	}
	for _, model := range checks {
		var count int64
		if err := db.DB.Model(model).Where("user_id = ?", userID).Limit(1).Count(&count).Error; err != nil {
			return false, err
		}
		if count > 0 {
			return true, nil
		}
	}
	return false, nil
}

func HasInitVocabularyData() (bool, error) {
	var count int64
	if err := db.DB.Model(&db.InitVocabulary{}).Limit(1).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func EnsureDefaultSettings(userID int64) error {
	var settings db.UserSettings
	if err := db.DB.Where("user_id = ?", userID).First(&settings).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		settings = db.UserSettings{
			UserID:                 userID,
			PairsToSend:            5,
			ReminderMorning:        true,
			ReminderAfternoon:      true,
			ReminderEvening:        true,
			TimezoneOffsetHours:    0,
			MissedTrainingSessions: 0,
			TrainingPaused:         false,
		}
		return db.DB.Create(&settings).Error
	}
	return nil
}

func CountEligiblePairs(learningCode, knownCode string) (int, error) {
	if err := validateLanguagePair(learningCode, knownCode); err != nil {
		return 0, err
	}
	var count int64
	query := fmt.Sprintf("TRIM(%s) <> '' AND TRIM(%s) <> ''", learningCode, knownCode)
	if err := db.DB.Model(&db.InitVocabulary{}).Where(query).Count(&count).Error; err != nil {
		return 0, err
	}
	return int(count), nil
}

func ProvisionUserVocabularyAndDefaults(userID int64, learningCode, knownCode string) (int, error) {
	if err := validateLanguagePair(learningCode, knownCode); err != nil {
		return 0, err
	}

	inserted := 0
	err := db.DB.Transaction(func(tx *gorm.DB) error {
		state, err := stateForProvisionTx(tx, userID, learningCode, knownCode)
		if err != nil {
			return err
		}

		count, err := countEligiblePairsTx(tx, learningCode, knownCode)
		if err != nil {
			return err
		}
		if count == 0 {
			return ErrNoEligiblePairs
		}

		var initRows []db.InitVocabulary
		if err := tx.Find(&initRows).Error; err != nil {
			return err
		}

		if state.ResetPending {
			if err := deleteReplaceableUserDataTx(tx, userID); err != nil {
				return err
			}
		}

		now := time.Now().UTC()
		for _, row := range initRows {
			word1 := strings.TrimSpace(valueForCode(row, learningCode))
			word2 := strings.TrimSpace(valueForCode(row, knownCode))
			if word1 == "" || word2 == "" {
				continue
			}

			result := tx.Model(&db.WordPair{}).
				Where("user_id = ? AND word1 = ?", userID, word1).
				Update("word2", word2)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected > 0 {
				continue
			}

			pair := db.WordPair{
				UserID:          userID,
				Word1:           word1,
				Word2:           word2,
				SrsState:        "new",
				SrsDueAt:        now,
				SrsIntervalDays: 0,
				SrsEase:         2.5,
				SrsStep:         0,
				SrsNewRank:      randomSrsNewRank(),
			}
			if err := tx.Create(&pair).Error; err != nil {
				return err
			}
			inserted++
		}

		if err := tx.Where("user_id = ?", userID).Delete(&db.UserSettings{}).Error; err != nil {
			return err
		}
		settings := db.UserSettings{
			UserID:                 userID,
			PairsToSend:            5,
			ReminderMorning:        true,
			ReminderAfternoon:      true,
			ReminderEvening:        true,
			TimezoneOffsetHours:    0,
			MissedTrainingSessions: 0,
			TrainingPaused:         false,
		}
		if err := tx.Create(&settings).Error; err != nil {
			return err
		}

		if err := tx.Where("user_id = ?", userID).Delete(&db.OnboardingState{}).Error; err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		return 0, err
	}
	return inserted, nil
}

func stateForProvisionTx(tx *gorm.DB, userID int64, learningCode, knownCode string) (*db.OnboardingState, error) {
	var state db.OnboardingState
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("user_id = ?", userID).
		First(&state).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errInvalidState
		}
		return nil, err
	}
	if state.Step != StepConfirmImport ||
		state.LearningLang != learningCode ||
		state.KnownLang != knownCode ||
		state.AwaitingResetPhrase {
		return nil, errInvalidState
	}
	return &state, nil
}

func deleteReplaceableUserDataTx(tx *gorm.DB, userID int64) error {
	models := []any{
		&db.WordPair{},
		&db.UserSettings{},
		&db.TrainingSession{},
		&db.GameSession{},
	}
	for _, model := range models {
		if err := tx.Where("user_id = ?", userID).Delete(model).Error; err != nil {
			return err
		}
	}
	return nil
}

func countEligiblePairsTx(tx *gorm.DB, learningCode, knownCode string) (int, error) {
	var count int64
	query := fmt.Sprintf("TRIM(%s) <> '' AND TRIM(%s) <> ''", learningCode, knownCode)
	if err := tx.Model(&db.InitVocabulary{}).Where(query).Count(&count).Error; err != nil {
		return 0, err
	}
	return int(count), nil
}

func validateLanguagePair(learningCode, knownCode string) error {
	if !IsSupportedLanguage(learningCode) || !IsSupportedLanguage(knownCode) || learningCode == knownCode {
		return errInvalidLanguagePair
	}
	return nil
}

func ensureState(userID int64) (*db.OnboardingState, error) {
	var state db.OnboardingState
	if err := db.DB.Where("user_id = ?", userID).First(&state).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		state = db.OnboardingState{UserID: userID}
		if err := db.DB.Create(&state).Error; err != nil {
			return nil, err
		}
	}
	return &state, nil
}

func valueForCode(row db.InitVocabulary, code string) string {
	switch code {
	case "en":
		return row.EN
	case "ru":
		return row.RU
	case "nl":
		return row.NL
	case "es":
		return row.ES
	case "de":
		return row.DE
	case "fr":
		return row.FR
	default:
		return ""
	}
}

func randomSrsNewRank() int {
	srsNewRankMu.Lock()
	defer srsNewRankMu.Unlock()
	return srsNewRankRand.Intn(db.SrsNewRankMax) + 1
}
