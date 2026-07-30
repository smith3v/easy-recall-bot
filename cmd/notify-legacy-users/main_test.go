package main

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	telegram "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func TestParseUserIDs(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    []int64
		wantErr bool
	}{
		{name: "valid and deduplicated", value: " 30,10,30,20 ", want: []int64{30, 10, 20}},
		{name: "missing", value: "", wantErr: true},
		{name: "empty entry", value: "10,,20", wantErr: true},
		{name: "not integer", value: "10,user", wantErr: true},
		{name: "zero", value: "0", wantErr: true},
		{name: "negative", value: "-10", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseUserIDs(tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got IDs %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseUserIDs returned an error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parseUserIDs = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRunDryRunDoesNotLoadConfigOrCreateSender(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run(
		context.Background(),
		[]string{"-user-ids", "30,10,30"},
		&stdout,
		&stderr,
		func(string) (string, error) {
			t.Fatal("dry run must not load configuration")
			return "", nil
		},
		func(string) (messageSender, error) {
			t.Fatal("dry run must not create a Telegram sender")
			return nil, nil
		},
	)

	if exitCode != 0 {
		t.Fatalf("run exit code = %d, stderr = %q", exitCode, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "Dry run: 2 recipient(s)") ||
		!strings.Contains(got, "\n30\n10\n") ||
		!strings.Contains(got, "No messages sent") {
		t.Fatalf("unexpected dry-run output: %q", got)
	}
}

func TestRunSendReportsPartialFailure(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	sender := &fakeMessageSender{
		errorsByUserID: map[int64]error{20: errors.New("network failure")},
	}

	exitCode := run(
		context.Background(),
		[]string{"-config", "production.json", "-user-ids", "10,20", "-send"},
		&stdout,
		&stderr,
		func(path string) (string, error) {
			if path != "production.json" {
				t.Fatalf("config path = %q", path)
			}
			return "test-token", nil
		},
		func(token string) (messageSender, error) {
			if token != "test-token" {
				t.Fatalf("token = %q", token)
			}
			return sender, nil
		},
	)

	if exitCode != 1 {
		t.Fatalf("run exit code = %d, want 1", exitCode)
	}
	if !reflect.DeepEqual(sender.userIDs, []int64{10, 20}) {
		t.Fatalf("sent user IDs = %v", sender.userIDs)
	}
	if got := stdout.String(); !strings.Contains(got, "10: sent") ||
		!strings.Contains(got, "Summary: 1 sent, 0 skipped, 1 failed") {
		t.Fatalf("unexpected stdout: %q", got)
	}
	if !strings.Contains(stderr.String(), "20: failed: network failure") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestRunSendSkipsForbiddenRecipients(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	sender := &fakeMessageSender{
		errorsByUserID: map[int64]error{
			20: errors.Join(telegram.ErrorForbidden, errors.New("user is deactivated")),
		},
	}

	exitCode := run(
		context.Background(),
		[]string{"-user-ids", "10,20", "-send"},
		&stdout,
		&stderr,
		func(string) (string, error) { return "test-token", nil },
		func(string) (messageSender, error) { return sender, nil },
	)

	if exitCode != 0 {
		t.Fatalf("run exit code = %d, stderr = %q", exitCode, stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "20: skipped") ||
		!strings.Contains(got, "Summary: 1 sent, 1 skipped, 0 failed") {
		t.Fatalf("unexpected stdout: %q", got)
	}
}

func TestRunRejectsInvalidIDsBeforeLoadingConfig(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run(
		context.Background(),
		[]string{"-user-ids", "10,invalid", "-send"},
		&stdout,
		&stderr,
		func(string) (string, error) {
			t.Fatal("invalid input must not load configuration")
			return "", nil
		},
		func(string) (messageSender, error) {
			t.Fatal("invalid input must not create a Telegram sender")
			return nil, nil
		},
	)

	if exitCode != 2 {
		t.Fatalf("run exit code = %d, want 2", exitCode)
	}
	if !strings.Contains(stderr.String(), "invalid -user-ids") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

type fakeMessageSender struct {
	userIDs        []int64
	errorsByUserID map[int64]error
}

func (s *fakeMessageSender) SendMessage(_ context.Context, params *telegram.SendMessageParams) (*models.Message, error) {
	userID, ok := params.ChatID.(int64)
	if !ok {
		return nil, errors.New("unexpected chat ID type")
	}
	s.userIDs = append(s.userIDs, userID)
	if err := s.errorsByUserID[userID]; err != nil {
		return nil, err
	}
	if params.Text != invitationMessage {
		return nil, errors.New("unexpected invitation text")
	}
	return &models.Message{}, nil
}
