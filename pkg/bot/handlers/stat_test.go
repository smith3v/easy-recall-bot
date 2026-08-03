package handlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	telegram "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/smith3v/tg-word-reminder/pkg/bot/stats"
	"github.com/smith3v/tg-word-reminder/pkg/logger"
)

type fakeStatsStarter struct {
	job     stats.Job
	err     error
	started chan int64
}

func (starter *fakeStatsStarter) Start(_ context.Context, userID int64) (stats.Job, error) {
	if starter.started != nil {
		starter.started <- userID
	}
	return starter.job, starter.err
}

type fakeStatsJob struct {
	report         stats.Report
	err            error
	release        <-chan struct{}
	waitForContext bool
}

func (job *fakeStatsJob) Collect(ctx context.Context) (stats.Report, error) {
	if job.waitForContext {
		<-ctx.Done()
		return stats.Report{}, ctx.Err()
	}
	if job.release != nil {
		select {
		case <-job.release:
		case <-ctx.Done():
			return stats.Report{}, ctx.Err()
		}
	}
	return job.report, job.err
}

type statTestClient struct {
	texts chan string
}

func newStatTestClient() *statTestClient {
	return &statTestClient{texts: make(chan string, 10)}
}

func (client *statTestClient) Do(req *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, fmt.Errorf("read request body: %w", err)
	}
	if err := req.Body.Close(); err != nil {
		return nil, fmt.Errorf("close request body: %w", err)
	}
	text, err := statRequestText(req.Header.Get("Content-Type"), body)
	if err != nil {
		return nil, err
	}
	client.texts <- text

	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"ok":true,"result":{}}`)),
		Header:     make(http.Header),
	}, nil
}

func statRequestText(contentType string, body []byte) (string, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", fmt.Errorf("parse media type: %w", err)
	}
	if !strings.HasPrefix(mediaType, "multipart/") {
		return "", fmt.Errorf("unexpected media type %q", mediaType)
	}

	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read multipart request: %w", err)
		}
		if part.FormName() != "text" {
			continue
		}
		value, err := io.ReadAll(part)
		if err != nil {
			return "", fmt.Errorf("read text field: %w", err)
		}
		return string(value), nil
	}
	return "", errors.New("text field not found")
}

func newStatTestBot(t *testing.T, client *statTestClient) *telegram.Bot {
	t.Helper()
	b, err := telegram.New(
		"test-token",
		telegram.WithSkipGetMe(),
		telegram.WithHTTPClient(time.Second, client),
	)
	if err != nil {
		t.Fatalf("failed to create test bot: %v", err)
	}
	return b
}

func receiveStatText(t *testing.T, client *statTestClient) string {
	t.Helper()
	select {
	case text := <-client.texts:
		return text
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for statistics message")
		return ""
	}
}

func assertNoStatText(t *testing.T, client *statTestClient) {
	t.Helper()
	select {
	case text := <-client.texts:
		t.Fatalf("unexpected statistics message: %q", text)
	case <-time.After(50 * time.Millisecond):
	}
}

func privateStatUpdate(userID int64) *models.Update {
	update := newTestUpdate("/stat", userID)
	update.Message.Chat.Type = models.ChatTypePrivate
	return update
}

func TestStatHandlerAcknowledgesThenSendsAsynchronousResult(t *testing.T) {
	logger.SetLogLevel(logger.ERROR)
	release := make(chan struct{})
	starter := &fakeStatsStarter{
		started: make(chan int64, 1),
		job: &fakeStatsJob{
			release: release,
			report: stats.Report{
				SnapshotAt: time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC),
				Vocabulary: stats.VocabularyReport{
					Total:      1,
					New:        1,
					NewPercent: 100,
				},
			},
		},
	}
	handler := NewStatHandler(starter)
	client := newStatTestClient()
	b := newStatTestBot(t, client)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	handler.Handle(ctx, b, privateStatUpdate(7101))

	if gotUser := <-starter.started; gotUser != 7101 {
		t.Fatalf("starter user = %d, want 7101", gotUser)
	}
	ack := receiveStatText(t, client)
	if ack != "Collecting your stats… I’ll send a new message when it’s ready." {
		t.Fatalf("unexpected acknowledgement: %q", ack)
	}
	assertNoStatText(t, client)

	close(release)
	result := receiveStatText(t, client)
	if !strings.Contains(result, "Learning stats") || !strings.Contains(result, "Total: 1 card") {
		t.Fatalf("unexpected result: %q", result)
	}
}

func TestStatHandlerBusySendsOneMessageWithoutQueueing(t *testing.T) {
	logger.SetLogLevel(logger.ERROR)
	starter := &fakeStatsStarter{
		err:     stats.ErrBusy,
		started: make(chan int64, 1),
	}
	handler := NewStatHandler(starter)
	client := newStatTestClient()
	b := newStatTestBot(t, client)

	handler.Handle(context.Background(), b, privateStatUpdate(7102))

	<-starter.started
	got := receiveStatText(t, client)
	if !strings.Contains(got, "busy right now") {
		t.Fatalf("expected busy message, got %q", got)
	}
	assertNoStatText(t, client)
}

func TestStatHandlerCollectionFailureAcknowledgesThenRetriesLater(t *testing.T) {
	logger.SetLogLevel(logger.ERROR)
	starter := &fakeStatsStarter{
		started: make(chan int64, 1),
		job:     &fakeStatsJob{err: errors.New("query failed")},
	}
	handler := NewStatHandler(starter)
	client := newStatTestClient()
	b := newStatTestBot(t, client)

	handler.Handle(context.Background(), b, privateStatUpdate(7103))

	<-starter.started
	ack := receiveStatText(t, client)
	if !strings.HasPrefix(ack, "Collecting your stats") {
		t.Fatalf("expected acknowledgement, got %q", ack)
	}
	failure := receiveStatText(t, client)
	if !strings.Contains(failure, "try again later") {
		t.Fatalf("expected retry-later message, got %q", failure)
	}
}

func TestStatHandlerEmptyReportSendsNoLearningData(t *testing.T) {
	logger.SetLogLevel(logger.ERROR)
	starter := &fakeStatsStarter{
		started: make(chan int64, 1),
		job: &fakeStatsJob{report: stats.Report{
			SnapshotAt: time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC),
		}},
	}
	handler := NewStatHandler(starter)
	client := newStatTestClient()
	b := newStatTestBot(t, client)

	handler.Handle(context.Background(), b, privateStatUpdate(7104))

	<-starter.started
	receiveStatText(t, client)
	result := receiveStatText(t, client)
	if !strings.Contains(result, "No learning data yet.") {
		t.Fatalf("expected no-learning-data message, got %q", result)
	}
}

func TestStatHandlerTimeoutSendsRetryLater(t *testing.T) {
	logger.SetLogLevel(logger.ERROR)
	starter := &fakeStatsStarter{
		started: make(chan int64, 1),
		job:     &fakeStatsJob{waitForContext: true},
	}
	handler := NewStatHandler(starter)
	handler.timeout = 15 * time.Millisecond
	client := newStatTestClient()
	b := newStatTestBot(t, client)

	handler.Handle(context.Background(), b, privateStatUpdate(7105))

	<-starter.started
	receiveStatText(t, client)
	failure := receiveStatText(t, client)
	if !strings.Contains(failure, "try again later") {
		t.Fatalf("expected timeout failure message, got %q", failure)
	}
}

func TestStatHandlerRejectsNonPrivateChat(t *testing.T) {
	logger.SetLogLevel(logger.ERROR)
	starter := &fakeStatsStarter{started: make(chan int64, 1)}
	handler := NewStatHandler(starter)
	client := newStatTestClient()
	b := newStatTestBot(t, client)
	update := privateStatUpdate(7106)
	update.Message.Chat.Type = models.ChatTypeGroup

	handler.Handle(context.Background(), b, update)

	got := receiveStatText(t, client)
	if got != "The /stat command works only in private chat." {
		t.Fatalf("unexpected private-chat warning: %q", got)
	}
	select {
	case userID := <-starter.started:
		t.Fatalf("unexpected statistics start for user %d", userID)
	default:
	}
}

func TestStatHandlerRejectsInvalidUpdate(t *testing.T) {
	logger.SetLogLevel(logger.ERROR)
	starter := &fakeStatsStarter{started: make(chan int64, 1)}
	handler := NewStatHandler(starter)
	client := newStatTestClient()
	b := newStatTestBot(t, client)

	handler.Handle(context.Background(), b, nil)

	assertNoStatText(t, client)
	select {
	case userID := <-starter.started:
		t.Fatalf("unexpected statistics start for user %d", userID)
	default:
	}
}
