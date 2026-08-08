// Package domain holds the inventory bounded context's pure domain model: the
// Item aggregate (mirrors the rAthena `inventory` table) and the repository port.
package domain

import (
	"context"
	"errors"
)

// ItemID is the rAthena inventory.id (auto-increment per row).
type ItemID uint32

// Item mirrors the rAthena `inventory` table — one row per stacked/equipped item
// instance owned by a character. Column tags match the legacy schema exactly.
type Item struct {
	ID         ItemID `gorm:"column:id;primaryKey;autoIncrement"`
	CharID     uint32 `gorm:"column:char_id"`
	NameID     uint32 `gorm:"column:nameid"` // item_db id
	Amount     uint32 `gorm:"column:amount"`
	Equip      uint32 `gorm:"column:equip"` // equipped slot bitmask (0 = not equipped)
	Identify   int16  `gorm:"column:identify"`
	Refine     uint8  `gorm:"column:refine"`
	Attribute  uint8  `gorm:"column:attribute"`
	Card0      uint32 `gorm:"column:card0"`
	Card1      uint32 `gorm:"column:card1"`
	Card2      uint32 `gorm:"column:card2"`
	Card3      uint32 `gorm:"column:card3"`
	ExpireTime uint32 `gorm:"column:expire_time"`
	Favorite   uint8  `gorm:"column:favorite"`
	Bound      uint8  `gorm:"column:bound"`
	UniqueID   uint64 `gorm:"column:unique_id"`
}

// TableName fixes the legacy table name so GORM does not pluralize it.
func (Item) TableName() string { return "inventory" }

// IsEquipped reports whether the item is in an equipment slot.
func (i Item) IsEquipped() bool { return i.Equip != 0 }

// errors  for the inventory domain.
var (
	// ErrItemNotFound is returned when no item row matches the id.
	ErrItemNotFound = errors.New("item not found")
	// ErrInsufficientAmount is returned when a remove/stack-merge underflows.
	ErrInsufficientAmount = errors.New("insufficient item amount")
)

// ItemRepository is the persistence port for the item-container aggregate.
type ItemRepository interface {
	// LoadByChar returns all inventory rows owned by charID.
	LoadByChar(ctx context.Context, accountID, charID uint32) ([]Item, error)
	// Add inserts or stacks an item for charID. Same NameID rows stack (amount
	// added) when the item is stackable (equip==0); equipment rows are always
	// separate inserts.
	Add(ctx context.Context, charID, nameID uint32, amount int) (Item, error)
	// Remove decrements amount (and deletes the row if it hits zero) or returns
	// ErrInsufficientAmount / ErrItemNotFound.
	Remove(ctx context.Context, id ItemID, amount int) error
}
