// Package domain contains entities and port interfaces for the inventory
// feature (WS-C adjacent): persistence of per-character items, cart, and
// equipment state. The domain layer is GORM-free: types here are pure
// Go values with no annotations and no SQL awareness, so a future
// alternative store (e.g. PostgreSQL via the same gorm.DB, or a
// in-memory test double) can be swapped in by a different repository
// implementation.
package domain

// EquipSlot is the rAthena equip-position bitfield stored in
// `inventory.equip` (pc.h::EQP_*). Zero means "the item lives in the
// regular item grid, not on the body" — the legacy convention carried
// over from main.sql. The mask is open-ended (new positions are
// occasionally added in newer rAthena revisions), so the domain type
// is uint32 to leave room without an artificial cap.
type EquipSlot uint32

// ItemOption is one of up to five random-option slots
// (`option_id0..4` / `option_val0..4` / `option_parm0..4` on the
// inventory row). The schema reserves five slots and rAthena's
// itemdb code only ever reads five; we mirror that here so callers
// can iterate IndexOption(0..4) without a bounds check.
type ItemOption struct {
	ID    int16 // option_id (signed smallint in schema)
	Value int16 // option_val (signed smallint in schema)
	Parm  int8  // option_parm (signed tinyint in schema)
}

// InventoryItem is one row of the rAthena `inventory` table. Field
// names map 1:1 to the SQL columns; the type width matches the schema
// (int unsigned → uint32, bigint unsigned → uint64, signed smallint →
// int16, etc.) so the repository layer can do a direct row scan
// without lossy conversions. Zero value is a usable "empty" item.
//
// The Weight field is NOT part of the inventory row in rAthena — item
// weight is derived from the itemdb (db/item_db.yml) at load time and
// summed across the inventory. Persistence therefore does not carry
// it; callers that need the current inventory weight fetch itemdb
// and aggregate. Keeping it out of the entity avoids a phantom
// column that would have to default to 0 on every migration.
type InventoryItem struct {
	ID           uint32        // `id` — primary key
	CharID       uint32        // `char_id` — owning character
	NameID       uint32        // `nameid` — item_db.yml id
	Amount       uint32        // `amount` — stack count (0 invalid; equipment is amount=1)
	Equip        EquipSlot     // `equip` — bitfield of EQP_* positions; 0 = in grid
	Identify     int16         // `identify` — signed smallint; 1 = identified, 0 = unidentified
	Refine       uint8         // `refine` — +0..+10
	Attribute    uint8         // `attribute` — broken / elemental flag
	Card0        uint32        // `card0` — forged card slot 0
	Card1        uint32        // `card1` — forged card slot 1
	Card2        uint32        // `card2` — forged card slot 2
	Card3        uint32        // `card3` — forged card slot 3
	Options      [5]ItemOption // `option_idN` / `option_valN` / `option_parmN` for N=0..4
	ExpireTime   uint32        // `expire_time` — unix seconds, 0 = no expiry
	Favorite     uint8         // `favorite` — 1 = starred in UI
	Bound        uint8         // `bound` — 1 = bound to character
	UniqueID     uint64        // `unique_id` — globally unique item id (bigint unsigned)
	EquipSwitch  uint32        // `equip_switch` — alternate equipment view state
	EnchantGrade uint8         // `enchantgrade` — enchant tier
}

// SlotRange is the conventional item-grid index range used across the
// inventory and equipment views. The repository never asserts on it —
// it is purely a service-layer helper — but exposing it here keeps
// slot arithmetic discoverable from the package that owns the row.
const (
	// MaxItemSlots is the rAthena canonical inventory size before the
	// `inventory_slots` per-character expansion (itemdb / pc.cpp MAX_INVENTORY).
	MaxItemSlots = 100
)
