package game

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/smith3v/tg-word-reminder/pkg/db"
	"github.com/smith3v/tg-word-reminder/pkg/internal/testutil"
)

type testClock struct {
	t time.Time
}

func (c *testClock) Now() time.Time {
	return c.t
}

func (c *testClock) Advance(d time.Duration) {
	c.t = c.t.Add(d)
}

func TestGetSessionKey(t *testing.T) {
	got := getSessionKey(10, 20)
	if got != "10:20" {
		t.Fatalf("expected key to be %q, got %q", "10:20", got)
	}
}

func TestStartOrRestartCreatesDeckAndSetsCurrent(t *testing.T) {
	clock := &testClock{t: time.Date(2024, 2, 1, 8, 0, 0, 0, time.UTC)}
	manager := NewGameManager(clock.Now)

	pairs := []db.WordPair{
		{ID: 1, UserID: 10, Word1: "hola", Word2: "adios"},
		{ID: 2, UserID: 10, Word1: "uno", Word2: "dos"},
	}

	session := manager.StartOrRestart(100, 200, pairs)
	if session == nil {
		t.Fatalf("expected session to be created")
	}
	if session.chatID != 100 || session.userID != 200 {
		t.Fatalf("unexpected session identifiers: chat=%d user=%d", session.chatID, session.userID)
	}
	if !session.startedAt.Equal(clock.t) || !session.lastActivityAt.Equal(clock.t) {
		t.Fatalf("expected timestamps to be initialized from clock")
	}
	if session.correctCount != 0 || session.attemptCount != 0 {
		t.Fatalf("expected counters to start at zero")
	}
	if session.currentCard == nil || session.currentResolved {
		t.Fatalf("expected a current card and unresolved state")
	}
	if session.currentMessageID != 0 || session.currentToken == "" {
		t.Fatalf("expected prompt metadata to be initialized")
	}
	expectedTotal := len(pairs) * 2
	if len(session.deck) != expectedTotal-1 {
		t.Fatalf("expected deck size %d, got %d", expectedTotal-1, len(session.deck))
	}

	key := getSessionKey(100, 200)
	manager.mu.Lock()
	stored := manager.sessions[key]
	manager.mu.Unlock()
	if stored != session {
		t.Fatalf("expected session stored in map")
	}

	expectedCounts := make(map[string]int)
	for _, card := range buildDeck(pairs) {
		cardKey := fmt.Sprintf("%d:%s:%s:%s", card.PairID, card.Direction, card.Shown, card.Expected)
		expectedCounts[cardKey]++
	}
	seenCounts := make(map[string]int)
	for _, card := range session.deck {
		cardKey := fmt.Sprintf("%d:%s:%s:%s", card.PairID, card.Direction, card.Shown, card.Expected)
		seenCounts[cardKey]++
	}
	current := *session.currentCard
	currentKey := fmt.Sprintf("%d:%s:%s:%s", current.PairID, current.Direction, current.Shown, current.Expected)
	seenCounts[currentKey]++
	if len(expectedCounts) != len(seenCounts) {
		t.Fatalf("expected card set size to match after start")
	}
	for key, count := range expectedCounts {
		if seenCounts[key] != count {
			t.Fatalf("card %s count mismatch", key)
		}
	}
}

func TestNextPromptDequeuesAndSetsCurrent(t *testing.T) {
	manager := NewGameManager(time.Now)
	card1 := Card{
		PairID:    1,
		Direction: DirectionAToB,
		Shown:     "a",
		Expected:  "b",
	}
	card2 := Card{
		PairID:    2,
		Direction: DirectionBToA,
		Shown:     "d",
		Expected:  "c",
	}
	session := &GameSession{
		deck:            []Card{card1, card2},
		currentResolved: true,
	}

	got, ok := manager.NextPrompt(session)
	if !ok {
		t.Fatalf("expected card to be dequeued")
	}
	if got != card1 {
		t.Fatalf("expected first card, got %+v", got)
	}
	if len(session.deck) != 1 || session.deck[0] != card2 {
		t.Fatalf("expected remaining deck to contain second card")
	}
	if session.currentCard == nil || *session.currentCard != card1 {
		t.Fatalf("expected current card to be set")
	}
	if session.currentResolved || session.currentMessageID != 0 || session.currentToken == "" {
		t.Fatalf("expected prompt to be unresolved with zero message ID")
	}
}

func TestResolveCorrectUpdatesCounts(t *testing.T) {
	clock := &testClock{t: time.Date(2024, 2, 1, 8, 0, 0, 0, time.UTC)}
	manager := NewGameManager(clock.Now)
	card := Card{
		PairID:    1,
		Direction: DirectionAToB,
		Shown:     "a",
		Expected:  "b",
	}
	session := &GameSession{
		currentCard:     &card,
		currentResolved: false,
	}

	clock.Advance(2 * time.Minute)
	manager.ResolveCorrect(session)

	if session.attemptCount != 1 || session.correctCount != 1 {
		t.Fatalf("expected counts to increment, got attempts=%d correct=%d", session.attemptCount, session.correctCount)
	}
	if !session.currentResolved {
		t.Fatalf("expected current card to be marked resolved")
	}
	if !session.lastActivityAt.Equal(clock.t) {
		t.Fatalf("expected lastActivityAt to update")
	}
}

func TestResolveCorrectPersistsState(t *testing.T) {
	testutil.SetupTestDB(t)
	clock := &testClock{t: time.Date(2024, 2, 2, 8, 0, 0, 0, time.UTC)}
	manager := NewGameManager(clock.Now)

	pairs := []db.WordPair{
		{ID: 1, UserID: 22, Word1: "hola", Word2: "adios"},
	}

	session := manager.StartOrRestart(11, 22, pairs)
	if session == nil {
		t.Fatalf("expected session")
	}

	manager.ResolveCorrect(session)

	var state db.GameSession
	if err := db.DB.Where("chat_id = ? AND user_id = ?", 11, 22).First(&state).Error; err != nil {
		t.Fatalf("failed to load game session state: %v", err)
	}
	if state.ScoreCorrect != 1 || state.ScoreAttempted != 1 {
		t.Fatalf("expected persisted counts 1/1, got correct=%d attempted=%d", state.ScoreCorrect, state.ScoreAttempted)
	}
	if state.CurrentToken == "" {
		t.Fatalf("expected persisted token")
	}
}

func TestResolveMissRequeueUpdatesCountsAndDeck(t *testing.T) {
	clock := &testClock{t: time.Date(2024, 2, 1, 8, 0, 0, 0, time.UTC)}
	manager := NewGameManager(clock.Now)
	card := Card{
		PairID:    3,
		Direction: DirectionAToB,
		Shown:     "x",
		Expected:  "y",
	}
	session := &GameSession{
		deck:            []Card{},
		currentCard:     &card,
		currentResolved: false,
	}

	clock.Advance(5 * time.Minute)
	manager.ResolveMissRequeue(session)

	if session.attemptCount != 1 || session.correctCount != 0 {
		t.Fatalf("expected miss counts to update, got attempts=%d correct=%d", session.attemptCount, session.correctCount)
	}
	if !session.currentResolved {
		t.Fatalf("expected current card to be marked resolved")
	}
	if len(session.deck) != 1 || session.deck[0] != card {
		t.Fatalf("expected card to be requeued")
	}
	if !session.lastActivityAt.Equal(clock.t) {
		t.Fatalf("expected lastActivityAt to update")
	}
}

func TestFinishStatsFormatsStats(t *testing.T) {
	manager := NewGameManager(time.Now)

	message := manager.FinishStats(nil)
	expected := "Game over!\nYou got 0 correct answers.\nAccuracy: 0% (0/0)"
	if message != expected {
		t.Fatalf("unexpected message: %q", message)
	}

	session := &GameSession{
		correctCount: 2,
		attemptCount: 3,
	}
	message = manager.FinishStats(session)
	expected = "Game over!\nYou got 2 correct answers.\nAccuracy: 67% (2/3)"
	if message != expected {
		t.Fatalf("unexpected stats message: %q", message)
	}
}

func TestSelectRandomPairsReturnsDistinctPairs(t *testing.T) {
	testutil.SetupTestDB(t)

	userID := int64(800)
	otherID := int64(801)
	var pairs []db.WordPair
	for i := range 8 {
		pairs = append(pairs, db.WordPair{
			UserID: userID,
			Word1:  fmt.Sprintf("word-%d", i),
			Word2:  fmt.Sprintf("term-%d", i),
		})
	}
	for i := range 3 {
		pairs = append(pairs, db.WordPair{
			UserID: otherID,
			Word1:  fmt.Sprintf("other-%d", i),
			Word2:  fmt.Sprintf("else-%d", i),
		})
	}
	if err := db.DB.Create(&pairs).Error; err != nil {
		t.Fatalf("failed to seed pairs: %v", err)
	}

	selected, err := SelectRandomPairs(userID, DeckPairs)
	if err != nil {
		t.Fatalf("failed to select pairs: %v", err)
	}
	if len(selected) != DeckPairs {
		t.Fatalf("expected %d pairs, got %d", DeckPairs, len(selected))
	}
	seen := make(map[uint]struct{})
	for _, pair := range selected {
		if pair.UserID != userID {
			t.Fatalf("expected user_id %d, got %d", userID, pair.UserID)
		}
		if _, exists := seen[pair.ID]; exists {
			t.Fatalf("expected distinct pairs, got duplicate ID %d", pair.ID)
		}
		seen[pair.ID] = struct{}{}
	}
}

func TestBuildDeckCreatesTwoCardsPerPair(t *testing.T) {
	pairs := []db.WordPair{
		{ID: 1, Word1: "uno", Word2: "one"},
		{ID: 2, Word1: "dos", Word2: "two"},
	}
	deck := buildDeck(pairs)
	if len(deck) != len(pairs)*2 {
		t.Fatalf("expected %d cards, got %d", len(pairs)*2, len(deck))
	}
	counts := make(map[string]int)
	for _, card := range deck {
		key := fmt.Sprintf("%d:%s", card.PairID, card.Direction)
		counts[key]++
	}
	for _, pair := range pairs {
		for _, direction := range []CardDirection{DirectionAToB, DirectionBToA} {
			key := fmt.Sprintf("%d:%s", pair.ID, direction)
			if counts[key] != 1 {
				t.Fatalf("expected one card for %s, got %d", key, counts[key])
			}
		}
	}
}

func TestShuffleRetainsAllCards(t *testing.T) {
	pairs := []db.WordPair{
		{ID: 1, Word1: "uno", Word2: "one"},
		{ID: 2, Word1: "dos", Word2: "two"},
		{ID: 3, Word1: "tres", Word2: "three"},
	}
	deck := buildDeck(pairs)
	before := make(map[string]int)
	for _, card := range deck {
		key := fmt.Sprintf("%d:%s:%s:%s", card.PairID, card.Direction, card.Shown, card.Expected)
		before[key]++
	}

	shuffleDeck(deck)

	after := make(map[string]int)
	for _, card := range deck {
		key := fmt.Sprintf("%d:%s:%s:%s", card.PairID, card.Direction, card.Shown, card.Expected)
		after[key]++
	}
	if len(before) != len(after) {
		t.Fatalf("expected card set size to match after shuffle")
	}
	for key, count := range before {
		if after[key] != count {
			t.Fatalf("card %s count changed after shuffle", key)
		}
	}
}

func TestBuildCardSetsShownAndExpectedByDirection(t *testing.T) {
	pair := db.WordPair{ID: 7, Word1: "hola", Word2: "adios"}

	card := buildCard(pair, DirectionAToB)
	if card.PairID != pair.ID || card.Direction != DirectionAToB {
		t.Fatalf("unexpected card metadata: %+v", card)
	}
	if card.Shown != pair.Word1 || card.Expected != pair.Word2 {
		t.Fatalf("expected A_to_B mapping, got %+v", card)
	}

	card = buildCard(pair, DirectionBToA)
	if card.PairID != pair.ID || card.Direction != DirectionBToA {
		t.Fatalf("unexpected card metadata: %+v", card)
	}
	if card.Shown != pair.Word2 || card.Expected != pair.Word1 {
		t.Fatalf("expected B_to_A mapping, got %+v", card)
	}
}

type fakeSender struct {
	messages []sentMessage
}

type sentMessage struct {
	chatID int64
	text   string
}

func (s *fakeSender) SendMessage(_ context.Context, chatID int64, text string) error {
	s.messages = append(s.messages, sentMessage{chatID: chatID, text: text})
	return nil
}

func TestCollectInactiveRemovesExpiredSessions(t *testing.T) {
	clock := &testClock{t: time.Date(2024, 2, 1, 10, 0, 0, 0, time.UTC)}
	manager := NewGameManager(clock.Now)

	expiredSession := &GameSession{
		chatID:         9,
		userID:         9,
		lastActivityAt: clock.t.Add(-InactivityTimeout - time.Second),
		attemptCount:   2,
		correctCount:   1,
	}
	activeSession := &GameSession{
		chatID:         10,
		userID:         10,
		lastActivityAt: clock.t.Add(-InactivityTimeout + time.Second),
	}

	manager.sessions[getSessionKey(9, 9)] = expiredSession
	manager.sessions[getSessionKey(10, 10)] = activeSession

	expired := manager.collectInactive(clock.Now())
	if len(expired) != 1 {
		t.Fatalf("expected one expired session, got %d", len(expired))
	}
	if expired[0].chatID != 9 {
		t.Fatalf("expected expired chatID 9, got %d", expired[0].chatID)
	}
	if manager.GetSession(9, 9) != nil {
		t.Fatalf("expected expired session to be removed")
	}
	if manager.GetSession(10, 10) == nil {
		t.Fatalf("expected active session to remain")
	}
}

func TestEndAllForUserRemovesOnlyMatchingSessionsAndClosesStatistics(t *testing.T) {
	testutil.SetupTestDB(t)

	clock := &testClock{t: time.Date(2024, 2, 1, 10, 0, 0, 0, time.UTC)}
	manager := NewGameManager(clock.Now)
	target := manager.StartOrRestart(11, 21, []db.WordPair{
		{ID: 1, UserID: 21, Word1: "hola", Word2: "hello"},
	})
	other := manager.StartOrRestart(12, 22, []db.WordPair{
		{ID: 2, UserID: 22, Word1: "adios", Word2: "goodbye"},
	})

	clock.Advance(5 * time.Minute)
	manager.EndAllForUser(21, "reset")

	if manager.GetSession(11, 21) != nil {
		t.Fatalf("expected target user's session to be removed")
	}
	if manager.GetSession(12, 22) != other {
		t.Fatalf("expected other user's session to remain")
	}

	targetStats := fetchGameSession(t, target.sessionID)
	if targetStats.EndedAt == nil || targetStats.EndedReason == nil || targetStats.DurationSeconds == nil {
		t.Fatalf("expected target session statistics to be closed")
	}
	if targetStats.EndedAt.UTC() != clock.t {
		t.Fatalf("expected ended_at %v, got %v", clock.t, targetStats.EndedAt.UTC())
	}
	if *targetStats.EndedReason != "reset" {
		t.Fatalf("expected ended_reason reset, got %q", *targetStats.EndedReason)
	}
	if *targetStats.DurationSeconds != int((5 * time.Minute).Seconds()) {
		t.Fatalf("expected duration_seconds %d, got %d", int((5 * time.Minute).Seconds()), *targetStats.DurationSeconds)
	}

	otherStats := fetchGameSession(t, other.sessionID)
	if otherStats.EndedAt != nil || otherStats.EndedReason != nil || otherStats.DurationSeconds != nil {
		t.Fatalf("expected other user's statistics to remain open")
	}
}

func TestSweepInactiveSendsStats(t *testing.T) {
	clock := &testClock{t: time.Date(2024, 2, 1, 11, 0, 0, 0, time.UTC)}
	manager := NewGameManager(clock.Now)

	session := &GameSession{
		chatID:         12,
		userID:         12,
		lastActivityAt: clock.t.Add(-InactivityTimeout - time.Second),
		attemptCount:   3,
		correctCount:   2,
	}
	manager.sessions[getSessionKey(12, 12)] = session

	sender := &fakeSender{}
	manager.SweepInactive(context.Background(), sender)

	if len(sender.messages) != 1 {
		t.Fatalf("expected one message sent, got %d", len(sender.messages))
	}
	if sender.messages[0].chatID != 12 {
		t.Fatalf("expected chatID 12, got %d", sender.messages[0].chatID)
	}
	if sender.messages[0].text == "" {
		t.Fatalf("expected stats text to be sent")
	}
}

func TestResolveTextAttemptCorrectDiscards(t *testing.T) {
	clock := &testClock{t: time.Date(2024, 2, 1, 9, 0, 0, 0, time.UTC)}
	manager := NewGameManager(clock.Now)

	current := Card{PairID: 1, Direction: DirectionAToB, Shown: "hola", Expected: "adios"}
	next := Card{PairID: 2, Direction: DirectionBToA, Shown: "uno", Expected: "one"}
	last := Card{PairID: 3, Direction: DirectionAToB, Shown: "dos", Expected: "two"}

	session := &GameSession{
		chatID:           1,
		userID:           2,
		currentCard:      &current,
		currentMessageID: 10,
		currentResolved:  false,
		deck:             []Card{next, last},
	}
	manager.sessions[getSessionKey(1, 2)] = session

	clock.Advance(30 * time.Second)
	result := manager.ResolveTextAttempt(1, 2, "  Adios ")
	if !result.Handled || !result.Correct {
		t.Fatalf("expected handled correct result, got %+v", result)
	}
	if result.PromptMessageID != 10 {
		t.Fatalf("expected prompt message ID 10, got %d", result.PromptMessageID)
	}
	if result.NextCard == nil || *result.NextCard != next {
		t.Fatalf("expected next card to be dequeued")
	}

	updated := manager.GetSession(1, 2)
	if updated == nil {
		t.Fatalf("expected session to remain active")
	}
	if updated.attemptCount != 1 || updated.correctCount != 1 {
		t.Fatalf("expected counts to increment, got attempts=%d correct=%d", updated.attemptCount, updated.correctCount)
	}
	if !updated.lastActivityAt.Equal(clock.t) {
		t.Fatalf("expected lastActivityAt to update")
	}
	if len(updated.deck) != 1 || updated.deck[0] != last {
		t.Fatalf("expected one remaining card after dequeuing, got %+v", updated.deck)
	}
}

func TestResolveTextAttemptIncorrectRequeues(t *testing.T) {
	clock := &testClock{t: time.Date(2024, 2, 1, 9, 0, 0, 0, time.UTC)}
	manager := NewGameManager(clock.Now)

	current := Card{PairID: 1, Direction: DirectionAToB, Shown: "hola", Expected: "adios"}
	next := Card{PairID: 2, Direction: DirectionBToA, Shown: "uno", Expected: "one"}

	session := &GameSession{
		chatID:           3,
		userID:           4,
		currentCard:      &current,
		currentMessageID: 22,
		currentResolved:  false,
		deck:             []Card{next},
	}
	manager.sessions[getSessionKey(3, 4)] = session

	result := manager.ResolveTextAttempt(3, 4, "nope")
	if !result.Handled || result.Correct {
		t.Fatalf("expected handled incorrect result, got %+v", result)
	}
	if result.NextCard == nil || *result.NextCard != next {
		t.Fatalf("expected next card to be dequeued")
	}

	updated := manager.GetSession(3, 4)
	if updated == nil {
		t.Fatalf("expected session to remain active")
	}
	if updated.attemptCount != 1 || updated.correctCount != 0 {
		t.Fatalf("expected counts to increment for miss, got attempts=%d correct=%d", updated.attemptCount, updated.correctCount)
	}
	if len(updated.deck) != 1 || updated.deck[0] != current {
		t.Fatalf("expected current card to be requeued, got %+v", updated.deck)
	}
}

func TestResolveTextAttemptAcceptsCommaOption(t *testing.T) {
	manager := NewGameManager(time.Now)
	current := Card{PairID: 1, Direction: DirectionAToB, Shown: "hola", Expected: "adios, hasta luego"}
	session := &GameSession{
		chatID:           7,
		userID:           8,
		currentCard:      &current,
		currentMessageID: 33,
		currentResolved:  false,
		deck:             []Card{},
	}
	manager.sessions[getSessionKey(7, 8)] = session

	result := manager.ResolveTextAttempt(7, 8, " Hasta luego ")
	if !result.Handled || !result.Correct {
		t.Fatalf("expected comma option to be accepted, got %+v", result)
	}
}

func TestResolveTextAttemptAcceptsAnswerWithoutParentheticalClarification(t *testing.T) {
	manager := NewGameManager(time.Now)
	current := Card{PairID: 1, Direction: DirectionAToB, Shown: "gehen", Expected: "go (on foot)"}
	session := &GameSession{
		chatID:           9,
		userID:           10,
		currentCard:      &current,
		currentMessageID: 44,
		currentResolved:  false,
		deck:             []Card{},
	}
	manager.sessions[getSessionKey(9, 10)] = session

	result := manager.ResolveTextAttempt(9, 10, "go")
	if !result.Handled || !result.Correct {
		t.Fatalf("expected answer without parenthetical clarification to be accepted, got %+v", result)
	}
}

func TestResolveTextAttemptAcceptsCommaOptionWithoutParentheticalClarification(t *testing.T) {
	manager := NewGameManager(time.Now)
	current := Card{
		PairID: 1, Direction: DirectionAToB, Shown: "hola",
		Expected: "adios (formal), hasta luego (casual)",
	}
	session := &GameSession{
		chatID:           17,
		userID:           18,
		currentCard:      &current,
		currentMessageID: 88,
		currentResolved:  false,
		deck:             []Card{},
	}
	manager.sessions[getSessionKey(17, 18)] = session

	result := manager.ResolveTextAttempt(17, 18, "hasta luego")
	if !result.Handled || !result.Correct {
		t.Fatalf("expected comma option without clarification to be accepted, got %+v", result)
	}
}

func TestResolveTextAttemptAcceptsFullCommaAnswerWhenPromptHasComma(t *testing.T) {
	manager := NewGameManager(time.Now)
	current := Card{PairID: 1, Direction: DirectionAToB, Shown: "new york, usa", Expected: "adios, hasta luego"}
	session := &GameSession{
		chatID:           11,
		userID:           12,
		currentCard:      &current,
		currentMessageID: 55,
		currentResolved:  false,
		deck:             []Card{},
	}
	manager.sessions[getSessionKey(11, 12)] = session

	result := manager.ResolveTextAttempt(11, 12, "adios, hasta luego")
	if !result.Handled || !result.Correct {
		t.Fatalf("expected full comma answer to be accepted, got %+v", result)
	}
}

func TestResolveTextAttemptAcceptsFullCommaAnswerWhenPromptHasCommaAndClarifications(t *testing.T) {
	manager := NewGameManager(time.Now)
	current := Card{
		PairID: 1, Direction: DirectionAToB, Shown: "new york, usa",
		Expected: "adios (formal), hasta luego (casual)",
	}
	session := &GameSession{
		chatID:           19,
		userID:           20,
		currentCard:      &current,
		currentMessageID: 99,
		currentResolved:  false,
		deck:             []Card{},
	}
	manager.sessions[getSessionKey(19, 20)] = session

	result := manager.ResolveTextAttempt(19, 20, "adios, hasta luego")
	if !result.Handled || !result.Correct {
		t.Fatalf("expected full comma answer without clarifications to be accepted, got %+v", result)
	}
}

func TestResolveTextAttemptAcceptsSingleAnswerWhenPromptHasComma(t *testing.T) {
	manager := NewGameManager(time.Now)
	current := Card{PairID: 1, Direction: DirectionAToB, Shown: "new york, usa", Expected: "adios, hasta luego"}
	session := &GameSession{
		chatID:           13,
		userID:           14,
		currentCard:      &current,
		currentMessageID: 66,
		currentResolved:  false,
		deck:             []Card{},
	}
	manager.sessions[getSessionKey(13, 14)] = session

	result := manager.ResolveTextAttempt(13, 14, "Hasta luego")
	if !result.Handled || !result.Correct {
		t.Fatalf("expected single answer to be accepted, got %+v", result)
	}
}

func TestResolveTextAttemptRejectsPartialCommaAnswerWhenPromptHasComma(t *testing.T) {
	manager := NewGameManager(time.Now)
	current := Card{PairID: 1, Direction: DirectionAToB, Shown: "new york, usa", Expected: "adios, hasta luego"}
	session := &GameSession{
		chatID:           15,
		userID:           16,
		currentCard:      &current,
		currentMessageID: 77,
		currentResolved:  false,
		deck:             []Card{},
	}
	manager.sessions[getSessionKey(15, 16)] = session

	result := manager.ResolveTextAttempt(15, 16, "adios, nope")
	if !result.Handled || result.Correct {
		t.Fatalf("expected partial comma answer to be rejected, got %+v", result)
	}
}

func TestResolveTextAttemptFinishesOnEmptyDeck(t *testing.T) {
	manager := NewGameManager(time.Now)
	current := Card{PairID: 1, Direction: DirectionAToB, Shown: "hola", Expected: "adios"}
	session := &GameSession{
		chatID:           5,
		userID:           6,
		currentCard:      &current,
		currentMessageID: 11,
		currentResolved:  false,
		deck:             []Card{},
	}
	manager.sessions[getSessionKey(5, 6)] = session

	result := manager.ResolveTextAttempt(5, 6, "adios")
	if !result.Handled || !result.Correct {
		t.Fatalf("expected handled correct result, got %+v", result)
	}
	if result.StatsText == "" {
		t.Fatalf("expected stats text for finished session")
	}
	if manager.GetSession(5, 6) != nil {
		t.Fatalf("expected session to be removed after finish")
	}
}

func TestNormalizeAnswer(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"  Hello  ", "hello"},
		{"Hello   World", "hello world"},
		{"Hello, ", "hello"},
		{"Hello!!!", "hello"},
		{"Hello ! ", "hello"},
		{"  ", ""},
	}

	for _, tc := range cases {
		got := normalizeAnswer(tc.input)
		if got != tc.expected {
			t.Fatalf("normalizeAnswer(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestNormalizeOptionalParentheses(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"go (on foot)", "go"},
		{"go(on foot)", "go"},
		{"(formal) hello", "hello"},
		{"hello (formal)!", "hello"},
		{"no parentheses", "no parentheses"},
	}

	for _, tc := range cases {
		got := normalizeOptionalParentheses(tc.input)
		if got != tc.expected {
			t.Fatalf("normalizeOptionalParentheses(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestGameSessionPersistenceStartCreatesRow(t *testing.T) {
	testutil.SetupTestDB(t)

	clock := &testClock{t: time.Date(2024, 3, 1, 9, 0, 0, 0, time.UTC)}
	manager := NewGameManager(clock.Now)
	pairs := []db.WordPair{
		{ID: 1, UserID: 501, Word1: "hola", Word2: "adios"},
	}

	session := manager.StartOrRestart(1001, 501, pairs)
	if session.sessionID == 0 {
		t.Fatalf("expected session to persist and have an ID")
	}

	stored := fetchGameSession(t, session.sessionID)
	if stored.UserID != 501 {
		t.Fatalf("expected user_id 501, got %d", stored.UserID)
	}
	if stored.StartedAt.UTC() != clock.t {
		t.Fatalf("expected started_at %v, got %v", clock.t, stored.StartedAt.UTC())
	}
	if stored.SessionDate.Year() != clock.t.Year() ||
		stored.SessionDate.Month() != clock.t.Month() ||
		stored.SessionDate.Day() != clock.t.Day() {
		t.Fatalf("unexpected session_date: %v", stored.SessionDate)
	}
	if stored.EndedAt != nil || stored.EndedReason != nil || stored.DurationSeconds != nil {
		t.Fatalf("expected end fields to be null for new session")
	}
	if stored.AttemptCount != 0 || stored.CorrectCount != 0 {
		t.Fatalf("expected counts to start at zero")
	}
}

func TestGameSessionPersistenceAttemptsUpdateCounts(t *testing.T) {
	tests := []struct {
		name         string
		apply        func(manager *GameManager, session *GameSession, chatID, userID int64)
		wantAttempts int
		wantCorrect  int
	}{
		{
			name: "text-correct",
			apply: func(manager *GameManager, session *GameSession, chatID, userID int64) {
				manager.ResolveTextAttempt(chatID, userID, session.currentCard.Expected)
			},
			wantAttempts: 1,
			wantCorrect:  1,
		},
		{
			name: "reveal-miss",
			apply: func(manager *GameManager, session *GameSession, chatID, userID int64) {
				manager.ResolveRevealAttempt(chatID, userID, session.currentToken, session.currentMessageID)
			},
			wantAttempts: 1,
			wantCorrect:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.SetupTestDB(t)

			clock := &testClock{t: time.Date(2024, 3, 2, 10, 0, 0, 0, time.UTC)}
			manager := NewGameManager(clock.Now)
			pairs := []db.WordPair{
				{ID: 1, UserID: 601, Word1: "uno", Word2: "one"},
			}
			chatID := int64(1002)
			userID := int64(601)
			session := manager.StartOrRestart(chatID, userID, pairs)
			manager.SetCurrentMessageID(session, 77)

			tt.apply(manager, session, chatID, userID)

			stored := fetchGameSession(t, session.sessionID)
			if stored.AttemptCount != tt.wantAttempts || stored.CorrectCount != tt.wantCorrect {
				t.Fatalf("expected attempts=%d correct=%d, got attempts=%d correct=%d",
					tt.wantAttempts, tt.wantCorrect, stored.AttemptCount, stored.CorrectCount)
			}
		})
	}
}

func TestGameSessionPersistenceFinishUpdatesEndFields(t *testing.T) {
	testutil.SetupTestDB(t)

	clock := &testClock{t: time.Date(2024, 3, 3, 11, 0, 0, 0, time.UTC)}
	manager := NewGameManager(clock.Now)
	pairs := []db.WordPair{
		{ID: 1, UserID: 701, Word1: "uno", Word2: "one"},
	}
	chatID := int64(1003)
	userID := int64(701)
	session := manager.StartOrRestart(chatID, userID, pairs)
	manager.SetCurrentMessageID(session, 88)
	session.deck = nil

	clock.Advance(10 * time.Minute)
	manager.ResolveTextAttempt(chatID, userID, session.currentCard.Expected)

	stored := fetchGameSession(t, session.sessionID)
	if stored.EndedAt == nil || stored.EndedReason == nil || stored.DurationSeconds == nil {
		t.Fatalf("expected end fields to be set")
	}
	if stored.EndedAt.UTC() != clock.t {
		t.Fatalf("expected ended_at %v, got %v", clock.t, stored.EndedAt.UTC())
	}
	if *stored.EndedReason != "finished" {
		t.Fatalf("expected ended_reason finished, got %q", *stored.EndedReason)
	}
	if *stored.DurationSeconds != int((10 * time.Minute).Seconds()) {
		t.Fatalf("expected duration_seconds %d, got %d", int((10 * time.Minute).Seconds()), *stored.DurationSeconds)
	}
	if stored.AttemptCount != 1 || stored.CorrectCount != 1 {
		t.Fatalf("expected attempts=1 correct=1, got attempts=%d correct=%d", stored.AttemptCount, stored.CorrectCount)
	}
}

func TestGameSessionPersistenceTimeoutUpdatesEndFields(t *testing.T) {
	testutil.SetupTestDB(t)

	clock := &testClock{t: time.Date(2024, 3, 4, 12, 0, 0, 0, time.UTC)}
	manager := NewGameManager(clock.Now)
	pairs := []db.WordPair{
		{ID: 1, UserID: 801, Word1: "uno", Word2: "one"},
	}
	chatID := int64(1004)
	userID := int64(801)
	session := manager.StartOrRestart(chatID, userID, pairs)

	clock.Advance(20 * time.Minute)
	expired := manager.collectInactive(clock.t)
	if len(expired) != 1 {
		t.Fatalf("expected one expired session, got %d", len(expired))
	}

	stored := fetchGameSession(t, session.sessionID)
	if stored.EndedAt == nil || stored.EndedReason == nil || stored.DurationSeconds == nil {
		t.Fatalf("expected end fields to be set")
	}
	if stored.EndedAt.UTC() != clock.t {
		t.Fatalf("expected ended_at %v, got %v", clock.t, stored.EndedAt.UTC())
	}
	if *stored.EndedReason != "timeout" {
		t.Fatalf("expected ended_reason timeout, got %q", *stored.EndedReason)
	}
	if *stored.DurationSeconds != int((20 * time.Minute).Seconds()) {
		t.Fatalf("expected duration_seconds %d, got %d", int((20 * time.Minute).Seconds()), *stored.DurationSeconds)
	}
	if stored.AttemptCount != 0 || stored.CorrectCount != 0 {
		t.Fatalf("expected attempts=0 correct=0, got attempts=%d correct=%d", stored.AttemptCount, stored.CorrectCount)
	}
}

func TestGameSessionPersistenceEndDoesNotOverwrite(t *testing.T) {
	testutil.SetupTestDB(t)

	clock := &testClock{t: time.Date(2024, 3, 5, 13, 0, 0, 0, time.UTC)}
	manager := NewGameManager(clock.Now)
	pairs := []db.WordPair{
		{ID: 1, UserID: 901, Word1: "uno", Word2: "one"},
	}
	session := manager.StartOrRestart(1005, 901, pairs)
	session.attemptCount = 2
	session.correctCount = 1

	endFirst := clock.t.Add(5 * time.Minute)
	persistSessionEnd(session, endFirst, "finished")

	endSecond := clock.t.Add(20 * time.Minute)
	persistSessionEnd(session, endSecond, "timeout")

	stored := fetchGameSession(t, session.sessionID)
	if stored.EndedAt == nil || stored.EndedReason == nil || stored.DurationSeconds == nil {
		t.Fatalf("expected end fields to be set")
	}
	if stored.EndedAt.UTC() != endFirst {
		t.Fatalf("expected ended_at %v, got %v", endFirst, stored.EndedAt.UTC())
	}
	if *stored.EndedReason != "finished" {
		t.Fatalf("expected ended_reason finished, got %q", *stored.EndedReason)
	}
	if *stored.DurationSeconds != int((5 * time.Minute).Seconds()) {
		t.Fatalf("expected duration_seconds %d, got %d", int((5 * time.Minute).Seconds()), *stored.DurationSeconds)
	}
	if stored.AttemptCount != 2 || stored.CorrectCount != 1 {
		t.Fatalf("expected attempts=2 correct=1, got attempts=%d correct=%d", stored.AttemptCount, stored.CorrectCount)
	}
}

func fetchGameSession(t *testing.T, sessionID uint) db.GameSessionStatistics {
	t.Helper()
	var stored db.GameSessionStatistics
	if err := db.DB.First(&stored, sessionID).Error; err != nil {
		t.Fatalf("failed to load game session: %v", err)
	}
	return stored
}
