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
