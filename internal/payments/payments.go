// Package payments turns Telegram Stars into balance.
//
// It owns both halves of a top-up because they have to agree: the payload
// written into an invoice here is the same payload read back off the payment
// that settles it.
package payments

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/cahisa/racketka/internal/storage"
	"github.com/cahisa/racketka/internal/telegram"
)

// Packages are the amounts a player may buy. Offering a fixed set rather than
// a free-form number keeps a crafted request from inventing its own price.
// The single Star at the front is there to make the payment path testable
// without spending anything worth spending.
var Packages = []int64{1, 100, 250, 500, 1000}

// How long each long poll waits before Telegram answers empty-handed. Long
// enough that the loop is idle almost all of the time, short enough that a
// shutdown is not left hanging on it.
const pollWait = 25 * time.Second

// After a failed poll the loop waits this long rather than hammering the API
// through whatever is wrong.
const pollBackoff = 5 * time.Second

var ErrUnknownPackage = errors.New("payments: no such Stars package")

// API is the part of the Bot API this needs. It is an interface so the paths
// that move money can be tested without a bot on the other end.
type API interface {
	CreateInvoiceLink(ctx context.Context, in telegram.Invoice) (string, error)
	GetUpdates(ctx context.Context, offset int64, wait time.Duration) ([]telegram.Update, error)
	AnswerPreCheckoutQuery(ctx context.Context, id string, ok bool, reason string) error
}

type Store interface {
	UserByTgID(ctx context.Context, tgID int64) (*storage.User, error)
}

type Wallet interface {
	// TopUp reports the balance after the payment and whether this call was
	// the one that applied it.
	TopUp(ctx context.Context, userID, stars int64, chargeID string) (int64, bool, error)
}

// Notifier lets a connected player see a top-up land without reopening the app.
type Notifier interface {
	NudgeBalance(userID, balance int64)
}

type Service struct {
	bot    API
	store  Store
	wallet Wallet
	notify Notifier
	log    *slog.Logger
}

func New(bot API, s Store, w Wallet, n Notifier, log *slog.Logger) *Service {
	return &Service{bot: bot, store: s, wallet: w, notify: n, log: log}
}

// InvoiceLink prices a package and hands back a link the Mini App can open.
func (s *Service) InvoiceLink(ctx context.Context, userID, stars int64) (string, error) {
	if !allowed(stars) {
		return "", ErrUnknownPackage
	}

	return s.bot.CreateInvoiceLink(ctx, telegram.Invoice{
		Title:       fmt.Sprintf("%d ⭐", stars),
		Description: "Пополнение баланса в Racketka",
		Payload:     payload(userID, stars),
		Stars:       stars,
	})
}

// Watch consumes payment updates until ctx is done.
//
// Long polling rather than a webhook: a webhook needs a stable public address,
// and this game runs behind tunnels that get a new one every restart.
func (s *Service) Watch(ctx context.Context) {
	var offset int64

	for ctx.Err() == nil {
		updates, err := s.bot.GetUpdates(ctx, offset, pollWait)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.log.Warn("could not read payment updates", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(pollBackoff):
			}
			continue
		}

		for _, u := range updates {
			// Advance past every update, handled or not: leaving one behind
			// would have Telegram redeliver it forever.
			if u.ID >= offset {
				offset = u.ID + 1
			}
			s.handle(ctx, u)
		}
	}
}

func (s *Service) handle(ctx context.Context, u telegram.Update) {
	switch {
	case u.PreCheckoutQuery != nil:
		s.approve(ctx, u.PreCheckoutQuery)
	case u.Message != nil && u.Message.SuccessfulPayment != nil:
		s.credit(ctx, u.Message)
	}
}

// approve answers the go-ahead question Telegram asks before charging. It has
// ten seconds to reply, so it only checks what it can check quickly.
func (s *Service) approve(ctx context.Context, q *telegram.PreCheckoutQuery) {
	refuse := func(reason string, err error) {
		s.log.Warn("refused a Stars charge", "payload", q.InvoicePayload, "error", err)
		if err := s.bot.AnswerPreCheckoutQuery(ctx, q.ID, false, reason); err != nil {
			s.log.Error("could not refuse a Stars charge", "error", err)
		}
	}

	if q.Currency != telegram.StarsCurrency {
		refuse("Этот счёт оплачивается только звёздами.", fmt.Errorf("currency %q", q.Currency))
		return
	}
	userID, _, err := parsePayload(q.InvoicePayload)
	if err != nil {
		refuse("Счёт больше не действителен, откройте пополнение заново.", err)
		return
	}
	// The payer has to be the account the invoice was cut for; the id in the
	// payload is ours, and from.id is Telegram's word for who is paying.
	if _, err := s.userFor(ctx, q.From.ID, userID); err != nil {
		refuse("Счёт выписан на другой аккаунт.", err)
		return
	}

	if err := s.bot.AnswerPreCheckoutQuery(ctx, q.ID, true, ""); err != nil {
		s.log.Error("could not approve a Stars charge", "error", err)
	}
}

func (s *Service) credit(ctx context.Context, m *telegram.Message) {
	p := m.SuccessfulPayment
	if p.Currency != telegram.StarsCurrency {
		s.log.Warn("ignored a payment that was not in Stars", "currency", p.Currency)
		return
	}
	if m.From == nil {
		s.log.Warn("ignored a payment with no payer", "charge", p.ChargeID)
		return
	}

	claimed, _, err := parsePayload(p.InvoicePayload)
	if err != nil {
		s.log.Error("a settled payment carried an unreadable payload",
			"charge", p.ChargeID, "payload", p.InvoicePayload, "error", err)
		return
	}

	user, err := s.userFor(ctx, m.From.ID, claimed)
	if err != nil {
		s.log.Error("could not place a settled payment", "charge", p.ChargeID, "error", err)
		return
	}

	// The amount charged is what Telegram says was charged, never what the
	// payload claims it should have been.
	balance, applied, err := s.wallet.TopUp(ctx, user.ID, p.TotalAmount, p.ChargeID)
	if err != nil {
		s.log.Error("could not credit a Stars top-up",
			"charge", p.ChargeID, "user", user.ID, "error", err)
		return
	}
	if !applied {
		// A redelivered update. The balance already includes it.
		return
	}

	s.log.Info("credited a Stars top-up",
		"user", user.ID, "stars", p.TotalAmount, "balance", balance, "charge", p.ChargeID)
	s.notify.NudgeBalance(user.ID, balance)
}

// userFor loads the payer and checks they are who the invoice was written for.
func (s *Service) userFor(ctx context.Context, tgID, claimedUserID int64) (*storage.User, error) {
	user, err := s.store.UserByTgID(ctx, tgID)
	if err != nil {
		return nil, err
	}
	if user.ID != claimedUserID {
		return nil, fmt.Errorf(
			"payments: invoice was cut for user %d but paid by user %d", claimedUserID, user.ID)
	}
	return user, nil
}

func allowed(stars int64) bool {
	for _, p := range Packages {
		if p == stars {
			return true
		}
	}
	return false
}

// payload is what Telegram hands back on the payment. It carries the player it
// was cut for, so a settled charge lands on the right balance.
func payload(userID, stars int64) string {
	return fmt.Sprintf("topup:%d:%d", userID, stars)
}

func parsePayload(raw string) (userID, stars int64, err error) {
	parts := strings.Split(raw, ":")
	if len(parts) != 3 || parts[0] != "topup" {
		return 0, 0, fmt.Errorf("payments: unrecognised invoice payload %q", raw)
	}
	if userID, err = strconv.ParseInt(parts[1], 10, 64); err != nil {
		return 0, 0, fmt.Errorf("payments: invoice payload %q carries no user: %w", raw, err)
	}
	if stars, err = strconv.ParseInt(parts[2], 10, 64); err != nil {
		return 0, 0, fmt.Errorf("payments: invoice payload %q carries no amount: %w", raw, err)
	}
	return userID, stars, nil
}
