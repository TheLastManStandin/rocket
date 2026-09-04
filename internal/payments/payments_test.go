package payments

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/cahisa/racketka/internal/storage"
	"github.com/cahisa/racketka/internal/telegram"
)

type fakeAPI struct {
	invoice   telegram.Invoice
	link      string
	answers   []answer
	answerErr error
}

type answer struct {
	id     string
	ok     bool
	reason string
}

func (f *fakeAPI) CreateInvoiceLink(_ context.Context, in telegram.Invoice) (string, error) {
	f.invoice = in
	return f.link, nil
}

func (f *fakeAPI) GetUpdates(context.Context, int64, time.Duration) ([]telegram.Update, error) {
	return nil, nil
}

func (f *fakeAPI) AnswerPreCheckoutQuery(_ context.Context, id string, ok bool, reason string) error {
	f.answers = append(f.answers, answer{id: id, ok: ok, reason: reason})
	return f.answerErr
}

// fakeStore maps telegram ids to players, and refuses anyone it has not heard of.
type fakeStore map[int64]*storage.User

func (f fakeStore) UserByTgID(_ context.Context, tgID int64) (*storage.User, error) {
	if u, ok := f[tgID]; ok {
		return u, nil
	}
	return nil, errors.New("no such user")
}

type fakeWallet struct {
	credited []credit
	// seen makes the fake behave like the real one: the same charge twice
	// applies once.
	seen    map[string]bool
	balance int64
	err     error
}

type credit struct {
	userID, stars int64
	chargeID      string
}

func newWallet() *fakeWallet { return &fakeWallet{seen: map[string]bool{}, balance: 1000} }

func (f *fakeWallet) TopUp(_ context.Context, userID, stars int64, chargeID string) (int64, bool, error) {
	if f.err != nil {
		return 0, false, f.err
	}
	if f.seen[chargeID] {
		return f.balance, false, nil
	}
	f.seen[chargeID] = true
	f.credited = append(f.credited, credit{userID: userID, stars: stars, chargeID: chargeID})
	f.balance += stars
	return f.balance, true, nil
}

type fakeNotifier struct{ nudges []int64 }

func (f *fakeNotifier) NudgeBalance(userID, _ int64) { f.nudges = append(f.nudges, userID) }

func newService(api API, store Store, w Wallet, n Notifier) *Service {
	return New(api, store, w, n, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func paidMessage(tgID, stars int64, payload, charge string) *telegram.Message {
	return &telegram.Message{
		From: &telegram.User{ID: tgID},
		SuccessfulPayment: &telegram.SuccessfulPayment{
			Currency:       telegram.StarsCurrency,
			TotalAmount:    stars,
			InvoicePayload: payload,
			ChargeID:       charge,
		},
	}
}

func TestInvoiceRefusesAnAmountThatIsNotOnOffer(t *testing.T) {
	s := newService(&fakeAPI{}, fakeStore{}, newWallet(), &fakeNotifier{})

	for _, stars := range []int64{0, -100, 50, 999, 1_000_000} {
		if _, err := s.InvoiceLink(context.Background(), 7, stars); !errors.Is(err, ErrUnknownPackage) {
			t.Errorf("%d stars: want ErrUnknownPackage, got %v", stars, err)
		}
	}
}

func TestInvoiceCarriesThePlayerItWasCutFor(t *testing.T) {
	api := &fakeAPI{link: "https://t.me/invoice"}
	s := newService(api, fakeStore{}, newWallet(), &fakeNotifier{})

	link, err := s.InvoiceLink(context.Background(), 42, 250)
	if err != nil {
		t.Fatalf("InvoiceLink: %v", err)
	}
	if link != "https://t.me/invoice" {
		t.Errorf("link = %q", link)
	}
	if api.invoice.Stars != 250 {
		t.Errorf("stars = %d, want 250", api.invoice.Stars)
	}

	userID, stars, err := parsePayload(api.invoice.Payload)
	if err != nil {
		t.Fatalf("the payload it wrote does not parse back: %v", err)
	}
	if userID != 42 || stars != 250 {
		t.Errorf("payload carries user %d and %d stars, want 42 and 250", userID, stars)
	}
}

func TestPaymentCreditsThePayer(t *testing.T) {
	store := fakeStore{99: {ID: 42, TgID: 99}}
	purse := newWallet()
	notes := &fakeNotifier{}
	s := newService(&fakeAPI{}, store, purse, notes)

	s.credit(context.Background(), paidMessage(99, 250, payload(42, 250), "charge-1"))

	if len(purse.credited) != 1 {
		t.Fatalf("credited %d times, want 1", len(purse.credited))
	}
	if got := purse.credited[0]; got.userID != 42 || got.stars != 250 {
		t.Errorf("credited %+v, want user 42 and 250 stars", got)
	}
	if len(notes.nudges) != 1 || notes.nudges[0] != 42 {
		t.Errorf("nudges = %v, want the payer told once", notes.nudges)
	}
}

// Telegram may deliver the same payment twice, and the second copy must not
// pay out again.
func TestRedeliveredPaymentCreditsOnce(t *testing.T) {
	store := fakeStore{99: {ID: 42, TgID: 99}}
	purse := newWallet()
	notes := &fakeNotifier{}
	s := newService(&fakeAPI{}, store, purse, notes)

	msg := paidMessage(99, 250, payload(42, 250), "charge-1")
	s.credit(context.Background(), msg)
	s.credit(context.Background(), msg)

	if len(purse.credited) != 1 {
		t.Errorf("credited %d times, want 1", len(purse.credited))
	}
	if len(notes.nudges) != 1 {
		t.Errorf("nudged %d times, want 1: a repeat is not news", len(notes.nudges))
	}
}

// The amount comes off the payment, never off the payload, or a doctored
// payload would mint balance.
func TestCreditFollowsTheChargedAmountNotThePayload(t *testing.T) {
	store := fakeStore{99: {ID: 42, TgID: 99}}
	purse := newWallet()
	s := newService(&fakeAPI{}, store, purse, &fakeNotifier{})

	s.credit(context.Background(), paidMessage(99, 50, payload(42, 1000), "charge-1"))

	if len(purse.credited) != 1 {
		t.Fatalf("credited %d times, want 1", len(purse.credited))
	}
	if got := purse.credited[0].stars; got != 50 {
		t.Errorf("credited %d stars, want the 50 actually charged", got)
	}
}

func TestPaymentForSomeoneElsesInvoiceIsRefused(t *testing.T) {
	store := fakeStore{99: {ID: 42, TgID: 99}}
	purse := newWallet()
	s := newService(&fakeAPI{}, store, purse, &fakeNotifier{})

	// Paid by 99, but the invoice was cut for a different player.
	s.credit(context.Background(), paidMessage(99, 250, payload(7, 250), "charge-1"))

	if len(purse.credited) != 0 {
		t.Errorf("credited %+v, want nothing", purse.credited)
	}
}

func TestNonStarPaymentIsIgnored(t *testing.T) {
	store := fakeStore{99: {ID: 42, TgID: 99}}
	purse := newWallet()
	s := newService(&fakeAPI{}, store, purse, &fakeNotifier{})

	m := paidMessage(99, 250, payload(42, 250), "charge-1")
	m.SuccessfulPayment.Currency = "USD"
	s.credit(context.Background(), m)

	if len(purse.credited) != 0 {
		t.Errorf("credited %+v, want nothing", purse.credited)
	}
}

func TestPreCheckoutApprovesItsOwnInvoice(t *testing.T) {
	api := &fakeAPI{}
	store := fakeStore{99: {ID: 42, TgID: 99}}
	s := newService(api, store, newWallet(), &fakeNotifier{})

	s.approve(context.Background(), &telegram.PreCheckoutQuery{
		ID:             "q1",
		From:           telegram.User{ID: 99},
		Currency:       telegram.StarsCurrency,
		TotalAmount:    250,
		InvoicePayload: payload(42, 250),
	})

	if len(api.answers) != 1 || !api.answers[0].ok {
		t.Fatalf("answers = %+v, want one approval", api.answers)
	}
}

func TestPreCheckoutRefusesAnInvoiceCutForSomeoneElse(t *testing.T) {
	api := &fakeAPI{}
	store := fakeStore{99: {ID: 42, TgID: 99}}
	s := newService(api, store, newWallet(), &fakeNotifier{})

	s.approve(context.Background(), &telegram.PreCheckoutQuery{
		ID:             "q1",
		From:           telegram.User{ID: 99},
		Currency:       telegram.StarsCurrency,
		TotalAmount:    250,
		InvoicePayload: payload(7, 250),
	})

	if len(api.answers) != 1 || api.answers[0].ok {
		t.Fatalf("answers = %+v, want one refusal", api.answers)
	}
	if api.answers[0].reason == "" {
		t.Error("a refusal should say why: the player sees it")
	}
}

func TestPayloadRoundTrip(t *testing.T) {
	userID, stars, err := parsePayload(payload(42, 500))
	if err != nil {
		t.Fatalf("parsePayload: %v", err)
	}
	if userID != 42 || stars != 500 {
		t.Errorf("got user %d and %d stars, want 42 and 500", userID, stars)
	}

	for _, raw := range []string{"", "topup", "topup:42", "topup:x:500", "topup:42:x", "gift:42:500"} {
		if _, _, err := parsePayload(raw); err == nil {
			t.Errorf("parsePayload(%q) accepted what it should not have", raw)
		}
	}
}
