package stats

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const maxCardSideRunes = 48

func Format(report Report) string {
	var builder strings.Builder
	builder.WriteString("Learning stats\n\n")

	if report.Vocabulary.Total == 0 && report.Games.Started == 0 {
		builder.WriteString("No learning data yet. Add vocabulary or finish onboarding, then use /review or /game to get started.\n\n")
		writeSnapshot(&builder, report)
		return builder.String()
	}

	fmt.Fprintf(&builder, "Vocabulary\n")
	fmt.Fprintf(
		&builder,
		"• Total: %d %s\n",
		report.Vocabulary.Total,
		pluralize(report.Vocabulary.Total, "card", "cards"),
	)
	fmt.Fprintf(
		&builder,
		"• New: %d (%.1f%%) · Learning: %d (%.1f%%) · Review: %d (%.1f%%)\n",
		report.Vocabulary.New,
		report.Vocabulary.NewPercent,
		report.Vocabulary.Learning,
		report.Vocabulary.LearningPercent,
		report.Vocabulary.Review,
		report.Vocabulary.ReviewPercent,
	)
	fmt.Fprintf(&builder, "• Learned and due now: %d\n\n", report.Vocabulary.LearnedDue)

	fmt.Fprintf(&builder, "Progress\n")
	fmt.Fprintf(
		&builder,
		"• Seen: %d · Graduated: %d · Mature (21+ days): %d\n",
		report.Review.Seen,
		report.Review.Graduated,
		report.Review.Mature,
	)
	fmt.Fprintf(
		&builder,
		"• Active cards: %d in 7 days · %d in 30 days\n",
		report.Review.Reviewed7Days,
		report.Review.Reviewed30Days,
	)
	fmt.Fprintf(
		&builder,
		"• Review repetitions: %d successful · %d %s\n",
		report.Review.Repetitions,
		report.Review.Lapses,
		pluralize(report.Review.Lapses, "lapse", "lapses"),
	)

	if report.Games.Started > 0 {
		builder.WriteString("\nGames (all-time)\n")
		fmt.Fprintf(
			&builder,
			"• Started: %d · Completed: %d\n",
			report.Games.Started,
			report.Games.Completed,
		)
		fmt.Fprintf(
			&builder,
			"• Answers: %d/%d correct (%.1f%%)\n",
			report.Games.Correct,
			report.Games.Attempts,
			report.Games.Accuracy,
		)
		if report.Games.LastPlayed != nil {
			fmt.Fprintf(
				&builder,
				"• Last played: %s\n",
				reportTime(*report.Games.LastPlayed, report.UTCOffsetHours).Format("2 Jan 2006"),
			)
		}
	}

	if len(report.Challenging) > 0 {
		builder.WriteString("\nMost challenging\n")
		challenging := report.Challenging
		if len(challenging) > 3 {
			challenging = challenging[:3]
		}
		for index, card := range challenging {
			fmt.Fprintf(
				&builder,
				"%d. %s → %s — %d %s\n",
				index+1,
				normalizeAndTruncate(card.Word1),
				normalizeAndTruncate(card.Word2),
				card.Lapses,
				pluralize(int64(card.Lapses), "lapse", "lapses"),
			)
		}
	}

	builder.WriteString("\n")
	writeSnapshot(&builder, report)
	return builder.String()
}

func writeSnapshot(builder *strings.Builder, report Report) {
	label := "UTC"
	if report.UTCOffsetHours != 0 {
		label = fmt.Sprintf("UTC%+d", report.UTCOffsetHours)
	}
	fmt.Fprintf(
		builder,
		"Snapshot: %s (%s)",
		reportTime(report.SnapshotAt, report.UTCOffsetHours).Format("2 Jan 2006, 15:04"),
		label,
	)
}

func reportTime(value time.Time, offsetHours int) time.Time {
	zone := time.FixedZone("report", offsetHours*60*60)
	return value.In(zone)
}

func normalizeAndTruncate(value string) string {
	normalized := strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(normalized) <= maxCardSideRunes {
		return normalized
	}

	runes := []rune(normalized)
	return string(runes[:maxCardSideRunes-1]) + "…"
}

func pluralize(value int64, singular, plural string) string {
	if value == 1 {
		return singular
	}
	return plural
}
