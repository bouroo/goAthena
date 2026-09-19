// Package domain — zeny ledger. The ledger is the audit trail behind every
// balance change: every deduct/credit appends one ZenyTransaction row carrying
// the signed delta, the reason, an optional peer charID (for trades/vending),
// and a map name (for context). The ledger is append-only — there is no Update
// or Delete path — so the sum of all transactions for a char equals the
// current balance modulo the initial endowment.
//
// The economy service writes to the ledger BEFORE updating the char row, so a
// failed balance write still leaves an auditable record of the attempted
// movement. Operators query the ledger through LedgerRepository.ListByChar to
// reconcile drift; the math is trivial (sum of Amount by CharID) but only the
// ledger makes "why" recoverable.
package domain

import (
	"context"
	"time"
)

// Reason classifies a zeny movement. The string form is what gets persisted —
// short, snake_case, and stable across releases so ops dashboards can group by
// it without a migration. New reasons append; renaming an existing one is a
// breaking change for dashboards and is treated as a schema change.
type Reason string

// Reason values used across the bounded contexts. The shop, trade, vending,
// storage, and content (NPC script) call sites each pick the value that
// describes their movement. Anything that has not yet been wired up uses
// ReasonUnknown so the audit row is still useful even when the cause is not
// classified.
const (
	// ReasonUnknown is the default for callers that don't carry a reason yet
	// (legacy callers; the deprecation target is to require a reason).
	ReasonUnknown Reason = "unknown"
	// ReasonShopBuy: the player bought an item from an NPC shop.
	ReasonShopBuy Reason = "shop_buy"
	// ReasonShopSell: the player sold an item to an NPC shop.
	ReasonShopSell Reason = "shop_sell"
	// ReasonMobKill: zeny drop from a defeated mob.
	ReasonMobKill Reason = "mob_kill"
	// ReasonTrade: player-to-player trade leg (each leg carries the peer).
	ReasonTrade Reason = "trade"
	// ReasonVending: vending-machine leg.
	ReasonVending Reason = "vending"
	// ReasonStorage: warehouse deposit/withdraw.
	ReasonStorage Reason = "storage"
	// ReasonQuest: NPC quest reward.
	ReasonQuest Reason = "quest"
	// ReasonScript: free-form NPC script (warp/portal/etc.).
	ReasonScript Reason = "script"
	// ReasonAdmin: GM command.
	ReasonAdmin Reason = "admin"
)

// ZenyTransaction is one append-only audit row. Amount is a SIGNED int32: a
// deduct is negative, a credit is positive. The char's current balance equals
// the sum of all its transactions plus the initial endowment (a constant per
// char, captured by the first transaction's preceding balance which we don't
// store — sum-deltas-toward-zero is the invariant the operator checks).
type ZenyTransaction struct {
	// ID is the auto-increment primary key. Zero on input; populated by Append.
	ID int64
	// AccountID is the owning account; denormalised so ops can filter without a
	// char-table join.
	AccountID uint32
	// CharID is the player whose balance moved.
	CharID uint32
	// Amount is the signed delta (negative = debit).
	Amount int32
	// Reason classifies the movement (see Reason constants).
	Reason Reason
	// PeerCharID is the other side of a movement when one exists (trade,
	// vending). Zero otherwise.
	PeerCharID uint32
	// MapName is the player's current map at movement time; empty when the
	// movement is offline (quest, admin).
	MapName string
	// CreatedAt is the server time the row was appended.
	CreatedAt time.Time
}

// Valid reports whether the transaction is well-formed. Append calls use this
// so a malformed caller-side record fails fast rather than reaching the DB.
func (t ZenyTransaction) Valid() bool {
	if t.CharID == 0 {
		return false
	}
	if t.Amount == 0 {
		return false
	}
	if t.Reason == "" {
		return false
	}
	if t.CreatedAt.IsZero() {
		return false
	}
	return true
}

// LedgerRepository is the persistence port for the zeny ledger. Implementations
// must guarantee Append is durable before returning nil — a transient or
// network-only success is a bug; the caller has already failed its own state
// mutation.
type LedgerRepository interface {
	// Append writes one transaction row and returns the populated ID/CreatedAt.
	Append(ctx context.Context, tx ZenyTransaction) (ZenyTransaction, error)
	// ListByChar returns the most recent `limit` transactions for a char,
	// newest first. A zero limit is implementation-defined (callers pass a
	// positive value).
	ListByChar(ctx context.Context, charID uint32, limit int) ([]ZenyTransaction, error)
	// SumByChar returns the signed sum of every transaction for charID. Used by
	// the reconciliation check (current balance == stored balance + sum).
	SumByChar(ctx context.Context, charID uint32) (int64, error)
}
