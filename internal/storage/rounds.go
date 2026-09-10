package storage

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// RoundRecord is one finished round of the shared table, and every stake that
// was on it. Multipliers arrive in hundredths, the same units the engine
// settles in.
type RoundRecord struct {
	CrashPoint int64
	Bets       []SettledBet
}

// SettledBet is one player's stake as the burst left it.
type SettledBet struct {
	UserID      int64
	Amount      int64
	CashedOutAt int64 // 0 when they never got out
	Payout      int64
}

// RecordRound files a burst and every stake that rode it. The round has no
// owner: it is the table's, and the players on it hang off the bets.
func (s *Store) RecordRound(ctx context.Context, r RoundRecord) error {
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		const round = `
			INSERT INTO rounds (crash_point, crashed_at, status)
			VALUES ($1::numeric / 100, now(), 'crashed')
			RETURNING id`

		var roundID int64
		if err := tx.QueryRow(ctx, round, r.CrashPoint).Scan(&roundID); err != nil {
			return err
		}

		const bet = `
			INSERT INTO bets (round_id, user_id, amount, cashout_multiplier, payout, status, settled_at)
			VALUES ($1, $2, $3, $4, $5, $6, now())`
		for _, b := range r.Bets {
			status, cashedOut := "lost", any(nil)
			if b.CashedOutAt > 0 {
				status = "won"
				cashedOut = float64(b.CashedOutAt) / 100
			}
			if _, err := tx.Exec(ctx, bet, roundID, b.UserID, b.Amount, cashedOut, b.Payout, status); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("storage: recording a round: %w", err)
	}
	return nil
}

// RecentCrashPoints is the table's last bursts, newest first, in hundredths.
// It fills the history strip the moment anyone opens the app.
//
// Only rounds without an owner are counted. Rounds from before the table was
// shared belong to one player each, and mixing them in would show the strip
// somebody else's game.
func (s *Store) RecentCrashPoints(ctx context.Context, limit int) ([]int64, error) {
	const q = `
		SELECT (crash_point * 100)::bigint
		FROM rounds
		WHERE user_id IS NULL AND crashed_at IS NOT NULL
		ORDER BY id DESC
		LIMIT $1`

	rows, err := s.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("storage: reading recent rounds: %w", err)
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
