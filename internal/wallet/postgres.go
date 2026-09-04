package wallet

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres keeps users.balance and the ledger in step: every movement writes
// both inside one transaction, so the ledger always explains the balance.
type Postgres struct{ pool *pgxpool.Pool }

func NewPostgres(pool *pgxpool.Pool) *Postgres { return &Postgres{pool: pool} }

func (w *Postgres) Balance(ctx context.Context, userID int64) (int64, error) {
	var balance int64
	if err := w.pool.QueryRow(ctx, `SELECT balance FROM users WHERE id = $1`, userID).Scan(&balance); err != nil {
		return 0, fmt.Errorf("wallet: reading balance for %d: %w", userID, err)
	}
	return balance, nil
}

func (w *Postgres) Debit(ctx context.Context, userID, amount int64, reason, ref string) (int64, error) {
	if amount <= 0 {
		return 0, fmt.Errorf("wallet: debit must be positive, got %d", amount)
	}
	// The balance guard lives in the WHERE clause, so two concurrent bets from
	// two tabs cannot both pass a check-then-write and overdraw the account.
	const q = `
		UPDATE users SET balance = balance - $2, updated_at = now()
		WHERE id = $1 AND balance >= $2
		RETURNING balance`
	return w.move(ctx, q, userID, amount, -amount, reason, ref, ErrInsufficientFunds)
}

func (w *Postgres) Credit(ctx context.Context, userID, amount int64, reason, ref string) (int64, error) {
	if amount <= 0 {
		return 0, fmt.Errorf("wallet: credit must be positive, got %d", amount)
	}
	const q = `
		UPDATE users SET balance = balance + $2, updated_at = now()
		WHERE id = $1
		RETURNING balance`
	return w.move(ctx, q, userID, amount, amount, reason, ref, pgx.ErrNoRows)
}

// TopUp books a Stars payment, once. chargeID is Telegram's id for the
// payment; it lands in the ledger as the ref, where a unique index refuses the
// second copy of a redelivered update. applied reports whether this call was
// the one that moved the balance.
//
// Unlike Credit this writes the ledger row first: the insert is what claims
// the charge, so a duplicate loses the race there rather than after the
// balance has already grown.
func (w *Postgres) TopUp(
	ctx context.Context,
	userID, stars int64,
	chargeID string,
) (balance int64, applied bool, err error) {
	if stars <= 0 {
		return 0, false, fmt.Errorf("wallet: a top-up must be positive, got %d", stars)
	}
	if chargeID == "" {
		return 0, false, errors.New("wallet: a top-up needs the charge id it is keyed on")
	}

	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	const claim = `
		INSERT INTO ledger (user_id, delta, reason, ref_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT DO NOTHING`
	tag, err := tx.Exec(ctx, claim, userID, stars, ReasonTopUp, chargeID)
	if err != nil {
		return 0, false, fmt.Errorf("wallet: recording top-up %s: %w", chargeID, err)
	}

	if tag.RowsAffected() == 0 {
		// Already booked. Report the balance as it stands so the caller can
		// still answer the player with something true.
		balance, err = w.Balance(ctx, userID)
		if err != nil {
			return 0, false, err
		}
		return balance, false, nil
	}

	const grow = `
		UPDATE users SET balance = balance + $2, updated_at = now()
		WHERE id = $1
		RETURNING balance`
	if err := tx.QueryRow(ctx, grow, userID, stars).Scan(&balance); err != nil {
		return 0, false, fmt.Errorf("wallet: crediting top-up %s: %w", chargeID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, false, err
	}
	return balance, true, nil
}

func (w *Postgres) move(
	ctx context.Context,
	q string,
	userID, amount, delta int64,
	reason, ref string,
	missing error,
) (int64, error) {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed

	var balance int64
	switch err := tx.QueryRow(ctx, q, userID, amount).Scan(&balance); {
	case errors.Is(err, pgx.ErrNoRows):
		return 0, missing
	case err != nil:
		return 0, fmt.Errorf("wallet: moving %d for user %d: %w", delta, userID, err)
	}

	const record = `INSERT INTO ledger (user_id, delta, reason, ref_id) VALUES ($1, $2, $3, $4)`
	if _, err := tx.Exec(ctx, record, userID, delta, reason, ref); err != nil {
		return 0, fmt.Errorf("wallet: recording %d for user %d: %w", delta, userID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return balance, nil
}
