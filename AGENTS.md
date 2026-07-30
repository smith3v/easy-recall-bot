# Repository Guidelines

## Project Structure & Module Organization
The Go entrypoint lives in `cmd/tg-word-reminder/main.go`, while reusable logic sits under `pkg/`: `pkg/bot/handlers` owns Telegram command/update handlers, `pkg/bot/game` contains quiz session logic, `pkg/bot/reminders` schedules periodic reminders, and `pkg/bot/importexport` handles CSV parsing/export. `pkg/db` wires GORM to PostgreSQL, `pkg/config` loads JSON settings, and `pkg/logger` wraps `slog`. Test helpers live in `pkg/internal/testutil`. Sample CSVs (`example.csv`, `dutch-english.csv`) and the optional `utils/convert-xml-to-csv.py` script help prepare word pairs. Copy `config.example.json` to `config.json` for local secrets; the generated binary (`tg-word-reminder`) stays git-ignored.

## Build, Test, and Development Commands
- `go run ./cmd/tg-word-reminder` – compile and launch the bot using the current `config.json`.
- `go build -o bin/tg-word-reminder ./cmd/tg-word-reminder` – produce a reusable binary; ensure `bin/` exists first.
- `go fmt ./... && go vet ./...` – keep formatting canonical and catch obvious issues before review.
- `docker build -t tg-word-reminder .` – assemble the container defined in `Dockerfile` for parity with production.

## Coding Style & Naming Conventions
Use Go 1.26 features sparingly and keep imports tidy via `go fmt` (or `gofmt` + `goimports` in your editor). Follow idiomatic Go naming: exported types and functions in PascalCase, unexported identifiers in camelCase, configuration structs named after their domain (`DatabaseConfig`, etc.). Prefer small, focused packages and keep Telegram command handlers grouped under `pkg/bot/handlers`.

## Testing Guidelines
Table-driven tests in `_test.go` files colocated with the code are preferred. Target deterministic units: database repositories (using a test schema) and Telegram command handlers. Run `go test ./...` before pushing; add `-cover` to monitor coverage for new features. When adding fixtures, place them alongside tests or under a dedicated `testdata/` folder.

## Commit & Pull Request Guidelines
Commit history shows concise, sentence-case subjects (e.g., “Fix ticker interval calculation and update README”). Follow that tone, keep lines ≤72 characters, and describe *why* the change matters in the body when needed. Pull requests should include: a short summary, configuration or migration notes, linked issues, and screenshots or logs when behavior changes.

## Security & Configuration Tips
Never commit real tokens or database passwords; `config.json` is ignored—use `config.example.json` as your template and inject secrets via environment variables or local-only files. Review rate limits and bot permissions before enabling new Telegram commands. Rotate database credentials and bot tokens if they leak, and prefer least-privilege roles for the PostgreSQL user.
