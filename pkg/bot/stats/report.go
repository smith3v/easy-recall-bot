package stats

import "time"

// Report contains the statistics collected for one user from one database
// snapshot.
type Report struct {
	SnapshotAt     time.Time
	UTCOffsetHours int
	Vocabulary     VocabularyReport
	Review         ReviewReport
	Games          GameReport
	Challenging    []ChallengingCard
}

type VocabularyReport struct {
	Total           int64
	New             int64
	NewPercent      float64
	Learning        int64
	LearningPercent float64
	Review          int64
	ReviewPercent   float64
	LearnedDue      int64
}

type ReviewReport struct {
	Seen           int64
	Graduated      int64
	Mature         int64
	Reviewed7Days  int64
	Reviewed30Days int64
	Repetitions    int64
	Lapses         int64
}

type GameReport struct {
	Started    int64
	Completed  int64
	Correct    int64
	Attempts   int64
	Accuracy   float64
	LastPlayed *time.Time
}

type ChallengingCard struct {
	ID           uint
	Word1        string
	Word2        string
	Lapses       int
	IntervalDays int
	Ease         float64
}
