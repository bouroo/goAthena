//go:build unit

package app

import (
	"context"
	"testing"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	invapp "github.com/bouroo/goAthena/internal/modules/inventory/app"
	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	"github.com/bouroo/goAthena/pkg/ro/statcalc"
)

// fakeCharRepo is the minimum character repository surface the script
// inventory adapter needs: only FindByID (for accountID resolution).
type fakeCharRepo struct {
	chars map[chardomain.CharID]chardomain.Character
}

func (f *fakeCharRepo) FindByID(_ context.Context, id chardomain.CharID) (chardomain.Character, error) {
	c, ok := f.chars[id]
	if !ok {
		return chardomain.Character{}, chardomain.ErrCharacterNotFound
	}
	return c, nil
}

// memoryInvRepo is the minimum inventory surface the adapter needs:
// LoadByChar + Add + Remove. Implemented inline so the test stays in this
// file. The adapter does not use EquipService's heavier path (slot
// validation, conflict resolution) — it delegates to a real *EquipService
// the world DI builds, but the test path wires a fake that always succeeds.
type memoryInvRepo struct {
	inv   *invapp.InventoryService
	store *fakeInvStore
}

// fakeInvStore is the in-memory ItemRepository the test plugs in.
type fakeInvStore struct {
	rows map[invdomain.ItemID]invdomain.Item
	next invdomain.ItemID
}

func newFakeInvStore() *fakeInvStore {
	return &fakeInvStore{rows: map[invdomain.ItemID]invdomain.Item{}}
}

func (s *fakeInvStore) LoadByChar(_ context.Context, _ uint32, charID uint32) ([]invdomain.Item, error) {
	var out []invdomain.Item
	for _, it := range s.rows {
		if it.CharID == charID {
			out = append(out, it)
		}
	}
	return out, nil
}

func (s *fakeInvStore) Add(_ context.Context, charID, nameID uint32, amount int) (invdomain.Item, error) {
	s.next++
	it := invdomain.Item{ID: s.next, CharID: charID, NameID: nameID, Amount: uint32(amount), Identify: 1}
	s.rows[it.ID] = it
	return it, nil
}

func (s *fakeInvStore) Remove(_ context.Context, id invdomain.ItemID, amount int) error {
	it, ok := s.rows[id]
	if !ok {
		return invdomain.ErrItemNotFound
	}
	if int(it.Amount) < amount {
		return invdomain.ErrInsufficientAmount
	}
	if int(it.Amount) == amount {
		delete(s.rows, id)
		return nil
	}
	it.Amount -= uint32(amount)
	s.rows[id] = it
	return nil
}

func (s *fakeInvStore) SetEquip(_ context.Context, id invdomain.ItemID, equip uint32) error {
	it, ok := s.rows[id]
	if !ok {
		return invdomain.ErrItemNotFound
	}
	it.Equip = equip
	s.rows[id] = it
	return nil
}

// fakeEquipSvc implements EquipService's contract for the adapter. The
// adapter only calls Equip and Unequip, so the fake records those.
type fakeEquipSvc struct {
	equipped   []equipCall
	unequipped []int
	err        error
}

type equipCall struct {
	charID uint32
	index  int
	slot   uint32
}

func (f *fakeEquipSvc) Equip(_ context.Context, _ uint32, charID uint32, index int, slot uint32) error {
	if f.err != nil {
		return f.err
	}
	f.equipped = append(f.equipped, equipCall{charID, index, slot})
	return nil
}

func (f *fakeEquipSvc) Unequip(_ context.Context, _ uint32, charID uint32, index int) error {
	if f.err != nil {
		return f.err
	}
	f.unequipped = append(f.unequipped, index)
	_ = charID
	return nil
}

func (f *fakeEquipSvc) EquipmentProfile(_ context.Context, _ uint32, _ uint32) (statcalc.Equipment, error) {
	return statcalc.Equipment{}, nil
}

// TestScriptInvAdapterGetItemDelItemCountItem: the inventory mutation round
// trip — getitem grants, countitem reports, delitem removes all-or-nothing.
func TestScriptInvAdapterGetItemDelItemCountItem(t *testing.T) {
	chars := &fakeCharRepo{chars: map[chardomain.CharID]chardomain.Character{
		42: {ID: 42, AccountID: 100, Zeny: 1000},
	}}
	store := newFakeInvStore()
	inv := invapp.NewInventoryService(store)
	equip := &fakeEquipSvc{}
	adapter := NewScriptInventoryAdapter(inv, equip, chars).(*scriptInventoryAdapter)

	if !adapter.GetItem(42, 501, 5) {
		t.Fatal("GetItem: want true on first grant")
	}
	if got := adapter.CountItem(42, 501); got != 5 {
		t.Errorf("CountItem after grant = %d, want 5", got)
	}
	if !adapter.DelItem(42, 501, 3) {
		t.Fatal("DelItem 3: want true")
	}
	if got := adapter.CountItem(42, 501); got != 2 {
		t.Errorf("CountItem after partial del = %d, want 2", got)
	}
}

// TestScriptInvAdapterDelItemRejectsShortStack: the all-or-nothing contract —
// a request exceeding the held amount returns false without partial drain.
func TestScriptInvAdapterDelItemRejectsShortStack(t *testing.T) {
	chars := &fakeCharRepo{chars: map[chardomain.CharID]chardomain.Character{
		1: {ID: 1, AccountID: 1, Zeny: 0},
	}}
	store := newFakeInvStore()
	inv := invapp.NewInventoryService(store)
	adapter := NewScriptInventoryAdapter(inv, &fakeEquipSvc{}, chars).(*scriptInventoryAdapter)

	if !adapter.GetItem(1, 501, 2) {
		t.Fatal("GetItem: want true")
	}
	if adapter.DelItem(1, 501, 5) {
		t.Error("DelItem 5 of 2 held: want false (all-or-nothing)")
	}
	// Held amount is still 2 (the rejection did not partial-drain).
	if got := adapter.CountItem(1, 501); got != 2 {
		t.Errorf("CountItem after rejected overdraw = %d, want 2", got)
	}
}

// TestScriptInvAdapterEquipRoutes: equip / unequip go to the equip service
// with the right slot/index. The script-side adapter is a thin shim; this
// test pins the call shape so refactors don't drift.
func TestScriptInvAdapterEquipRoutes(t *testing.T) {
	chars := &fakeCharRepo{chars: map[chardomain.CharID]chardomain.Character{
		7: {ID: 7, AccountID: 7, Zeny: 0},
	}}
	store := newFakeInvStore()
	inv := invapp.NewInventoryService(store)
	equipSvc := &fakeEquipSvc{}
	adapter := NewScriptInventoryAdapter(inv, equipSvc, chars).(*scriptInventoryAdapter)

	if !adapter.Equip(7, 3, 2) {
		t.Fatal("Equip: want true")
	}
	if len(equipSvc.equipped) != 1 || equipSvc.equipped[0] != (equipCall{7, 3, 2}) {
		t.Errorf("equipped = %v, want [{7 3 2}]", equipSvc.equipped)
	}
	if !adapter.Unequip(7, 3) {
		t.Fatal("Unequip: want true")
	}
	if len(equipSvc.unequipped) != 1 || equipSvc.unequipped[0] != 3 {
		t.Errorf("unequipped = %v, want [3]", equipSvc.unequipped)
	}
}

// TestScriptInvAdapterResolvesAccountID: accountID lookup is the bridge
// between script charID and the legacy schema's accountID-keyed inventory
// query. A not-found char returns false on every op (the safe default).
func TestScriptInvAdapterResolvesAccountID(t *testing.T) {
	chars := &fakeCharRepo{chars: map[chardomain.CharID]chardomain.Character{
		1: {ID: 1, AccountID: 100},
	}}
	store := newFakeInvStore()
	inv := invapp.NewInventoryService(store)
	adapter := NewScriptInventoryAdapter(inv, &fakeEquipSvc{}, chars).(*scriptInventoryAdapter)

	if adapter.GetItem(999, 501, 1) {
		t.Error("GetItem on unknown char: want false")
	}
	if got := adapter.CountItem(999, 501); got != 0 {
		t.Errorf("CountItem on unknown char = %d, want 0", got)
	}
	if adapter.Equip(999, 0, 1) {
		t.Error("Equip on unknown char: want false")
	}
}
