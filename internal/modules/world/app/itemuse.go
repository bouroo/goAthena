package app

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"

	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
)

// itemUseInventory is the narrow inventory capability ItemUseService needs:
// loading a character's items and consuming one unit of a row. The production
// *invapp.InventoryService satisfies it; tests inject a fake.
type itemUseInventory interface {
	LoadByChar(ctx context.Context, accountID, charID uint32) ([]invdomain.Item, error)
	Remove(ctx context.Context, id invdomain.ItemID, amount int) error
}

// vitals is the narrow world capability ItemUseService needs: applying a flat
// HP/SP delta to a player. The production *WorldService satisfies it via
// AddVitals; tests inject a fake.
type vitals interface {
	AddVitals(charID uint32, hp, sp int32) (hpAfter, spAfter int32, err error)
}

// ItemUseService runs the usable-item (potion) use case: consume one unit of an
// item at a 1-based inventory index and apply its effects. Healing items
// (itemheal script) restore flat HP/SP through the vitals port; non-healing
// usable items are consumed but apply no vitals here (buffs/status are deferred).
// Item state lives on the inventory rows; the item_db Heal() ranges supply the
// restore amounts.
type ItemUseService struct {
	inv   itemUseInventory
	items *itemdb.Registry
	world vitals
}

// NewItemUseService builds an ItemUseService backed by the inventory port, the
// loaded item_db registry, and the world vitals port. items may be nil
// (best-effort degradation: every item resolves as unknown), matching the
// boot-time empty-registry fallback in world/di.go.
func NewItemUseService(inv itemUseInventory, items *itemdb.Registry, world vitals) *ItemUseService {
	return &ItemUseService{inv: inv, items: items, world: world}
}

// ItemUseAck reports a use-item outcome for the S→C ack: the resolved item_db id,
// the stack count remaining after consuming one unit (0 if the row was deleted),
// and the HP/SP actually restored (0 for non-healing items). ItemID/Remaining
// feed ZC_USE_ITEM_ACK2; the healed amounts drive the separate ZC_PAR_CHANGE.
type ItemUseAck struct {
	ItemID    uint16
	Remaining uint16
	HealedHP  int32
	HealedSP  int32
}

// Item-use errors are distinct sentinels so the gateway maps each to a specific
// S→C ack result without parsing strings (no branch on error strings).
var (
	// ErrItemUseNotFound means the 1-based inventory index is out of range.
	ErrItemUseNotFound = errors.New("itemuse: inventory index out of range")
	// ErrNotUsable means the item is not a usable/healing type (the client
	// should not send CZ_USE_ITEM for it, but this rejects it defensively).
	ErrNotUsable = errors.New("itemuse: item is not usable")
)

// Use consumes one unit of the item at the 1-based inventory index invIndex and
// applies its effects.
//
// Healing items (itemheal script) restore a rolled [hpMin,hpMax]/[spMin,spMax]
// amount through the vitals port — deterministic for fixed-range potions where
// min==max. Non-healing usable items are consumed but apply no vitals here.
//
// Consume-before-heal order: the item is removed first (inv.Remove), then the
// vitals are applied. A zero/missing stack surfaces ErrInsufficientAmount /
// ErrItemNotFound before any heal; an AddVitals failure after the remove loses
// the unit — acceptable since heal failures are rare and the alternative
// (heal-then-remove) can duplicate the item on a mid-way crash.
func (s *ItemUseService) Use(ctx context.Context, accountID, charID uint32, invIndex int) (ItemUseAck, error) {
	items, err := s.inv.LoadByChar(ctx, accountID, charID)
	if err != nil {
		return ItemUseAck{}, fmt.Errorf("itemuse: load inventory: %w", err)
	}
	if invIndex < 1 || invIndex > len(items) {
		return ItemUseAck{}, ErrItemUseNotFound
	}
	target := items[invIndex-1]
	entry := s.itemEntry(target.NameID)
	if !isUsable(entry) {
		return ItemUseAck{}, ErrNotUsable
	}

	// Consume one unit first (atomic against stack depletion): a zero/missing
	// stack surfaces a typed error before any heal is applied.
	if err := s.inv.Remove(ctx, target.ID, 1); err != nil {
		return ItemUseAck{}, fmt.Errorf("itemuse: consume item %d: %w", target.ID, err)
	}

	var ack ItemUseAck
	if hpMin, hpMax, spMin, spMax, healOK := entry.Heal(); healOK {
		ack.HealedHP = rollRange(hpMin, hpMax)
		ack.HealedSP = rollRange(spMin, spMax)
	}
	// Apply vitals only when something actually changes; a (0,0) heal (or a
	// non-healing usable item) skips AddVitals so no redundant ZC_PAR_CHANGE
	// is emitted. A failure here loses the consumed unit — see the doc comment.
	if ack.HealedHP != 0 || ack.HealedSP != 0 {
		if _, _, err := s.world.AddVitals(charID, ack.HealedHP, ack.HealedSP); err != nil {
			return ack, fmt.Errorf("itemuse: apply vitals: %w", err)
		}
	}

	ack.ItemID = uint16(target.NameID)            //nolint:gosec // G115: usable item_db ids fit uint16 (< 2^16).
	ack.Remaining = remainingStack(target.Amount) //nolint:gosec // G115: uint32->uint16 stack count, small.
	return ack, nil
}

// itemEntry resolves the item_db entry for a NameID, tolerating a nil registry
// and an out-of-range id (both resolve to nil = unknown item).
func (s *ItemUseService) itemEntry(nameID uint32) *itemdb.ItemEntry {
	if s.items == nil || nameID > maxItemID {
		return nil
	}
	return s.items.Get(int32(nameID)) //nolint:gosec // G115: guarded <= maxInt31
}

// isUsable reports whether entry is a consumable type (the kinds the client can
// right-click to use): Healing/Usable/DelayConsume. A nil entry (unknown nameid)
// is not usable. Mirrors itemdb's IT_HEALING / IT_USABLE taxonomy.
func isUsable(entry *itemdb.ItemEntry) bool {
	if entry == nil {
		return false
	}
	switch strings.ToLower(entry.Type) {
	case "healing", "usable", "delayconsume":
		return true
	default:
		return false
	}
}

// rollRange returns a value in the inclusive [lo,hi] range; lo==hi is the
// deterministic fixed-amount case (e.g. Red Potion's parsed 45 HP). A degenerate
// hi<lo yields lo. Game RNG, not security-sensitive.
func rollRange(lo, hi int32) int32 {
	if hi <= lo {
		return lo
	}
	return lo + int32(rand.Intn(int(hi-lo+1))) //nolint:gosec // G115: bounded to [lo,hi]; small heal ranges.
}

// remainingStack returns the stack count after consuming one unit, floored at 0:
// a row whose stack hit zero (deleted by Remove) reports 0, the client-visible
// remaining count.
func remainingStack(amount uint32) uint16 {
	if amount <= 1 {
		return 0
	}
	return uint16(amount - 1) //nolint:gosec // G115: amount > 1; small inventory stacks.
}
