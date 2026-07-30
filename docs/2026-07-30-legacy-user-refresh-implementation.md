# Legacy User Refresh Implementation Plan

**Goal:** Invite selected legacy users to opt into an atomic, failure-safe account refresh.

**Architecture:** Persist reset intent in onboarding state and make final vocabulary provisioning the single transactional replacement boundary. Add a dry-run-first command that sends an invitation only to an explicit operator-provided allowlist.

**Tech Stack:** Go, GORM, PostgreSQL/SQLite tests, go-telegram/bot

---

### Task 1: Persist reset intent without deleting user data

**Prompt:**
Add `ResetPending` to `db.OnboardingState`. Update onboarding state transitions
so ordinary onboarding clears the marker, reset-phrase confirmation starts the
wizard with the marker set, and state navigation preserves it. Replace the
handler's immediate `ResetUserDataTx` call with the new state transition. Add
tests proving that the reset phrase starts onboarding without deleting existing
rows. Run `go test ./pkg/bot/onboarding ./pkg/bot/handlers`, address failures,
and commit with a sentence-case summary.

---

### Task 2: Replace data atomically during final provisioning

**Prompt:**
Update `ProvisionUserVocabularyAndDefaults` to load the persisted onboarding
state inside its transaction. Verify eligible starter cards before destructive
work. When `ResetPending` is true, delete that user's old word pairs, settings,
training sessions, and game sessions before inserting replacement cards and
defaults. Always preserve game-session statistics. Roll back all deletion when
provisioning fails, then clear onboarding state only after success. Remove the
now-unused direct reset service. Add service and handler tests for successful
replacement, preserved statistics, abandoned onboarding, and rollback. Run the
focused tests and commit with a sentence-case summary.

---

### Task 3: Add a dry-run-first notification command

**Prompt:**
Create `cmd/notify-legacy-users/main.go`. Accept `-config` (default
`config.json`), required `-user-ids` as a comma-separated allowlist, and
optional `-send`. Parse only positive `int64` IDs, reject malformed input, and
deduplicate while preserving order. In dry-run mode, print the validated
recipients without creating a Telegram client. In send mode, load the bot token,
send the agreed refresh invitation sequentially, and report each outcome plus a
summary. Return a non-zero exit code for validation/configuration errors or any
delivery failure. Extract parsing and sending seams that can be tested without
network access. Run command-package tests and commit with a sentence-case
summary.

---

### Task 4: Verify and review

**Prompt:**
Run `go fmt ./...`, `go test ./...`, and `go vet ./...`; fix every failure.
Review `git diff main...HEAD` and the working-tree diff for accidental data
exposure, unsafe default sending, non-atomic deletion, missing rollback
coverage, or unrelated changes. Address findings, rerun verification, and
commit any fix-up with a concise sentence-case summary.
