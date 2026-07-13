package domain

import (
	"context"
	"errors"
	"fmt"
	"time"
)

//go:generate go run go.uber.org/mock/mockgen -destination=mock/vending_service_mock.go -package=domainmock . VendingService

//go:generate go run go.uber.org/mock/mockgen -destination=mock/vending_repository_mock.go -package=domainmock . VendingRepository

// Sentinel errors returned by the vending service and repository.
// Service-layer callers compare these using errors.Is.
//
//go:generate go run go.uber.org/mock/mockgen -destination=mock/lock_store_mock.go -package=domainmock . LockStore
var (
	// ErrShopNotFound is returned when a shop ID does not exist.
	ErrShopNotFound = errors.New("vending shop not found")

	// ErrShopAlreadyOpen is returned when a character already has an open shop.
	ErrShopAlreadyOpen = errors.New("character already has an open shop")

	// ErrShopClosed is returned when an operation targets a closed shop.
	ErrShopClosed = errors.New("vending shop is closed")

	// ErrInsufficientItems is returned when the shop has fewer items than requested.
	ErrInsufficientItems = errors.New("insufficient items in shop")

	// ErrInsufficientFunds is returned when the buyer lacks zeny for a purchase.
	ErrInsufficientFunds = errors.New("insufficient zeny")

	// ErrInvalidItem is returned when a vending item reference is invalid.
	ErrInvalidItem = errors.New("invalid vending item")

	// ErrLockBusy is returned when a character lock is already held.
	ErrLockBusy = errors.New("vending lock busy")
)

// VendingService is the inbound port for player vending use-cases.
// Each method manages the lifecycle of a vending shop and processes
// purchases from other players.
//
// Vending operations acquire per-character locks (key pattern:
// "vend:{char_id}") to serialize concurrent operations for the same
// character. A non-nil error indicates a system failure (DB/lock);
// business rule violations return domain errors from the errors list
// above.
type VendingService interface {
	// OpenShop creates a vending shop for the owner character at the given
	// location with the listed items. The owner must not already have an
	// open shop.
	OpenShop(ctx context.Context, shop VendingShop) (VendingShop, error)

	// CloseShop closes the owner's vending shop, removing it from the
	// active shop registry.
	CloseShop(ctx context.Context, ownerID uint32) error

	// BuyItem processes a purchase from a vending shop. The buyer's zeny
	// is deducted and items are transferred; the shop's stock is reduced.
	// Returns the updated zeny balance for the buyer.
	BuyItem(ctx context.Context, buyerID uint32, shopID string, inventoryID uint32, amount int32) (buyerZeny uint32, err error)

	// ListShopItems returns the items currently listed in a shop.
	ListShopItems(ctx context.Context, shopID string) ([]VendingItem, error)

	// ListShopsOnMap returns all open vending shops on the given map.
	ListShopsOnMap(ctx context.Context, mapName string) ([]VendingShop, error)

	// GetShop returns the shop owned by the given character, if any.
	GetShop(ctx context.Context, ownerID uint32) (VendingShop, error)
}

// VendingRepository is the outbound port for vending shop persistence.
// It manages the lifecycle of vending shops from creation through closure.
type VendingRepository interface {
	// CreateShop stores a new vending shop and returns its ID.
	CreateShop(ctx context.Context, shop VendingShop) (string, error)

	// GetShop retrieves a shop by its ID.
	// Returns ErrShopNotFound if the shop does not exist.
	GetShop(ctx context.Context, shopID string) (VendingShop, error)

	// GetShopByOwner retrieves the shop owned by the given character.
	// Returns ErrShopNotFound if the character has no open shop.
	GetShopByOwner(ctx context.Context, ownerID uint32) (VendingShop, error)

	// ListShopsOnMap returns all open shops on the given map.
	ListShopsOnMap(ctx context.Context, mapName string) ([]VendingShop, error)

	// UpdateShop persists changes to a shop (e.g., reduced stock after a purchase).
	UpdateShop(ctx context.Context, shop VendingShop) error

	// DeleteShop removes a shop from the active registry.
	DeleteShop(ctx context.Context, shopID string) error
}

// LockStore is the outbound port for per-character distributed locks.
// It serializes concurrent vending operations for the same character.
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

// CharLockKey returns the lock key for a character's vending mutex. The
// prefix namespaces vending locks away from other locks (trade, storage).
func CharLockKey(charID uint32) string {
	return fmt.Sprintf("vending:char:%d", charID)
}
