// Package session drives the table everyone plays on: a goroutine owns the
// round, a ticker advances it, and connected clients read events off a
// fan-out.
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

// Archive keeps finished rounds, so a player opening the app finds the history
// strip already filled in rather than blank. The strip is the table's, not any
// one player's: everybody has been watching the same bursts.
type Archive interface {
	RecentCrashPoints(ctx context.Context, limit int) ([]int64, error)
	RecordRound(ctx context.Context, r storage.RoundRecord) error
}

// Session is the one table, and the only owner of its Game. Everything that
// touches the round goes through the command channel, so the game itself needs
// no locking.
//
// Most of what happens here is everyone's business -- the curve, the bursts,
// who is in the round and where they got out. A balance is not, so subscribers
// are tagged with whose connection they are and the private half of an event
// goes only there.
type Session struct {
	cfg     game.Config
	wallet  wallet.Wallet
	archive Archive
	log     *slog.Logger

	cmds chan func(*game.Game, time.Time)
	done chan struct{}

	mu   sync.Mutex
	subs map[chan game.Event]int64
}

func newSession(cfg game.Config, w wallet.Wallet, a Archive, log *slog.Logger) *Session {
	return &Session{
		cfg:     cfg,
		wallet:  w,
		archive: a,
		log:     log,
		cmds:    make(chan func(*game.Game, time.Time)),
		done:    make(chan struct{}),
		subs:    map[chan game.Event]int64{},
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
	past, err := s.archive.RecentCrashPoints(ctx, game.HistoryLen)
	if err != nil {
		// A blank strip is a cosmetic loss; refusing to start the game is not.
		if s.log != nil {
			s.log.Warn("could not restore the history strip", "error", err)
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
		record := storage.RoundRecord{CrashPoint: int64(e.Multiplier)}
		for _, bet := range e.Settled {
			record.Bets = append(record.Bets, storage.SettledBet{
				UserID:      bet.Player.ID,
				Amount:      bet.Amount,
				CashedOutAt: int64(bet.CashedOutAt),
				Payout:      bet.Payout,
			})
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.archive.RecordRound(ctx, record); err != nil && s.log != nil {
				s.log.Warn("could not record a round", "error", err)
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
//
// A stake offered while the rocket is already up waits for the next round. It
// is paid for now either way: the game seats it when that round opens, and
// that path must not need the wallet.
func (s *Session) PlaceBet(ctx context.Context, p game.Player, amount int64) (int64, error) {
	if amount < s.cfg.MinBet || amount > s.cfg.MaxBet {
		return 0, game.ErrStakeOutOfRange
	}

	ref := fmt.Sprintf("user:%d", p.ID)
	balance, err := s.wallet.Debit(ctx, p.ID, amount, wallet.ReasonBet, ref)
	if err != nil {
		return 0, err
	}

	var placement game.Placement
	recorded := error(nil)
	if err := s.do(ctx, func(g *game.Game, _ time.Time) {
		placement, recorded = g.PlaceBet(p, amount)
	}); err != nil {
		recorded = err
	}
	if recorded != nil {
		if refunded, rerr := s.wallet.Credit(ctx, p.ID, amount, wallet.ReasonRefund, ref); rerr == nil {
			balance = refunded
		}
		return balance, recorded
	}

	// A stake on the round in progress joins the table everyone is looking at.
	// One waiting for the next round is not in it yet, so nobody but the
	// player it belongs to has any business hearing about it.
	if placement == game.QueuedForNext {
		s.send(p.ID, []game.Event{{Type: game.EventBetQueued, Player: &p, Amount: amount}})
	} else {
		s.broadcast([]game.Event{{Type: game.EventBetPlaced, Player: &p, Amount: amount}})
	}
	s.send(p.ID, []game.Event{{Type: game.EventBalance, Balance: balance}})
	return balance, nil
}

// CashOut settles at the curve as the server reads it when the request lands,
// never at a multiplier the client claims to be showing.
func (s *Session) CashOut(ctx context.Context, userID int64) (game.Multiplier, int64, int64, error) {
	var (
		at      game.Multiplier
		payout  int64
		player  game.Player
		settled error
	)
	if err := s.do(ctx, func(g *game.Game, now time.Time) {
		// Read who it was inside the same turn as the settlement: by the time
		// this returns the round may have burst and taken the bet with it.
		if bet := g.Bet(userID); bet != nil {
			player = bet.Player
		}
		at, payout, settled = g.CashOut(userID, now)
	}); err != nil {
		return 0, 0, 0, err
	}
	if settled != nil {
		return 0, 0, 0, settled
	}

	balance, err := s.wallet.Credit(ctx, userID, payout, wallet.ReasonPayout, fmt.Sprintf("user:%d", userID))
	if err != nil {
		// The round is already settled in the game; the ledger is what owes the
		// player, so surface this loudly rather than pretending it paid.
		return at, payout, 0, fmt.Errorf("session: crediting a settled win: %w", err)
	}

	s.broadcast([]game.Event{{
		Type:       game.EventCashedOut,
		Player:     &player,
		Multiplier: at,
		Payout:     payout,
	}})
	s.send(userID, []game.Event{{Type: game.EventBalance, Balance: balance}})
	return at, payout, balance, nil
}

// Subscribe hands back a stream that opens with a full snapshot. Registering
// inside the goroutine means no event can slip between the snapshot and the
// first live message.
func (s *Session) Subscribe(ctx context.Context, userID int64) (<-chan game.Event, func(), error) {
	ch := make(chan game.Event, outboxSize)

	if err := s.do(ctx, func(g *game.Game, now time.Time) {
		ch <- g.Snapshot(userID, now)
		s.mu.Lock()
		s.subs[ch] = userID
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
func (s *Session) PushBalance(userID, balance int64) {
	s.send(userID, []game.Event{{Type: game.EventBalance, Balance: balance}})
}

func (s *Session) broadcast(events []game.Event) { s.deliver(events, nil) }

// send is the private half of the fan-out: a balance belongs to one player and
// must not go out over the table.
func (s *Session) send(userID int64, events []game.Event) { s.deliver(events, &userID) }

func (s *Session) deliver(events []game.Event, only *int64) {
	if len(events) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	for ch, owner := range s.subs {
		if only != nil && owner != *only {
			continue
		}
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
