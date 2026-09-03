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
