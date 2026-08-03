package stats

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync"
	"time"

	"gorm.io/gorm"
)

const (
	advisoryLockNamespace int32 = 1163023170 // "ERCB"
	advisoryLockName      int32 = 1398030676 // "STAT"
)

var ErrBusy = errors.New("statistics collection is busy")

// Starter attempts to start one statistics collection.
type Starter interface {
	Start(ctx context.Context, userID int64) (Job, error)
}

// Job owns the snapshot transaction and installation-wide lock until Collect
// commits or rolls back.
type Job interface {
	Collect(ctx context.Context) (Report, error)
}

type transactionBeginner interface {
	begin(ctx context.Context) (statsTransaction, error)
}

type statsTransaction interface {
	tryLock(ctx context.Context) (bool, error)
	collect(ctx context.Context, userID int64) (Report, error)
	commit() error
	rollback() error
}

// PostgresStarter starts report transactions against PostgreSQL.
type PostgresStarter struct {
	beginner transactionBeginner
}

func NewPostgresStarter(database *gorm.DB) *PostgresStarter {
	return &PostgresStarter{
		beginner: &gormTransactionBeginner{database: database},
	}
}

func (s *PostgresStarter) Start(ctx context.Context, userID int64) (Job, error) {
	if s == nil || s.beginner == nil {
		return nil, errors.New("statistics starter is not configured")
	}

	tx, err := s.beginner.begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin statistics transaction: %w", err)
	}

	locked, err := tx.tryLock(ctx)
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("acquire statistics lock: %w", err),
			rollbackError(tx),
		)
	}
	if !locked {
		return nil, errors.Join(ErrBusy, rollbackError(tx))
	}

	return &transactionJob{
		tx:     tx,
		userID: userID,
	}, nil
}

type transactionJob struct {
	mu       sync.Mutex
	tx       statsTransaction
	userID   int64
	finished bool
}

func (j *transactionJob) Collect(ctx context.Context) (Report, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.finished {
		return Report{}, errors.New("statistics job already finished")
	}
	j.finished = true

	report, err := j.tx.collect(ctx, j.userID)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return Report{}, errors.Join(
			fmt.Errorf("collect statistics: %w", err),
			rollbackError(j.tx),
		)
	}

	if err := j.tx.commit(); err != nil {
		return Report{}, errors.Join(
			fmt.Errorf("commit statistics transaction: %w", err),
			rollbackError(j.tx),
		)
	}
	return report, nil
}

func rollbackError(tx statsTransaction) error {
	if err := tx.rollback(); err != nil {
		return fmt.Errorf("rollback statistics transaction: %w", err)
	}
	return nil
}

type gormTransactionBeginner struct {
	database *gorm.DB
}

func (b *gormTransactionBeginner) begin(ctx context.Context) (statsTransaction, error) {
	if b == nil || b.database == nil {
		return nil, errors.New("database is not initialized")
	}

	tx := b.database.WithContext(ctx).Begin(&sql.TxOptions{
		Isolation: sql.LevelRepeatableRead,
		ReadOnly:  true,
	})
	if tx.Error != nil {
		return nil, tx.Error
	}
	return &gormStatsTransaction{database: tx}, nil
}

type gormStatsTransaction struct {
	database *gorm.DB
}

func (tx *gormStatsTransaction) tryLock(ctx context.Context) (bool, error) {
	var row struct {
		Locked bool
	}
	err := tx.database.WithContext(ctx).
		Raw(
			"SELECT pg_try_advisory_xact_lock(?, ?) AS locked",
			advisoryLockNamespace,
			advisoryLockName,
		).
		Scan(&row).Error
	return row.Locked, err
}

func (tx *gormStatsTransaction) collect(ctx context.Context, userID int64) (Report, error) {
	return collectReport(ctx, tx.database, userID)
}

func (tx *gormStatsTransaction) commit() error {
	return tx.database.Commit().Error
}

func (tx *gormStatsTransaction) rollback() error {
	return tx.database.Rollback().Error
}

type snapshotRow struct {
	SnapshotAt     time.Time
	Cutoff7Days    time.Time
	Cutoff30Days   time.Time
	UTCOffsetHours int
}

type databaseTime struct {
	Time  time.Time
	Valid bool
}

func (value databaseTime) Value() (driver.Value, error) {
	if !value.Valid {
		return nil, nil
	}
	return value.Time, nil
}

func (value *databaseTime) Scan(source any) error {
	if source == nil {
		value.Time = time.Time{}
		value.Valid = false
		return nil
	}
	if timestamp, ok := source.(time.Time); ok {
		value.Time = timestamp
		value.Valid = true
		return nil
	}

	var text string
	switch timestamp := source.(type) {
	case string:
		text = timestamp
	case []byte:
		text = string(timestamp)
	default:
		return fmt.Errorf("unsupported database timestamp type %T", source)
	}

	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		timestamp, err := time.Parse(layout, text)
		if err == nil {
			value.Time = timestamp
			value.Valid = true
			return nil
		}
	}
	return fmt.Errorf("unsupported database timestamp %q", text)
}

func collectReport(ctx context.Context, database *gorm.DB, userID int64) (Report, error) {
	if database == nil {
		return Report{}, errors.New("database is not initialized")
	}

	snapshot, err := collectSnapshot(ctx, database, userID)
	if err != nil {
		return Report{}, err
	}
	return collectReportAt(ctx, database, userID, snapshot)
}

func collectReportAt(
	ctx context.Context,
	database *gorm.DB,
	userID int64,
	snapshot snapshotRow,
) (Report, error) {
	report := Report{
		SnapshotAt:     snapshot.SnapshotAt,
		UTCOffsetHours: snapshot.UTCOffsetHours,
	}
	if err := database.WithContext(ctx).Raw(`
SELECT
	COUNT(*) AS total,
	COALESCE(SUM(CASE WHEN srs_state = 'new' THEN 1 ELSE 0 END), 0) AS new,
	COALESCE(ROUND(
		100.0 * SUM(CASE WHEN srs_state = 'new' THEN 1 ELSE 0 END)
		/ NULLIF(COUNT(*), 0),
		1
	), 0) AS new_percent,
	COALESCE(SUM(CASE WHEN srs_state = 'learning' THEN 1 ELSE 0 END), 0) AS learning,
	COALESCE(ROUND(
		100.0 * SUM(CASE WHEN srs_state = 'learning' THEN 1 ELSE 0 END)
		/ NULLIF(COUNT(*), 0),
		1
	), 0) AS learning_percent,
	COALESCE(SUM(CASE WHEN srs_state = 'review' THEN 1 ELSE 0 END), 0) AS review,
	COALESCE(ROUND(
		100.0 * SUM(CASE WHEN srs_state = 'review' THEN 1 ELSE 0 END)
		/ NULLIF(COUNT(*), 0),
		1
	), 0) AS review_percent,
	COALESCE(SUM(CASE
		WHEN srs_last_reviewed_at IS NOT NULL AND srs_due_at <= ? THEN 1
		ELSE 0
	END), 0) AS learned_due
FROM word_pairs
WHERE user_id = ?
`, snapshot.SnapshotAt, userID).Scan(&report.Vocabulary).Error; err != nil {
		return Report{}, fmt.Errorf("collect vocabulary statistics: %w", err)
	}

	if err := database.WithContext(ctx).Raw(`
SELECT
	COALESCE(SUM(CASE WHEN srs_last_reviewed_at IS NOT NULL THEN 1 ELSE 0 END), 0) AS seen,
	COALESCE(SUM(CASE WHEN srs_interval_days > 0 THEN 1 ELSE 0 END), 0) AS graduated,
	COALESCE(SUM(CASE WHEN srs_interval_days >= 21 THEN 1 ELSE 0 END), 0) AS mature,
	COUNT(DISTINCT CASE WHEN srs_last_reviewed_at >= ? THEN id END) AS reviewed7_days,
	COUNT(DISTINCT CASE WHEN srs_last_reviewed_at >= ? THEN id END) AS reviewed30_days,
	COALESCE(SUM(srs_reps), 0) AS repetitions,
	COALESCE(SUM(srs_lapses), 0) AS lapses
FROM word_pairs
WHERE user_id = ?
`, snapshot.Cutoff7Days, snapshot.Cutoff30Days, userID).Scan(&report.Review).Error; err != nil {
		return Report{}, fmt.Errorf("collect review statistics: %w", err)
	}

	var gameRow struct {
		Started    int64
		Completed  int64
		Correct    int64
		Attempts   int64
		Accuracy   float64
		LastPlayed databaseTime
	}
	if err := database.WithContext(ctx).Raw(`
SELECT
	COUNT(*) AS started,
	COALESCE(SUM(CASE WHEN ended_reason = 'finished' THEN 1 ELSE 0 END), 0) AS completed,
	COALESCE(SUM(correct_count), 0) AS correct,
	COALESCE(SUM(attempt_count), 0) AS attempts,
	COALESCE(ROUND(
		100.0 * SUM(correct_count) / NULLIF(SUM(attempt_count), 0),
		1
	), 0) AS accuracy,
	MAX(started_at) AS last_played
FROM game_session_statistics
WHERE user_id = ?
`, userID).Scan(&gameRow).Error; err != nil {
		return Report{}, fmt.Errorf("collect game statistics: %w", err)
	}
	report.Games = GameReport{
		Started:   gameRow.Started,
		Completed: gameRow.Completed,
		Correct:   gameRow.Correct,
		Attempts:  gameRow.Attempts,
		Accuracy:  gameRow.Accuracy,
	}
	if gameRow.LastPlayed.Valid {
		report.Games.LastPlayed = &gameRow.LastPlayed.Time
	}

	order := challengingCardOrder(database.Dialector.Name())
	if err := database.WithContext(ctx).
		Table("word_pairs").
		Select("id, word1, word2, srs_lapses AS lapses, srs_interval_days AS interval_days, srs_ease AS ease").
		Where("user_id = ? AND srs_lapses > 0", userID).
		Order(order).
		Limit(3).
		Scan(&report.Challenging).Error; err != nil {
		return Report{}, fmt.Errorf("collect challenging cards: %w", err)
	}

	return report, nil
}

func collectSnapshot(ctx context.Context, database *gorm.DB, userID int64) (snapshotRow, error) {
	query := `
SELECT
	CURRENT_TIMESTAMP AS snapshot_at,
	CURRENT_TIMESTAMP - INTERVAL '7 days' AS cutoff7_days,
	CURRENT_TIMESTAMP - INTERVAL '30 days' AS cutoff30_days,
	COALESCE((
		SELECT timezone_offset_hours
		FROM user_settings
		WHERE user_id = ?
		ORDER BY id DESC
		LIMIT 1
	), 0) AS utc_offset_hours
`
	if database.Dialector.Name() == "sqlite" {
		query = `
SELECT
	CURRENT_TIMESTAMP AS snapshot_at,
	datetime(CURRENT_TIMESTAMP, '-7 days') AS cutoff7_days,
	datetime(CURRENT_TIMESTAMP, '-30 days') AS cutoff30_days,
	COALESCE((
		SELECT timezone_offset_hours
		FROM user_settings
		WHERE user_id = ?
		ORDER BY id DESC
		LIMIT 1
	), 0) AS utc_offset_hours
`
	}

	var scanned struct {
		SnapshotAt     databaseTime
		Cutoff7Days    databaseTime
		Cutoff30Days   databaseTime
		UTCOffsetHours int
	}
	if err := database.WithContext(ctx).Raw(query, userID).Scan(&scanned).Error; err != nil {
		return snapshotRow{}, fmt.Errorf("collect statistics snapshot: %w", err)
	}
	if !scanned.SnapshotAt.Valid || !scanned.Cutoff7Days.Valid || !scanned.Cutoff30Days.Valid {
		return snapshotRow{}, errors.New("database returned an incomplete statistics snapshot")
	}
	return snapshotRow{
		SnapshotAt:     scanned.SnapshotAt.Time,
		Cutoff7Days:    scanned.Cutoff7Days.Time,
		Cutoff30Days:   scanned.Cutoff30Days.Time,
		UTCOffsetHours: scanned.UTCOffsetHours,
	}, nil
}

func challengingCardOrder(dialect string) string {
	if dialect == "sqlite" {
		return `
srs_lapses DESC,
srs_interval_days ASC,
srs_ease ASC,
lower(trim(replace(replace(replace(replace(replace(
	word1, char(9), ' '), char(10), ' '), char(13), ' '), '  ', ' '), '  ', ' '))) ASC,
id ASC
`
	}
	return `
srs_lapses DESC,
srs_interval_days ASC,
srs_ease ASC,
regexp_replace(lower(btrim(word1)), '[[:space:]]+', ' ', 'g') ASC,
id ASC
`
}
