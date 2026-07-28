// Package domain defines the inventory bounded context's entity and outbound
// ports: the InventoryItem (one row of rAthena's `inventory` table, migration
// 000003) and the repository the world use cases (pickup, equip) depend on. It
// is pure — no transport or persistence dependencies — so the GORM and
// in-memory adapters, the world use cases, and tests program against these types
// rather than against each other.
package domain

import (
	"context"
	"errors"
)

// Sentinel errors returned by the repository and the application layer. Service
// code compares with errors.Is so wrapping is preserved; repository adapters
// must return these (wrapped) rather than driver-specific types.
var (
	// ErrInventoryFull: no stackable merge was possible and the bag is at its
	// slot cap (MaxInventorySlots). rAthena's pc_additem returns ADDITEM_FAIL
	// (0) here; the pickup use case maps it to the fail ack (result=6).
	ErrInventoryFull = errors.New("inventory full")
	// ErrItemNotFound: no row matched the lookup key (accountID, charID, index).
	// The equip/unequip use cases treat this as a stale or forged request and
	// answer with their fail ack rather than faulting.
	ErrItemNotFound = errors.New("inventory item not found")
)

// MaxInventorySlots is the bag cap rAthena compiles as MAX_INVENTORY (100). A
// non-stackable pickup beyond this yields ErrInventoryFull. Stackable pickups
// merge into an existing row and never consume a new slot, so they are never
// full-gated.
const MaxInventorySlots = 100

// ItemOption is one random/option entry attached to an item (rAthena's
// ItemOptions: option_id / option_val / option_parm). The `inventory` table
// stores five of these per row (option_*0..4). Mob drops carry none, so the
// zero value is the common case.
type ItemOption struct {
	ID    uint16
	Value int16
	Param int8
}

// InventoryItem is one row of the `inventory` table (migration 000003). It maps
// rAthena's per-char live bag: the item identity (NameID), stack count (Amount),
// the worn-location bitmask (Equip), and the equip/option/rental detail columns
// the wire encoders carry. The table owns no Type column — the IT_* wire enum is
// always resolved at emit time from the item_db — so this struct carries no Type.
//
// Index is NOT a column: rAthena assigns the grid slot from the in-memory array
// position at load (the `inventory` table has no slot column by design). The
// repository assigns Index on LoadByChar (id-ascending) and on AddItem (merge
// keeps the matched row's index; insert takes the next slot) so it is stable for
// the life of a session — the client reuses it in CZ_REQ_WEAR_EQUIP / CZ_USE_ITEM2
// after a pickup ack. See InventoryRepository.AddItem for the stability contract.
type InventoryItem struct {
	ID           uint32        // id (auto-increment PK; not the wire slot)
	CharID       uint32        // char_id (owning character)
	Index        uint16        // grid slot (wire index; load-derived, not a column)
	NameID       uint32        // nameid (item_db id → wire nameID)
	Amount       uint32        // amount (stack count)
	Equip        uint32        // equip (EQP_* bitmask; 0 = unequipped)
	Identified   bool          // identify
	Refine       uint8         // refine (+0..+10)
	Attribute    uint8         // attribute (broken/etc. card state)
	Cards        [4]uint32     // card0..card3
	Options      [5]ItemOption // option_id/val/parm 0..4
	ExpireTime   uint32        // expire_time (rental epoch; 0 = no expiry)
	Favorite     bool          // favorite
	Bound        uint8         // bound
	UniqueID     uint64        // unique_id
	EquipSwitch  uint32        // equip_switch
	EnchantGrade uint8         // enchantgrade
}

// NewItem is the validated input to InventoryRepository.AddItem: the item to add
// and whether it may merge into an existing stack. The pickup use case resolves
// Stackable once from the item_db (itemdb.IsStackable) and hands the decision
// down so the repository performs the atomic read-modify-write without depending
// on the item_db package — the merge is a persistence concern, the stackability
// rule is a data concern.
type NewItem struct {
	NameID    uint32
	Amount    uint32
	Stackable bool
}

// InventoryRepository is the outbound persistence port for a character's bag.
// The GORM and in-memory adapters implement it. Every method is scoped by
// (accountID, charID) — the impersonation guard shared with the character
// module: a charID belonging to another account yields ErrItemNotFound (for
// lookups) or a no-op (for writes that find no scoped row), never a cross-account
// mutation. accountID must come from the verified session, never the packet.
type InventoryRepository interface {
	// LoadByChar returns the character's bag, ordered so the assigned Index is
	// stable (id-ascending → 0,1,2,…). A character with no items yields an empty
	// (non-nil) slice and a nil error.
	LoadByChar(ctx context.Context, accountID, charID uint32) ([]InventoryItem, error)
	// AddItem merges item into an existing stackable row (same NameID) or inserts
	// a new row when Stackable is false or no match exists. It returns the row as
	// it now stands — Index, NameID, and the new Amount the pickup ack carries.
	// A non-stackable insert at the slot cap yields ErrInventoryFull; the caller
	// answers the client with the fail ack rather than faulting. The returned
	// Index is stable for the session: a merge keeps the matched row's index, an
	// insert takes the next free slot (the current row count), and pickups never
	// reorder existing rows.
	AddItem(ctx context.Context, accountID, charID uint32, item NewItem) (InventoryItem, error)
}
