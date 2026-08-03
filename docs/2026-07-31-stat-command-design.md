# `/stat` User Statistics Command

Date: 2026-07-31

## Goal

Add a private-chat `/stat` command that asynchronously sends each user a concise
personal learning report. Every report must come from one consistent database
snapshot, and only one report may be collected at a time across bot processes
that share the same PostgreSQL database.

No schema migration or configuration change is required.

## User experience

`/stat` is accepted only in a private chat. Once the database transaction and
installation-wide lock are acquired, the bot sends:

> Collecting your stats… I’ll send a new message when it’s ready.

The completed report is sent as a new plain-text message. A request that cannot
acquire the lock fails immediately with a friendly busy message rather than
waiting. Collection has a 30-second timeout; query, transaction, or timeout
failures produce a retry-later message.

The report contains:

- **Vocabulary:** total cards, `new`/`learning`/`review` distribution with
  percentages, and learned cards currently due.
- **Progress:** seen, graduated, and mature cards; distinct cards reviewed in
  the last 7 and 30 days; successful review-state repetitions and lapses.
- **Games (all-time):** sessions started and completed, correct answers and
  attempts, accuracy, and the last-played date. This section is omitted without
  game history.
- **Most challenging:** up to three cards with lapses. This section is omitted
  when no card has lapsed.

A user without vocabulary or game history receives a clear no-learning-data
message. The snapshot time and last-played date use the most recent saved user
UTC offset, with UTC as the default.

## Architecture

The new `pkg/bot/stats` package owns typed report values, formatting, and the
PostgreSQL collection implementation. Its `Starter` and `Job` interfaces split
lock acquisition from report collection:

1. `Starter.Start` begins a `REPEATABLE READ, READ ONLY` transaction.
2. It calls `pg_try_advisory_xact_lock` with a fixed two-part namespace key.
3. A failed lock attempt rolls back and returns `ErrBusy`.
4. A successful attempt returns a `Job` while the transaction remains open.
5. `Job.Collect` runs all aggregate queries and commits before returning the
   report. Any error or cancellation rolls back.

Because the lock is transaction-scoped, commit and rollback both release it
automatically, including failure and cancellation paths. The lock is global to
all processes using the same database, not just one Go process.

`handlers.StatHandler` receives a `stats.Starter`. Its Telegram-facing `Handle`
method validates the update synchronously and launches collection in a
goroutine using a context detached from the update handler. The goroutine adds
the 30-second timeout, starts the locked job, acknowledges successful lock
acquisition, collects the report, and sends the final message after the
transaction has committed.

## Snapshot queries and metric definitions

The database supplies `CURRENT_TIMESTAMP`, 7-day and 30-day cutoffs, and the
saved UTC offset inside the report transaction. Go maps query results and
formats them; it does not load cards and derive aggregate statistics in memory.

Metrics use the following definitions:

- **Learned and due:** `srs_last_reviewed_at` is non-null and `srs_due_at` is at
  or before the database snapshot. Untouched new cards are excluded.
- **Seen:** `srs_last_reviewed_at` is non-null.
- **Graduated:** `srs_interval_days` is greater than zero. This includes a
  previously graduated card that is temporarily relearning after a lapse.
- **Mature:** `srs_interval_days` is at least 21.
- **Recent practice:** distinct card IDs whose latest
  `srs_last_reviewed_at` falls at or after the database-provided 7-day or
  30-day cutoff.
- **Successful review repetitions:** the sum of `srs_reps`.
- **Lapses:** the sum of `srs_lapses`.
- **Game completion:** a statistics row whose `ended_reason` is `finished`.
- **Game accuracy:** summed `correct_count` divided by summed
  `attempt_count`, with a zero result for a zero denominator.
- **Last played:** the latest game `started_at`.

Percentages and accuracy are rounded to one decimal place in SQL. Every query
filters by the requesting user. Challenging cards require a positive lapse
count and are ordered by descending lapses, ascending interval, ascending ease,
normalized first side, and ID before applying the limit of three.

## Formatting and safety

The report is plain text and uses fixed headings and bounded card details.
Embedded whitespace on displayed card sides is collapsed. Each side is limited
to 48 runes, including an ellipsis when truncated. With at most three
challenging cards, the fixed report remains well below Telegram's
4,096-character text limit.

Logging includes the user ID, elapsed duration, and error. Vocabulary content
is never logged.

## Testing

- SQLite fixture tests exercise user isolation, vocabulary stages and
  SQL-rounded percentages, due boundaries, recent activity, maturity,
  repetitions/lapses, game history variants, zero denominators, and stable
  challenging-card ordering.
- Transaction fakes exercise lock acquisition and contention, commit and
  rollback release, query failure, timeout, and cancellation.
- Handler fakes exercise private-chat validation, asynchronous acknowledgement
  and result ordering, busy handling, collection failure, empty data, and
  timeout behavior.
- Formatter tests cover the full layout, omitted sections, timezone display,
  whitespace cleanup, rune truncation, and the Telegram length bound.
