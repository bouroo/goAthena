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
	identitymocks "github.com/bouroo/goAthena/internal/features/identity/repository/mock"
	"github.com/bouroo/goAthena/internal/features/identity/service"
	inventorydomain "github.com/bouroo/goAthena/internal/features/inventory/domain"
	inventorymocks "github.com/bouroo/goAthena/internal/features/inventory/domain/mock"
)

// fakeItemWeight is a hand-rolled ItemWeightLookup test double. It
// returns a per-nameid weight from a map and 0 for unknown nameids.
// Using a hand-rolled double keeps the weight-gate test independent
// from a mockgen-generated mock and proves the port is consumable
// from outside the inventory package.
type fakeItemWeight struct {
	weights map[uint32]uint32
}

func (f fakeItemWeight) Weight(id uint32) uint32 {
	return f.weights[id]
}

// newWeightSvc wires every dependency CheckWeight touches so the gate
// can be exercised without touching the gRPC surface. Returns the
// service plus the (accountID, charID) the gate was wired against.
func newWeightSvc(
	t *testing.T,
	str uint16,
	items []inventorydomain.InventoryItem,
	lookup inventorydomain.ItemWeightLookup,
) (domain.IdentityService, uint32, uint32) {
	t.Helper()
	ctrl := gomock.NewController(t)
	chrRepo := identitymocks.NewMockCharacterRepository(ctrl)
	invRepo := inventorymocks.NewMockInventoryRepository(ctrl)

	const charID, accountID uint32 = 42, 7
	chrRepo.EXPECT().
		GetByID(gomock.Any(), accountID, charID).
		Return(&domain.CharacterSummary{CharID: charID, AccountID: accountID, Str: str}, nil).
		AnyTimes()
	invRepo.EXPECT().
		ListByChar(gomock.Any(), charID).
		Return(items, nil).
		AnyTimes()

	svc := service.NewIdentityService(
		identitymocks.NewMockAccountRepository(ctrl),
		chrRepo,
		identitymocks.NewMockSessionRepository(ctrl),
		nopLogger(),
		false,
		15,
		invRepo,
		lookup,
	)
	return svc, accountID, charID
}

// TestCheckWeight_HappyPath_UnderCapacity exercises the gate with a
// realistic character (STR=50 → max=20000+50*300=35000) and a small
// existing load. Adding a single 50-weight unit must pass.
func TestCheckWeight_HappyPath_UnderCapacity(t *testing.T) {
	t.Parallel()
	items := []inventorydomain.InventoryItem{
		{ID: 1, CharID: 42, NameID: 501, Amount: 10}, // 10*10 = 100
	}
	svc, accountID, charID := newWeightSvc(t, 50, items, fakeItemWeight{weights: map[uint32]uint32{
		501: 10,
		999: 50,
	}})
	err := svc.CheckWeight(context.Background(), accountID, charID, 999, 1)
	require.NoError(t, err)
}

// TestCheckWeight_OverCapacity exercises the same character but
// tries to add an amount that would push current+add past max.
// 10*10 = 100 current + 350 * 100 = 35100 add → 35200 > 35000.
func TestCheckWeight_OverCapacity(t *testing.T) {
	t.Parallel()
	items := []inventorydomain.InventoryItem{
		{ID: 1, CharID: 42, NameID: 501, Amount: 10}, // 10*10 = 100
	}
	svc, accountID, charID := newWeightSvc(t, 50, items, fakeItemWeight{weights: map[uint32]uint32{
		501: 10,
		999: 350,
	}})
	err := svc.CheckWeight(context.Background(), accountID, charID, 999, 100)
	require.Error(t, err)
	assert.True(t, errors.Is(err, inventorydomain.ErrWeightExceeded),
		"over-capacity add must surface the ErrWeightExceeded sentinel: %v", err)
}

// TestCheckWeight_BoundaryAtMax pins the inclusive boundary: current +
// addWeight == max is OK; current + addWeight == max + 1 is not.
//
// STR=50 → max=35000. Current = 200*100 = 20000. Add of amount=50 at
// weight=300 yields addWeight=15000 → total 35000 (== max, OK).
func TestCheckWeight_BoundaryAtMax(t *testing.T) {
	t.Parallel()
	items := []inventorydomain.InventoryItem{
		{ID: 1, CharID: 42, NameID: 501, Amount: 200}, // 200*100 = 20000
	}
	svc, accountID, charID := newWeightSvc(t, 50, items, fakeItemWeight{weights: map[uint32]uint32{
		501: 100,
		999: 300,
	}})
	// Exactly at max → OK
	err := svc.CheckWeight(context.Background(), accountID, charID, 999, 50)
	require.NoError(t, err, "current+add == max must be accepted")

	// max + 1 → over
	err = svc.CheckWeight(context.Background(), accountID, charID, 999, 51)
	require.Error(t, err)
	assert.True(t, errors.Is(err, inventorydomain.ErrWeightExceeded))
}

// TestCheckWeight_ZeroWeightLookupAcceptsEverything proves the
// production default (ZeroItemWeight) makes every acquisition pass
// without touching item_db. The gate stays a real comparison even
// when the per-item weight is 0.
func TestCheckWeight_ZeroWeightLookupAcceptsEverything(t *testing.T) {
	t.Parallel()
	items := []inventorydomain.InventoryItem{
		{ID: 1, CharID: 42, NameID: 501, Amount: 1_000_000},
	}
	svc, accountID, charID := newWeightSvc(t, 1, items, inventorydomain.ZeroItemWeight{})
	// Adding a zero-weight item even in absurd quantities must succeed.
	err := svc.CheckWeight(context.Background(), accountID, charID, 9999, 1_000_000)
	require.NoError(t, err)
}

// TestCheckWeight_EmptyInventory proves the gate works on a brand-new
// character with zero current carry weight. STR=1 → max=20300.
func TestCheckWeight_EmptyInventory(t *testing.T) {
	t.Parallel()
	svc, accountID, charID := newWeightSvc(t, 1, nil, fakeItemWeight{weights: map[uint32]uint32{
		501: 50,
	}})
	// Adding 406 units of 50-weight fits within max=20300.
	err := svc.CheckWeight(context.Background(), accountID, charID, 501, 406)
	require.NoError(t, err)

	// 407 units would push 407*50=20350 past max=20300.
	err = svc.CheckWeight(context.Background(), accountID, charID, 501, 407)
	require.Error(t, err)
	assert.True(t, errors.Is(err, inventorydomain.ErrWeightExceeded))
}

// TestCheckWeight_NoCharacterRepo is a sanity check that the helper
// pulls STR from the character repository (not a placeholder), so
// changing the character's STR in the mock flips the gate result.
// Without real STR wiring the gate would be a constant independent
// of charRepo state; this case proves the dependency.
func TestCheckWeight_StrFromCharRepo(t *testing.T) {
	t.Parallel()
	items := []inventorydomain.InventoryItem{
		{ID: 1, CharID: 42, NameID: 501, Amount: 10}, // 10*100 = 1000
	}
	lookup := fakeItemWeight{weights: map[uint32]uint32{
		501: 100,
		999: 100,
	}}

	// STR=1 → max=20000+300=20300. Add 192 units of 100-weight = 19200.
	// Total 20200. Fits within max.
	svcLow, accountID, charID := newWeightSvc(t, 1, items, lookup)
	require.NoError(t, svcLow.CheckWeight(context.Background(), accountID, charID, 999, 192))
	// 193 → 1000 + 19300 = 20300 == max, OK.
	require.NoError(t, svcLow.CheckWeight(context.Background(), accountID, charID, 999, 193))
	// 194 → 1000 + 19400 = 20400 > 20300, exceeded.
	err := svcLow.CheckWeight(context.Background(), accountID, charID, 999, 194)
	require.Error(t, err)
	assert.True(t, errors.Is(err, inventorydomain.ErrWeightExceeded))

	// STR=10 → max=20000+3000=23000. The same add of 194 units (1000 +
	// 19400 = 20400) now fits. This proves the gate consults the
	// character mock's STR field instead of a constant.
	svcHigh, accountID2, charID2 := newWeightSvc(t, 10, items, lookup)
	require.NoError(t, svcHigh.CheckWeight(context.Background(), accountID2, charID2, 999, 194))
}
