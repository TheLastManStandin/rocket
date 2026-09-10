package ws_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/cahisa/racketka/internal/auth"
	"github.com/cahisa/racketka/internal/game"
	"github.com/cahisa/racketka/internal/session"
	"github.com/cahisa/racketka/internal/wallet"
	"github.com/cahisa/racketka/internal/ws"
)

type fakeWallet struct {
	mu      sync.Mutex
	balance int64
}

func (w *fakeWallet) Balance(context.Context, int64) (int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.balance, nil
}

func (w *fakeWallet) Debit(_ context.Context, _, amount int64, _, _ string) (int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.balance < amount {
		return 0, wallet.ErrInsufficientFunds
	}
	w.balance -= amount
	return w.balance, nil
}

func (w *fakeWallet) Credit(_ context.Context, _, amount int64, _, _ string) (int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.balance += amount
	return w.balance, nil
}

// rig is a live server wired to a short round, so a test can watch a whole
// flight without waiting on the production timings.
type rig struct {
	url    string
	issuer *auth.Issuer
	purse  *fakeWallet
}

func newRig(t *testing.T, crash game.Multiplier) *rig {
	t.Helper()

	purse := &fakeWallet{balance: 5000}
	issuer := auth.NewIssuer([]byte("test-secret"), time.Hour)

	manager := session.NewManager(game.Config{
		MinBet:        10,
		MaxBet:        10000,
		BettingWindow: 300 * time.Millisecond,
		CrashedPause:  200 * time.Millisecond,
		DrawCrash:     func() game.Multiplier { return crash },
	}, purse, nil, nil)
	t.Cleanup(manager.Shutdown)

	srv := httptest.NewServer(ws.Handler(ws.Deps{
		Manager: manager,
		Issuer:  issuer,
		Wallet:  purse,
	}))
	t.Cleanup(srv.Close)

	return &rig{url: "ws" + strings.TrimPrefix(srv.URL, "http"), issuer: issuer, purse: purse}
}

func (r *rig) token(t *testing.T, userID int64) string {
	t.Helper()
	tok, err := r.issuer.Issue(auth.Claims{UserID: userID, TgID: userID}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func (r *rig) dial(t *testing.T, ctx context.Context, token string) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.Dial(ctx, r.url+"?token="+token, nil)
	if err != nil {
		t.Fatalf("dialling the socket: %v", err)
	}
	t.Cleanup(func() { conn.CloseNow() }) //nolint:errcheck
	return conn
}

// readUntil drains the socket until want arrives, collecting everything on the
// way so a test can assert on the whole exchange.
func readUntil(ctx context.Context, conn *websocket.Conn, want string) ([]game.Event, error) {
	var seen []game.Event
	for {
		var e game.Event
		if err := wsjson.Read(ctx, conn, &e); err != nil {
			return seen, err
		}
		seen = append(seen, e)
		if e.Type == want {
			return seen, nil
		}
	}
}

func TestSocketRefusesAnyoneWithoutAGoodToken(t *testing.T) {
	rig := newRig(t, 1000)

	tests := map[string]string{
		"no token":      "",
		"garbage token": "not.a.token",
		"another issuer": func() string {
			other := auth.NewIssuer([]byte("a-different-secret"), time.Hour)
			tok, err := other.Issue(auth.Claims{UserID: 1, TgID: 1}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			return tok
		}(),
	}

	for name, token := range tests {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			conn, resp, err := websocket.Dial(ctx, rig.url+"?token="+token, nil)
			if err == nil {
				conn.CloseNow() //nolint:errcheck
				t.Fatal("the socket opened without a valid token")
			}
			if resp == nil {
				t.Fatalf("dial failed without a response: %v", err)
			}
			defer resp.Body.Close()
			if _, err := io.Copy(io.Discard, resp.Body); err != nil {
				t.Log(err)
			}
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("status is %d, want %d", resp.StatusCode, http.StatusUnauthorized)
			}
		})
	}
}

func TestSocketOpensWithTheBalanceAndASnapshot(t *testing.T) {
	rig := newRig(t, 1000)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn := rig.dial(t, ctx, rig.token(t, 1))

	seen, err := readUntil(ctx, conn, game.EventState)
	if err != nil {
		t.Fatalf("waiting for the snapshot: %v", err)
	}

	var snapshot, balance *game.Event
	for i := range seen {
		switch seen[i].Type {
		case game.EventState:
			snapshot = &seen[i]
		case game.EventBalance:
			balance = &seen[i]
		}
	}

	if snapshot == nil {
		t.Fatal("no snapshot arrived")
	}
	if snapshot.Phase != game.PhaseBetting {
		t.Errorf("snapshot phase is %q, want %q", snapshot.Phase, game.PhaseBetting)
	}
	// The balance event may land either side of the snapshot; only its presence
	// is guaranteed.
	if balance == nil {
		if _, err := readUntil(ctx, conn, game.EventBalance); err != nil {
			t.Fatalf("the balance never arrived: %v", err)
		}
	}
}

func TestBetAndCashOutTravelOverTheSocket(t *testing.T) {
	rig := newRig(t, 10000) // x100, so the round is in no hurry
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn := rig.dial(t, ctx, rig.token(t, 1))
	if _, err := readUntil(ctx, conn, game.EventState); err != nil {
		t.Fatal(err)
	}

	if err := wsjson.Write(ctx, conn, map[string]any{"type": "bet", "amount": 500}); err != nil {
		t.Fatal(err)
	}
	seen, err := readUntil(ctx, conn, game.EventBetPlaced)
	if err != nil {
		t.Fatalf("waiting for the bet to be taken: %v", err)
	}
	placed := seen[len(seen)-1]
	if placed.Amount != 500 {
		t.Errorf("bet_placed carried %d, want 500", placed.Amount)
	}
	if placed.Player == nil || placed.Player.ID != 1 {
		t.Errorf("bet_placed says it belongs to %+v, want player 1", placed.Player)
	}
	// The balance follows on its own, addressed to the player who staked.
	told, err := readUntil(ctx, conn, game.EventBalance)
	if err != nil {
		t.Fatalf("the balance never arrived: %v", err)
	}
	if got := told[len(told)-1].Balance; got != 4500 {
		t.Errorf("balance after the stake is %d, want 4500", got)
	}

	// Let the rocket leave, then take the money.
	if _, err := readUntil(ctx, conn, game.EventTookOff); err != nil {
		t.Fatalf("waiting for take-off: %v", err)
	}
	if _, err := readUntil(ctx, conn, game.EventTick); err != nil {
		t.Fatal(err)
	}
	if err := wsjson.Write(ctx, conn, map[string]any{"type": "cashout"}); err != nil {
		t.Fatal(err)
	}

	seen, err = readUntil(ctx, conn, game.EventCashedOut)
	if err != nil {
		t.Fatalf("waiting for the cash-out: %v", err)
	}
	settled := seen[len(seen)-1]
	if settled.Multiplier < game.One {
		t.Errorf("settled at %v, want at least x1.00", settled.Multiplier)
	}
	if want := settled.Multiplier.Payout(500); settled.Payout != want {
		t.Errorf("paid %d, want %d for 500 at %v", settled.Payout, want, settled.Multiplier)
	}
	paid, err := readUntil(ctx, conn, game.EventBalance)
	if err != nil {
		t.Fatalf("the balance never arrived: %v", err)
	}
	if got := paid[len(paid)-1].Balance; got != 4500+settled.Payout {
		t.Errorf("balance is %d, want %d", got, 4500+settled.Payout)
	}
}

// The client owns the wording, so refusals have to arrive as stable codes.
func TestRefusalsComeBackAsStableCodes(t *testing.T) {
	rig := newRig(t, 10000)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn := rig.dial(t, ctx, rig.token(t, 1))
	if _, err := readUntil(ctx, conn, game.EventState); err != nil {
		t.Fatal(err)
	}

	t.Run("stake out of range", func(t *testing.T) {
		if err := wsjson.Write(ctx, conn, map[string]any{"type": "bet", "amount": 1}); err != nil {
			t.Fatal(err)
		}
		seen, err := readUntil(ctx, conn, game.EventError)
		if err != nil {
			t.Fatal(err)
		}
		if code := seen[len(seen)-1].Code; code != "stake_out_of_range" {
			t.Errorf("refusal code is %q, want %q", code, "stake_out_of_range")
		}
	})

	t.Run("cashing out with nothing down", func(t *testing.T) {
		if err := wsjson.Write(ctx, conn, map[string]any{"type": "cashout"}); err != nil {
			t.Fatal(err)
		}
		seen, err := readUntil(ctx, conn, game.EventError)
		if err != nil {
			t.Fatal(err)
		}
		// Either refusal is honest here: it depends whether the rocket has left
		// by the time the command lands.
		switch code := seen[len(seen)-1].Code; code {
		case "no_bet", "not_flying":
		default:
			t.Errorf("refusal code is %q, want no_bet or not_flying", code)
		}
	})

	t.Run("an unknown command", func(t *testing.T) {
		if err := wsjson.Write(ctx, conn, map[string]any{"type": "launch_the_missiles"}); err != nil {
			t.Fatal(err)
		}
		seen, err := readUntil(ctx, conn, game.EventError)
		if err != nil {
			t.Fatal(err)
		}
		if code := seen[len(seen)-1].Code; code != "unknown" {
			t.Errorf("refusal code is %q, want %q", code, "unknown")
		}
	})
}

// Everyone plays the same round, and sees who else is in it.
func TestPlayersShareOneRoundAndSeeEachOther(t *testing.T) {
	rig := newRig(t, 10000)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	first := rig.dial(t, ctx, rig.token(t, 1))
	second := rig.dial(t, ctx, rig.token(t, 2))

	snapshotOf := func(conn *websocket.Conn) game.Event {
		t.Helper()
		seen, err := readUntil(ctx, conn, game.EventState)
		if err != nil {
			t.Fatal(err)
		}
		return seen[len(seen)-1]
	}

	a, b := snapshotOf(first), snapshotOf(second)
	if a.RoundID == 0 || b.RoundID == 0 {
		t.Fatal("a player opened without a round")
	}
	if a.RoundID != b.RoundID {
		t.Errorf("players opened on rounds %d and %d, want the same one", a.RoundID, b.RoundID)
	}

	if err := wsjson.Write(ctx, first, map[string]any{"type": "bet", "amount": 500}); err != nil {
		t.Fatal(err)
	}

	// The stake reaches the other player, carrying who it belongs to.
	seen, err := readUntil(ctx, second, game.EventBetPlaced)
	if err != nil {
		t.Fatalf("the first player's stake never reached the second: %v", err)
	}
	placed := seen[len(seen)-1]
	if placed.Amount != 500 {
		t.Errorf("the stake arrived as %d, want 500", placed.Amount)
	}
	if placed.Player == nil || placed.Player.ID != 1 {
		t.Fatalf("the stake arrived from %+v, want player 1", placed.Player)
	}

	// Their balance does not: it went out only to the player who staked.
	for _, e := range seen {
		if e.Type == game.EventBalance {
			t.Error("one player's balance was broadcast over the table")
		}
	}
	if placed.Balance != 0 {
		t.Errorf("bet_placed carried a balance of %d over the table", placed.Balance)
	}
}
