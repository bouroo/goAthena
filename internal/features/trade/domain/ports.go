package domain

import (
	"context"
	"errors"
	"fmt"
	"time"
)

//go:generate go run go.uber.org/mock/mockgen -destination=mock/trade_service_mock.go -package=domainmock . TradeService

//go:generate go run go.uber.org/mock/mockgen -destination=mock/trade_repository_mock.go -package=domainmock . TradeRepository

//go:generate go run go.uber.org/mock/mockgen -destination=mock/lock_store_mock.go -package=domainmock . LockStore

// Sentinel errors returned by the trade service and repository.
// Service-layer callers compare these using errors.Is.
var (
	// ErrTradeNotFound is returned when a trade ID does not exist.
	ErrTradeNotFound = errors.New("trade not found")

	// ErrInvalidTradeState is returned when an operation is invalid for the current state.
	ErrInvalidTradeState = errors.New("invalid trade state")

	// ErrTradeTargetUnavailable is returned when the target character cannot trade.
	ErrTradeTargetUnavailable = errors.New("trade target unavailable")

	// ErrInsufficientInventory is returned when an item is not owned or insufficient quantity.
	ErrInsufficientInventory = errors.New("insufficient inventory")

	// ErrLockBusy is returned when a character lock is already held.
	ErrLockBusy = errors.New("trade lock busy")
)

// TradeService is the inbound port for player trading use-cases. Each method
// manages a trade session between two characters, handling state transitions,
// item/zeny validation, and atomic transfer execution.
//
// Trade operations acquire per-character locks (key pattern: "trade:{char_id}")
// to serialize concurrent trade attempts for the same character. A non-nil
// error indicates a system failure (DB/lock); business rule violations return
// domain errors from the errors list above.
type TradeService interface {
	// RequestTrade initiates a trade session between charID and targetCharID.
	// The target must accept before the trade moves to TradeStateOpen.
	// Returns the created trade session.
	RequestTrade(ctx context.Context, charID uint32, targetCharID uint32) (Trade, error)

	// AcceptTrade accepts a trade request and transitions to TradeStateOpen.
	// Updates Player2CharID and allows both parties to add items/zeny.
	AcceptTrade(ctx context.Context, tradeID string, charID uint32) error

	// AddTradeItem adds an item to the trade window for the offering character.
	// Fails if the trade is not in TradeStateOpen or the item is not owned.
	AddTradeItem(ctx context.Context, tradeID string, charID uint32, itemID uint32, amount int32) error

	// AddTradeZeny adds zeny to the trade window for the offering character.
	// Fails if the trade is not in TradeStateOpen or zeny is insufficient.
	AddTradeZeny(ctx context.Context, tradeID string, charID uint32, zeny uint32) error

	// ConfirmTrade locks the character's offer, preventing further modifications.
	// Once both parties confirm, the trade moves to TradeStateLocked.
	ConfirmTrade(ctx context.Context, tradeID string, charID uint32) error

	// CompleteTrade executes the atomic transfer when both parties have confirmed.
	// Moves the trade to TradeStateCompleted on success.
	CompleteTrade(ctx context.Context, tradeID string, charID uint32) error

	// CancelTrade aborts the trade session from any state.
	// Moves the trade to TradeStateCancelled.
	CancelTrade(ctx context.Context, tradeID string, charID uint32) error
}

// TradeRepository is the outbound port for trade session persistence.
// It manages the lifecycle of trade entities from creation through completion.
type TradeRepository interface {
	// CreateTrade stores a new trade session and returns its ID.
	CreateTrade(ctx context.Context, trade Trade) (string, error)

	// GetTrade fetches a trade by ID. Returns ErrTradeNotFound if missing.
	GetTrade(ctx context.Context, tradeID string) (Trade, error)

	// UpdateTrade persists trade state, items, or zeny changes.
	UpdateTrade(ctx context.Context, trade Trade) error

	// DeleteTrade removes a completed or cancelled trade from storage.
	DeleteTrade(ctx context.Context, tradeID string) error

	// ExecuteTradeTransfer atomically transfers items and zeny between both parties.
	// Returns error on insufficient inventory/zeny (rollback).
	ExecuteTradeTransfer(ctx context.Context, trade Trade) error
}

// LockStore is the outbound port for per-character distributed locks.
// It serializes concurrent trade operations for the same character, preventing
// race conditions during state transitions and item/zeny validation.
//
// Implementations must make Release idempotent: releasing an absent or
// expired lock, or a lock owned by a different token, is a no-op (nil error).
type LockStore interface {
	// Acquire attempts to take the lock named key, holding it for at most ttl.
	// On success it returns an opaque ownership token that Release must present.
	// A held lock yields a wrapped ErrLockBusy.
	Acquire(ctx context.Context, key string, ttl time.Duration) (token string, err error)

	// Release frees the lock only if still owned by token (compare-and-delete).
	// Releasing an absent/expired lock is a no-op.
	Release(ctx context.Context, key string, token string) error
}

// CharLockKey returns the lock key for a character's trade mutex. The
// prefix namespaces trade locks away from other locks.
func CharLockKey(charID uint32) string {
	return fmt.Sprintf("trade:char:%d", charID)
}
