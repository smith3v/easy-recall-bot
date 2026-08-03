package stats

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestFormatFullReport(t *testing.T) {
	lastPlayed := time.Date(2026, 7, 30, 22, 0, 0, 0, time.UTC)
	report := Report{
		SnapshotAt:     time.Date(2026, 7, 31, 12, 5, 0, 0, time.UTC),
		UTCOffsetHours: 4,
		Vocabulary: VocabularyReport{
			Total:           10,
			New:             4,
			NewPercent:      40,
			Learning:        2,
			LearningPercent: 20,
			Review:          4,
			ReviewPercent:   40,
			LearnedDue:      3,
		},
		Review: ReviewReport{
			Seen:           6,
			Graduated:      4,
			Mature:         2,
			Reviewed7Days:  3,
			Reviewed30Days: 5,
			Repetitions:    12,
			Lapses:         3,
		},
		Games: GameReport{
			Started:    4,
			Completed:  3,
			Correct:    8,
			Attempts:   10,
			Accuracy:   80,
			LastPlayed: &lastPlayed,
		},
		Challenging: []ChallengingCard{
			{Word1: "  hello\tworld ", Word2: " привет\nмир ", Lapses: 3},
			{Word1: "uno", Word2: "one", Lapses: 1},
		},
	}

	got := Format(report)
	want := `Learning stats

Vocabulary
• Total: 10 cards
• New: 4 (40.0%) · Learning: 2 (20.0%) · Review: 4 (40.0%)
• Learned and due now: 3

Progress
• Seen: 6 · Graduated: 4 · Mature (21+ days): 2
• Active cards: 3 in 7 days · 5 in 30 days
• Review repetitions: 12 successful · 3 lapses

Games (all-time)
• Started: 4 · Completed: 3
• Answers: 8/10 correct (80.0%)
• Last played: 31 Jul 2026

Most challenging
1. hello world → привет мир — 3 lapses
2. uno → one — 1 lapse

Snapshot: 31 Jul 2026, 16:05 (UTC+4)`

	if got != want {
		t.Fatalf("Format() mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestFormatOmitsConditionalSections(t *testing.T) {
	got := Format(Report{
		SnapshotAt: time.Date(2026, 7, 31, 12, 5, 0, 0, time.UTC),
		Vocabulary: VocabularyReport{
			Total:      1,
			New:        1,
			NewPercent: 100,
		},
	})

	if strings.Contains(got, "Games (all-time)") {
		t.Fatalf("expected Games section to be omitted, got:\n%s", got)
	}
	if strings.Contains(got, "Most challenging") {
		t.Fatalf("expected challenging section to be omitted, got:\n%s", got)
	}
	if !strings.Contains(got, "Snapshot: 31 Jul 2026, 12:05 (UTC)") {
		t.Fatalf("expected UTC snapshot, got:\n%s", got)
	}
}

func TestFormatShowsNoLearningData(t *testing.T) {
	got := Format(Report{
		SnapshotAt: time.Date(2026, 7, 31, 12, 5, 0, 0, time.UTC),
	})

	if !strings.Contains(got, "No learning data yet.") {
		t.Fatalf("expected empty-data message, got:\n%s", got)
	}
	if strings.Contains(got, "\nVocabulary\n") || strings.Contains(got, "\nProgress\n") {
		t.Fatalf("expected metric sections to be omitted, got:\n%s", got)
	}
}

func TestNormalizeAndTruncateCardSide(t *testing.T) {
	input := " \t" + strings.Repeat("界", 60) + "\n "
	got := normalizeAndTruncate(input)

	if utf8.RuneCountInString(got) != maxCardSideRunes {
		t.Fatalf("rune count = %d, want %d", utf8.RuneCountInString(got), maxCardSideRunes)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("expected ellipsis, got %q", got)
	}
	if strings.ContainsAny(got, "\t\n") {
		t.Fatalf("expected whitespace normalization, got %q", got)
	}
}

func TestFormatRemainsWithinTelegramLimit(t *testing.T) {
	lastPlayed := time.Now().UTC()
	longSide := strings.Repeat("very long vocabulary side ", 1000)
	report := Report{
		SnapshotAt: time.Now().UTC(),
		Vocabulary: VocabularyReport{
			Total:    1<<63 - 1,
			New:      1<<63 - 1,
			Learning: 1<<63 - 1,
			Review:   1<<63 - 1,
		},
		Review: ReviewReport{
			Seen:           1<<63 - 1,
			Graduated:      1<<63 - 1,
			Mature:         1<<63 - 1,
			Reviewed7Days:  1<<63 - 1,
			Reviewed30Days: 1<<63 - 1,
			Repetitions:    1<<63 - 1,
			Lapses:         1<<63 - 1,
		},
		Games: GameReport{
			Started:    1<<63 - 1,
			Completed:  1<<63 - 1,
			Correct:    1<<63 - 1,
			Attempts:   1<<63 - 1,
			LastPlayed: &lastPlayed,
		},
		Challenging: []ChallengingCard{
			{Word1: longSide, Word2: longSide, Lapses: 1<<31 - 1},
			{Word1: longSide, Word2: longSide, Lapses: 1<<31 - 1},
			{Word1: longSide, Word2: longSide, Lapses: 1<<31 - 1},
			{Word1: "must not appear", Word2: "fourth card", Lapses: 1},
		},
	}

	got := Format(report)
	if utf8.RuneCountInString(got) >= 4096 {
		t.Fatalf("report length = %d runes, want below 4096", utf8.RuneCountInString(got))
	}
	if strings.Contains(got, "must not appear") {
		t.Fatalf("formatter included more than three challenging cards:\n%s", got)
	}
}
