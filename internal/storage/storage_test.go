package storage_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"testing"
	"time"

	"github.com/cahisa/racketka/internal/storage"
	"github.com/cahisa/racketka/internal/wallet"
)

// These run against a real Postgres because the guarantees under test -- the
// balance guard and the ledger staying in step -- live in SQL, not in Go. They
// skip rather than fail when no database is reachable, so `go test ./...` still
// works on a machine without one.
func testStore(t *testing.T) *storage.Store {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://racketka:racketka@localhost:5433/racketka?sslmode=disable"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	store, err := storage.Open(ctx, dsn)
	if err != nil {
		t.Skipf("no database at %s (%v)", dsn, err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	t.Cleanup(store.Close)
	return store
}

// newPlayer signs in a brand new account and removes it afterwards.
func newPlayer(t *testing.T, store *storage.Store, opening int64) *storage.User {
	t.Helper()
	ctx := context.Background()

	profile := storage.Profile{
		TgID:      -(rand.Int64N(1_000_000_000) + 1),
		Username:  "integration",
		FirstName: "Тест",
	}
	user, err := store.UpsertUser(ctx, profile, opening)
	if err != nil {
		t.Fatalf("signing in: %v", err)
	}
	t.Cleanup(func() {
		_, _ = store.Pool().Exec(context.Background(), `DELETE FROM users WHERE id = $1`, user.ID)
	})
	return user
}

// ledgerSum is what the journal says the player should be holding.
func ledgerSum(t *testing.T, store *storage.Store, userID int64) int64 {
	t.Helper()
	var total int64
	err := store.Pool().QueryRow(context.Background(),
		`SELECT COALESCE(SUM(delta), 0) FROM ledger WHERE user_id = $1`, userID).Scan(&total)
	if err != nil {
		t.Fatalf("summing the ledger: %v", err)
	}
	return total
}

func currentBalance(t *testing.T, store *storage.Store, userID int64) int64 {
	t.Helper()
	user, err := store.UserByID(context.Background(), userID)
	if err != nil {
		t.Fatalf("loading the player: %v", err)
	}
	return user.Balance
}

// The invariant everything else rests on: the ledger has to explain the balance
// down to the last unit, opening grant included.
func requireReconciled(t *testing.T, store *storage.Store, userID int64) {
	t.Helper()
	balance, journal := currentBalance(t, store, userID), ledgerSum(t, store, userID)
	if balance != journal {
		t.Errorf("balance is %d but the ledger accounts for %d (drift %d)", balance, journal, balance-journal)
	}
}

func TestOpeningBalanceIsBookedToTheLedger(t *testing.T) {
	store := testStore(t)
	user := newPlayer(t, store, 1000)

	if user.Balance != 1000 {
		t.Errorf("opened with %d, want 1000", user.Balance)
	}
	requireReconciled(t, store, user.ID)

	var grants int
	err := store.Pool().QueryRow(context.Background(),
		`SELECT count(*) FROM ledger WHERE user_id = $1 AND reason = $2`, user.ID, wallet.ReasonWelcome).
		Scan(&grants)
	if err != nil {
		t.Fatal(err)
	}
	if grants != 1 {
		t.Errorf("found %d welcome grants, want exactly 1", grants)
	}
}

// Reopening the Mini App must refresh the profile without handing out money.
func TestSigningInAgainDoesNotTopUp(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	user := newPlayer(t, store, 1000)

	if _, err := wallet.NewPostgres(store.Pool()).Debit(ctx, user.ID, 400, wallet.ReasonBet, "t"); err != nil {
		t.Fatal(err)
	}

	again, err := store.UpsertUser(ctx, storage.Profile{
		TgID:      user.TgID,
		Username:  "renamed",
		FirstName: "Тест",
	}, 1000)
	if err != nil {
		t.Fatal(err)
	}

	if again.Balance != 600 {
		t.Errorf("balance is %d after signing in again, want the 600 they were left with", again.Balance)
	}
	if again.Username != "renamed" {
		t.Errorf("username is %q, want the refreshed %q", again.Username, "renamed")
	}
	requireReconciled(t, store, user.ID)
}

func TestLedgerExplainsTheBalanceAfterPlay(t *testing.T) {
	store := testStore(t)
	purse := wallet.NewPostgres(store.Pool())
	ctx := context.Background()
	user := newPlayer(t, store, 1000)

	for i := range 5 {
		ref := fmt.Sprintf("round-%d", i)
		if _, err := purse.Debit(ctx, user.ID, 100, wallet.ReasonBet, ref); err != nil {
			t.Fatalf("staking: %v", err)
		}
		if i%2 == 0 {
			if _, err := purse.Credit(ctx, user.ID, 170, wallet.ReasonPayout, ref); err != nil {
				t.Fatalf("paying out: %v", err)
			}
		}
		requireReconciled(t, store, user.ID)
	}

	// 1000 - 5*100 + 3*170 = 1010
	if got := currentBalance(t, store, user.ID); got != 1010 {
		t.Errorf("balance is %d after the session, want 1010", got)
	}
}

// The guard lives in the UPDATE's WHERE clause, so two tabs racing a bet cannot
// both pass a check and overdraw the account.
func TestDebitRefusesToOverdraw(t *testing.T) {
	store := testStore(t)
	purse := wallet.NewPostgres(store.Pool())
	ctx := context.Background()
	user := newPlayer(t, store, 500)

	if _, err := purse.Debit(ctx, user.ID, 501, wallet.ReasonBet, "too-much"); !errors.Is(err, wallet.ErrInsufficientFunds) {
		t.Fatalf("overdrawing returned %v, want ErrInsufficientFunds", err)
	}
	if got := currentBalance(t, store, user.ID); got != 500 {
		t.Errorf("a refused debit left the balance at %d, want 500", got)
	}
	requireReconciled(t, store, user.ID)

	// Spending the balance down to exactly zero is allowed.
	if _, err := purse.Debit(ctx, user.ID, 500, wallet.ReasonBet, "all-in"); err != nil {
		t.Fatalf("staking the whole balance returned %v", err)
	}
	if got := currentBalance(t, store, user.ID); got != 0 {
		t.Errorf("balance is %d after going all in, want 0", got)
	}
	requireReconciled(t, store, user.ID)
}

// A Stars top-up has to land exactly once. Telegram redelivers updates it has
// not seen acknowledged, so the second copy of a payment must be a no-op
// rather than a second credit.
func TestTopUpCreditsOnceForACharge(t *testing.T) {
	store := testStore(t)
	purse := wallet.NewPostgres(store.Pool())
	user := newPlayer(t, store, 1000)
	ctx := context.Background()

	balance, applied, err := purse.TopUp(ctx, user.ID, 250, "charge-once")
	if err != nil {
		t.Fatalf("first top-up: %v", err)
	}
	if !applied {
		t.Fatal("the first delivery of a charge has to apply")
	}
	if balance != 1250 {
		t.Errorf("balance = %d, want 1250", balance)
	}

	balance, applied, err = purse.TopUp(ctx, user.ID, 250, "charge-once")
	if err != nil {
		t.Fatalf("redelivered top-up: %v", err)
	}
	if applied {
		t.Error("a redelivered charge must not apply a second time")
	}
	if balance != 1250 {
		t.Errorf("balance after the repeat = %d, want it unmoved at 1250", balance)
	}

	requireReconciled(t, store, user.ID)
}

// Two charges are two top-ups; only the id is what makes one a duplicate.
func TestTopUpTakesEveryDistinctCharge(t *testing.T) {
	store := testStore(t)
	purse := wallet.NewPostgres(store.Pool())
	user := newPlayer(t, store, 0)
	ctx := context.Background()

	for _, id := range []string{"charge-a", "charge-b"} {
		if _, applied, err := purse.TopUp(ctx, user.ID, 100, id); err != nil || !applied {
			t.Fatalf("top-up %s: applied=%v err=%v", id, applied, err)
		}
	}

	if got := currentBalance(t, store, user.ID); got != 200 {
		t.Errorf("balance = %d, want 200", got)
	}
	requireReconciled(t, store, user.ID)
}

func TestTopUpRefusesNonsense(t *testing.T) {
	store := testStore(t)
	purse := wallet.NewPostgres(store.Pool())
	user := newPlayer(t, store, 0)
	ctx := context.Background()

	if _, _, err := purse.TopUp(ctx, user.ID, 0, "charge-zero"); err == nil {
		t.Error("a zero top-up should be refused")
	}
	if _, _, err := purse.TopUp(ctx, user.ID, -100, "charge-negative"); err == nil {
		t.Error("a negative top-up should be refused")
	}
	if _, _, err := purse.TopUp(ctx, user.ID, 100, ""); err == nil {
		t.Error("a top-up with no charge id has nothing to be idempotent on")
	}

	if got := currentBalance(t, store, user.ID); got != 0 {
		t.Errorf("balance = %d, want it untouched at 0", got)
	}
}

// Payment updates name the payer by Telegram id, so that lookup has to work.
func TestUserByTgID(t *testing.T) {
	store := testStore(t)
	user := newPlayer(t, store, 500)

	found, err := store.UserByTgID(context.Background(), user.TgID)
	if err != nil {
		t.Fatalf("UserByTgID: %v", err)
	}
	if found.ID != user.ID {
		t.Errorf("found user %d, want %d", found.ID, user.ID)
	}

	if _, err := store.UserByTgID(context.Background(), 0); err == nil {
		t.Error("an unknown telegram id should not resolve to a player")
	}
}

// A burst is one row whoever was on it, and the strip everybody reads is the
// same one. Both live in SQL: the round lost its owner and the bets table lost
// its one-bet-per-round constraint in the same migration.
func TestRecordRoundFilesOneSharedRoundWithEveryStake(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()

	alice := newPlayer(t, store, 5000)
	bob := newPlayer(t, store, 5000)

	record := storage.RoundRecord{
		CrashPoint: 250,
		Bets: []storage.SettledBet{
			{UserID: alice.ID, Amount: 500, CashedOutAt: 200, Payout: 1000},
			{UserID: bob.ID, Amount: 700},
		},
	}
	if err := store.RecordRound(ctx, record); err != nil {
		t.Fatalf("RecordRound: %v", err)
	}

	// One round, no owner, both stakes hanging off it.
	var roundID int64
	var owner *int64
	const round = `
		SELECT id, user_id FROM rounds
		WHERE crash_point = 2.50 AND user_id IS NULL
		ORDER BY id DESC LIMIT 1`
	if err := store.Pool().QueryRow(ctx, round).Scan(&roundID, &owner); err != nil {
		t.Fatalf("the shared round was not filed: %v", err)
	}
	t.Cleanup(func() {
		_, _ = store.Pool().Exec(context.Background(), `DELETE FROM rounds WHERE id = $1`, roundID)
	})
	if owner != nil {
		t.Errorf("the round was filed under player %d, want nobody", *owner)
	}

	rows, err := store.Pool().Query(ctx,
		`SELECT user_id, amount, payout, status FROM bets WHERE round_id = $1 ORDER BY user_id`, roundID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	type filed struct {
		userID, amount, payout int64
		status                 string
	}
	var got []filed
	for rows.Next() {
		var f filed
		if err := rows.Scan(&f.userID, &f.amount, &f.payout, &f.status); err != nil {
			t.Fatal(err)
		}
		got = append(got, f)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("the round carries %d stakes, want both", len(got))
	}
	for _, f := range got {
		switch f.userID {
		case alice.ID:
			if f.amount != 500 || f.payout != 1000 || f.status != "won" {
				t.Errorf("alice was filed as %+v, want 500 staked, 1000 paid, won", f)
			}
		case bob.ID:
			if f.amount != 700 || f.payout != 0 || f.status != "lost" {
				t.Errorf("bob was filed as %+v, want 700 staked, nothing paid, lost", f)
			}
		default:
			t.Errorf("a stranger turned up on the round: %+v", f)
		}
	}

	// The strip is the table's, so the burst just filed is on it.
	strip, err := store.RecentCrashPoints(ctx, 10)
	if err != nil {
		t.Fatalf("RecentCrashPoints: %v", err)
	}
	if len(strip) == 0 || strip[0] != 250 {
		t.Errorf("the strip opens with %v, want the 250 just filed", strip)
	}
}
