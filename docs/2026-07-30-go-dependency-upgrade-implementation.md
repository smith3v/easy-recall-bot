# Go and Dependency Upgrade Implementation Plan

**Goal:** Upgrade the repository to Go 1.26.5 and refresh all Go module and GitHub Actions dependencies to their latest stable releases.

**Architecture:** Keep a single exact Go version across the module, Docker builder, CI, and developer documentation. Use the Go module resolver to update the complete build list, then update each versioned GitHub Action to its current stable major and verify the application and container build without changing runtime database infrastructure.

**Tech Stack:** Go 1.26.5, Go modules, Docker, GitHub Actions, PostgreSQL/GORM.

---

### Task 1: Align the Go toolchain version

**Prompt:**
Update `go.mod` from `go 1.26.2` to `go 1.26.5`, `Dockerfile` from `golang:1.26.2` to `golang:1.26.5`, `.github/workflows/test.yml` from Go 1.26.2 to Go 1.26.5, `README.md` from Go 1.26.2+ to Go 1.26.5+, and `AGENTS.md` from Go 1.25 guidance to Go 1.26 guidance. Run `rg -n "1\\.26\\.2|Go 1\\.25" go.mod Dockerfile README.md AGENTS.md .github` and expect no active version pins to remain. Run `go version` and expect `go1.26.5`. Review the edits for consistency, then commit them with the sentence-case subject `Update project to Go 1.26.5`.

---

### Task 2: Upgrade the complete Go module build list

**Prompt:**
From the repository root, run `go get -u ./...` to upgrade the application build and test dependencies, including `github.com/go-telegram/bot` to v1.22.0 and `gorm.io/gorm` to v1.31.2. Update any remaining outdated requirement explicitly recorded in `go.mod`, such as `golang.org/x/crypto` v0.54.0, then run `go mod tidy` to remove obsolete requirements and checksums. Run `go list -m -u all` and verify that every requirement recorded in `go.mod` is current; newer versions may still appear for modules that exist only in dependency-module tests or the pruned transitive graph and should not be promoted to root requirements. Review `go.mod` and `go.sum` for expected resolver-only changes, then commit them with the sentence-case subject `Update Go module dependencies`.

---

### Task 3: Upgrade GitHub Actions dependencies

**Prompt:**
In `.github/workflows/test.yml`, update `actions/checkout` to v6 and `actions/setup-go` to v6. In `.github/workflows/publish-latest.yml` and `.github/workflows/publish-tag.yml`, update `actions/checkout` to v6, `docker/setup-buildx-action` to v4, `docker/login-action` to v4, and `docker/build-push-action` to v7. Run `rg -n "uses:" .github/workflows` and verify that only these current stable majors are present. Review both publish workflows to ensure their triggers, credentials, and image tags are unchanged, then commit the edits with the sentence-case subject `Update GitHub Actions dependencies`.

---

### Task 4: Verify the upgraded repository

**Prompt:**
Run `go fmt ./...`, `go test ./...`, `go vet ./...`, and `go build ./cmd/tg-word-reminder ./cmd/notify-legacy-users`; each command must exit successfully. Run `docker build -t tg-word-reminder:go-1.26.5-upgrade .` when Docker is available and expect both Go binaries to build successfully in the builder stage. Review the complete branch diff for unexpected source changes, then commit any verification-driven fixes with a concise sentence-case subject.

---

### Task 5: Review the branch against `main`

**Prompt:**
Run `git diff --check` and correct every whitespace error. Run `git diff --stat main...HEAD` and `git diff main...HEAD -- go.mod go.sum Dockerfile README.md AGENTS.md .github/workflows` to review the branch against `main`, accounting for the feature work already present at the branch point. Re-run `go test ./...` and `go vet ./...` after any fix. Commit review fixes with the sentence-case subject `Fix Go upgrade review findings`.

---
