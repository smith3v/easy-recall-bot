package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	telegram "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/smith3v/tg-word-reminder/pkg/config"
)

const invitationMessage = `👋 Hi! We've improved the bot with guided setup and a much larger starter vocabulary 📚

If you'd like a fresh start, send /start and follow the reset prompts ✨

🔒 Your current cards will remain unchanged unless you finish setup and tap Initialize.`

type messageSender interface {
	SendMessage(context.Context, *telegram.SendMessageParams) (*models.Message, error)
}

type senderFactory func(string) (messageSender, error)
type tokenLoader func(string) (string, error)

func main() {
	os.Exit(run(
		context.Background(),
		os.Args[1:],
		os.Stdout,
		os.Stderr,
		loadTelegramToken,
		newTelegramSender,
	))
}

func run(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	loadToken tokenLoader,
	newSender senderFactory,
) int {
	flags := flag.NewFlagSet("notify-legacy-users", flag.ContinueOnError)
	flags.SetOutput(stderr)

	configPath := flags.String("config", "config.json", "path to the bot configuration")
	rawUserIDs := flags.String("user-ids", "", "comma-separated Telegram user IDs")
	send := flags.Bool("send", false, "deliver messages; without this flag the command is a dry run")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected positional arguments")
		return 2
	}

	userIDs, err := parseUserIDs(*rawUserIDs)
	if err != nil {
		fmt.Fprintf(stderr, "invalid -user-ids: %v\n", err)
		return 2
	}

	if !*send {
		fmt.Fprintf(stdout, "Dry run: %d recipient(s)\n", len(userIDs))
		for _, userID := range userIDs {
			fmt.Fprintln(stdout, userID)
		}
		fmt.Fprintln(stdout, "No messages sent. Re-run with -send to deliver.")
		return 0
	}

	token, err := loadToken(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "failed to load Telegram token: %v\n", err)
		return 1
	}
	sender, err := newSender(token)
	if err != nil {
		fmt.Fprintf(stderr, "failed to create Telegram client: %v\n", err)
		return 1
	}

	sent := 0
	skipped := 0
	failed := 0
	for _, userID := range userIDs {
		if _, err := sender.SendMessage(ctx, &telegram.SendMessageParams{
			ChatID: userID,
			Text:   invitationMessage,
		}); err != nil {
			if errors.Is(err, telegram.ErrorForbidden) {
				skipped++
				fmt.Fprintf(stdout, "%d: skipped (bot unavailable to user)\n", userID)
				continue
			}
			failed++
			fmt.Fprintf(stderr, "%d: failed: %v\n", userID, err)
			continue
		}
		sent++
		fmt.Fprintf(stdout, "%d: sent\n", userID)
	}

	fmt.Fprintf(stdout, "Summary: %d sent, %d skipped, %d failed\n", sent, skipped, failed)
	if failed > 0 {
		return 1
	}
	return 0
}

func parseUserIDs(value string) ([]int64, error) {
	if strings.TrimSpace(value) == "" {
		return nil, errors.New("at least one user ID is required")
	}

	seen := make(map[int64]struct{})
	userIDs := make([]int64, 0)
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, errors.New("user IDs must not contain empty entries")
		}
		userID, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a valid integer", part)
		}
		if userID <= 0 {
			return nil, fmt.Errorf("%d must be positive", userID)
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		userIDs = append(userIDs, userID)
	}
	return userIDs, nil
}

func loadTelegramToken(configPath string) (string, error) {
	if err := config.LoadConfig(configPath); err != nil {
		return "", err
	}
	token := strings.TrimSpace(config.AppConfig.Telegram.Token)
	if token == "" {
		return "", errors.New("telegram.token is empty")
	}
	return token, nil
}

func newTelegramSender(token string) (messageSender, error) {
	return telegram.New(token, telegram.WithSkipGetMe())
}
