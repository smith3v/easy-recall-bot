package stats

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/smith3v/tg-word-reminder/pkg/db"
	"github.com/smith3v/tg-word-reminder/pkg/internal/testutil"
)

type fakeBeginner struct {
	tx    statsTransaction
	err   error
	calls int
}

func (b *fakeBeginner) begin(context.Context) (statsTransaction, error) {
	b.calls++
	return b.tx, b.err
}

type fakeTransaction struct {
	locked      bool
	lockErr     error
	report      Report
	collectErr  error
	commitErr   error
	rollbackErr error
	collectFn   func(context.Context, int64) (Report, error)

	lockCalls     int
	collectCalls  int
	commitCalls   int
	rollbackCalls int
	collectedUser int64
}

func (tx *fakeTransaction) tryLock(context.Context) (bool, error) {
	tx.lockCalls++
	return tx.locked, tx.lockErr
}

func (tx *fakeTransaction) collect(ctx context.Context, userID int64) (Report, error) {
	tx.collectCalls++
	tx.collectedUser = userID
	if tx.collectFn != nil {
		return tx.collectFn(ctx, userID)
	}
	return tx.report, tx.collectErr
}

func (tx *fakeTransaction) commit() error {
	tx.commitCalls++
	return tx.commitErr
}

func (tx *fakeTransaction) rollback() error {
	tx.rollbackCalls++
	return tx.rollbackErr
}

func TestPostgresStarterCommitsAndReleasesSuccessfulJob(t *testing.T) {
	want := Report{Vocabulary: VocabularyReport{Total: 7}}
	tx := &fakeTransaction{locked: true, report: want}
	starter := &PostgresStarter{beginner: &fakeBeginner{tx: tx}}

	job, err := starter.Start(context.Background(), 1234)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	got, err := job.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Collect report = %+v, want %+v", got, want)
	}
	if tx.lockCalls != 1 || tx.collectCalls != 1 || tx.commitCalls != 1 || tx.rollbackCalls != 0 {
		t.Fatalf("unexpected transaction calls: %+v", tx)
	}
	if tx.collectedUser != 1234 {
		t.Fatalf("collected user = %d, want 1234", tx.collectedUser)
	}
}

func TestPostgresStarterContentionFailsFastAndRollsBack(t *testing.T) {
	tx := &fakeTransaction{locked: false}
	starter := &PostgresStarter{beginner: &fakeBeginner{tx: tx}}

	job, err := starter.Start(context.Background(), 1234)

	if job != nil {
		t.Fatalf("expected no job during contention")
	}
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("Start error = %v, want ErrBusy", err)
	}
	if tx.collectCalls != 0 || tx.commitCalls != 0 || tx.rollbackCalls != 1 {
		t.Fatalf("unexpected transaction calls: %+v", tx)
	}
}

func TestPostgresStarterRollsBackLockError(t *testing.T) {
	lockErr := errors.New("lock query failed")
	tx := &fakeTransaction{lockErr: lockErr}
	starter := &PostgresStarter{beginner: &fakeBeginner{tx: tx}}

	_, err := starter.Start(context.Background(), 1234)

	if !errors.Is(err, lockErr) {
		t.Fatalf("Start error = %v, want lock error", err)
	}
	if tx.rollbackCalls != 1 {
		t.Fatalf("rollback calls = %d, want 1", tx.rollbackCalls)
	}
}

func TestPostgresStarterReturnsBeginError(t *testing.T) {
	beginErr := errors.New("begin failed")
	beginner := &fakeBeginner{err: beginErr}
	starter := &PostgresStarter{beginner: beginner}

	_, err := starter.Start(context.Background(), 1234)

	if !errors.Is(err, beginErr) {
		t.Fatalf("Start error = %v, want begin error", err)
	}
}

func TestStatisticsJobRollsBackQueryFailure(t *testing.T) {
	queryErr := errors.New("query failed")
	tx := &fakeTransaction{locked: true, collectErr: queryErr}
	starter := &PostgresStarter{beginner: &fakeBeginner{tx: tx}}
	job, err := starter.Start(context.Background(), 4)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	_, err = job.Collect(context.Background())

	if !errors.Is(err, queryErr) {
		t.Fatalf("Collect error = %v, want query error", err)
	}
	if tx.commitCalls != 0 || tx.rollbackCalls != 1 {
		t.Fatalf("unexpected transaction calls: %+v", tx)
	}
}

func TestStatisticsJobRollsBackCommitFailure(t *testing.T) {
	commitErr := errors.New("commit failed")
	tx := &fakeTransaction{locked: true, commitErr: commitErr}
	starter := &PostgresStarter{beginner: &fakeBeginner{tx: tx}}
	job, err := starter.Start(context.Background(), 4)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	_, err = job.Collect(context.Background())

	if !errors.Is(err, commitErr) {
		t.Fatalf("Collect error = %v, want commit error", err)
	}
	if tx.commitCalls != 1 || tx.rollbackCalls != 1 {
		t.Fatalf("unexpected transaction calls: %+v", tx)
	}
}

func TestStatisticsJobRollsBackOnTimeout(t *testing.T) {
	tx := &fakeTransaction{
		locked: true,
		collectFn: func(ctx context.Context, _ int64) (Report, error) {
			<-ctx.Done()
			return Report{}, ctx.Err()
		},
	}
	starter := &PostgresStarter{beginner: &fakeBeginner{tx: tx}}
	job, err := starter.Start(context.Background(), 4)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err = job.Collect(ctx)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Collect error = %v, want deadline exceeded", err)
	}
	if tx.commitCalls != 0 || tx.rollbackCalls != 1 {
		t.Fatalf("unexpected transaction calls: %+v", tx)
	}
}

func TestStatisticsJobRollsBackOnCancellation(t *testing.T) {
	tx := &fakeTransaction{
		locked: true,
		collectFn: func(ctx context.Context, _ int64) (Report, error) {
			<-ctx.Done()
			return Report{}, ctx.Err()
		},
	}
	starter := &PostgresStarter{beginner: &fakeBeginner{tx: tx}}
	job, err := starter.Start(context.Background(), 4)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = job.Collect(ctx)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Collect error = %v, want cancellation", err)
	}
	if tx.commitCalls != 0 || tx.rollbackCalls != 1 {
		t.Fatalf("unexpected transaction calls: %+v", tx)
	}
}

func TestCollectReportMetricsAndUserIsolation(t *testing.T) {
	testutil.SetupTestDB(t)
	userID := int64(1001)
	otherUserID := int64(2002)
	snapshotAt := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	cutoff7Days := snapshotAt.AddDate(0, 0, -7)
	cutoff30Days := snapshotAt.AddDate(0, 0, -30)

	pairs := []db.WordPair{
		{
			UserID:   userID,
			Word1:    "new due",
			Word2:    "untouched",
			SrsState: "new",
			SrsDueAt: snapshotAt,
		},
		{
			UserID:   userID,
			Word1:    "new future",
			Word2:    "untouched",
			SrsState: "new",
			SrsDueAt: snapshotAt.Add(time.Hour),
		},
		{
			UserID:            userID,
			Word1:             "learning due",
			Word2:             "seen",
			SrsState:          "learning",
			SrsDueAt:          snapshotAt,
			SrsLastReviewedAt: timePointer(cutoff7Days),
			SrsIntervalDays:   5,
			SrsEase:           2.2,
			SrsReps:           1,
			SrsLapses:         2,
		},
		{
			UserID:            userID,
			Word1:             "learning future",
			Word2:             "seen",
			SrsState:          "learning",
			SrsDueAt:          snapshotAt.Add(time.Hour),
			SrsLastReviewedAt: timePointer(snapshotAt.AddDate(0, 0, -20)),
			SrsReps:           2,
		},
		{
			UserID:            userID,
			Word1:             "review due",
			Word2:             "mature",
			SrsState:          "review",
			SrsDueAt:          snapshotAt.Add(-time.Minute),
			SrsLastReviewedAt: timePointer(cutoff30Days),
			SrsIntervalDays:   21,
			SrsEase:           2.4,
			SrsReps:           3,
			SrsLapses:         1,
		},
		{
			UserID:            userID,
			Word1:             "review future",
			Word2:             "not mature",
			SrsState:          "review",
			SrsDueAt:          snapshotAt.Add(time.Hour),
			SrsLastReviewedAt: timePointer(snapshotAt.AddDate(0, 0, -31)),
			SrsIntervalDays:   20,
			SrsReps:           4,
		},
		{
			UserID:            otherUserID,
			Word1:             "other user",
			Word2:             "excluded",
			SrsState:          "review",
			SrsDueAt:          snapshotAt.Add(-time.Hour),
			SrsLastReviewedAt: timePointer(snapshotAt),
			SrsIntervalDays:   99,
			SrsReps:           999,
			SrsLapses:         99,
		},
	}
	if err := db.DB.Create(&pairs).Error; err != nil {
		t.Fatalf("failed to seed word pairs: %v", err)
	}

	endedAt := snapshotAt.AddDate(0, 0, -3)
	finishedReason := "finished"
	timeoutReason := "timeout"
	games := []db.GameSessionStatistics{
		{
			UserID:       userID,
			SessionDate:  endedAt,
			StartedAt:    endedAt,
			EndedAt:      timePointer(endedAt.Add(time.Minute)),
			EndedReason:  &finishedReason,
			CorrectCount: 3,
			AttemptCount: 4,
		},
		{
			UserID:       userID,
			SessionDate:  snapshotAt.AddDate(0, 0, -1),
			StartedAt:    snapshotAt.AddDate(0, 0, -1),
			CorrectCount: 1,
			AttemptCount: 2,
		},
		{
			UserID:       userID,
			SessionDate:  snapshotAt.AddDate(0, 0, -2),
			StartedAt:    snapshotAt.AddDate(0, 0, -2),
			EndedAt:      timePointer(snapshotAt.AddDate(0, 0, -2).Add(time.Minute)),
			EndedReason:  &timeoutReason,
			CorrectCount: 1,
			AttemptCount: 1,
		},
		{
			UserID:       otherUserID,
			SessionDate:  snapshotAt,
			StartedAt:    snapshotAt,
			EndedAt:      timePointer(snapshotAt),
			CorrectCount: 100,
			AttemptCount: 100,
		},
	}
	if err := db.DB.Create(&games).Error; err != nil {
		t.Fatalf("failed to seed game history: %v", err)
	}

	report, err := collectReportAt(context.Background(), db.DB, userID, snapshotRow{
		SnapshotAt:     snapshotAt,
		Cutoff7Days:    cutoff7Days,
		Cutoff30Days:   cutoff30Days,
		UTCOffsetHours: 4,
	})
	if err != nil {
		t.Fatalf("collectReportAt returned error: %v", err)
	}

	wantVocabulary := VocabularyReport{
		Total:           6,
		New:             2,
		NewPercent:      33.3,
		Learning:        2,
		LearningPercent: 33.3,
		Review:          2,
		ReviewPercent:   33.3,
		LearnedDue:      2,
	}
	if !reflect.DeepEqual(report.Vocabulary, wantVocabulary) {
		t.Fatalf("vocabulary = %+v, want %+v", report.Vocabulary, wantVocabulary)
	}

	wantReview := ReviewReport{
		Seen:           4,
		Graduated:      3,
		Mature:         1,
		Reviewed7Days:  1,
		Reviewed30Days: 3,
		Repetitions:    10,
		Lapses:         3,
	}
	if !reflect.DeepEqual(report.Review, wantReview) {
		t.Fatalf("review = %+v, want %+v", report.Review, wantReview)
	}

	if report.Games.Started != 3 ||
		report.Games.Completed != 1 ||
		report.Games.Correct != 5 ||
		report.Games.Attempts != 7 ||
		report.Games.Accuracy != 71.4 {
		t.Fatalf("unexpected game report: %+v", report.Games)
	}
	wantLastPlayed := snapshotAt.AddDate(0, 0, -1)
	if report.Games.LastPlayed == nil || !report.Games.LastPlayed.Equal(wantLastPlayed) {
		t.Fatalf("last played = %v, want %v", report.Games.LastPlayed, wantLastPlayed)
	}

	if len(report.Challenging) != 2 ||
		report.Challenging[0].Word1 != "learning due" ||
		report.Challenging[1].Word1 != "review due" {
		t.Fatalf("unexpected challenging cards: %+v", report.Challenging)
	}
}

func TestCollectReportUsesDatabaseSnapshotAndLatestTimezone(t *testing.T) {
	testutil.SetupTestDB(t)
	userID := int64(1002)
	settings := []db.UserSettings{
		{UserID: userID, TimezoneOffsetHours: -5},
		{UserID: userID, TimezoneOffsetHours: 4},
	}
	if err := db.DB.Create(&settings).Error; err != nil {
		t.Fatalf("failed to seed settings: %v", err)
	}

	before := time.Now().UTC().Add(-time.Second)
	report, err := collectReport(context.Background(), db.DB, userID)
	after := time.Now().UTC().Add(time.Second)
	if err != nil {
		t.Fatalf("collectReport returned error: %v", err)
	}

	if report.SnapshotAt.Before(before) || report.SnapshotAt.After(after) {
		t.Fatalf("snapshot = %v, want between %v and %v", report.SnapshotAt, before, after)
	}
	if report.UTCOffsetHours != 4 {
		t.Fatalf("UTC offset = %d, want latest value 4", report.UTCOffsetHours)
	}
	if report.Vocabulary.Total != 0 || report.Games.Started != 0 {
		t.Fatalf("expected empty report, got %+v", report)
	}
	if report.Vocabulary.NewPercent != 0 || report.Games.Accuracy != 0 {
		t.Fatalf("expected zero-denominator percentages, got %+v", report)
	}
}

func TestCollectReportGameAccuracyWithZeroAttempts(t *testing.T) {
	testutil.SetupTestDB(t)
	userID := int64(1003)
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	if err := db.DB.Create(&db.GameSessionStatistics{
		UserID:      userID,
		SessionDate: now,
		StartedAt:   now,
		EndedAt:     timePointer(now),
		EndedReason: stringPointer("finished"),
	}).Error; err != nil {
		t.Fatalf("failed to seed game history: %v", err)
	}

	report, err := collectReportAt(context.Background(), db.DB, userID, snapshotRow{
		SnapshotAt:   now,
		Cutoff7Days:  now.AddDate(0, 0, -7),
		Cutoff30Days: now.AddDate(0, 0, -30),
	})
	if err != nil {
		t.Fatalf("collectReportAt returned error: %v", err)
	}
	if report.Games.Started != 1 || report.Games.Completed != 1 || report.Games.Accuracy != 0 {
		t.Fatalf("unexpected zero-attempt game report: %+v", report.Games)
	}
}

func TestCollectReportChallengingCardOrderIsStable(t *testing.T) {
	testutil.SetupTestDB(t)
	userID := int64(1004)
	pairs := []db.WordPair{
		{UserID: userID, Word1: "zeta", Word2: "1", SrsState: "review", SrsLapses: 5, SrsIntervalDays: 3, SrsEase: 1.8},
		{UserID: userID, Word1: "zulu", Word2: "2", SrsState: "review", SrsLapses: 5, SrsIntervalDays: 2, SrsEase: 2.5},
		{UserID: userID, Word1: "zebra", Word2: "3", SrsState: "review", SrsLapses: 5, SrsIntervalDays: 2, SrsEase: 2.1},
		{UserID: userID, Word1: "  Beta", Word2: "4", SrsState: "review", SrsLapses: 5, SrsIntervalDays: 2, SrsEase: 2.1},
		{UserID: userID, Word1: "alpha  ", Word2: "5", SrsState: "review", SrsLapses: 5, SrsIntervalDays: 2, SrsEase: 2.1},
		{UserID: 9999, Word1: "excluded", Word2: "6", SrsState: "review", SrsLapses: 99, SrsIntervalDays: 1, SrsEase: 1.3},
	}
	if err := db.DB.Create(&pairs).Error; err != nil {
		t.Fatalf("failed to seed word pairs: %v", err)
	}
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)

	report, err := collectReportAt(context.Background(), db.DB, userID, snapshotRow{
		SnapshotAt:   now,
		Cutoff7Days:  now.AddDate(0, 0, -7),
		Cutoff30Days: now.AddDate(0, 0, -30),
	})
	if err != nil {
		t.Fatalf("collectReportAt returned error: %v", err)
	}

	gotIDs := []uint{
		report.Challenging[0].ID,
		report.Challenging[1].ID,
		report.Challenging[2].ID,
	}
	wantIDs := []uint{pairs[4].ID, pairs[3].ID, pairs[2].ID}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("challenging IDs = %v, want %v", gotIDs, wantIDs)
	}
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func stringPointer(value string) *string {
	return &value
}
