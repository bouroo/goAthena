//go:build unit

package app_test

import (
	"context"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/modules/inventory/domain"
	worldapp "github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/pkg/ro/equip"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
)

// equipYAML seeds a tiny item_db: a Dagger weapon (Attack 20, right hand), a
// Cotton Shirt armor (Defense 10, armor slot), and a Jellopy Etc item (no equip
// locations). Locations resolve to the EQP_* bitmask the EquipService validates.
const equipYAML = `Header:
  Type: ITEM_DB
  Version: 3

Body:
  - Id: 1101
    AegisName: Knife
    Name: Knife
    Type: Weapon
    SubType: Dagger
    Attack: 20
    Locations:
      right_hand: true
  - Id: 2301
    AegisName: Cotton_Shirt
    Name: Cotton Shirt
    Type: Armor
    Defense: 10
    Locations:
      armor: true
  - Id: 909
    AegisName: Jellopy
    Name: Jellopy
    Type: Etc
`

// equipFakeInv is the minimal inventory port the EquipService consumes. It stores rows
// by id, serves them sorted by id (so the 1-based invIndex is deterministic, like
// the GORM repo's ORDER BY id), and persists SetEquip in place.
type equipFakeInv struct {
	mu    sync.Mutex
	items map[domain.ItemID]domain.Item
	next  uint32
}

func newEquipFakeInv() *equipFakeInv {
	return &equipFakeInv{items: make(map[domain.ItemID]domain.Item)}
}

// seed inserts a row, assigning the next autoincrement id when unset.
func (f *equipFakeInv) seed(it domain.Item) domain.Item {
	f.mu.Lock()
	defer f.mu.Unlock()
	if it.ID == 0 {
		f.next++
		it.ID = domain.ItemID(f.next)
	}
	f.items[it.ID] = it
	return it
}

func (f *equipFakeInv) LoadByChar(_ context.Context, _, charID uint32) ([]domain.Item, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]domain.Item, 0, len(f.items))
	for _, it := range f.items {
		if it.CharID == charID {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *equipFakeInv) SetEquip(_ context.Context, id domain.ItemID, equip uint32) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	it, ok := f.items[id]
	if !ok {
		return domain.ErrItemNotFound
	}
	it.Equip = equip
	f.items[id] = it
	return nil
}

func newEquipSvc(t *testing.T) (*worldapp.EquipService, *equipFakeInv) {
	t.Helper()
	reg, err := itemdb.Load(strings.NewReader(equipYAML))
	require.NoError(t, err)
	require.Equal(t, 3, reg.Len(), "item_db fixture must load 3 entries")
	inv := newEquipFakeInv()
	return worldapp.NewEquipService(inv, reg), inv
}

const (
	equipAccID uint32 = 1
	equipChar  uint32 = 150001
)

func TestEquipWeaponAddsWeaponATK(t *testing.T) {
	svc, inv := newEquipSvc(t)
	ctx := context.Background()

	inv.seed(domain.Item{CharID: equipChar, NameID: 1101, Amount: 1, Identify: 1}) // index 1

	require.NoError(t, svc.Equip(ctx, equipAccID, equipChar, 1, equip.HandRight))

	eq, err := svc.EquipmentProfile(ctx, equipAccID, equipChar)
	require.NoError(t, err)
	assert.Equal(t, int32(20), eq.WeaponATK, "equipped Knife contributes Attack 20")
	assert.Equal(t, int32(0), eq.ItemDEF, "no armor worn")

	loaded, err := inv.LoadByChar(ctx, equipAccID, equipChar)
	require.NoError(t, err)
	assert.Equal(t, equip.HandRight, loaded[0].Equip, "row equip bitmask set to requested position")
}

func TestEquipArmorAddsItemDEF(t *testing.T) {
	svc, inv := newEquipSvc(t)
	ctx := context.Background()

	inv.seed(domain.Item{CharID: equipChar, NameID: 2301, Amount: 1, Identify: 1}) // index 1

	require.NoError(t, svc.Equip(ctx, equipAccID, equipChar, 1, equip.Armor))

	eq, err := svc.EquipmentProfile(ctx, equipAccID, equipChar)
	require.NoError(t, err)
	assert.Equal(t, int32(10), eq.ItemDEF, "equipped Cotton Shirt contributes Defense 10")
	assert.Equal(t, int32(0), eq.WeaponATK, "no weapon worn")
	assert.Equal(t, int32(0), eq.ItemMDEF, "item_db has no MDEF column; equipment MDEF is out of scope")
}

func TestEquipWeaponAndArmorStacks(t *testing.T) {
	svc, inv := newEquipSvc(t)
	ctx := context.Background()

	inv.seed(domain.Item{CharID: equipChar, NameID: 1101, Amount: 1, Identify: 1}) // index 1 weapon
	inv.seed(domain.Item{CharID: equipChar, NameID: 2301, Amount: 1, Identify: 1}) // index 2 armor

	require.NoError(t, svc.Equip(ctx, equipAccID, equipChar, 1, equip.HandRight))
	require.NoError(t, svc.Equip(ctx, equipAccID, equipChar, 2, equip.Armor))

	eq, err := svc.EquipmentProfile(ctx, equipAccID, equipChar)
	require.NoError(t, err)
	assert.Equal(t, int32(20), eq.WeaponATK)
	assert.Equal(t, int32(10), eq.ItemDEF)
}

func TestEquipNonEquippableItem(t *testing.T) {
	svc, inv := newEquipSvc(t)
	ctx := context.Background()

	inv.seed(domain.Item{CharID: equipChar, NameID: 909, Amount: 1}) // Jellopy (Etc, no locations)

	err := svc.Equip(ctx, equipAccID, equipChar, 1, equip.Armor)
	assert.ErrorIs(t, err, worldapp.ErrNotEquippable)
}

func TestEquipWrongSlot(t *testing.T) {
	svc, inv := newEquipSvc(t)
	ctx := context.Background()

	inv.seed(domain.Item{CharID: equipChar, NameID: 1101, Amount: 1}) // Knife (right_hand only)

	// A weapon cannot go in the armor slot.
	err := svc.Equip(ctx, equipAccID, equipChar, 1, equip.Armor)
	assert.ErrorIs(t, err, worldapp.ErrWrongSlot)
}

func TestEquipSlotConflictUnequipsFirst(t *testing.T) {
	svc, inv := newEquipSvc(t)
	ctx := context.Background()

	inv.seed(domain.Item{CharID: equipChar, NameID: 1101, Amount: 1}) // index 1
	inv.seed(domain.Item{CharID: equipChar, NameID: 1101, Amount: 1}) // index 2 (second Knife)

	require.NoError(t, svc.Equip(ctx, equipAccID, equipChar, 1, equip.HandRight))
	// Equip the second Knife into the same slot: the first must be unequipped.
	require.NoError(t, svc.Equip(ctx, equipAccID, equipChar, 2, equip.HandRight))

	loaded, err := inv.LoadByChar(ctx, equipAccID, equipChar)
	require.NoError(t, err)
	require.Len(t, loaded, 2)
	assert.Equal(t, uint32(0), loaded[0].Equip, "first Knife unequipped on conflict")
	assert.Equal(t, equip.HandRight, loaded[1].Equip, "second Knife now worn")

	eq, err := svc.EquipmentProfile(ctx, equipAccID, equipChar)
	require.NoError(t, err)
	assert.Equal(t, int32(20), eq.WeaponATK, "only one weapon counts after the swap")
}

func TestUnequipClearsSlot(t *testing.T) {
	svc, inv := newEquipSvc(t)
	ctx := context.Background()

	inv.seed(domain.Item{CharID: equipChar, NameID: 1101, Amount: 1}) // index 1
	require.NoError(t, svc.Equip(ctx, equipAccID, equipChar, 1, equip.HandRight))

	require.NoError(t, svc.Unequip(ctx, equipAccID, equipChar, 1))

	eq, err := svc.EquipmentProfile(ctx, equipAccID, equipChar)
	require.NoError(t, err)
	assert.Equal(t, int32(0), eq.WeaponATK, "unequipping drops the weapon contribution")

	loaded, err := inv.LoadByChar(ctx, equipAccID, equipChar)
	require.NoError(t, err)
	assert.Equal(t, uint32(0), loaded[0].Equip)
}

func TestEquipIndexOutOfRange(t *testing.T) {
	svc, inv := newEquipSvc(t)
	ctx := context.Background()

	inv.seed(domain.Item{CharID: equipChar, NameID: 1101, Amount: 1})

	err := svc.Equip(ctx, equipAccID, equipChar, 5, equip.HandRight)
	assert.ErrorIs(t, err, worldapp.ErrItemNotFound)

	err = svc.Unequip(ctx, equipAccID, equipChar, 5)
	assert.ErrorIs(t, err, worldapp.ErrItemNotFound)
}
