//go:build unit

package app

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
	"github.com/bouroo/goAthena/pkg/ro/statcalc"
)

// fakeBag is a minimal InventoryRepository stand-in whose LoadByChar returns a
// fixed bag; the mutating methods are unused by the LoadEndAck init path. It
// keeps the test free of an inventory/infra import.
type fakeBag struct{ rows []invdomain.InventoryItem }

func (f *fakeBag) LoadByChar(context.Context, uint32, uint32) ([]invdomain.InventoryItem, error) {
	return f.rows, nil
}

func (f *fakeBag) AddItem(context.Context, uint32, uint32, invdomain.NewItem) (invdomain.InventoryItem, error) {
	return invdomain.InventoryItem{}, nil
}

func (f *fakeBag) EquipItem(context.Context, uint32, uint32, uint16, uint32) (invdomain.InventoryItem, error) {
	return invdomain.InventoryItem{}, nil
}

func (f *fakeBag) UnequipItem(context.Context, uint32, uint32, uint16) (invdomain.InventoryItem, error) {
	return invdomain.InventoryItem{}, nil
}

func (f *fakeBag) ConsumeItem(context.Context, uint32, uint32, uint16, uint16) (invdomain.InventoryItem, bool, error) {
	return invdomain.InventoryItem{}, false, nil
}

// TestSpawnService_inventoryLists_SplitsByType proves the LoadEndAck bag list
// routes equipment (Weapon/Armor) to ZC_INVENTORY_ITEMLIST_EQUIP and everything
// else (Healing/Etc/Card/…) to ZC_INVENTORY_ITEMLIST_NORMAL, resolving type and
// view from item_db. A Knife (1201, weapon) lands in equip with its view; a Red
// Potion (501, healing) lands in normal with its stack count.
func TestSpawnService_inventoryLists_SplitsByType(t *testing.T) {
	t.Parallel()
	reg, err := itemdb.Load(strings.NewReader(`Header:
  Type: ITEM_DB
  Version: 3
Body:
  - Id: 1201
    AegisName: Knife
    Type: Weapon
    View: 1
  - Id: 501
    AegisName: Red_Potion
    Type: Healing
`))
	require.NoError(t, err)

	players := domain.NewPlayerRegistry()
	require.NoError(t, players.Register(&domain.Player{AccountID: 1, CharID: 7}))

	svc := NewSpawnService(nil, nil, players, domain.NewMobRegistry(), domain.NewNPCRegistry(), statcalc.PreRenewalSet,
		&fakeBag{rows: []invdomain.InventoryItem{
			{Index: 0, NameID: 1201, Amount: 1, Identified: true},
			{Index: 1, NameID: 501, Amount: 5, Identified: true},
		}}, reg)

	normal, equip := svc.inventoryLists(context.Background(), 1)
	require.Len(t, normal, 1)
	require.Len(t, equip, 1)
	assert.Equal(t, uint16(501), normal[0].ITID, "Red Potion is a normal-list item")
	assert.Equal(t, uint8(0), normal[0].Type, "healing IT type byte")
	assert.Equal(t, uint16(5), normal[0].Count, "stack count carried")
	assert.Equal(t, uint8(1), normal[0].Flag, "identified flag bit 0 set")
	assert.Equal(t, uint16(1201), equip[0].ITID, "Knife is an equip-list item")
	assert.Equal(t, uint8(5), equip[0].Type, "weapon IT type byte")
	assert.Equal(t, uint16(1), equip[0].ItemSpriteNumber, "view sprite resolved from item_db")
}

// TestSpawnService_inventoryLists_DegradesEmpty covers the nil-contract: with no
// inventory store the split yields empty slices so SendLoadEndAckInit emits the
// well-formed empty lists rather than faulting. A registered player with a nil
// repo exercises the items==nil guard specifically.
func TestSpawnService_inventoryLists_DegradesEmpty(t *testing.T) {
	t.Parallel()
	players := domain.NewPlayerRegistry()
	require.NoError(t, players.Register(&domain.Player{AccountID: 1, CharID: 7}))
	svc := NewSpawnService(nil, nil, players, domain.NewMobRegistry(), domain.NewNPCRegistry(), statcalc.PreRenewalSet, nil, nil)
	normal, equip := svc.inventoryLists(context.Background(), 1)
	assert.Empty(t, normal)
	assert.Empty(t, equip)
}
