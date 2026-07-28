package infra

import (
	"context"
	"slices"
	"sort"

	"github.com/bouroo/goAthena/internal/modules/inventory/domain"
)

// MemoryInventoryRepository is an in-memory domain.InventoryRepository for
// hermetic unit tests and the world use-case fakes. It mirrors the GORM adapter's
// merge/insert/full semantics so a test against the memory twin proves the same
// contract the production adapter honors. Rows are keyed by their stable (char,
// id) slot; the grid Index is the id-ascending position, recomputed on every
// access so it matches LoadByChar.
type MemoryInventoryRepository struct {
	items []domain.InventoryItem
	next  uint32
}

// NewMemoryInventoryRepository seeds the store with copies of the given items and
// allocates row ids continuing past the largest seeded id (so a seeded row keeps
// its id and a later AddItem never collides).
func NewMemoryInventoryRepository(seeds ...domain.InventoryItem) *MemoryInventoryRepository {
	r := &MemoryInventoryRepository{next: 1}
	for _, it := range seeds {
		if it.ID >= r.next {
			r.next = it.ID + 1
		}
		r.items = append(r.items, it)
	}
	return r
}

// forChar returns a defensive snapshot of the char's rows ordered by id, so the
// assigned Index (0,1,2,…) matches LoadByChar's id-ascending contract.
func (r *MemoryInventoryRepository) forChar(charID uint32) []domain.InventoryItem {
	var rows []domain.InventoryItem
	for _, it := range r.items {
		if it.CharID == charID {
			rows = append(rows, it)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows
}

// LoadByChar returns defensive copies of the char's bag with id-ascending grid
// indices. A character with no items yields an empty (non-nil) slice.
func (r *MemoryInventoryRepository) LoadByChar(_ context.Context, _, charID uint32) ([]domain.InventoryItem, error) {
	rows := r.forChar(charID)
	out := make([]domain.InventoryItem, len(rows))
	for i, it := range rows {
		cp := it
		cp.Index = uint16(i) //nolint:gosec // G115: index < MaxInventorySlots (100) < uint16
		out[i] = cp
	}
	return out, nil
}

// AddItem mirrors GORMInventoryRepository.AddItem: merge into a stackable match
// (same NameID), else insert a new row; a non-stackable insert at the slot cap
// yields ErrInventoryFull. The returned Index is the matched/inserted row's
// id-ascending position — stable, since pickups only append.
func (r *MemoryInventoryRepository) AddItem(_ context.Context, _, charID uint32, item domain.NewItem) (domain.InventoryItem, error) {
	if item.Stackable {
		for i, it := range r.items {
			if it.CharID == charID && it.NameID == item.NameID {
				r.items[i].Amount += item.Amount
				cp := r.items[i]
				cp.Index = r.indexOf(charID, it.ID)
				return cp, nil
			}
		}
	}
	rows := r.forChar(charID)
	if len(rows) >= domain.MaxInventorySlots {
		return domain.InventoryItem{}, domain.ErrInventoryFull
	}
	row := domain.InventoryItem{
		ID:     r.next,
		CharID: charID,
		Index:  uint16(len(rows)), //nolint:gosec // G115: len < MaxInventorySlots (100) < uint16
		NameID: item.NameID,
		Amount: item.Amount,
	}
	r.next++
	r.items = append(r.items, row)
	return row, nil
}

// indexOf returns the id-ascending position of a row within the char's bag (the
// grid Index LoadByChar would assign it), used so a merge keeps the matched
// row's index.
func (r *MemoryInventoryRepository) indexOf(charID, rowID uint32) uint16 {
	rows := r.forChar(charID)
	return uint16(slices.IndexFunc(rows, func(it domain.InventoryItem) bool { return it.ID == rowID })) //nolint:gosec // G115: pos < MaxInventorySlots (100) < uint16
}

// setEquip assigns the equip bitmask to the bag row occupying the char's grid
// slot `index` and returns the row as it now stands. A slot past the last row
// (forged index, stale UI) yields ErrItemNotFound. The assignment (not OR)
// mirrors rAthena's inventory.u.items_inventory[n].equip = flag; passing 0
// clears it (unequip).
func (r *MemoryInventoryRepository) setEquip(charID uint32, index uint16, equip uint32) (domain.InventoryItem, error) {
	rows := r.forChar(charID)
	if int(index) >= len(rows) { //nolint:gosec // G115: index is a bag slot < 100; len fits int
		return domain.InventoryItem{}, domain.ErrItemNotFound
	}
	target := rows[index]
	i := slices.IndexFunc(r.items, func(it domain.InventoryItem) bool {
		return it.CharID == charID && it.ID == target.ID
	})
	if i < 0 {
		return domain.InventoryItem{}, domain.ErrItemNotFound
	}
	r.items[i].Equip = equip
	cp := r.items[i]
	cp.Index = index
	return cp, nil
}

// EquipItem assigns the worn-location bitmask to the bag row at the given grid
// slot, returning the row as it now stands. A missing slot yields
// ErrItemNotFound.
func (r *MemoryInventoryRepository) EquipItem(_ context.Context, _, charID uint32, index uint16, equip uint32) (domain.InventoryItem, error) {
	return r.setEquip(charID, index, equip)
}

// UnequipItem clears the worn-location bitmask on the bag row at the given grid
// slot, returning the row as it now stands. A missing slot yields
// ErrItemNotFound.
func (r *MemoryInventoryRepository) UnequipItem(_ context.Context, _, charID uint32, index uint16) (domain.InventoryItem, error) {
	return r.setEquip(charID, index, 0)
}
