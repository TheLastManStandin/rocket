package storage

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// RoundRecord is one finished round of a player's private game. Multipliers
// arrive in hundredths, the same units the engine settles in.
type RoundRecord struct {
	UserID      int64
	CrashPoint  int64
	BetAmount   int64 // 0 when the player sat the round out
	CashedOutAt int64 // 0 when they never got out
	Payout      int64
}

// RecordRound files a burst, and the stake on it when there was one.
func (s *Store) RecordRound(ctx context.Context, r RoundRecord) error {
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		const round = `
			INSERT INTO rounds (user_id, crash_point, crashed_at, status)
			VALUES ($1, $2::numeric / 100, now(), 'crashed')
			RETURNING id`

		var roundID int64
		if err := tx.QueryRow(ctx, round, r.UserID, r.CrashPoint).Scan(&roundID); err != nil {
			return err
		}
		if r.BetAmount == 0 {
			return nil
		}

		status, cashedOut := "lost", any(nil)
		if r.CashedOutAt > 0 {
			status = "won"
			cashedOut = float64(r.CashedOutAt) / 100
		}

		const bet = `
			INSERT INTO bets (round_id, user_id, amount, cashout_multiplier, payout, status, settled_at)
			VALUES ($1, $2, $3, $4, $5, $6, now())`
		_, err := tx.Exec(ctx, bet, roundID, r.UserID, r.BetAmount, cashedOut, r.Payout, status)
		return err
	})
	if err != nil {
		return fmt.Errorf("storage: recording a round for user %d: %w", r.UserID, err)
	}
	return nil
}

// RecentCrashPoints is the player's last bursts, newest first, in hundredths.
// It fills the history strip the moment they open the app.
func (s *Store) RecentCrashPoints(ctx context.Context, userID int64, limit int) ([]int64, error) {
	const q = `
		SELECT (crash_point * 100)::bigint
		FROM rounds
		WHERE user_id = $1 AND crashed_at IS NOT NULL
		ORDER BY id DESC
		LIMIT $2`

	rows, err := s.pool.Query(ctx, q, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("storage: reading recent rounds for user %d: %w", userID, err)
	}
	defer rows.Close()

	var out []int64
	for rows.Next() {
		var hundredths int64
		if err := rows.Scan(&hundredths); err != nil {
			return nil, err
		}
		out = append(out, hundredths)
	}
	return out, rows.Err()
}
