package app

import (
	"context"
	"fmt"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	contentdomain "github.com/bouroo/goAthena/internal/modules/content/domain"
	invapp "github.com/bouroo/goAthena/internal/modules/inventory/app"
)

// scriptInvAccountResolver looks up the owning accountID for a charID. The
// inventory service requires accountID on LoadByChar (the legacy schema
// queries by account); the script VM only has charID, so the adapter
// resolves it on every call. A nil resolver returns 0 and the inventory
// reads fall through to an empty result — better than a crash.
type scriptInvAccountResolver interface {
	FindByID(ctx context.Context, id chardomain.CharID) (chardomain.Character, error)
}

// scriptInvEquip is the narrow equip-service surface the script adapter needs:
// wear/remove a row by its LoadByChar index, with slot validation. The
// production *EquipService satisfies it; tests inject a fake.
type scriptInvEquip interface {
	Equip(ctx context.Context, accountID, charID uint32, serverRow int, position uint32) error
	Unequip(ctx context.Context, accountID, charID uint32, serverRow int) error
}

// scriptInventoryAdapter is the world-side implementation of
// contentdomain.ScriptInventory: it bridges the script VM's getitem/delitem/
// countitem/equip/unequip builtins to the inventory and equip services, with
// the accountID resolution the legacy schema needs on every LoadByChar call.
//
// The adapter is intentionally narrow: it does not own inventory state, nor
// does it know about packet emission (the gateway handles ZC_ITEM_PICKUP_ACK
// separately). It is a use-case translator, not a duplicate of InventoryService.
type scriptInventoryAdapter struct {
	inv   *invapp.InventoryService
	equip scriptInvEquip
	chars scriptInvAccountResolver
}

// NewScriptInventoryAdapter builds a contentdomain.ScriptInventory over the
// inventory service, the equip port, and the character repository (for
// accountID resolution).
func NewScriptInventoryAdapter(inv *invapp.InventoryService, equip scriptInvEquip, chars scriptInvAccountResolver) contentdomain.ScriptInventory {
	return &scriptInventoryAdapter{inv: inv, equip: equip, chars: chars}
}

// accountFor is the resolver helper. A not-found char returns 0 and a
// not-found error: the inventory reads then return an empty set and the
// script VM sees CountItem=0 / Equip=false, which is the safe default for
// "this character has no on-server state right now".
func (a *scriptInventoryAdapter) accountFor(ctx context.Context, charID uint32) (uint32, error) {
	c, err := a.chars.FindByID(ctx, chardomain.CharID(charID))
	if err != nil {
		return 0, fmt.Errorf("scriptinv: account for char %d: %w", charID, err)
	}
	return c.AccountID, nil
}

// GetItem grants amount units of nameID. A nil account resolver or a load
// error returns false (the VM's `getitem` reports 0 — script authors can
// branch on it).
func (a *scriptInventoryAdapter) GetItem(charID uint32, nameID uint32, amount int) bool {
	if amount <= 0 {
		return false
	}
	if _, err := a.accountFor(context.Background(), charID); err != nil {
		return false
	}
	if _, err := a.inv.Add(context.Background(), charID, nameID, amount); err != nil {
		return false
	}
	return true
}

// DelItem removes amount units of nameID, all-or-nothing. The inventory repo
// operates on a single row id; for delitem-from-NameID the adapter scans the
// loaded set, drains stacks in id order, and rejects if the total count is
// short. This mirrors rAthena's delitem returning 0 unless the entire amount
// was removed.
func (a *scriptInventoryAdapter) DelItem(charID uint32, nameID uint32, amount int) bool {
	if amount <= 0 {
		return false
	}
	ctx := context.Background()
	accountID, err := a.accountFor(ctx, charID)
	if err != nil {
		return false
	}
	items, err := a.inv.LoadByChar(ctx, accountID, charID)
	if err != nil {
		return false
	}
	var total int
	for _, it := range items {
		if it.NameID != nameID {
			continue
		}
		total += int(it.Amount)
	}
	if total < amount {
		return false
	}
	// Drain stacks in id order (the same order LoadByChar returns, so the
	// drain is deterministic). Stop as soon as the requested amount is gone.
	remaining := amount
	for _, it := range items {
		if remaining <= 0 {
			break
		}
		if it.NameID != nameID {
			continue
		}
		take := int(it.Amount)
		if take > remaining {
			take = remaining
		}
		if err := a.inv.Remove(ctx, it.ID, take); err != nil {
			// Best-effort: a partial drain failure leaves the bag in a
			// recoverable state (each Remove is itself atomic), but the
			// all-or-nothing contract is broken — report false.
			return false
		}
		remaining -= take
	}
	return remaining == 0
}

// CountItem returns the player's total count of nameID across all rows.
func (a *scriptInventoryAdapter) CountItem(charID uint32, nameID uint32) int {
	ctx := context.Background()
	accountID, err := a.accountFor(ctx, charID)
	if err != nil {
		return 0
	}
	items, err := a.inv.LoadByChar(ctx, accountID, charID)
	if err != nil {
		return 0
	}
	var total int
	for _, it := range items {
		if it.NameID == nameID {
			total += int(it.Amount)
		}
	}
	return total
}

// Equip wears the item at the given LoadByChar index into the slot bitmask.
// The adapter delegates to EquipService for slot/location validation.
func (a *scriptInventoryAdapter) Equip(charID uint32, index int, slot uint32) bool {
	ctx := context.Background()
	accountID, err := a.accountFor(ctx, charID)
	if err != nil {
		return false
	}
	if err := a.equip.Equip(ctx, accountID, charID, index, slot); err != nil {
		return false
	}
	return true
}

// Unequip clears the equip bitmask of the item at the given index.
func (a *scriptInventoryAdapter) Unequip(charID uint32, index int) bool {
	ctx := context.Background()
	accountID, err := a.accountFor(ctx, charID)
	if err != nil {
		return false
	}
	if err := a.equip.Unequip(ctx, accountID, charID, index); err != nil {
		return false
	}
	return true
}

// compile-time check: the adapter satisfies the content port.
var _ contentdomain.ScriptInventory = (*scriptInventoryAdapter)(nil)
