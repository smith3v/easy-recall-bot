# Easy Recall Bot

Easy Recall Bot is a Telegram bot for vocabulary practice with onboarding, spaced review, quiz mode, and reminders.

## Public Links

- Telegram bot: [@EasyRecallBot](https://t.me/EasyRecallBot)
- Landing page: [smith3v.com/easy-recall-bot](https://www.smith3v.com/easy-recall-bot)

## Highlights

- Onboarding wizard on `/start` to initialize a personal deck from built-in multilingual vocabulary.
- Safe re-initialization for existing users with explicit `RESET MY DATA` confirmation.
- Spaced-repetition review flow (`/review`) plus quiz mode (`/game`).
- CSV import/export for personal vocabulary updates.
- Configurable reminder schedule and cards per session.

## Onboarding Experience

For new users, `/start` opens a language selection wizard:
1. Choose the language you are learning.
2. Choose the language you already know.
3. Confirm import and start training.

Defaults after onboarding:
- 3 reminder sessions (morning, afternoon, evening)
- 5 cards per session

If built-in onboarding vocabulary is unavailable, users can still upload their own CSV file to begin.

## Commands

- `/start` - start onboarding or re-initialize account
- `/review` - start/resume spaced-repetition training
- `/game` - start quiz mode
- `/getpair` - get one random card
- `/settings` - configure reminders and training preferences
- `/export` - download your vocabulary as CSV
- `/clear` - remove uploaded vocabulary
- `/feedback` - send feedback to admins (private chat)

## Quick Start

### Prerequisites

- Go 1.26.5+
- PostgreSQL
- Telegram bot token from [BotFather](https://core.telegram.org/bots#botfather)

### Run with Docker Compose

1. Create config:
   ```bash
   cp config.example.json config.json
   ```
2. Edit `config.json`:
   - set `telegram.token`
   - set database settings to match your compose environment
3. Create `.env` for PostgreSQL:
   ```dotenv
   POSTGRES_USER=your_db_user
   POSTGRES_PASSWORD=your_db_password
   POSTGRES_DB=your_db_name
   ```
4. Start services:
   ```bash
   docker compose up --build
   ```

Useful logs command:
```bash
docker compose logs -f tg-word-reminder
```

### Run without Docker

```bash
go run ./cmd/tg-word-reminder
```

## CSV Import Format

Send a CSV document to the bot chat.

- First two columns are used as `word1,word2`
- Supported separators: comma, semicolon, tab
- Example file: `vocabularies/example.csv`

## Configuration Notes

- `onboarding.init_vocabulary_path` controls startup refresh of built-in multilingual vocabulary.
- `feedback.admin_ids` controls who receives `/feedback` messages.
- Logging options (`logging.level`, `logging.gorm_level`, `logging.file`) are optional.

Reference config: `config.example.json`.

## Legacy User Refresh

After deploying the atomic re-initialization flow, preview a one-time invitation
to an explicit user allowlist:

```bash
go run ./cmd/notify-legacy-users -user-ids "123,456"
```

Review the dry-run output, then add `-send` to deliver. The command reads the
Telegram token from `config.json` by default; use `-config` to select another
configuration file. Delivery does not change user data. Users choose whether to
replace their vocabulary by completing `/start`.

The Docker image includes the utility alongside the bot. In the production
Compose deployment, run it as a one-shot container so it reuses the selected
bot service's read-only configuration:

```bash
sudo /usr/local/bin/docker compose \
  -p to-word-reminder \
  -f compose.yaml \
  run --rm --no-deps recall-bot \
  /app/notify-legacy-users \
  -user-ids "123,456"
```

Use `reminder-bot` instead of `recall-bot` when targeting the reminder
installation. Add `-send` only after reviewing the dry-run recipient list.

## Development

- Run tests: `go test ./...`
- Format and vet: `go fmt ./... && go vet ./...`
- Build binary: `mkdir -p bin && go build -o bin/tg-word-reminder ./cmd/tg-word-reminder`
- Build container: `docker build -t tg-word-reminder .`

## Project Layout

- `cmd/tg-word-reminder` - application entrypoint
- `cmd/notify-legacy-users` - dry-run-first legacy-user invitation tool
- `pkg/bot/handlers` - Telegram handlers
- `pkg/bot/onboarding` - onboarding state machine and provisioning
- `pkg/bot/training` - spaced-repetition training flow
- `pkg/bot/game` - quiz session logic
- `pkg/bot/reminders` - periodic reminders
- `pkg/bot/importexport` - CSV import/export
- `pkg/db` - GORM models, repository, migrations
- `pkg/config` - JSON config loading
- `pkg/logger` - slog wrapper

## Additional Docs

- `docs/2026-02-12-onboarding-wizard-design.md`
- `docs/2026-02-12-onboarding-wizard-implementation.md`
- `docs/2026-01-15-spaced-repetition.md`
- `docs/2025-12-24-game.md`
- `docs/2025-12-28-import-export.md`

## License

MIT. See `LICENSE`.
