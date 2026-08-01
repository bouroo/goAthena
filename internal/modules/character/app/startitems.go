package app

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
)

// startItemMaxAmount is rAthena's MAX_AMOUNT (30000), the per-stack ceiling
// char.cpp:2896 clamps each start_items amount to with min(amount, MAX_AMOUNT).
const startItemMaxAmount = 30000

// StartItem is one parsed entry of the rAthena char_athena.conf start_items
// string. NameID is the item id, Amount the stack size, and Equip the EQP_*
// worn-location bitmask — 0 leaves the item loose in the bag, nonzero has it
// created already-equipped in that slot (char.cpp:1519 inserts equip=location).
type StartItem struct {
	NameID uint32
	Amount uint32
	Equip  uint32
}

// ParseStartItems parses the rAthena start_items format: colon-separated
// "nameid,amount,location" triples such as "1201,1,2:2301,1,16:23484,1,0". A
// blank string yields no items (seeding disabled). Empty segments — a leading,
// trailing, or doubled colon — are tolerated rather than rejected, matching
// rAthena's field-split loop which skips a final empty token. Amount is clamped
// to MAX_AMOUNT exactly as char.cpp:2896 does; a zero amount is rejected because
// an inventory row with no count is meaningless.
func ParseStartItems(raw string) ([]StartItem, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var items []StartItem
	for i, entry := range strings.Split(raw, ":") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		fields := strings.Split(entry, ",")
		if len(fields) != 3 {
			return nil, fmt.Errorf("start_items entry %d %q: want nameid,amount,location", i+1, entry)
		}
		nameID, err := parseStartItemField(fields[0], "nameid", i+1)
		if err != nil {
			return nil, err
		}
		amount, err := parseStartItemField(fields[1], "amount", i+1)
		if err != nil {
			return nil, err
		}
		if amount == 0 {
			return nil, fmt.Errorf("start_items entry %d: amount must be > 0", i+1)
		}
		equip, err := parseStartItemField(fields[2], "location", i+1)
		if err != nil {
			return nil, err
		}
		items = append(items, StartItem{
			NameID: nameID,
			Amount: min(amount, startItemMaxAmount),
			Equip:  equip,
		})
	}
	return items, nil
}

// parseStartItemField trims and base-10 parses one start_items field, wrapping
// the parse error with the 1-based entry index and field name for config
// diagnosis.
func parseStartItemField(field, name string, entry int) (uint32, error) {
	v, err := strconv.ParseUint(strings.TrimSpace(field), 10, 32) //nolint:gosec // G115: uint32 width is the parse bound
	if err != nil {
		return 0, fmt.Errorf("start_items entry %d %s: %w", entry, name, err)
	}
	return uint32(v), nil
}

// StartingItemSeeder seeds a newly created character's bag with the configured
// start_items. It owns the parsed item list and the inventory repository port;
// the CH_MAKE_CHAR handler runs it right after the character row is committed.
type StartingItemSeeder struct {
	items []StartItem
	inv   invdomain.InventoryRepository
}

// NewStartingItemSeeder binds a parsed start_items list to the inventory port.
// An empty list yields a seeder whose Seed is a no-op, so start_items can be
// disabled by clearing the config without touching the handler wiring.
func NewStartingItemSeeder(items []StartItem, inv invdomain.InventoryRepository) *StartingItemSeeder {
	return &StartingItemSeeder{items: items, inv: inv}
}

// Len reports how many start items are configured; the handler uses it only to
// skip construction when none are configured.
func (s *StartingItemSeeder) Len() int { return len(s.items) }

// Seed inserts each configured start item into the new character's bag and, for
// entries with a nonzero location, assigns the worn-slot bitmask — one
// inventory row per entry with equip=location, exactly as rAthena's makechar
// does (char.cpp:1518-1519). Stackable is false for every entry because
// makechar never merges starting items: each configured triple is its own row.
// accountID and charID come from the just-created character aggregate (the
// conn-auth-sourced owner), never the packet. A failure on any item returns the
// wrapped error; items inserted before it stay (the character row is already
// committed, so partial seeding is preferable to rolling the character back).
func (s *StartingItemSeeder) Seed(ctx context.Context, accountID, charID uint32) error {
	for _, it := range s.items {
		row, err := s.inv.AddItem(ctx, accountID, charID, invdomain.NewItem{
			NameID:    it.NameID,
			Amount:    it.Amount,
			Stackable: false,
		})
		if err != nil {
			return fmt.Errorf("start_items: add nameid %d to char %d: %w", it.NameID, charID, err)
		}
		if it.Equip == 0 {
			continue
		}
		if _, err := s.inv.EquipItem(ctx, accountID, charID, row.Index, it.Equip); err != nil {
			return fmt.Errorf("start_items: equip nameid %d on char %d: %w", it.NameID, charID, err)
		}
	}
	return nil
}
