package domain

import "time"

// VendingItem represents a single item listing in a player's vending shop.
// Each listing references an inventory item the shop owner has put up for sale
// at a player-determined price.
type VendingItem struct {
	InventoryID uint32 // Reference to the owner's inventory item row
	ItemID      uint32 // Item type ID (nameid in rAthena)
	Amount      int32  // Stack count available for sale (positive)
	Price       uint32 // Per-unit price in zeny
}

// VendingShop represents a player's vending shop. The shop exists on the map
// where the owner opened it; other players can browse and buy items while the
// owner remains stationary with the shop open.
type VendingShop struct {
	ID        string        // Unique shop identifier
	OwnerID   uint32        // Character ID of the shop owner
	Title     string        // Player-visible shop name
	Items     []VendingItem // Items listed for sale
	MapName   string        // Map where the shop is open
	X         int32         // X position of the shop
	Y         int32         // Y position of the shop
	CreatedAt time.Time     // Shop creation timestamp
	UpdatedAt time.Time     // Last modification timestamp
}
