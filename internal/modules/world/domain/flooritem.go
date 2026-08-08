package domain

// FloorItem is an item lying on the map, addressable by a map-object GroundID.
// Produced by mob drops (and player drops); consumed by CZ_ITEM_PICKUP.
type FloorItem struct {
	GroundID uint32 // map-object id (unique per world)
	NameID   uint32 // item_db id
	Amount   uint32
	PosX     int16
	PosY     int16
	Map      string
	// Dropper is the EntityID that dropped the item (mob or player), for telemetry.
	Dropper EntityID
}
