# SRS-Prioritized Game Selection Implementation Plan

**Goal:** Make `/game` practice the same due and new vocabulary prioritized by daily reminders instead of random vocabulary.

**Architecture:** Reuse `training.SelectSessionPairs` from the game-start handler with the existing five-pair limit and UTC request time. Keep game deck construction, shuffling, persistence, scoring, and timeout behavior unchanged; game results remain independent of SRS grading.

**Tech Stack:** Go 1.26, GORM, PostgreSQL, SQLite-backed tests, Telegram Bot API

---

### Task 1: Cover reminder-prioritized game selection

**Prompt:**
Add handler tests that seed due learning/review pairs, due and future new pairs, a future review pair, and another user's pair. Start `/game`, decode the persisted game deck, and assert that it contains exactly the five pairs chosen by the reminder scheduler. Add an empty-state test proving that future review cards are not used and no game session is created.

Run `go test ./pkg/bot/training ./pkg/bot/game ./pkg/bot/handlers` and confirm the new tests fail against random selection.

---

### Task 2: Reuse the training scheduler

**Prompt:**
Update the game-start handler to call `training.SelectSessionPairs(userID, game.DeckPairs, now)`. Return `Nothing to practice right now.` when the selector is empty. Remove the obsolete exported random selector and its unit test. Do not grade or reschedule SRS cards from game attempts.

Format the changed Go files and rerun `go test ./pkg/bot/training ./pkg/bot/game ./pkg/bot/handlers`; expect all packages to pass.

---

### Task 3: Document and verify the behavior

**Prompt:**
Update the README and game feature specification to describe reminder-prioritized selection, the five-pair cap, exclusion of future review cards, the empty state, and the absence of SRS side effects.

Run `go test ./...`, `go vet ./...`, and `git diff --check`. Review the complete diff against `main`, address any findings, and rerun affected checks.
