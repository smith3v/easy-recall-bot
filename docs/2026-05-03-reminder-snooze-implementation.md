# Reminder Snooze Implementation Plan

**Goal:** Fix issue `#28` so `Snooze 1 day` and `Snooze 1 week` reliably suppress all scheduled reminders for the selected window.

**Architecture:** Stop treating snooze as a per-card due-date rewrite. Add a user-level `ReminderSnoozedUntil` timestamp in `UserSettings`, set it from overdue callback actions, and make reminder dispatch return early while the snooze window is active. Keep SRS card state unchanged so the fix is isolated to scheduling behavior.

**Tech Stack:** Go 1.26, GORM/PostgreSQL, go-telegram/bot, existing handler/reminder test suites.

---

Before starting tasks, create a dedicated branch/worktree and follow @brainstorming guidance: small, frequent commits, DRY, YAGNI.

### Task 1: Add reminder snooze state to the DB model

**Prompt:**
Implement the minimal persistence change for user-level snoozing.
- Update `pkg/db/models.go`:
  - Add `ReminderSnoozedUntil *time.Time` to `UserSettings`.
  - Keep the field nullable so existing users remain compatible.
- Do not add a separate migration file; rely on the existing `AutoMigrate` flow.

Write the tests for the new code:
- Extend `pkg/db/repository_test.go` to assert `AutoMigrate` still succeeds with the new `UserSettings` schema.
- If there is already schema-inspection coverage, add an assertion that the `user_settings` table includes the new column after migration.

Run the tests and make sure they pass:
- `go test ./pkg/db/...`
- Expected result: `ok` for `pkg/db` and any dependent DB test packages.

Commit with the summary of the change as a commit message.

---

### Task 2: Change overdue snooze handling to write user-level state

**Prompt:**
Replace per-card snooze writes with a `UserSettings` update.
- Update `pkg/bot/handlers/overdue.go`:
  - Remove the current `snoozeOverdue(userID, nextDue, now)` behavior that updates `WordPair.srs_due_at`.
  - Add a helper that loads or updates `db.UserSettings` for the user and sets `ReminderSnoozedUntil` to:
    - `now.Add(24 * time.Hour)` for `snooze1d`
    - `now.Add(7 * 24 * time.Hour)` for `snooze1w`
  - Keep `training.DefaultManager.End(...)` so the active training session still closes after snooze.
  - Update the confirmation text so it describes reminder snoozing rather than only “catch up”.
- Keep callback names (`snooze1d`, `snooze1w`) unchanged.

Write the tests for the new code:
- Extend `pkg/bot/handlers/review_test.go`:
  - Add a `snooze1d` test that verifies `ReminderSnoozedUntil` is set roughly 24 hours ahead.
  - Add a `snooze1w` test that verifies `ReminderSnoozedUntil` is set roughly 7 days ahead.
  - Keep/extend the existing assertion that snoozing ends the active training session.
- Assert `WordPair.srs_due_at` is not rewritten by snooze.

Run the tests and make sure they pass:
- `go test ./pkg/bot/handlers -run 'Overdue|Review'`
- Expected result: all overdue/review handler tests pass.

Commit with the summary of the change as a commit message.

---

### Task 3: Suppress scheduled reminders while snooze is active

**Prompt:**
Apply the snooze gate at reminder dispatch time.
- Update `pkg/bot/reminders/tickers.go`:
  - In `handleUserReminder`, return immediately when `user.ReminderSnoozedUntil != nil` and `now.Before(*user.ReminderSnoozedUntil)`.
  - Apply this check before overdue counting, active-session expiration, or any send/edit action.
  - Do not clear the field proactively; once `now` is at or after the timestamp, normal reminder flow should resume automatically.
- Do not change `training.SelectSessionPairs`, overdue thresholds, or pause-after-misses logic unless required by tests.

Write the tests for the new code:
- Extend `pkg/bot/reminders/tickers_test.go`:
  - Add a test where cards are overdue but `ReminderSnoozedUntil` is in the future; assert no `sendMessage` or `editMessageText` calls occur.
  - Add a test where a normal reminder slot is due and there are reviewable cards, but snooze is active; assert no reminder is sent.
  - Add a test where `ReminderSnoozedUntil` is in the past; assert reminder delivery resumes normally.

Run the tests and make sure they pass:
- `go test ./pkg/bot/reminders -run Reminder`
- Expected result: reminder scheduling tests pass, including the new snooze coverage.

Commit with the summary of the change as a commit message.

---

### Task 4: Verify integration across handlers, reminders, and DB

**Prompt:**
Run the smallest integration pass that exercises the changed surfaces together.
- Run the focused packages first:
  - `go test ./pkg/db/... ./pkg/bot/handlers ./pkg/bot/reminders`
- If any failure reveals hidden coupling, fix the implementation with the minimum code change needed.
- Do not broaden scope into unrelated refactors.

Write the tests for the new code:
- Only add tests if a failing integration case is not already covered by the package-level work above.

Run the tests and make sure they pass:
- `go test ./pkg/db/... ./pkg/bot/handlers ./pkg/bot/reminders`
- Expected result: all targeted tests pass.

Commit with the summary of the change as a commit message.

---

### Task 5: Run the full repository test suite

**Prompt:**
Validate that the snooze fix does not break unrelated bot flows.
- Run the full suite from the repository root:
  - `go test ./...`
- If failures are unrelated pre-existing problems, document them clearly before changing code.
- If failures were caused by the snooze change, fix them with the smallest safe patch and rerun `go test ./...`.

Write the tests for the new code:
- Do not add new tests in this task unless a full-suite failure exposes a missing regression test directly caused by the snooze change.

Run the tests and make sure they pass:
- `go test ./...`
- Expected result: all packages report `ok`.

Commit with the summary of the change as a commit message.

---

### Task 6: Review against `main` and address findings

**Prompt:**
Perform an explicit review before final delivery.
- Fetch and compare with `main` using non-interactive git commands.
- Inspect the diff for:
  - accidental behavior changes outside snooze handling
  - stale wording in overdue callback responses
  - missing test coverage for active snooze, expired snooze, and unchanged card due dates
- If you find issues, fix them immediately and rerun the smallest relevant test command, then rerun `go test ./...`.

Write the tests for the new code:
- Add only the regression test needed for any review finding you fix.

Run the tests and make sure they pass:
- `git diff --stat main...HEAD`
- `git diff main...HEAD`
- `go test ./...`
- Expected result: diff contains only the snooze fix scope and all tests pass.

Commit with the summary of the change as a commit message.

