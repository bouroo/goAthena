// Package domain holds the economy bounded context's pure domain: the Zeny
// value object (rAthena's currency) and the ledger port. No infrastructure.
package domain

import "errors"

// Zeny is an in-game currency amount. rAthena stores zeny as an unsigned 32-bit
// int (char.zeny); the value object enforces non-negativity and overflow-safe
// arithmetic so the caller never writes a negative or wrapped balance to the DB.
type Zeny struct{ amount uint32 }

// NewZeny wraps a raw amount, rejecting negatives.
func NewZeny(amount int32) (Zeny, error) {
	if amount < 0 {
		return Zeny{}, ErrInsufficientFunds
	}
	return Zeny{amount: uint32(amount)}, nil
}

// Amount returns the raw balance.
func (z Zeny) Amount() int32 { return int32(z.amount) } //nolint:gosec // G115: bounded to non-negative int32 by construction.

// CanAfford reports whether the balance covers cost.
func (z Zeny) CanAfford(cost int32) bool { return int32(z.amount) >= cost } //nolint:gosec // G115: bounded to int32 by construction (max 2^31).

// Deduct subtracts cost, returning the new balance or ErrInsufficientFunds.
func (z Zeny) Deduct(cost int32) (Zeny, error) {
	if cost < 0 || int32(z.amount) < cost { //nolint:gosec // G115: bounded by construction.
		return Zeny{}, ErrInsufficientFunds
	}
	return Zeny{amount: z.amount - uint32(cost)}, nil
}

// Credit adds gain, returning the new balance or ErrOverflow on wrap.
func (z Zeny) Credit(gain int32) (Zeny, error) {
	if gain < 0 {
		return Zeny{}, errors.New("economy: credit must be non-negative")
	}
	next := uint64(z.amount) + uint64(gain)
	if next > 1<<31 { // rAthena zeny is uint32 in practice capped at INT32_MAX
		return Zeny{}, ErrOverflow
	}
	return Zeny{amount: uint32(next)}, nil
}

var (
	// ErrInsufficientFunds is returned when a deduction exceeds the balance.
	ErrInsufficientFunds = errors.New("insufficient funds")
	// ErrOverflow is returned when a credit would exceed the zeny cap.
	ErrOverflow = errors.New("zeny overflow")
)
