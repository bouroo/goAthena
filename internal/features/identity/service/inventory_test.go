//go:build unit

package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/bouroo/goAthena/internal/features/identity/domain"
	mocks "github.com/bouroo/goAthena/internal/features/identity/repository/mock"
	"github.com/bouroo/goAthena/internal/features/identity/service"
	inventorydomain "github.com/bouroo/goAthena/internal/features/inventory/domain"
	inventorymocks "github.com/bouroo/goAthena/internal/features/inventory/domain/mock"
)

// newSvcForInventory is the test-only constructor used by every
// inventory test below. It spins up fresh mocks for the three
// "auth-shape" dependencies (which the inventory use cases do not
// touch) and injects a caller-supplied inventory mock so each case
// declares only what it cares about. The ItemWeightLookup port is
// wired to ZeroItemWeight because the P2A inventory use cases
// (GetInventory / EquipItem / UnequipItem / UseItem) do not consume
// it — checkWeight is a separate helper tested in weight_gate_test.go.
func newSvcForInventory(t *testing.T, inv *inventorymocks.MockInventoryRepository) domain.IdentityService {
	t.Helper()
	ctrl := gomock.NewController(t)
	return service.NewIdentityService(
		mocks.NewMockAccountRepository(ctrl),
		mocks.NewMockCharacterRepository(ctrl),
		mocks.NewMockSessionRepository(ctrl),
		nopLogger(),
		false,
		15,
		inv,
		inventorydomain.ZeroItemWeight{},
	)
}

func TestGetInventory_HappyPath(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	inv := inventorymocks.NewMockInventoryRepository(ctrl)
	items := []inventorydomain.InventoryItem{
		{ID: 1, CharID: 42, NameID: 501, Amount: 5, Equip: 0},
		{ID: 2, CharID: 42, NameID: 502, Amount: 1, Equip: inventorydomain.EquipSlot(0x0002)},
	}
	inv.EXPECT().ListByChar(gomock.Any(), uint32(42)).Return(items, nil)

	svc := newSvcForInventory(t, inv)
	got, err := svc.GetInventory(context.Background(), 7, 42)
	require.NoError(t, err)
	assert.Len(t, got, 2)
	assert.Equal(t, items[0].ID, got[0].ID)
	assert.Equal(t, items[1].ID, got[1].ID)
}

func TestGetInventory_Empty(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	inv := inventorymocks.NewMockInventoryRepository(ctrl)
	inv.EXPECT().ListByChar(gomock.Any(), uint32(42)).Return(nil, nil)

	svc := newSvcForInventory(t, inv)
	got, err := svc.GetInventory(context.Background(), 7, 42)
	require.NoError(t, err, "empty inventory must be a non-error outcome")
	require.NotNil(t, got, "empty inventory must surface as a non-nil slice")
	assert.Empty(t, got)
}

func TestGetInventory_ZeroKeys(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	inv := inventorymocks.NewMockInventoryRepository(ctrl)
	// No calls expected on inv — zero keys must short-circuit before any
	// outbound port call.
	svc := newSvcForInventory(t, inv)
	_, err := svc.GetInventory(context.Background(), 0, 42)
	require.Error(t, err)
	assert.True(t, errors.Is(err, inventorydomain.ErrItemNotFound))
}

func TestGetInventory_RepoError_Wrapped(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	inv := inventorymocks.NewMockInventoryRepository(ctrl)
	boom := errors.New("db down")
	inv.EXPECT().ListByChar(gomock.Any(), uint32(42)).Return(nil, boom)

	svc := newSvcForInventory(t, inv)
	_, err := svc.GetInventory(context.Background(), 7, 42)
	require.Error(t, err)
	assert.True(t, errors.Is(err, boom), "wrapcheck: original error must remain in chain")
}

func TestEquipItem_HappyPath(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	inv := inventorymocks.NewMockInventoryRepository(ctrl)
	const charID, itemID, eqPos uint32 = 42, 100, 0x0002
	inv.EXPECT().ListByChar(gomock.Any(), charID).
		Return([]inventorydomain.InventoryItem{{ID: itemID, CharID: charID, Amount: 1}}, nil)
	inv.EXPECT().SetEquip(gomock.Any(), itemID, eqPos).Return(nil)

	svc := newSvcForInventory(t, inv)
	require.NoError(t, svc.EquipItem(context.Background(), 7, charID, itemID, eqPos))
}

func TestEquipItem_NotOwned(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	inv := inventorymocks.NewMockInventoryRepository(ctrl)
	inv.EXPECT().ListByChar(gomock.Any(), uint32(42)).
		Return([]inventorydomain.InventoryItem{{ID: 999, CharID: 42}}, nil)
	// SetEquip MUST NOT be called when the ownership check fails.

	svc := newSvcForInventory(t, inv)
	err := svc.EquipItem(context.Background(), 7, 42, 100, 0x0002)
	require.Error(t, err)
	assert.True(t, errors.Is(err, inventorydomain.ErrItemNotFound))
}

func TestEquipItem_RepoError_Wrapped(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	inv := inventorymocks.NewMockInventoryRepository(ctrl)
	const charID, itemID uint32 = 42, 100
	inv.EXPECT().ListByChar(gomock.Any(), charID).
		Return([]inventorydomain.InventoryItem{{ID: itemID, CharID: charID}}, nil)
	boom := errors.New("write failed")
	inv.EXPECT().SetEquip(gomock.Any(), itemID, uint32(0x0002)).Return(boom)

	svc := newSvcForInventory(t, inv)
	err := svc.EquipItem(context.Background(), 7, charID, itemID, 0x0002)
	require.Error(t, err)
	assert.True(t, errors.Is(err, boom))
}

func TestUnequipItem_HappyPath(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	inv := inventorymocks.NewMockInventoryRepository(ctrl)
	const charID, itemID uint32 = 42, 100
	const priorPos inventorydomain.EquipSlot = 0x0002
	inv.EXPECT().ListByChar(gomock.Any(), charID).
		Return([]inventorydomain.InventoryItem{{ID: itemID, CharID: charID, Equip: priorPos}}, nil)
	inv.EXPECT().SetEquip(gomock.Any(), itemID, uint32(0)).Return(nil)

	svc := newSvcForInventory(t, inv)
	prior, err := svc.UnequipItem(context.Background(), 7, charID, itemID)
	require.NoError(t, err)
	assert.Equal(t, uint32(priorPos), prior)
}

func TestUnequipItem_NotOwned(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	inv := inventorymocks.NewMockInventoryRepository(ctrl)
	inv.EXPECT().ListByChar(gomock.Any(), uint32(42)).Return([]inventorydomain.InventoryItem{}, nil)

	svc := newSvcForInventory(t, inv)
	_, err := svc.UnequipItem(context.Background(), 7, 42, 100)
	require.Error(t, err)
	assert.True(t, errors.Is(err, inventorydomain.ErrItemNotFound))
}

func TestUseItem_DecrementsAmount(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	inv := inventorymocks.NewMockInventoryRepository(ctrl)
	const charID, itemID uint32 = 42, 100
	inv.EXPECT().ListByChar(gomock.Any(), charID).
		Return([]inventorydomain.InventoryItem{
			{ID: itemID, CharID: charID, NameID: 501, Amount: 3},
		}, nil)
	inv.EXPECT().ConsumeOne(gomock.Any(), itemID).Return(uint32(2), nil)

	svc := newSvcForInventory(t, inv)
	remaining, err := svc.UseItem(context.Background(), 7, charID, itemID)
	require.NoError(t, err)
	assert.Equal(t, uint32(2), remaining)
}

func TestUseItem_RemovesWhenZero(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	inv := inventorymocks.NewMockInventoryRepository(ctrl)
	const charID, itemID uint32 = 42, 100
	inv.EXPECT().ListByChar(gomock.Any(), charID).
		Return([]inventorydomain.InventoryItem{
			{ID: itemID, CharID: charID, NameID: 501, Amount: 1},
		}, nil)
	inv.EXPECT().ConsumeOne(gomock.Any(), itemID).Return(uint32(0), nil)

	svc := newSvcForInventory(t, inv)
	remaining, err := svc.UseItem(context.Background(), 7, charID, itemID)
	require.NoError(t, err)
	assert.Equal(t, uint32(0), remaining, "stack emptied -> row deleted -> remaining must be 0")
}

func TestUseItem_NotFound(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	inv := inventorymocks.NewMockInventoryRepository(ctrl)
	inv.EXPECT().ListByChar(gomock.Any(), uint32(42)).Return(nil, nil)

	svc := newSvcForInventory(t, inv)
	_, err := svc.UseItem(context.Background(), 7, 42, 100)
	require.Error(t, err)
	assert.True(t, errors.Is(err, inventorydomain.ErrItemNotFound))
}

func TestUseItem_UpdateError_Wrapped(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	inv := inventorymocks.NewMockInventoryRepository(ctrl)
	const charID, itemID uint32 = 42, 100
	inv.EXPECT().ListByChar(gomock.Any(), charID).
		Return([]inventorydomain.InventoryItem{
			{ID: itemID, CharID: charID, Amount: 5},
		}, nil)
	boom := errors.New("update failed")
	inv.EXPECT().ConsumeOne(gomock.Any(), itemID).Return(uint32(0), boom)

	svc := newSvcForInventory(t, inv)
	_, err := svc.UseItem(context.Background(), 7, charID, itemID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, boom))
}

// itemWeightLookup is a controllable ItemWeightLookup double: per-nameid
// weights and a set of nameids treated as non-stackable (everything else
// stacks, matching the ZeroItemWeight default). It drives the merge gate
// and the weight gate without touching item_db.
type itemWeightLookup struct {
	weights      map[uint32]uint32
	nonStackable map[uint32]bool
}

func (l itemWeightLookup) Weight(id uint32) uint32 { return l.weights[id] }

// IsStackable returns false only for nameids in the non-stackable set,
// true otherwise — mirrors ZeroItemWeight's permissive default.
func (l itemWeightLookup) IsStackable(id uint32) bool {
	return !l.nonStackable[id]
}

// newSvcForWeightedInventory wires an IdentityService for AddItem/ConsumeItem
// tests: the character's STR and the inventory rows they read, plus a
// caller-supplied weight lookup. CheckWeight calls GetByID once and
// ListByChar once, and AddItem's merge scan calls ListByChar again, so both
// are declared AnyTimes. The live inventory mock is returned so each test
// pins Add/UpdateAmount/Consume expectations.
func newSvcForWeightedInventory(
	t *testing.T,
	str uint16,
	items []inventorydomain.InventoryItem,
	lookup inventorydomain.ItemWeightLookup,
) (domain.IdentityService, *inventorymocks.MockInventoryRepository, uint32, uint32) {
	t.Helper()
	ctrl := gomock.NewController(t)
	chrRepo := mocks.NewMockCharacterRepository(ctrl)
	inv := inventorymocks.NewMockInventoryRepository(ctrl)

	const charID, accountID uint32 = 42, 7
	chrRepo.EXPECT().GetByID(gomock.Any(), accountID, charID).
		Return(&domain.CharacterSummary{CharID: charID, AccountID: accountID, Str: str}, nil).
		AnyTimes()
	inv.EXPECT().ListByChar(gomock.Any(), charID).Return(items, nil).AnyTimes()

	svc := service.NewIdentityService(
		mocks.NewMockAccountRepository(ctrl),
		chrRepo,
		mocks.NewMockSessionRepository(ctrl),
		nopLogger(),
		false,
		15,
		inv,
		lookup,
	)
	return svc, inv, accountID, charID
}

// TestAddItem_MergesIntoExistingStack proves a second acquisition of a
// stackable nameid merges into the matching plain stack (rAthena
// pc.cpp:6019) via UpdateAmount and returns the existing row id instead
// of inserting a duplicate.
func TestAddItem_MergesIntoExistingStack(t *testing.T) {
	t.Parallel()
	items := []inventorydomain.InventoryItem{
		{ID: 10, CharID: 42, NameID: 512, Amount: 5},
	}
	svc, inv, accountID, charID := newSvcForWeightedInventory(t, 1, items, inventorydomain.ZeroItemWeight{})
	inv.EXPECT().UpdateAmount(gomock.Any(), uint32(10), uint32(8)).Return(nil)

	id, err := svc.AddItem(context.Background(), accountID, charID, 512, 3)
	require.NoError(t, err)
	assert.Equal(t, uint32(10), id)
}

// TestAddItem_NewRowWhenNoStackMatches proves a fresh nameid (or one with
// no matching plain stack) inserts a new identified row and returns its id.
func TestAddItem_NewRowWhenNoStackMatches(t *testing.T) {
	t.Parallel()
	svc, inv, accountID, charID := newSvcForWeightedInventory(t, 1, nil, inventorydomain.ZeroItemWeight{})
	inv.EXPECT().Add(gomock.Any(), charID, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ uint32, item inventorydomain.InventoryItem) (uint32, error) {
			assert.Equal(t, uint32(512), item.NameID)
			assert.Equal(t, uint32(3), item.Amount)
			assert.Equal(t, int16(1), item.Identify, "floor pickups are identified at MVP")
			return 20, nil
		})

	id, err := svc.AddItem(context.Background(), accountID, charID, 512, 3)
	require.NoError(t, err)
	assert.Equal(t, uint32(20), id)
}

// TestAddItem_OverweightReturnsBeforeAnyWrite proves an overweight
// acquisition fails with ErrWeightExceeded and writes nothing. No
// Add/UpdateAmount expectation is set, so gomock fails the test if
// AddItem were to persist — the guarantee we are pinning.
func TestAddItem_OverweightReturnsBeforeAnyWrite(t *testing.T) {
	t.Parallel()
	items := []inventorydomain.InventoryItem{
		{ID: 1, CharID: 42, NameID: 501, Amount: 10}, // 10*10 = 100
	}
	// STR=50 -> max = 20000 + 50*300 = 35000. Add 100 of weight-350 = 35000.
	// current(100) + add(35000) = 35100 > 35000 -> exceeded.
	lookup := itemWeightLookup{weights: map[uint32]uint32{501: 10, 512: 350}}
	svc, _, accountID, charID := newSvcForWeightedInventory(t, 50, items, lookup)

	_, err := svc.AddItem(context.Background(), accountID, charID, 512, 100)
	require.Error(t, err)
	assert.True(t, errors.Is(err, inventorydomain.ErrWeightExceeded),
		"overweight add must surface ErrWeightExceeded: %v", err)
}

// TestAddItem_SkipsMergeForNonStackable proves a non-stackable nameid
// (weapon/armor/petegg/petarmor/shadowgear) always inserts a new row
// even when an existing row shares its nameid — it never merges. rAthena
// gates the merge on itemdb_isstackable2 (itemdb.cpp:4932).
func TestAddItem_SkipsMergeForNonStackable(t *testing.T) {
	t.Parallel()
	items := []inventorydomain.InventoryItem{
		{ID: 10, CharID: 42, NameID: 512, Amount: 1},
	}
	lookup := itemWeightLookup{nonStackable: map[uint32]bool{512: true}}
	svc, inv, accountID, charID := newSvcForWeightedInventory(t, 1, items, lookup)
	inv.EXPECT().Add(gomock.Any(), charID, gomock.Any()).Return(uint32(20), nil)

	id, err := svc.AddItem(context.Background(), accountID, charID, 512, 1)
	require.NoError(t, err)
	assert.Equal(t, uint32(20), id)
}

// TestConsumeItem_Partial proves a partial consume delegates to the repo
// Consume(id, amount) and surfaces the post-decrement count.
func TestConsumeItem_Partial(t *testing.T) {
	t.Parallel()
	items := []inventorydomain.InventoryItem{
		{ID: 909, CharID: 42, NameID: 512, Amount: 5},
	}
	svc, inv, accountID, charID := newSvcForWeightedInventory(t, 1, items, inventorydomain.ZeroItemWeight{})
	inv.EXPECT().Consume(gomock.Any(), uint32(909), uint32(2)).Return(uint32(3), nil)

	remaining, err := svc.ConsumeItem(context.Background(), accountID, charID, 909, 2)
	require.NoError(t, err)
	assert.Equal(t, uint32(3), remaining)
}

// TestConsumeItem_OwnershipFailReturnsNotFound proves a consume against an
// item the character does not own fails with ErrItemNotFound and never
// reaches the repo Consume (no partial over-consume possible).
func TestConsumeItem_OwnershipFailReturnsNotFound(t *testing.T) {
	t.Parallel()
	// Inventory holds item 1000, not the requested 909.
	items := []inventorydomain.InventoryItem{
		{ID: 1000, CharID: 42, NameID: 512, Amount: 5},
	}
	svc, _, accountID, charID := newSvcForWeightedInventory(t, 1, items, inventorydomain.ZeroItemWeight{})

	_, err := svc.ConsumeItem(context.Background(), accountID, charID, 909, 2)
	require.Error(t, err)
	assert.True(t, errors.Is(err, inventorydomain.ErrItemNotFound),
		"cross-character consume must surface ErrItemNotFound: %v", err)
}
