package app

import (
	"context"
	"errors"
	"fmt"

	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	"github.com/bouroo/goAthena/pkg/ro/equip"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
	"github.com/bouroo/goAthena/pkg/ro/statcalc"
)

// equipInventory is the narrow inventory capability EquipService needs: loading a
// character's items and persisting a changed equip bitmask on one row. The
// production *invapp.InventoryService satisfies it; tests inject a fake.
type equipInventory interface {
	LoadByChar(ctx context.Context, accountID, charID uint32) ([]invdomain.Item, error)
	SetEquip(ctx context.Context, id invdomain.ItemID, equip uint32) error
}

// EquipService runs the equipment use case: wear/remove gear into its EQP_* slot
// and build the statcalc.Equipment profile the kernel's damage/status math reads.
// Equipped state lives on the inventory rows (Item.Equip bitmask); the item_db
// Attack/Defense columns supply the weapon-ATK / armor-DEF contributions.
type EquipService struct {
	inv   equipInventory
	items *itemdb.Registry
}

// NewEquipService builds an EquipService backed by the inventory port and the
// loaded item_db registry. items may be nil (best-effort degradation: every item
// resolves as unknown and contributes no stats), matching the boot-time
// empty-registry fallback in world/di.go.
func NewEquipService(inv equipInventory, items *itemdb.Registry) *EquipService {
	return &EquipService{inv: inv, items: items}
}

// Equip errors are distinct sentinels so the gateway maps each to a specific
// S→C ack result without parsing strings (no branch on error strings).
var (
	// ErrNotEquippable means the item has no equipment locations at all (Etc/etc.).
	ErrNotEquippable = errors.New("equip: item is not equippable")
	// ErrWrongSlot means the item is equippable but not into the requested position.
	ErrWrongSlot = errors.New("equip: item cannot go in that slot")
	// ErrItemNotFound means the 1-based inventory index is out of range.
	ErrItemNotFound = errors.New("equip: inventory index out of range")
)

// maxItemID bounds the uint32→int32 cast for item_db id lookup. item_db ids are
// < 2^31; anything larger is not a real id and resolves to "not found".
const maxItemID = uint32(1<<31 - 1)

// Equip wears the item at the 1-based inventory index invIndex into position, the
// EQP_* bitmask the client requested.
//
// Validation: an item with no equip locations (EquipLocations == 0) yields
// ErrNotEquippable; an equippable item whose allowed locations do not overlap
// position yields ErrWrongSlot. On a slot conflict — another equipped item already
// occupying any bit of position — the conflicting item is unequipped first, then
// the requested item is worn.
func (s *EquipService) Equip(ctx context.Context, accountID, charID uint32, invIndex int, position uint32) error {
	items, err := s.inv.LoadByChar(ctx, accountID, charID)
	if err != nil {
		return fmt.Errorf("equip: load inventory: %w", err)
	}
	if invIndex < 1 || invIndex > len(items) {
		return ErrItemNotFound
	}
	target := items[invIndex-1]
	entry := s.itemEntry(target.NameID)
	if entry == nil || entry.EquipLocations == 0 {
		return ErrNotEquippable
	}
	if entry.EquipLocations&position == 0 {
		return ErrWrongSlot
	}
	// Slot conflict: unequip any other item occupying a bit of position first.
	for _, other := range items {
		if other.ID == target.ID {
			continue
		}
		if other.Equip&position != 0 {
			if err := s.inv.SetEquip(ctx, other.ID, 0); err != nil {
				return fmt.Errorf("equip: unequip conflicting item %d: %w", other.ID, err)
			}
		}
	}
	if err := s.inv.SetEquip(ctx, target.ID, position); err != nil {
		return fmt.Errorf("equip: wear item %d: %w", target.ID, err)
	}
	return nil
}

// Unequip removes the item at the 1-based inventory index from its slot.
func (s *EquipService) Unequip(ctx context.Context, accountID, charID uint32, invIndex int) error {
	items, err := s.inv.LoadByChar(ctx, accountID, charID)
	if err != nil {
		return fmt.Errorf("unequip: load inventory: %w", err)
	}
	if invIndex < 1 || invIndex > len(items) {
		return ErrItemNotFound
	}
	target := items[invIndex-1]
	if err := s.inv.SetEquip(ctx, target.ID, 0); err != nil {
		return fmt.Errorf("unequip: clear item %d: %w", target.ID, err)
	}
	return nil
}

// EquipmentProfile sums the equipped contributions the kernel's statcalc reads:
//
//   - WeaponATK is the Attack of every item worn in a hand slot (equip.Arms, the
//     right/left-hand composite). Shields sit in the left hand but carry Attack 0,
//     so they correctly add nothing.
//   - ItemDEF is the Defense summed over every equipped item — rAthena sums def
//     across all equipped gear (weapon included), so a weapon's Defense (usually
//     0) folds in naturally.
//   - ItemMDEF is left at 0: item_db has no MDEF column, so equipment MDEF (which
//     in rAthena comes from item scripts) is out of scope for this milestone.
func (s *EquipService) EquipmentProfile(ctx context.Context, accountID, charID uint32) (statcalc.Equipment, error) {
	items, err := s.inv.LoadByChar(ctx, accountID, charID)
	if err != nil {
		return statcalc.Equipment{}, fmt.Errorf("equipment profile: load inventory: %w", err)
	}
	var eq statcalc.Equipment
	for _, it := range items {
		if !it.IsEquipped() {
			continue
		}
		entry := s.itemEntry(it.NameID)
		if entry == nil {
			continue
		}
		if it.Equip&equip.Arms != 0 {
			eq.WeaponATK += entry.Attack
		}
		eq.ItemDEF += entry.Defense
	}
	return eq, nil
}

// itemEntry resolves the item_db entry for a NameID, tolerating a nil registry
// and an out-of-range id (both resolve to nil = unknown item).
func (s *EquipService) itemEntry(nameID uint32) *itemdb.ItemEntry {
	if s.items == nil || nameID > maxItemID {
		return nil
	}
	return s.items.Get(int32(nameID)) //nolint:gosec // G115: guarded <= maxInt31
}
