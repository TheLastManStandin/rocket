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

func TestManagerGivesEveryPlayerTheirOwnGame(t *testing.T) {
	m := NewManager(testConfig(), newFakeWallet(5000), nil, nil)
	defer m.Shutdown()

	first := m.Acquire(1)
	again := m.Acquire(1)
	other := m.Acquire(2)
	defer func() { m.Release(1); m.Release(1); m.Release(2) }()

	if first != again {
		t.Error("a second connection from the same player started a second game")
	}
	if first == other {
		t.Error("two players ended up sharing one game")
	}
	if got := m.Online(); got != 2 {
		t.Errorf("Online() = %d, want 2", got)
	}
}

func TestSessionSurvivesAReconnectInsideTheGracePeriod(t *testing.T) {
	m := NewManager(testConfig(), newFakeWallet(5000), nil, nil)
	defer m.Shutdown()
	m.grace = 300 * time.Millisecond

	first := m.Acquire(1)
	m.Release(1)

	// Back before the grace period runs out: same game, same round.
	time.Sleep(50 * time.Millisecond)
	resumed := m.Acquire(1)
	if resumed != first {
		t.Fatal("reconnecting inside the grace period started a fresh game")
	}

	m.Release(1)
	deadline := time.After(2 * time.Second)
	for m.Online() > 0 {
		select {
		case <-deadline:
			t.Fatal("the game was never torn down after the grace period")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestSubscribeOpensWithASnapshot(t *testing.T) {
	m := NewManager(testConfig(), newFakeWallet(5000), nil, nil)
	defer m.Shutdown()

	s := m.Acquire(1)
	defer m.Release(1)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	events, unsubscribe, err := s.Subscribe(ctx)
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

	s := m.Acquire(1)
	defer m.Release(1)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	events, unsubscribe, err := s.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	<-events // snapshot

	balance, err := s.PlaceBet(ctx, 500)
	if err != nil {
		t.Fatalf("PlaceBet returned %v", err)
	}
	if balance != 4500 {
		t.Errorf("balance after a 500 stake is %d, want 4500", balance)
	}

	for {
		select {
		case e := <-events:
			if e.Type == game.EventBetPlaced {
				if e.Amount != 500 || e.Balance != 4500 {
					t.Errorf("bet_placed carried %d at balance %d, want 500 at 4500", e.Amount, e.Balance)
				}
				return
			}
		case <-ctx.Done():
			t.Fatal("the bet was never announced")
		}
	}
}

// A stake the round will not take has to come straight back.
func TestPlaceBetRefundsWhenTheRoundWillNotTakeIt(t *testing.T) {
	w := newFakeWallet(5000)
	m := NewManager(testConfig(), w, nil, nil)
	defer m.Shutdown()

	s := m.Acquire(1)
	defer m.Release(1)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, err := s.PlaceBet(ctx, 500); err != nil {
		t.Fatal(err)
	}
	balance, err := s.PlaceBet(ctx, 700)
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
// round, and cancelling it puts the money back.
func TestBetMadeInFlightIsQueuedAndRefundableUntilItRides(t *testing.T) {
	cfg := testConfig()
	// Take off almost at once and then stay up, so the test never races a
	// burst it did not ask for.
	cfg.BettingWindow = 20 * time.Millisecond
	cfg.CrashedPause = 10 * time.Second
	cfg.DrawCrash = func() game.Multiplier { return 100000 }

	w := newFakeWallet(5000)
	m := NewManager(cfg, w, nil, nil)
	defer m.Shutdown()

	s := m.Acquire(1)
	defer m.Release(1)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	events, unsubscribe, err := s.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	waitFor(ctx, t, events, game.EventTookOff)

	balance, err := s.PlaceBet(ctx, 500)
	if err != nil {
		t.Fatalf("betting in flight returned %v", err)
	}
	if balance != 4500 {
		t.Errorf("balance after a queued 500 stake is %d, want 4500", balance)
	}
	queued := waitFor(ctx, t, events, game.EventBetQueued)
	if queued.Amount != 500 || queued.Balance != 4500 {
		t.Errorf("bet_queued carried %d at balance %d, want 500 at 4500", queued.Amount, queued.Balance)
	}

	balance, err = s.CancelBet(ctx)
	if err != nil {
		t.Fatalf("cancelling returned %v", err)
	}
	if balance != 5000 {
		t.Errorf("balance after cancelling is %d, want the 5000 it started at", balance)
	}
	if back := waitFor(ctx, t, events, game.EventBetCancelled); back.Balance != 5000 {
		t.Errorf("bet_cancelled carried a balance of %d, want 5000", back.Balance)
	}

	if _, err := s.CancelBet(ctx); !errors.Is(err, game.ErrNoQueuedBet) {
		t.Errorf("cancelling twice returned %v, want ErrNoQueuedBet", err)
	}

	want := []string{wallet.ReasonBet, wallet.ReasonRefund}
	if got := w.reasons(); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
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

	s := m.Acquire(1)
	defer m.Release(1)

	ctx := context.Background()
	for _, amount := range []int64{5, 50000} {
		if _, err := s.PlaceBet(ctx, amount); !errors.Is(err, game.ErrStakeOutOfRange) {
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

	s := m.Acquire(1)
	defer m.Release(1)

	ctx := context.Background()
	if _, err := s.PlaceBet(ctx, 500); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.CashOut(ctx); !errors.Is(err, game.ErrNotFlying) {
		t.Errorf("cashing out during betting returned %v, want ErrNotFlying", err)
	}
}

func TestClosedSessionStopsServingCommands(t *testing.T) {
	m := NewManager(testConfig(), newFakeWallet(5000), nil, nil)
	m.grace = 10 * time.Millisecond

	s := m.Acquire(1)
	m.Release(1)

	deadline := time.After(2 * time.Second)
	for m.Online() > 0 {
		select {
		case <-deadline:
			t.Fatal("the session never shut down")
		case <-time.After(5 * time.Millisecond):
		}
	}

	if _, err := s.PlaceBet(context.Background(), 500); !errors.Is(err, ErrSessionClosed) {
		t.Errorf("betting on a closed session returned %v, want ErrSessionClosed", err)
	}
}
