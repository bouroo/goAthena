//go:build unit

package service_test

import (
	"context"
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

// newSvcForStats is the test-only constructor for the stats use-case tests.
// It wires a real identityService with a mock CharacterRepository; the other
// collaborators (accounts/sessions/inventory) are fresh mocks the stats path
// never touches.
func newSvcForStats(t *testing.T, chars *mocks.MockCharacterRepository) domain.IdentityService {
	t.Helper()
	ctrl := gomock.NewController(t)
	return service.NewIdentityService(
		mocks.NewMockAccountRepository(ctrl),
		chars,
		mocks.NewMockSessionRepository(ctrl),
		nopLogger(),
		false,
		15,
		inventorymocks.NewMockInventoryRepository(ctrl),
		inventorydomain.ZeroItemWeight{},
	)
}

func TestApplyLevelUp_Happy(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	chars := mocks.NewMockCharacterRepository(ctrl)
	chars.EXPECT().ApplyLevelUp(gomock.Any(), uint32(7), uint32(42), uint32(49), uint32(50), uint32(3)).
		Return(uint32(50), uint32(48), true, nil)
	svc := newSvcForStats(t, chars)

	newLevel, newSP, applied, err := svc.ApplyLevelUp(context.Background(), 7, 42, 49, 50, 3)
	require.NoError(t, err)
	assert.True(t, applied)
	assert.Equal(t, uint32(50), newLevel)
	assert.Equal(t, uint32(48), newSP)
}

func TestApplyLevelUp_Concurrent(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	chars := mocks.NewMockCharacterRepository(ctrl)
	chars.EXPECT().ApplyLevelUp(gomock.Any(), uint32(7), uint32(42), uint32(49), uint32(50), uint32(3)).
		Return(uint32(0), uint32(0), false, nil)
	svc := newSvcForStats(t, chars)

	_, _, applied, err := svc.ApplyLevelUp(context.Background(), 7, 42, 49, 50, 3)
	require.NoError(t, err, "concurrent level-up is not an error")
	assert.False(t, applied)
}

func TestApplyLevelUp_ZeroKeys(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	chars := mocks.NewMockCharacterRepository(ctrl)
	svc := newSvcForStats(t, chars)

	_, _, _, err := svc.ApplyLevelUp(context.Background(), 0, 42, 1, 2, 1)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrCharacterNotFound)
}

func TestAllocateStat_HappyPath(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	chars := mocks.NewMockCharacterRepository(ctrl)
	chars.EXPECT().GetByID(gomock.Any(), uint32(7), uint32(42)).
		Return(&domain.CharacterSummary{Str: 10, StatusPoint: 100}, nil)
	// cost to raise Str 10->11 = 1+(10+9)/10 = 2
	chars.EXPECT().AllocateStat(gomock.Any(), uint32(7), uint32(42), "str", uint8(10), uint8(1), uint32(2)).
		Return(uint32(11), uint32(98), 0, nil)
	svc := newSvcForStats(t, chars)

	result, newVal, newSP, err := svc.AllocateStat(context.Background(), 7, 42, 13, 1) // SP_STR=13
	require.NoError(t, err)
	assert.Equal(t, 1, result, "OK")
	assert.Equal(t, uint32(11), newVal)
	assert.Equal(t, uint32(98), newSP)
}

func TestAllocateStat_InsufficientPoints(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	chars := mocks.NewMockCharacterRepository(ctrl)
	chars.EXPECT().GetByID(gomock.Any(), uint32(7), uint32(42)).
		Return(&domain.CharacterSummary{Str: 10, StatusPoint: 1}, nil)
	// cost to raise Str 10->11 = 2; status_point=1 < 2
	chars.EXPECT().AllocateStat(gomock.Any(), uint32(7), uint32(42), "str", uint8(10), uint8(1), uint32(2)).
		Return(uint32(10), uint32(1), 1, nil)
	svc := newSvcForStats(t, chars)

	result, _, _, err := svc.AllocateStat(context.Background(), 7, 42, 13, 1)
	require.NoError(t, err)
	assert.Equal(t, 2, result, "INSUFFICIENT_POINTS")
}

func TestAllocateStat_MaxStat(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	chars := mocks.NewMockCharacterRepository(ctrl)
	chars.EXPECT().GetByID(gomock.Any(), uint32(7), uint32(42)).
		Return(&domain.CharacterSummary{Str: 99, StatusPoint: 100}, nil)
	chars.EXPECT().AllocateStat(gomock.Any(), uint32(7), uint32(42), "str", uint8(99), uint8(1), uint32(11)).
		Return(uint32(99), uint32(100), 2, nil)
	svc := newSvcForStats(t, chars)

	result, _, _, err := svc.AllocateStat(context.Background(), 7, 42, 13, 1)
	require.NoError(t, err)
	assert.Equal(t, 3, result, "MAX_STAT")
}

func TestAllocateStat_InvalidStatID(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	chars := mocks.NewMockCharacterRepository(ctrl)
	svc := newSvcForStats(t, chars)

	result, _, _, err := svc.AllocateStat(context.Background(), 7, 42, 99, 1)
	require.NoError(t, err, "invalid stat is a wire-level outcome, not an error")
	assert.Equal(t, 4, result, "INVALID_STAT")
}

func TestAllocateStat_ZeroAmount(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	chars := mocks.NewMockCharacterRepository(ctrl)
	svc := newSvcForStats(t, chars)

	result, _, _, err := svc.AllocateStat(context.Background(), 7, 42, 13, 0)
	require.NoError(t, err)
	assert.Equal(t, 4, result, "zero amount = no-op = INVALID")
}

func TestAllocateStat_ZeroKeys(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	chars := mocks.NewMockCharacterRepository(ctrl)
	svc := newSvcForStats(t, chars)

	_, _, _, err := svc.AllocateStat(context.Background(), 0, 42, 13, 1)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrCharacterNotFound)
}
