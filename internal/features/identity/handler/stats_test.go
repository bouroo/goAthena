//go:build unit

package handler_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	identityv1 "github.com/bouroo/goAthena/api/pb/identity/v1"
	economydomainmock "github.com/bouroo/goAthena/internal/features/economy/domain/mock"
	domainmock "github.com/bouroo/goAthena/internal/features/identity/domain/mock"
	"github.com/bouroo/goAthena/internal/features/identity/handler"
)

func TestApplyLevelUp_HappyPath(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	shopSvc := economydomainmock.NewMockShopService(ctrl)
	h := handler.NewGRPCHandler(svc, shopSvc)

	svc.EXPECT().
		ApplyLevelUp(gomock.Any(), uint32(7), uint32(42), uint32(49), uint32(50), uint32(3)).
		Return(uint32(50), uint32(48), true, nil)

	resp, err := h.ApplyLevelUp(context.Background(), &identityv1.ApplyLevelUpRequest{
		AccountId:           7,
		CharId:              42,
		FromBaseLevel:       49,
		ToBaseLevel:         50,
		GrantedStatusPoints: 3,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, resp.GetSuccess())
	assert.Equal(t, uint32(50), resp.GetNewBaseLevel())
	assert.Equal(t, uint32(48), resp.GetNewStatusPoint())
}

func TestApplyLevelUp_NilRequest(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	shopSvc := economydomainmock.NewMockShopService(ctrl)
	h := handler.NewGRPCHandler(svc, shopSvc)

	_, err := h.ApplyLevelUp(context.Background(), nil)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestApplyLevelUp_ZeroKeys(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	shopSvc := economydomainmock.NewMockShopService(ctrl)
	h := handler.NewGRPCHandler(svc, shopSvc)

	_, err := h.ApplyLevelUp(context.Background(), &identityv1.ApplyLevelUpRequest{
		AccountId: 0,
		CharId:    42,
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAllocateStat_OK(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	shopSvc := economydomainmock.NewMockShopService(ctrl)
	h := handler.NewGRPCHandler(svc, shopSvc)

	svc.EXPECT().
		AllocateStat(gomock.Any(), uint32(7), uint32(42), uint32(13), uint32(1)).
		Return(1, uint32(11), uint32(98), nil)

	resp, err := h.AllocateStat(context.Background(), &identityv1.AllocateStatRequest{
		AccountId: 7,
		CharId:    42,
		StatId:    13,
		Amount:    1,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, identityv1.StatResult_STAT_RESULT_OK, resp.GetResult())
	assert.Equal(t, uint32(11), resp.GetNewValue())
	assert.Equal(t, uint32(98), resp.GetNewStatusPoint())
}

func TestAllocateStat_Insufficient(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	shopSvc := economydomainmock.NewMockShopService(ctrl)
	h := handler.NewGRPCHandler(svc, shopSvc)

	svc.EXPECT().
		AllocateStat(gomock.Any(), uint32(7), uint32(42), uint32(13), uint32(1)).
		Return(2, uint32(10), uint32(1), nil)

	resp, err := h.AllocateStat(context.Background(), &identityv1.AllocateStatRequest{
		AccountId: 7, CharId: 42, StatId: 13, Amount: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, identityv1.StatResult_STAT_RESULT_INSUFFICIENT_POINTS, resp.GetResult())
}

func TestAllocateStat_MaxStat(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	shopSvc := economydomainmock.NewMockShopService(ctrl)
	h := handler.NewGRPCHandler(svc, shopSvc)

	svc.EXPECT().
		AllocateStat(gomock.Any(), uint32(7), uint32(42), uint32(13), uint32(1)).
		Return(3, uint32(99), uint32(100), nil)

	resp, err := h.AllocateStat(context.Background(), &identityv1.AllocateStatRequest{
		AccountId: 7, CharId: 42, StatId: 13, Amount: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, identityv1.StatResult_STAT_RESULT_MAX_STAT, resp.GetResult())
}

func TestAllocateStat_InvalidStat(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	shopSvc := economydomainmock.NewMockShopService(ctrl)
	h := handler.NewGRPCHandler(svc, shopSvc)

	svc.EXPECT().
		AllocateStat(gomock.Any(), uint32(7), uint32(42), uint32(99), uint32(1)).
		Return(4, uint32(0), uint32(0), nil)

	resp, err := h.AllocateStat(context.Background(), &identityv1.AllocateStatRequest{
		AccountId: 7, CharId: 42, StatId: 99, Amount: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, identityv1.StatResult_STAT_RESULT_INVALID_STAT, resp.GetResult())
}

func TestAllocateStat_NilRequest(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	shopSvc := economydomainmock.NewMockShopService(ctrl)
	h := handler.NewGRPCHandler(svc, shopSvc)

	_, err := h.AllocateStat(context.Background(), nil)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAllocateStat_ZeroKeys(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	shopSvc := economydomainmock.NewMockShopService(ctrl)
	h := handler.NewGRPCHandler(svc, shopSvc)

	_, err := h.AllocateStat(context.Background(), &identityv1.AllocateStatRequest{
		AccountId: 0, CharId: 42, StatId: 13, Amount: 1,
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAllocateStat_InternalError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	shopSvc := economydomainmock.NewMockShopService(ctrl)
	h := handler.NewGRPCHandler(svc, shopSvc)

	svc.EXPECT().
		AllocateStat(gomock.Any(), uint32(7), uint32(42), uint32(13), uint32(1)).
		Return(0, uint32(0), uint32(0), errors.New("db down"))

	_, err := h.AllocateStat(context.Background(), &identityv1.AllocateStatRequest{
		AccountId: 7, CharId: 42, StatId: 13, Amount: 1,
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}
