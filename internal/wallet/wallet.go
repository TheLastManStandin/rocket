// Package wallet moves a player's balance and records why.
//
// The game only ever talks to the Wallet interface, so swapping the virtual
// balance for real Telegram Stars later means adding an implementation here
// rather than touching the round logic.
package wallet

import (
	"context"
	"errors"
)

var ErrInsufficientFunds = errors.New("wallet: balance will not cover this")

// Reasons recorded in the ledger.
const (
	ReasonBet     = "bet"
	ReasonPayout  = "payout"
	ReasonRefund  = "refund"
	ReasonWelcome = "welcome"
	ReasonTopUp   = "topup"
)

type Wallet interface {
	Balance(ctx context.Context, userID int64) (int64, error)

	// Debit fails with ErrInsufficientFunds rather than letting a balance go
	// negative. ref ties the movement back to whatever caused it.
	Debit(ctx context.Context, userID, amount int64, reason, ref string) (balance int64, err error)

	Credit(ctx context.Context, userID, amount int64, reason, ref string) (balance int64, err error)
}
