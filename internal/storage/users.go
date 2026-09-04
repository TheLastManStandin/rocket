package storage

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/cahisa/racketka/internal/wallet"
)

type User struct {
	ID        int64
	TgID      int64
	Username  string
	FirstName string
	PhotoURL  string
	Balance   int64
}

// Profile is what Telegram tells us about a player.
type Profile struct {
	TgID      int64
	Username  string
	FirstName string
	PhotoURL  string
}

// UpsertUser creates the player on first sight and refreshes their profile on
// every later visit. The opening balance is granted once and never re-applied:
// signing in again must not top anyone up.
//
// The grant is written to the ledger in the same transaction. Every movement of
// money has to be explained there, or the sum of a player's ledger stops
// matching their balance and reconciliation has nothing to stand on.
func (s *Store) UpsertUser(ctx context.Context, p Profile, startBalance int64) (*User, error) {
	// xmax = 0 marks the row as freshly inserted rather than updated, which is
	// how an upsert tells a first sign-in from a returning player.
	const upsert = `
		INSERT INTO users (tg_id, username, first_name, photo_url, balance)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (tg_id) DO UPDATE SET
			username   = EXCLUDED.username,
			first_name = EXCLUDED.first_name,
			photo_url  = EXCLUDED.photo_url,
			updated_at = now()
		RETURNING id, tg_id, username, first_name, photo_url, balance, (xmax = 0) AS created`

	const grant = `INSERT INTO ledger (user_id, delta, reason, ref_id) VALUES ($1, $2, $3, $4)`

	var u User
	var created bool

	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, upsert, p.TgID, p.Username, p.FirstName, p.PhotoURL, startBalance).
			Scan(&u.ID, &u.TgID, &u.Username, &u.FirstName, &u.PhotoURL, &u.Balance, &created); err != nil {
			return err
		}
		if !created || startBalance == 0 {
			return nil
		}
		_, err := tx.Exec(ctx, grant, u.ID, startBalance, wallet.ReasonWelcome, "signup")
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("storage: upserting user %d: %w", p.TgID, err)
	}
	return &u, nil
}

// UserByTgID finds a player by their Telegram id. Payment updates identify the
// payer that way and know nothing of our own ids.
func (s *Store) UserByTgID(ctx context.Context, tgID int64) (*User, error) {
	const q = `SELECT id, tg_id, username, first_name, photo_url, balance FROM users WHERE tg_id = $1`

	var u User
	err := s.pool.QueryRow(ctx, q, tgID).
		Scan(&u.ID, &u.TgID, &u.Username, &u.FirstName, &u.PhotoURL, &u.Balance)
	if err != nil {
		return nil, fmt.Errorf("storage: loading user with telegram id %d: %w", tgID, err)
	}
	return &u, nil
}

func (s *Store) UserByID(ctx context.Context, id int64) (*User, error) {
	const q = `SELECT id, tg_id, username, first_name, photo_url, balance FROM users WHERE id = $1`

	var u User
	err := s.pool.QueryRow(ctx, q, id).
		Scan(&u.ID, &u.TgID, &u.Username, &u.FirstName, &u.PhotoURL, &u.Balance)
	if err != nil {
		return nil, fmt.Errorf("storage: loading user %d: %w", id, err)
	}
	return &u, nil
}
