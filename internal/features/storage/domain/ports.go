package domain

import (
	"context"
)

// InventoryRepository is the outbound port for character inventory access.
// Used by storage operations to verify inventory space and item ownership.
type InventoryRepository interface {
	// ListByChar returns all inventory items for a character.
	ListByChar(ctx context.Context, charID uint32) ([]InventoryItem, error)
}

// InventoryItem represents a character inventory item (minimal subset needed
// by storage operations). The full inventory entity lives in the inventory
// feature domain.
type InventoryItem struct {
	ID     uint64 // Unique inventory item ID
	CharID uint32 // Owning character
	NameID uint32 // Item type ID from item_db
	Amount int32  // Stack amount
}
