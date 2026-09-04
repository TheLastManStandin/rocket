// Package session drives one player's game: a goroutine owns the round, a
// ticker advances it, and connected clients read events off a fan-out.
package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/cahisa/racketka/internal/game"
	"github.com/cahisa/racketka/internal/storage"
	"github.com/cahisa/racketka/internal/wallet"
)

const (
	// TickInterval is how often the server re-evaluates the round. The client
	// interpolates between these to draw at screen rate.
	TickInterval = 100 * time.Millisecond

	// outboxSize buys a slow client a couple of seconds of ticks before it is
	// cut loose and made to reconnect.
	outboxSize = 256
)

var ErrSessionClosed = errors.New("session: closed")

// Archive keeps finished rounds, so a returning player finds the history strip
// already filled in rather than blank.
type Archive interface {
	RecentCrashPoints(ctx context.Context, userID int64, limit int) ([]int64, error)
	RecordRound(ctx context.Context, r storage.RoundRecord) error
}

// Session is the only owner of its Game. Everything that touches the round
// goes through the command channel, so the game itself needs no locking.
type Session struct {
	userID  int64
	cfg     game.Config
	wallet  wallet.Wallet
	archive Archive
	log     *slog.Logger

	cmds chan func(*game.Game, time.Time)
	done chan struct{}

	mu   sync.Mutex
	subs map[chan game.Event]struct{}
}

func newSession(userID int64, cfg game.Config, w wallet.Wallet, a Archive, log *slog.Logger) *Session {
	return &Session{
		userID:  userID,
		cfg:     cfg,
		wallet:  w,
		archive: a,
		log:     log,
		cmds:    make(chan func(*game.Game, time.Time)),
		done:    make(chan struct{}),
		subs:    map[chan game.Event]struct{}{},
	}
}

// run drives the round until ctx is cancelled. It never performs I/O, so a slow
// database can never stall the curve.
func (s *Session) run(ctx context.Context) {
	defer close(s.done)
	defer s.closeAllSubscribers()

	g := game.New(s.cfg, rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())), time.Now())
	s.restoreHistory(ctx, g)

	ticker := time.NewTicker(TickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			events := g.Advance(time.Now())
			s.fileBursts(events)
			s.broadcast(events)
		case fn := <-s.cmds:
			// time.Now() here is the authoritative receipt instant for a
			// cash-out: the moment the server actually got to the request.
			fn(g, time.Now())
		}
	}
}

// restoreHistory fills the strip from the player's earlier rounds. It runs once
// before the ticker starts, so the only database call on this goroutine happens
// while nothing is being drawn yet.
func (s *Session) restoreHistory(ctx context.Context, g *game.Game) {
	if s.archive == nil {
		return
	}
	past, err := s.archive.RecentCrashPoints(ctx, s.userID, game.HistoryLen)
	if err != nil {
		// A blank strip is a cosmetic loss; refusing to start the game is not.
		if s.log != nil {
			s.log.Warn("could not restore the history strip", "user", s.userID, "error", err)
		}
		return
	}
	restored := make([]game.Multiplier, 0, len(past))
	for _, hundredths := range past {
		restored = append(restored, game.Multiplier(hundredths))
	}
	g.SeedHistory(restored)
}

// fileBursts records finished rounds off the loop. Writing inline would put a
// database round trip in the middle of the curve.
func (s *Session) fileBursts(events []game.Event) {
	if s.archive == nil {
		return
	}
	for _, e := range events {
		if e.Type != game.EventCrashed {
			continue
		}
		record := storage.RoundRecord{UserID: s.userID, CrashPoint: int64(e.Multiplier)}
		if bet := e.Settled; bet != nil {
			record.BetAmount = bet.Amount
			record.CashedOutAt = int64(bet.CashedOutAt)
			record.Payout = bet.Payout
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.archive.RecordRound(ctx, record); err != nil && s.log != nil {
				s.log.Warn("could not record a round", "user", record.UserID, "error", err)
			}
		}()
	}
}

// do runs fn inside the session goroutine, where the game is exclusively owned.
func (s *Session) do(ctx context.Context, fn func(*game.Game, time.Time)) error {
	finished := make(chan struct{})
	wrapped := func(g *game.Game, now time.Time) {
		defer close(finished)
		fn(g, now)
	}

	select {
	case s.cmds <- wrapped:
	case <-s.done:
		return ErrSessionClosed
	case <-ctx.Done():
		return ctx.Err()
	}

	select {
	case <-finished:
		return nil
	case <-s.done:
		return ErrSessionClosed
	case <-ctx.Done():
		return ctx.Err()
	}
}

// PlaceBet takes the stake before recording it, so a bet can never be accepted
// without the money behind it. If the round moves on in between, the stake goes
// straight back.
func (s *Session) PlaceBet(ctx context.Context, amount int64) (int64, error) {
	if amount < s.cfg.MinBet || amount > s.cfg.MaxBet {
		return 0, game.ErrStakeOutOfRange
	}

	ref := fmt.Sprintf("user:%d", s.userID)
	balance, err := s.wallet.Debit(ctx, s.userID, amount, wallet.ReasonBet, ref)
	if err != nil {
		return 0, err
	}

	recorded := error(nil)
	if err := s.do(ctx, func(g *game.Game, _ time.Time) { recorded = g.PlaceBet(amount) }); err != nil {
		recorded = err
	}
	if recorded != nil {
		if refunded, rerr := s.wallet.Credit(ctx, s.userID, amount, wallet.ReasonRefund, ref); rerr == nil {
			balance = refunded
		}
		return balance, recorded
	}

	s.broadcast([]game.Event{{Type: game.EventBetPlaced, Amount: amount, Balance: balance}})
	return balance, nil
}

// CashOut settles at the curve as the server reads it when the request lands,
// never at a multiplier the client claims to be showing.
func (s *Session) CashOut(ctx context.Context) (game.Multiplier, int64, int64, error) {
	var (
		at      game.Multiplier
		payout  int64
		settled error
	)
	if err := s.do(ctx, func(g *game.Game, now time.Time) {
		at, payout, settled = g.CashOut(now)
	}); err != nil {
		return 0, 0, 0, err
	}
	if settled != nil {
		return 0, 0, 0, settled
	}

	balance, err := s.wallet.Credit(ctx, s.userID, payout, wallet.ReasonPayout, fmt.Sprintf("user:%d", s.userID))
	if err != nil {
		// The round is already settled in the game; the ledger is what owes the
		// player, so surface this loudly rather than pretending it paid.
		return at, payout, 0, fmt.Errorf("session: crediting a settled win: %w", err)
	}

	s.broadcast([]game.Event{{
		Type:       game.EventCashedOut,
		Multiplier: at,
		Payout:     payout,
		Balance:    balance,
	}})
	return at, payout, balance, nil
}

// Subscribe hands back a stream that opens with a full snapshot. Registering
// inside the goroutine means no event can slip between the snapshot and the
// first live message.
func (s *Session) Subscribe(ctx context.Context) (<-chan game.Event, func(), error) {
	ch := make(chan game.Event, outboxSize)

	if err := s.do(ctx, func(g *game.Game, now time.Time) {
		ch <- g.Snapshot(now)
		s.mu.Lock()
		s.subs[ch] = struct{}{}
		s.mu.Unlock()
	}); err != nil {
		return nil, nil, err
	}

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if _, live := s.subs[ch]; live {
				delete(s.subs, ch)
				close(ch)
			}
		})
	}
	return ch, cancel, nil
}

// PushBalance reports a balance the round did not cause -- a Stars top-up
// landing mid-flight, say. It deliberately does not touch the game: the money
// arrived from outside it, and a round in progress is none of its business.
func (s *Session) PushBalance(balance int64) {
	s.broadcast([]game.Event{{Type: game.EventBalance, Balance: balance}})
}

func (s *Session) broadcast(events []game.Event) {
	if len(events) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	for ch := range s.subs {
		if !offerAll(ch, events) {
			// A client that cannot keep up gets dropped whole rather than fed a
			// stream with holes in it. Reconnecting hands it a fresh snapshot.
			delete(s.subs, ch)
			close(ch)
		}
	}
}

func offerAll(ch chan game.Event, events []game.Event) bool {
	for _, e := range events {
		select {
		case ch <- e:
		default:
			return false
		}
	}
	return true
}

func (s *Session) closeAllSubscribers() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subs {
		delete(s.subs, ch)
		close(ch)
	}
}
