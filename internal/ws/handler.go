// Package ws bridges one websocket connection to a player's session.
package ws

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/cahisa/racketka/internal/auth"
	"github.com/cahisa/racketka/internal/game"
	"github.com/cahisa/racketka/internal/session"
	"github.com/cahisa/racketka/internal/wallet"
)

const (
	// pingInterval keeps the connection alive through the proxies and mobile
	// networks a Telegram WebView sits behind.
	pingInterval = 20 * time.Second
	writeTimeout = 10 * time.Second
	readLimit    = 4 << 10
	outboundSize = 64
)

type command struct {
	Type   string `json:"type"`
	Amount int64  `json:"amount,omitempty"`
}

const (
	cmdBet       = "bet"
	cmdCashOut   = "cashout"
	cmdCancelBet = "cancel_bet"
)

type Deps struct {
	Manager        *session.Manager
	Issuer         *auth.Issuer
	Wallet         wallet.Wallet
	OriginPatterns []string
	Logger         *slog.Logger
}

func Handler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, err := d.Issuer.Parse(r.URL.Query().Get("token"), time.Now())
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			OriginPatterns: d.OriginPatterns,
		})
		if err != nil {
			return
		}
		defer conn.CloseNow() //nolint:errcheck // best effort on the way out
		conn.SetReadLimit(readLimit)

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		// The player's game starts here on their first connection and outlives
		// a brief drop, so a reconnect lands back in the same round.
		s := d.Manager.Acquire(claims.UserID)
		defer d.Manager.Release(claims.UserID)

		events, unsubscribe, err := s.Subscribe(ctx)
		if err != nil {
			return
		}
		defer unsubscribe()

		// One goroutine owns the socket's write side; everything that needs to
		// say something funnels through here.
		out := make(chan game.Event, outboundSize)

		go readCommands(ctx, cancel, conn, s, out, d.Logger)
		go func() {
			defer cancel()
			for e := range events {
				select {
				case out <- e:
				case <-ctx.Done():
					return
				}
			}
		}()

		if balance, err := d.Wallet.Balance(ctx, claims.UserID); err == nil {
			select {
			case out <- game.Event{Type: game.EventBalance, Balance: balance}:
			default:
			}
		}

		writeLoop(ctx, conn, out)
		conn.Close(websocket.StatusNormalClosure, "") //nolint:errcheck
	}
}

func readCommands(
	ctx context.Context,
	cancel context.CancelFunc,
	conn *websocket.Conn,
	s *session.Session,
	out chan<- game.Event,
	log *slog.Logger,
) {
	defer cancel()

	for {
		var cmd command
		if err := wsjson.Read(ctx, conn, &cmd); err != nil {
			return
		}

		var refusal error
		switch cmd.Type {
		case cmdBet:
			_, refusal = s.PlaceBet(ctx, cmd.Amount)
		case cmdCashOut:
			_, _, _, refusal = s.CashOut(ctx)
		case cmdCancelBet:
			_, refusal = s.CancelBet(ctx)
		default:
			refusal = errors.New("unknown command")
		}

		if refusal == nil {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if log != nil && errors.Is(refusal, wallet.ErrInsufficientFunds) {
			log.Debug("bet refused", "reason", refusal)
		}
		select {
		case out <- game.Event{Type: game.EventError, Code: codeFor(refusal), Message: refusal.Error()}:
		case <-ctx.Done():
			return
		}
	}
}

// codeFor maps a refusal onto a stable identifier the client can phrase itself.
func codeFor(err error) string {
	switch {
	case errors.Is(err, game.ErrBetsClosed):
		return "bets_closed"
	case errors.Is(err, game.ErrAlreadyBet):
		return "already_bet"
	case errors.Is(err, game.ErrAlreadyQueued):
		return "already_queued"
	case errors.Is(err, game.ErrNoQueuedBet):
		return "no_queued_bet"
	case errors.Is(err, game.ErrStakeOutOfRange):
		return "stake_out_of_range"
	case errors.Is(err, game.ErrNotFlying):
		return "not_flying"
	case errors.Is(err, game.ErrNoBet):
		return "no_bet"
	case errors.Is(err, game.ErrAlreadyCashedOut):
		return "already_cashed_out"
	case errors.Is(err, wallet.ErrInsufficientFunds):
		return "insufficient_funds"
	case errors.Is(err, session.ErrSessionClosed):
		return "session_closed"
	default:
		return "unknown"
	}
}

func writeLoop(ctx context.Context, conn *websocket.Conn, out <-chan game.Event) {
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case e := <-out:
			if err := writeOne(ctx, conn, e); err != nil {
				return
			}

		case <-ping.C:
			pingCtx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func writeOne(ctx context.Context, conn *websocket.Conn, e game.Event) error {
	writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return wsjson.Write(writeCtx, conn, e)
}
