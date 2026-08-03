# `/stat` User Statistics Command Implementation Plan

**Goal:** Add an asynchronous private-chat `/stat` command that reports personal vocabulary, review, and all-time game statistics from one locked database snapshot.

**Architecture:** Add a `pkg/bot/stats` package that separates transaction-scoped advisory-lock startup from report collection through `Starter` and `Job` interfaces. Inject that starter into a handler that performs timed work in a goroutine, then wire the exact command into the bot and document it.

**Tech Stack:** Go 1.26, GORM, PostgreSQL advisory transaction locks, SQLite fixture tests, Telegram Bot API

---

### Task 1: Define the report and transaction lifecycle

**Prompt:**
Create `pkg/bot/stats/report.go` with typed `Report`, `VocabularyReport`,
`ReviewReport`, `GameReport`, and `ChallengingCard` values. Create
`pkg/bot/stats/collector.go` with exported `Starter` and `Job` interfaces,
`ErrBusy`, and a PostgreSQL starter. Begin with `sql.LevelRepeatableRead` and
`ReadOnly: true`; attempt the fixed two-key
`pg_try_advisory_xact_lock(1163023170, 1398030676)`. Roll back on begin/lock
error or contention. Have the job commit only after all queries succeed and
roll back on any query, commit, timeout, or cancellation error.

Add transaction-fake tests in `pkg/bot/stats/collector_test.go` for successful
acquisition and commit, fail-fast contention, begin/lock/query/commit errors,
rollback release, deadline timeout, and cancellation.

Run `gofmt` on the package and `go test ./pkg/bot/stats`.

---

### Task 2: Collect aggregate metrics from the snapshot

**Prompt:**
Inside `pkg/bot/stats/collector.go`, query the database timestamp, 7-day and
30-day cutoffs, and latest saved user UTC offset. Add deterministic aggregate
queries for vocabulary stages and percentages, learned due cards, seen,
graduated, 21-day mature, recent distinct cards, repetitions, lapses, games,
accuracy, last-played time, and the top three challenging cards.

Keep all queries user-scoped. Calculate percentages and accuracy with SQL
`ROUND(..., 1)` and `NULLIF`; do not calculate metrics from loaded card rows.
Use PostgreSQL whitespace normalization in the challenging-card ordering, with
a test-dialect equivalent for SQLite fixtures.

Add fixture tests that seed two users and cover stage percentages, due-at-
snapshot behavior, untouched-new exclusion, 7/30-day boundaries, maturity,
repetitions/lapses, finished/open/absent games, zero attempts, and deterministic
top-three ordering.

Run `gofmt` and `go test ./pkg/bot/stats`.

---

### Task 3: Format the bounded conditional report

**Prompt:**
Create `pkg/bot/stats/format.go`. Format plain-text Vocabulary and Progress
sections, an all-time Games section only when game history exists, and a Most
challenging section only when positive lapses exist. For a user with neither
vocabulary nor games, show the no-learning-data message.

Apply the saved integer UTC offset with `time.FixedZone`, default zero to the
label `UTC`, and show the database snapshot in every result. Collapse embedded
whitespace on card sides and limit each to 48 runes including an ellipsis.

Add `pkg/bot/stats/format_test.go` with a full expected sample, conditional
sections, empty data, timezone conversion, whitespace normalization,
truncation, and a rune count below Telegram's 4,096-character limit.

Run `gofmt` and `go test ./pkg/bot/stats`.

---

### Task 4: Add the asynchronous injectable handler

**Prompt:**
Create `pkg/bot/handlers/stat.go` with `StatHandler` and
`NewStatHandler(stats.Starter)`. Validate updates like `/game` and `/export`,
consume feedback capture first, and reject non-private chats with
`The /stat command works only in private chat.` Launch valid collection in a
goroutine detached from the update context and apply a 30-second timeout.

After lock acquisition send
`Collecting your stats… I’ll send a new message when it’s ready.` Handle
`stats.ErrBusy` with one immediate busy response and no acknowledgement. On
success send `stats.Format(report)` as a separate message after job commit. On
failure log only user ID, duration, and error before sending a retry-later
message.

Add `pkg/bot/handlers/stat_test.go` with fake starters/jobs for asynchronous
acknowledgement-result ordering, busy behavior, collection errors, empty
reports, deadline timeout, private-chat enforcement, and invalid updates.

Run `gofmt` and
`go test ./pkg/bot/stats ./pkg/bot/handlers`.

---

### Task 5: Register and document `/stat`

**Prompt:**
In `cmd/tg-word-reminder/main.go`, create the injected handler with
`stats.NewPostgresStarter(db.DB)` and register `/stat` with exact text matching.
Add `/stat` to the built-in help in `pkg/bot/handlers/default.go` and to the
README command list. Do not call or add ownership of Telegram
`setMyCommands`.

Update the help-handler test to assert that `/stat` is listed.

Run `gofmt` on changed Go files and
`go test ./pkg/bot/stats ./pkg/bot/handlers`.

---

### Task 6: Verify, review, and commit

**Prompt:**
Run:

```text
go test ./pkg/bot/stats ./pkg/bot/handlers
go test ./...
go vet ./...
go build ./cmd/tg-word-reminder
git diff --check
```

Review `git diff main...HEAD` plus uncommitted work for correctness, data
isolation, transaction release, cancellation, logging privacy, Telegram length,
and documentation accuracy. Address every finding and rerun affected commands.

Commit the complete change with the sentence-case subject:

```text
Add personal statistics command
```
