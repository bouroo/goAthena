// Package service — inventory use cases (Phase 2A).
//
// This file owns the inventory mutation and listing use cases consumed
// by the IdentityService gRPC surface (GetInventory / EquipItem /
// UnequipItem / UseItem). The methods compose the same outbound
// InventoryRepository port the inventory feature owns and reuses the
// ErrItemNotFound sentinel so callers can branch with errors.Is.
package service

import (
	"context"
	"fmt"

	inventorydomain "github.com/bouroo/goAthena/internal/features/inventory/domain"
)

// GetInventory returns the entire inventory for charID, scoped to the
// authenticated accountID. Both keys must be non-zero; a zero key would
// otherwise bypass the ownership check and return a garbage slice.
func (s *identityService) GetInventory(
	ctx context.Context,
	accountID, charID uint32,
) ([]inventorydomain.InventoryItem, error) {
	if accountID == 0 || charID == 0 {
		return nil, fmt.Errorf("get inventory (account=%d, char=%d): %w",
			accountID, charID, inventorydomain.ErrItemNotFound)
	}
	items, err := s.inventory.ListByChar(ctx, charID)
	if err != nil {
		return nil, fmt.Errorf("list inventory for char %d: %w", charID, err)
	}
	if items == nil {
		// Keep the wire shape stable: an empty inventory is encoded
		// as a non-nil empty slice so callers can iterate without a
		// nil-check, mirroring ListCharacters' convention.
		return []inventorydomain.InventoryItem{}, nil
	}
	return items, nil
}

// EquipItem moves an inventory item into an equipment slot by
// overwriting the EQP_* bitmask. The method enforces ownership by
// loading every item the character owns and rejecting the call when
// itemID is not present, before any row is mutated.
//
// Equipping an already-owned item does NOT change carry weight (the
// row moves between the grid and the equipment slots in place), so the
// weight gate intentionally does NOT run here. Weight is enforced on
// acquisition (drop, mail, shop buy, NPC reward) via checkWeight once
// the add-item RPC lands.
func (s *identityService) EquipItem(
	ctx context.Context,
	accountID, charID, itemID, equipPos uint32,
) error {
	if accountID == 0 || charID == 0 || itemID == 0 {
		return fmt.Errorf("equip item (account=%d, char=%d, item=%d): %w",
			accountID, charID, itemID, inventorydomain.ErrItemNotFound)
	}
	if err := s.assertItemOwnedByChar(ctx, charID, itemID); err != nil {
		return err
	}
	if err := s.inventory.SetEquip(ctx, itemID, equipPos); err != nil {
		return fmt.Errorf("set equip (item=%d, pos=%d): %w", itemID, equipPos, err)
	}
	return nil
}

// UnequipItem clears the EQP_* bitmask, moving the item back into the
// inventory grid. The ownership check mirrors EquipItem's. Returns the
// prior EQP_* bitmask (before clearing to 0).
func (s *identityService) UnequipItem(
	ctx context.Context,
	accountID, charID, itemID uint32,
) (uint32, error) {
	if accountID == 0 || charID == 0 || itemID == 0 {
		return 0, fmt.Errorf("unequip item (account=%d, char=%d, item=%d): %w",
			accountID, charID, itemID, inventorydomain.ErrItemNotFound)
	}
	items, err := s.inventory.ListByChar(ctx, charID)
	if err != nil {
		return 0, fmt.Errorf("list inventory for unequip (char=%d): %w", charID, err)
	}
	var current *inventorydomain.InventoryItem
	for i := range items {
		if items[i].ID == itemID {
			current = &items[i]
			break
		}
	}
	if current == nil {
		return 0, fmt.Errorf("unequip item (item=%d not found on char %d): %w",
			itemID, charID, inventorydomain.ErrItemNotFound)
	}

	if err := s.inventory.SetEquip(ctx, itemID, 0); err != nil {
		return 0, fmt.Errorf("clear equip (item=%d): %w", itemID, err)
	}
	return uint32(current.Equip), nil
}

// UseItem decrements the stack count of a consumable item by one. If
// the post-decrement amount is zero the row is deleted; otherwise the
// new amount is persisted via UpdateAmount. The caller learns the
// post-decrement amount (0 means the row was deleted) so the wire
// response can advertise the updated stack size.
func (s *identityService) UseItem(
	ctx context.Context,
	accountID, charID, itemID uint32,
) (uint32, error) {
	if accountID == 0 || charID == 0 || itemID == 0 {
		return 0, fmt.Errorf("use item (account=%d, char=%d, item=%d): %w",
			accountID, charID, itemID, inventorydomain.ErrItemNotFound)
	}

	items, err := s.inventory.ListByChar(ctx, charID)
	if err != nil {
		return 0, fmt.Errorf("list inventory for use (char=%d): %w", charID, err)
	}
	var current *inventorydomain.InventoryItem
	for i := range items {
		if items[i].ID == itemID {
			current = &items[i]
			break
		}
	}
	if current == nil {
		return 0, fmt.Errorf("use item (char=%d, item=%d): %w",
			charID, itemID, inventorydomain.ErrItemNotFound)
	}
	if current.Amount == 0 {
		// Already drained out-of-band; treat as missing rather than
		// producing a silent zero-decade edge case.
		return 0, fmt.Errorf("use item (char=%d, item=%d): %w",
			charID, itemID, inventorydomain.ErrItemNotFound)
	}

	remaining, err := s.inventory.ConsumeOne(ctx, itemID)
	if err != nil {
		return 0, fmt.Errorf("use item (char=%d, item=%d): %w", charID, itemID, err)
	}
	return remaining, nil
}

// assertItemOwnedByChar looks the item up in charID's inventory and
// returns a wrapped ErrItemNotFound when it does not belong to that
// character. The error chain keeps the sentinel intact so handlers can
// map cross-character attempts onto success=false via errors.Is.
func (s *identityService) assertItemOwnedByChar(
	ctx context.Context,
	charID, itemID uint32,
) error {
	items, err := s.inventory.ListByChar(ctx, charID)
	if err != nil {
		return fmt.Errorf("ownership check for item %d on char %d: %w", itemID, charID, err)
	}
	for i := range items {
		if items[i].ID == itemID {
			return nil
		}
	}
	return fmt.Errorf("item %d not owned by char %d: %w",
		itemID, charID, inventorydomain.ErrItemNotFound)
}

// CheckWeight validates that acquiring addAmount units of nameID
// addNameID would not push charID's carried weight past MaxWeight.
// accountID scopes the char lookup so a cross-account charID cannot
// drive a weight probe on someone else's roster.
//
// The gate is intentionally structured to mirror rAthena's
// pc_additem → pc_calc_weight → status_calc_max_weight chain
// (src/map/pc.cpp:13864 + src/map/status.cpp:3663): the current
// carried weight is the sum of (lookup.Weight(item.NameID) * item.Amount)
// over every owned row, the max is base + str*300, and acquisition
// fails with ErrWeightExceeded when current + addWeight > max.
//
// In the production binary today this returns nil unconditionally
// because the default ItemWeightLookup is ZeroItemWeight (per-item
// weight is item_db-derived, and item_db loading is out of scope for
// Phase 2A). The mechanism is wired, deterministic, and unit-tested;
// item_db.yml just needs to plug into ItemWeightLookup and every
// acquire flow keeps working without further service changes.
func (s *identityService) CheckWeight(
	ctx context.Context,
	accountID, charID, addNameID, addAmount uint32,
) error {
	char, err := s.characters.GetByID(ctx, accountID, charID)
	if err != nil {
		return fmt.Errorf("check weight, load char (account=%d, char=%d): %w",
			accountID, charID, err)
	}
	if char == nil {
		return fmt.Errorf("check weight, char not found (account=%d, char=%d): %w",
			accountID, charID, inventorydomain.ErrItemNotFound)
	}

	items, err := s.inventory.ListByChar(ctx, charID)
	if err != nil {
		return fmt.Errorf("check weight, list inventory (char=%d): %w", charID, err)
	}

	var current uint64
	for i := range items {
		w := uint64(s.inventoryWeight.Weight(items[i].NameID))
		current += w * uint64(items[i].Amount)
	}

	addWeight := uint64(s.inventoryWeight.Weight(addNameID)) * uint64(addAmount)
	limit := uint64(inventorydomain.MaxWeight(inventorydomain.NoviceMaxWeightBase, char.Str))

	if current+addWeight > limit {
		return fmt.Errorf("check weight (char=%d, nameid=%d, amount=%d, current=%d, add=%d, max=%d): %w",
			charID, addNameID, addAmount, current, addWeight, limit,
			inventorydomain.ErrWeightExceeded)
	}
	return nil
}

// AddItem acquires amount units of nameid for charID, persisting the gain
// immediately. Weight is enforced first (CheckWeight); on
// ErrWeightExceeded nothing is written. When an existing plain stack
// matches the rAthena merge predicate (pc.cpp:6019) the amount merges into
// it; otherwise a new identified row is inserted. The returned id is the
// inventory row id callers store in the session invIndex.
func (s *identityService) AddItem(
	ctx context.Context,
	accountID, charID, nameid, amount uint32,
) (uint32, error) {
	if accountID == 0 || charID == 0 || nameid == 0 || amount == 0 {
		return 0, fmt.Errorf("add item (account=%d, char=%d, nameid=%d, amount=%d): %w",
			accountID, charID, nameid, amount, inventorydomain.ErrItemNotFound)
	}

	if err := s.CheckWeight(ctx, accountID, charID, nameid, amount); err != nil {
		return 0, fmt.Errorf("add item (char=%d, nameid=%d, amount=%d): %w", charID, nameid, amount, err)
	}

	items, err := s.inventory.ListByChar(ctx, charID)
	if err != nil {
		return 0, fmt.Errorf("add item, list inventory (char=%d): %w", charID, err)
	}

	// rAthena only merges stackable types and only into a plain stack whose
	// (nameid, bound, expire_time, unique_id, cards) all match a freshly
	// acquired zero-attr item (pc.cpp:6019). A non-stackable type or any
	// bound/expire/unique/card distinguishes the row — it never merges.
	if s.inventoryWeight.IsStackable(nameid) {
		for i := range items {
			if mergeableStack(items[i], nameid) {
				target := items[i]
				// TODO(A5-MAXAMOUNT): clamp at rAthena MAX_AMOUNT before the
				// add to avoid a uint32 overflow on an oversized merge.
				newAmount := target.Amount + amount
				if err := s.inventory.UpdateAmount(ctx, target.ID, newAmount); err != nil {
					return 0, fmt.Errorf("add item, merge (char=%d, id=%d, nameid=%d): %w",
						charID, target.ID, nameid, err)
				}
				return target.ID, nil
			}
		}
	}

	id, err := s.inventory.Add(ctx, charID, inventorydomain.InventoryItem{
		NameID:   nameid,
		Amount:   amount,
		Identify: 1, // floor pickups are identified at MVP (cards/options out of scope)
	})
	if err != nil {
		return 0, fmt.Errorf("add item (char=%d, nameid=%d, amount=%d): %w", charID, nameid, amount, err)
	}
	return id, nil
}

// mergeableStack reports whether the existing row is a plain stack of
// nameID that rAthena would merge a fresh acquisition into (pc.cpp:6019):
// same nameid, no bound, no expiry, no unique_id, and no forged cards.
// Options are deliberately NOT part of the classic predicate.
func mergeableStack(item inventorydomain.InventoryItem, nameID uint32) bool {
	return item.NameID == nameID &&
		item.Bound == 0 &&
		item.ExpireTime == 0 &&
		item.UniqueID == 0 &&
		item.Card0 == 0 && item.Card1 == 0 && item.Card2 == 0 && item.Card3 == 0
}

// ConsumeItem decrements the stack at itemID by amount, deleting the row
// when it hits zero. Ownership is verified first; the decrement is atomic
// and never commits a partial over-consume.
func (s *identityService) ConsumeItem(
	ctx context.Context,
	accountID, charID, itemID, amount uint32,
) (uint32, error) {
	if accountID == 0 || charID == 0 || itemID == 0 || amount == 0 {
		return 0, fmt.Errorf("consume item (account=%d, char=%d, item=%d, amount=%d): %w",
			accountID, charID, itemID, amount, inventorydomain.ErrItemNotFound)
	}

	if err := s.assertItemOwnedByChar(ctx, charID, itemID); err != nil {
		return 0, fmt.Errorf("consume item (char=%d, item=%d): %w", charID, itemID, err)
	}

	remaining, err := s.inventory.Consume(ctx, itemID, amount)
	if err != nil {
		return 0, fmt.Errorf("consume item (char=%d, item=%d, amount=%d): %w", charID, itemID, amount, err)
	}
	return remaining, nil
}
