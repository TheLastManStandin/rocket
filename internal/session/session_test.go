package session

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cahisa/racketka/internal/game"
	"github.com/cahisa/racketka/internal/wallet"
)

type move struct {
	userID int64
	delta  int64
	reason string
}

type fakeWallet struct {
	mu       sync.Mutex
	balances map[int64]int64
	moves    []move
}

func newFakeWallet(opening int64) *fakeWallet {
	return &fakeWallet{balances: map[int64]int64{1: opening, 2: opening}}
}

func (w *fakeWallet) Balance(_ context.Context, userID int64) (int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.balances[userID], nil
}

func (w *fakeWallet) Debit(_ context.Context, userID, amount int64, reason, _ string) (int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.balances[userID] < amount {
		return 0, wallet.ErrInsufficientFunds
	}
	w.balances[userID] -= amount
	w.moves = append(w.moves, move{userID, -amount, reason})
	return w.balances[userID], nil
}

func (w *fakeWallet) Credit(_ context.Context, userID, amount int64, reason, _ string) (int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.balances[userID] += amount
	w.moves = append(w.moves, move{userID, amount, reason})
	return w.balances[userID], nil
}

func (w *fakeWallet) reasons() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, 0, len(w.moves))
	for _, m := range w.moves {
		out = append(out, m.reason)
	}
	return out
}

// alice is the player these tests sit at the table.
var alice = game.NewPlayer(1, "Alice", "")

// testConfig keeps the betting window wide so tests are not racing the clock.
func testConfig() game.Config {
	return game.Config{
		MinBet:        10,
		MaxBet:        10000,
		HouseEdge:     0.04,
		MaxCrash:      1000,
		BettingWindow: 10 * time.Second,
		CrashedPause:  time.Second,
	}
}

func TestEveryPlayerLandsOnTheSameTable(t *testing.T) {
	m := NewManager(testConfig(), newFakeWallet(5000), nil, nil)
	defer m.Shutdown()

	first := m.Acquire()
	again := m.Acquire()
	other := m.Acquire()
	defer func() { m.Release(); m.Release(); m.Release() }()

	if first != again || first != other {
		t.Error("players ended up on tables of their own")
	}
	if got := m.Online(); got != 3 {
		t.Errorf("Online() = %d, want the 3 connections", got)
	}
}

// The table deals whether or not anybody is watching, so a disconnect costs
// nothing and a round runs on through an empty room.
func TestTableDealsOnWithNobodyWatching(t *testing.T) {
	m := NewManager(testConfig(), newFakeWallet(5000), nil, nil)
	defer m.Shutdown()

	first := m.Acquire()
	m.Release()
	if got := m.Online(); got != 0 {
		t.Errorf("Online() = %d after the last player left, want 0", got)
	}

	// Nobody is watching, and the round is still running: it answers, and it
	// is the same round it was before.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	before := roundOf(ctx, t, first)
	time.Sleep(50 * time.Millisecond)

	resumed := m.Acquire()
	defer m.Release()
	if resumed != first {
		t.Fatal("reconnecting landed on a different table")
	}
	if got := roundOf(ctx, t, resumed); got != before {
		t.Errorf("the round changed from %d to %d over an empty room", before, got)
	}
}

// roundOf asks the table which round it is on.
func roundOf(ctx context.Context, t *testing.T, s *Session) int64 {
	t.Helper()
	var id int64
	if err := s.do(ctx, func(g *game.Game, _ time.Time) { id = g.RoundID() }); err != nil {
		t.Fatalf("the table stopped answering: %v", err)
	}
	return id
}

func TestSubscribeOpensWithASnapshot(t *testing.T) {
	m := NewManager(testConfig(), newFakeWallet(5000), nil, nil)
	defer m.Shutdown()

	s := m.Acquire()
	defer m.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	events, unsubscribe, err := s.Subscribe(ctx, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()

	select {
	case first := <-events:
		if first.Type != game.EventState {
			t.Errorf("stream opened with %q, want %q", first.Type, game.EventState)
		}
		if first.Phase != game.PhaseBetting {
			t.Errorf("snapshot phase is %q, want %q", first.Phase, game.PhaseBetting)
		}
	case <-ctx.Done():
		t.Fatal("no snapshot arrived")
	}

	// The table opens empty and fills over the countdown, so the bots follow the
	// snapshot as arrivals rather than riding along inside it.
	for {
		select {
		case e := <-events:
			if e.Type == game.EventBotJoined && len(e.Bots) > 0 {
				return
			}
		case <-ctx.Done():
			t.Fatal("no bot ever sat down")
		}
	}
}

func TestPlaceBetTakesTheStakeAndAnnouncesIt(t *testing.T) {
	w := newFakeWallet(5000)
	m := NewManager(testConfig(), w, nil, nil)
	defer m.Shutdown()

	s := m.Acquire()
	defer m.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	events, unsubscribe, err := s.Subscribe(ctx, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	<-events // snapshot

	balance, err := s.PlaceBet(ctx, alice, 500)
	if err != nil {
		t.Fatalf("PlaceBet returned %v", err)
	}
	if balance != 4500 {
		t.Errorf("balance after a 500 stake is %d, want 4500", balance)
	}

	// The stake goes out to the table, the balance only to whoever staked it.
	placed := waitFor(ctx, t, events, game.EventBetPlaced)
	if placed.Amount != 500 {
		t.Errorf("bet_placed carried %d, want 500", placed.Amount)
	}
	if placed.Player == nil || placed.Player.ID != alice.ID {
		t.Errorf("bet_placed says it belongs to %+v, want alice", placed.Player)
	}
	if placed.Balance != 0 {
		t.Errorf("bet_placed carried a balance of %d over the table", placed.Balance)
	}
	if told := waitFor(ctx, t, events, game.EventBalance); told.Balance != 4500 {
		t.Errorf("the player was told their balance is %d, want 4500", told.Balance)
	}
}

// A stake the round will not take has to come straight back.
func TestPlaceBetRefundsWhenTheRoundWillNotTakeIt(t *testing.T) {
	w := newFakeWallet(5000)
	m := NewManager(testConfig(), w, nil, nil)
	defer m.Shutdown()

	s := m.Acquire()
	defer m.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := s.PlaceBet(ctx, alice, 500); err != nil {
		t.Fatal(err)
	}
	balance, err := s.PlaceBet(ctx, alice, 700)
	if !errors.Is(err, game.ErrAlreadyBet) {
		t.Fatalf("a second bet returned %v, want ErrAlreadyBet", err)
	}
	if balance != 4500 {
		t.Errorf("balance is %d after the rejected stake, want 4500 with the 700 refunded", balance)
	}

	want := []string{wallet.ReasonBet, wallet.ReasonBet, wallet.ReasonRefund}
	if got := w.reasons(); len(got) != len(want) {
		t.Fatalf("ledger recorded %v, want %v", got, want)
	}
}

// A stake taken in flight is paid for straight away and waits for the next
// round rather than being turned away.
func TestBetMadeInFlightIsQueuedForTheNextRound(t *testing.T) {
	cfg := testConfig()
	// Take off almost at once and then stay up, so the test never races a
	// burst it did not ask for.
	cfg.BettingWindow = 20 * time.Millisecond
	cfg.CrashedPause = 10 * time.Second
	cfg.DrawCrash = func() game.Multiplier { return 100000 }

	w := newFakeWallet(5000)
	m := NewManager(cfg, w, nil, nil)
	defer m.Shutdown()

	s := m.Acquire()
	defer m.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	events, unsubscribe, err := s.Subscribe(ctx, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	waitFor(ctx, t, events, game.EventTookOff)

	balance, err := s.PlaceBet(ctx, alice, 500)
	if err != nil {
		t.Fatalf("betting in flight returned %v", err)
	}
	if balance != 4500 {
		t.Errorf("balance after a queued 500 stake is %d, want 4500", balance)
	}
	queued := waitFor(ctx, t, events, game.EventBetQueued)
	if queued.Amount != 500 {
		t.Errorf("bet_queued carried %d, want 500", queued.Amount)
	}
	if told := waitFor(ctx, t, events, game.EventBalance); told.Balance != 4500 {
		t.Errorf("the player was told their balance is %d, want 4500", told.Balance)
	}

	if _, err := s.PlaceBet(ctx, alice, 500); !errors.Is(err, game.ErrAlreadyQueued) {
		t.Errorf("queueing twice returned %v, want ErrAlreadyQueued", err)
	}

	// The refused second stake is the only thing that comes back; the first is
	// down for the next round and stays down.
	want := []string{wallet.ReasonBet, wallet.ReasonBet, wallet.ReasonRefund}
	if got := w.reasons(); len(got) != len(want) {
		t.Errorf("ledger recorded %v, want %v", got, want)
	}
}

// waitFor drains the stream until the named event turns up.
func waitFor(ctx context.Context, t *testing.T, events <-chan game.Event, typ string) game.Event {
	t.Helper()
	for {
		select {
		case e, open := <-events:
			if !open {
				t.Fatalf("the stream closed before %q arrived", typ)
			}
			if e.Type == typ {
				return e
			}
		case <-ctx.Done():
			t.Fatalf("%q never arrived", typ)
		}
	}
}

func TestPlaceBetRejectsStakesOutsideTheLimitsWithoutTouchingTheWallet(t *testing.T) {
	w := newFakeWallet(5000)
	m := NewManager(testConfig(), w, nil, nil)
	defer m.Shutdown()

	s := m.Acquire()
	defer m.Release()

	ctx := context.Background()
	for _, amount := range []int64{5, 50000} {
		if _, err := s.PlaceBet(ctx, alice, amount); !errors.Is(err, game.ErrStakeOutOfRange) {
			t.Errorf("staking %d returned %v, want ErrStakeOutOfRange", amount, err)
		}
	}
	if got := w.reasons(); len(got) != 0 {
		t.Errorf("an out-of-range stake still moved money: %v", got)
	}
}

func TestCashOutBeforeTakeOffIsRefused(t *testing.T) {
	m := NewManager(testConfig(), newFakeWallet(5000), nil, nil)
	defer m.Shutdown()

	s := m.Acquire()
	defer m.Release()

	ctx := context.Background()
	if _, err := s.PlaceBet(ctx, alice, 500); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.CashOut(ctx, alice.ID); !errors.Is(err, game.ErrNotFlying) {
		t.Errorf("cashing out during betting returned %v, want ErrNotFlying", err)
	}
}

func TestClosedSessionStopsServingCommands(t *testing.T) {
	m := NewManager(testConfig(), newFakeWallet(5000), nil, nil)
	s := m.Acquire()

	// Only the server going down closes the table.
	m.Shutdown()
	deadline := time.After(2 * time.Second)
	for {
		if _, err := s.PlaceBet(context.Background(), alice, 500); errors.Is(err, ErrSessionClosed) {
			break
		}
		select {
		case <-deadline:
			t.Fatal("the table kept taking bets after shutdown")
		case <-time.After(5 * time.Millisecond):
		}
	}

	if _, err := s.PlaceBet(context.Background(), alice, 500); !errors.Is(err, ErrSessionClosed) {
		t.Errorf("betting on a closed session returned %v, want ErrSessionClosed", err)
	}
}
