//go:build unit

package app_test

import (
	"context"
	"log/slog"
	"math"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/modules/inventory/domain"
	worldapp "github.com/bouroo/goAthena/internal/modules/world/app"
	worlddomain "github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/internal/modules/world/infra"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
)

// itemUseYAML seeds a tiny item_db for the use-item tests: a fixed-range healing
// potion (itemheal 45,0 — deterministic, unlike the real Red Potion's rand), a
// non-healing usable item (buff deferred), and a non-usable weapon.
const itemUseYAML = `Header:
  Type: ITEM_DB
  Version: 3

Body:
  - Id: 501
    AegisName: Red_Potion
    Name: Red Potion
    Type: Healing
    Script: "itemheal 45,0;"
  - Id: 601
    AegisName: Fly_Wing
    Name: Fly Wing
    Type: Usable
    Script: ""
  - Id: 1101
    AegisName: Knife
    Name: Knife
    Type: Weapon
    SubType: Dagger
`

const (
	itemUseAccID uint32 = 1
	itemUseChar  uint32 = 150001
)

// itemUseFakeInv is a thread-safe in-memory inventory for the use-item tests. It
// mirrors the real repository's Remove semantics (decrement; delete at zero;
// ErrInsufficientAmount / ErrItemNotFound) so the service's consume path is
// exercised faithfully.
type itemUseFakeInv struct {
	mu    sync.Mutex
	items map[domain.ItemID]domain.Item
}

func newItemUseFakeInv(seed ...domain.Item) *itemUseFakeInv {
	f := &itemUseFakeInv{items: map[domain.ItemID]domain.Item{}}
	for i, it := range seed {
		if it.ID == 0 {
			it.ID = domain.ItemID(100 + i + 1)
		}
		f.items[it.ID] = it
	}
	return f
}

func (f *itemUseFakeInv) LoadByChar(_ context.Context, _, charID uint32) ([]domain.Item, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := make([]domain.ItemID, 0, len(f.items))
	for id := range f.items {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]domain.Item, 0, len(ids))
	for _, id := range ids {
		if it := f.items[id]; it.CharID == charID {
			out = append(out, it)
		}
	}
	return out, nil
}

func (f *itemUseFakeInv) Remove(_ context.Context, id domain.ItemID, amount int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	it, ok := f.items[id]
	if !ok {
		return domain.ErrItemNotFound
	}
	if amount <= 0 || uint32(amount) > it.Amount { //nolint:gosec // G115: amount guarded > 0; small consume counts.
		return domain.ErrInsufficientAmount
	}
	it.Amount -= uint32(amount) //nolint:gosec // G115: amount guarded > 0; small consume counts.
	if it.Amount == 0 {
		delete(f.items, id)
	} else {
		f.items[id] = it
	}
	return nil
}

func (f *itemUseFakeInv) amount(id domain.ItemID) (uint32, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	it, ok := f.items[id]
	if !ok {
		return 0, false
	}
	return it.Amount, true
}

// itemUseFakeVitals records AddVitals calls so tests assert the heal amounts the
// service passed through (without coupling to WorldService internals).
type itemUseFakeVitals struct {
	mu    sync.Mutex
	calls []itemUseVitalsCall
}

type itemUseVitalsCall struct {
	charID uint32
	hp, sp int32
}

func (f *itemUseFakeVitals) AddVitals(charID uint32, hp, sp int32) (int32, int32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, itemUseVitalsCall{charID, hp, sp})
	return 0, 0, nil
}

func (f *itemUseFakeVitals) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// newItemUseSvc builds an ItemUseService over the fixture item_db and returns it
// with its fake inventory and vitals for assertion.
func newItemUseSvc(t *testing.T) (*worldapp.ItemUseService, *itemUseFakeInv, *itemUseFakeVitals) {
	t.Helper()
	reg, err := itemdb.Load(strings.NewReader(itemUseYAML))
	require.NoError(t, err)
	require.Equal(t, 3, reg.Len(), "item_db fixture must load 3 entries")
	inv := newItemUseFakeInv()
	vit := &itemUseFakeVitals{}
	return worldapp.NewItemUseService(inv, reg, vit), inv, vit
}

func TestItemUseService_HealPotion(t *testing.T) {
	svc, inv, vit := newItemUseSvc(t)
	ctx := context.Background()

	inv.items[domain.ItemID(101)] = domain.Item{
		ID: 101, CharID: itemUseChar, NameID: 501, Amount: 5,
	}

	ack, err := svc.Use(ctx, itemUseAccID, itemUseChar, 1)
	require.NoError(t, err)
	assert.Equal(t, uint16(501), ack.ItemID)
	assert.Equal(t, uint16(4), ack.Remaining, "stack 5 -> 4 after one use")
	assert.Equal(t, int32(45), ack.HealedHP)
	assert.Equal(t, int32(0), ack.HealedSP)

	require.Len(t, vit.calls, 1, "AddVitals invoked once with the heal delta")
	assert.Equal(t, itemUseChar, vit.calls[0].charID)
	assert.Equal(t, int32(45), vit.calls[0].hp)
	assert.Equal(t, int32(0), vit.calls[0].sp)

	amt, ok := inv.amount(101)
	require.True(t, ok)
	assert.Equal(t, uint32(4), amt, "inventory row decremented")
}

func TestItemUseService_HealPotion_LastUnitDeleted(t *testing.T) {
	svc, inv, vit := newItemUseSvc(t)
	ctx := context.Background()

	inv.items[domain.ItemID(101)] = domain.Item{
		ID: 101, CharID: itemUseChar, NameID: 501, Amount: 1,
	}

	ack, err := svc.Use(ctx, itemUseAccID, itemUseChar, 1)
	require.NoError(t, err)
	assert.Equal(t, uint16(0), ack.Remaining, "deleted stack reports 0 remaining")
	require.Len(t, vit.calls, 1)
	_, ok := inv.amount(101)
	assert.False(t, ok, "row deleted when stack hit zero")
}

func TestItemUseService_NonHealUsable_ConsumedNoVitals(t *testing.T) {
	svc, inv, vit := newItemUseSvc(t)
	ctx := context.Background()

	inv.items[domain.ItemID(101)] = domain.Item{
		ID: 101, CharID: itemUseChar, NameID: 601, Amount: 3, // Fly Wing: usable, no itemheal
	}

	ack, err := svc.Use(ctx, itemUseAccID, itemUseChar, 1)
	require.NoError(t, err)
	assert.Equal(t, uint16(601), ack.ItemID)
	assert.Equal(t, uint16(2), ack.Remaining)
	assert.Equal(t, int32(0), ack.HealedHP, "non-healing item restores nothing")
	assert.Equal(t, 0, vit.callCount(), "no vitals change -> AddVitals not called")

	amt, ok := inv.amount(101)
	require.True(t, ok)
	assert.Equal(t, uint32(2), amt, "non-healing usable item still consumed")
}

func TestItemUseService_NotUsable_Rejected(t *testing.T) {
	svc, inv, vit := newItemUseSvc(t)
	ctx := context.Background()

	inv.items[domain.ItemID(101)] = domain.Item{
		ID: 101, CharID: itemUseChar, NameID: 1101, Amount: 1, // Knife: weapon, not usable
	}

	_, err := svc.Use(ctx, itemUseAccID, itemUseChar, 1)
	assert.ErrorIs(t, err, worldapp.ErrNotUsable)
	assert.Equal(t, 0, vit.callCount(), "rejected use applies no vitals")
	amt, ok := inv.amount(101)
	require.True(t, ok)
	assert.Equal(t, uint32(1), amt, "rejected use consumes nothing")
}

func TestItemUseService_InsufficientStack_NoHeal(t *testing.T) {
	svc, inv, vit := newItemUseSvc(t)
	ctx := context.Background()

	inv.items[domain.ItemID(101)] = domain.Item{
		ID: 101, CharID: itemUseChar, NameID: 501, Amount: 0, // depleted stack
	}

	_, err := svc.Use(ctx, itemUseAccID, itemUseChar, 1)
	assert.ErrorIs(t, err, domain.ErrInsufficientAmount, "consume fails before any heal")
	assert.Equal(t, 0, vit.callCount(), "no vitals applied when consume fails")
}

func TestItemUseService_IndexOutOfRange(t *testing.T) {
	svc, inv, _ := newItemUseSvc(t)
	ctx := context.Background()

	inv.items[domain.ItemID(101)] = domain.Item{
		ID: 101, CharID: itemUseChar, NameID: 501, Amount: 5,
	}

	_, err := svc.Use(ctx, itemUseAccID, itemUseChar, 5)
	assert.ErrorIs(t, err, worldapp.ErrItemUseNotFound)
}

func TestWorldService_AddVitals_AbsoluteHeal(t *testing.T) {
	const cid uint32 = 150001
	world, _ := newHealWorld(t, cid, 500, 1000, 10, 100)

	hp, sp, err := world.AddVitals(cid, 45, 0) // Red Potion +45 HP
	require.NoError(t, err)
	assert.Equal(t, int32(545), hp, "absolute +45 HP")
	assert.Equal(t, int32(10), sp, "zero SP delta leaves SP unchanged")
}

func TestWorldService_AddVitals_ClampsToMax(t *testing.T) {
	const cid uint32 = 150001
	world, _ := newHealWorld(t, cid, 980, 1000, 10, 100)

	hp, sp, err := world.AddVitals(cid, 45, 0) // 980 + 45 = 1025 -> clamp 1000
	require.NoError(t, err)
	assert.Equal(t, int32(1000), hp, "clamped to MaxHP")
	assert.Equal(t, int32(10), sp)

	// Large delta that would overflow int32 stays clamped, no wrap-around.
	hp, _, err = world.AddVitals(cid, math.MaxInt32, 0)
	require.NoError(t, err)
	assert.Equal(t, int32(1000), hp, "already at max stays at max")
}

func TestWorldService_AddVitals_NegativeFloorsAtZero(t *testing.T) {
	const cid uint32 = 150001
	world, _ := newHealWorld(t, cid, 10, 1000, 50, 100)

	hp, sp, err := world.AddVitals(cid, -50, -80) // drain below zero
	require.NoError(t, err)
	assert.Equal(t, int32(0), hp, "negative delta floors at 0")
	assert.Equal(t, int32(0), sp)
}

func TestWorldService_AddVitals_FiresOnStatChange(t *testing.T) {
	const cid uint32 = 150001
	world, _ := newHealWorld(t, cid, 500, 1000, 10, 100)

	var got struct {
		cid    uint32
		hp, sp int32
		fired  bool
	}
	var mu sync.Mutex
	world.OnStatChange = func(charID uint32, hp, sp int32) {
		mu.Lock()
		defer mu.Unlock()
		got.cid, got.hp, got.sp, got.fired = charID, hp, sp, true
	}

	hp, sp, err := world.AddVitals(cid, 45, 5)
	require.NoError(t, err)
	assert.Equal(t, int32(545), hp)
	assert.Equal(t, int32(15), sp)

	mu.Lock()
	defer mu.Unlock()
	require.True(t, got.fired, "OnStatChange fired")
	assert.Equal(t, cid, got.cid)
	assert.Equal(t, int32(545), got.hp)
	assert.Equal(t, int32(15), got.sp)
}

func TestWorldService_AddVitals_UnknownChar(t *testing.T) {
	repo := infra.NewMemoryWorldRepository(worlddomain.Entity{ID: worlddomain.EntityID(150001), Map: "prontera"})
	world := worldapp.NewWorldService(repo, slog.Default(), 50)

	_, _, err := world.AddVitals(999999, 45, 0)
	assert.ErrorIs(t, err, worlddomain.ErrEntityNotFound)
}
