package domain

import (
	"context"
	"errors"
	"fmt"
	"time"
)

//go:generate go run go.uber.org/mock/mockgen -destination=mock/storage_service_mock.go -package=domainmock . StorageService

//go:generate go run go.uber.org/mock/mockgen -destination=mock/storage_repository_mock.go -package=domainmock . StorageRepository

//go:generate go run go.uber.org/mock/mockgen -destination=mock/inventory_repository_mock.go -package=domainmock . InventoryRepository

//go:generate go run go.uber.org/mock/mockgen -destination=mock/lock_store_mock.go -package=domainmock . LockStore

// Sentinel errors returned by the storage service and repository.
// Service-layer callers compare these using errors.Is.
var (
	// ErrStorageNotFound is returned when a storage item does not exist.
	ErrStorageNotFound = errors.New("storage not found")

	// ErrStorageLocked is returned when a character's storage is locked by another operation.
	ErrStorageLocked = errors.New("storage locked")

	// ErrStorageFull is returned when storage cannot accept more items (limit reached).
	ErrStorageFull = errors.New("storage full")

	// ErrInsufficientStorageItem is returned when trying to withdraw more than exists in storage.
	ErrInsufficientStorageItem = errors.New("insufficient storage item")

	// ErrInventoryFull is returned when character inventory cannot accept deposited items.
	ErrInventoryFull = errors.New("inventory full")

	// ErrInvalidItemAmount is returned when a non-positive amount is provided.
	ErrInvalidItemAmount = errors.New("invalid item amount")
)

// StorageService is the inbound port for storage use-cases. It manages
// a character's personal storage, handling item deposits and withdrawals
// while enforcing capacity limits and inventory space constraints.
//
// Storage operations acquire per-character locks (key pattern: "storage:{char_id}")
// to serialize concurrent storage attempts for the same character.
type StorageService interface {
	// OpenStorage locks the character's storage for subsequent operations.
	// Returns ErrStorageLocked if already held by another operation.
	OpenStorage(ctx context.Context, charID uint32) error

	// DepositItem moves an item from character inventory to storage.
	// Fails if inventory item is not owned, amount is invalid, or storage is full.
	DepositItem(ctx context.Context, charID uint32, inventoryItemID uint64, amount int32) error

	// WithdrawItem moves an item from storage to character inventory.
	// Fails if storage item is not owned, amount is invalid, or inventory is full.
	WithdrawItem(ctx context.Context, charID uint32, storageItemID uint64, amount int32) error

	// CloseStorage unlocks the character's storage, allowing other operations.
	CloseStorage(ctx context.Context, charID uint32) error
}

// StorageRepository is the outbound port for storage persistence.
// It manages the lifecycle of storage items from creation through deletion.
type StorageRepository interface {
	// CreateStorageItem adds a new item to storage.
	CreateStorageItem(ctx context.Context, item StorageItem) error

	// ListStorageByChar returns all storage items for a character.
	ListStorageByChar(ctx context.Context, charID uint32) ([]StorageItem, error)

	// GetStorageItem retrieves a specific storage item by ID.
	// Returns ErrStorageNotFound if missing.
	GetStorageItem(ctx context.Context, itemID uint64) (StorageItem, error)

	// UpdateStorageItem modifies an existing storage item (amount, etc.).
	UpdateStorageItem(ctx context.Context, item StorageItem) error

	// DeleteStorageItem removes an item from storage.
	DeleteStorageItem(ctx context.Context, itemID uint64) error

	// CountStorageItems returns the total number of items in storage for a character.
	CountStorageItems(ctx context.Context, charID uint32) (int, error)
}

// LockStore is the outbound port for per-character distributed locks.
// It serializes concurrent storage operations for the same character.
//
// Implementations must make Release idempotent: releasing an absent or
// expired lock, or a lock owned by a different token, is a no-op (nil error).
type LockStore interface {
	// Acquire attempts to take the lock named key, holding it for at most ttl.
	// On success it returns an opaque ownership token that Release must present.
	// A held lock yields a wrapped ErrStorageLocked.
	Acquire(ctx context.Context, key string, ttl time.Duration) (token string, err error)

	// Release frees the lock only if still owned by token (compare-and-delete).
	// Releasing an absent/expired lock is a no-op.
	Release(ctx context.Context, key string, token string) error
}

// StorageLockKey returns the lock key for a character's storage mutex.
// The prefix namespaces storage locks away from other locks.
func StorageLockKey(charID uint32) string {
	return fmt.Sprintf("storage:char:%d", charID)
}
