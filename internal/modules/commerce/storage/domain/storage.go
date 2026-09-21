// Package domain holds the storage bounded context's pure domain model: the
// StorageItem aggregate (mirrors the rAthena `storage` table) and the
// repository port. Storage is the per-account warehouse; it is *not* the
// guild storage (M11). One row per stacked/equipped item instance.
package domain

import (
	"context"
	"errors"
)

// StorageItemID is the rAthena storage.id (auto-increment per row).
type StorageItemID uint32

// StorageItem mirrors the rAthena `storage` table — one row per stacked or
// equipped item instance owned by an account. Column tags match the legacy
// schema exactly so a rAthena dump can be loaded without a transform.
type StorageItem struct {
	ID         StorageItemID `gorm:"column:id;primaryKey;autoIncrement"`
	AccountID  uint32        `gorm:"column:account_id"`
	NameID     uint32        `gorm:"column:nameid"` // item_db id
	Amount     uint32        `gorm:"column:amount"`
	Equip      uint32        `gorm:"column:equip"` // equipped slot bitmask (0 = not equipped)
	Identify   int16         `gorm:"column:identify"`
	Refine     uint8         `gorm:"column:refine"`
	Attribute  uint8         `gorm:"column:attribute"`
	Card0      uint32        `gorm:"column:card0"`
	Card1      uint32        `gorm:"column:card1"`
	Card2      uint32        `gorm:"column:card2"`
	Card3      uint32        `gorm:"column:card3"`
	ExpireTime uint32        `gorm:"column:expire_time"`
	Favorite   uint8         `gorm:"column:favorite"`
	Bound      uint8         `gorm:"column:bound"`
	UniqueID   uint64        `gorm:"column:unique_id"`
}

// TableName fixes the legacy table name so GORM does not pluralize it.
func (StorageItem) TableName() string { return "storage" }

// IsEquipped reports whether the item is in an equipment slot.
func (s StorageItem) IsEquipped() bool { return s.Equip != 0 }

// Errors for the storage domain.
var (
	// ErrStorageItemNotFound is returned when no row matches the id.
	ErrStorageItemNotFound = errors.New("storage item not found")
	// ErrStorageFull is returned when adding would overflow MAX_STORAGE.
	// rAthena's MAX_STORAGE is 600 for basic accounts and goes up by guild
	// contributions; we surface it as a single constant until M11.
	ErrStorageFull = errors.New("storage full")
	// ErrStorageInsufficientAmount mirrors inventory's underflow error.
	ErrStorageInsufficientAmount = errors.New("insufficient storage item amount")
)

// MaxStorage is the rAthena hard cap on the per-account warehouse. It is
// 600 for the basic account; premium and guild upgrades raise this in
// rAthena via `storage->max_amount`. We start with the floor value; the
// service computes the effective ceiling at deposit time.
const MaxStorage = 600

// StorageRepository is the persistence port for the storage aggregate.
type StorageRepository interface {
	// LoadByAccount returns all storage rows owned by accountID, ordered by id.
	// The ordering is load-bearing: the warehouse index the client sends is
	// an offset into this list, so unstable order would map a client slot
	// to a different row between the LoadEndAck-style init burst and the
	// handler that consumes it.
	LoadByAccount(ctx context.Context, accountID uint32) ([]StorageItem, error)
	// Add inserts a new row. Equipment always inserts a new row; stackable
	// items (Equip==0) merge into an existing same-NameID row when the
	// account owns one. Returns ErrStorageFull if the account is at the
	// ceiling and the caller asked for a non-stacking insert.
	Add(ctx context.Context, accountID, nameID uint32, amount int) (StorageItem, error)
	// Remove decrements amount (deletes the row at zero) or returns
	// ErrStorageInsufficientAmount / ErrStorageItemNotFound.
	Remove(ctx context.Context, id StorageItemID, amount int) error
	// SetEquip overwrites the equip bitmask of one storage row.
	SetEquip(ctx context.Context, id StorageItemID, equip uint32) error
}
