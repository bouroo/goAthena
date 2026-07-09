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
	domainmock "github.com/bouroo/goAthena/internal/features/identity/domain/mock"
	"github.com/bouroo/goAthena/internal/features/identity/handler"
	inventorydomain "github.com/bouroo/goAthena/internal/features/inventory/domain"
)

func TestGetInventory_HappyPath(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(svc)
	svc.EXPECT().
		GetInventory(gomock.Any(), uint32(7), uint32(42)).
		Return([]inventorydomain.InventoryItem{
			{ID: 1, NameID: 501, Amount: 5, Equip: 0},
			{ID: 2, NameID: 502, Amount: 1, Equip: inventorydomain.EquipSlot(0x0002)},
		}, nil)

	resp, err := h.GetInventory(context.Background(), &identityv1.GetInventoryRequest{
		AccountId: 7, CharId: 42,
	})
	require.NoError(t, err)
	require.Len(t, resp.Items, 2)
	assert.Equal(t, uint32(1), resp.Items[0].Id)
	assert.Equal(t, uint32(501), resp.Items[0].Nameid)
	assert.Equal(t, uint32(5), resp.Items[0].Amount)
	assert.Equal(t, uint32(0x0002), resp.Items[1].Equip)
}

func TestGetInventory_NilRequest(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(svc)
	_, err := h.GetInventory(context.Background(), nil)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGetInventory_ZeroKeys(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(svc)
	_, err := h.GetInventory(context.Background(), &identityv1.GetInventoryRequest{AccountId: 0, CharId: 42})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGetInventory_InternalError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(svc)
	svc.EXPECT().GetInventory(gomock.Any(), uint32(7), uint32(42)).
		Return(nil, errors.New("db down"))

	_, err := h.GetInventory(context.Background(), &identityv1.GetInventoryRequest{
		AccountId: 7, CharId: 42,
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestEquipItem_HappyPath(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(svc)
	svc.EXPECT().EquipItem(gomock.Any(), uint32(7), uint32(42), uint32(100), uint32(0x0002)).Return(nil)

	resp, err := h.EquipItem(context.Background(), &identityv1.EquipItemRequest{
		AccountId: 7, CharId: 42, ItemId: 100, EquipPosition: 0x0002,
	})
	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Equal(t, uint32(100), resp.ItemId)
	assert.Equal(t, uint32(0x0002), resp.EquipPosition)
	assert.Empty(t, resp.Error)
}

func TestEquipItem_NotFound_EncodesAsSoftFailure(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(svc)
	svc.EXPECT().EquipItem(gomock.Any(), uint32(7), uint32(42), uint32(100), uint32(0x0002)).
		Return(notFoundWrapper{})

	resp, err := h.EquipItem(context.Background(), &identityv1.EquipItemRequest{
		AccountId: 7, CharId: 42, ItemId: 100, EquipPosition: 0x0002,
	})
	require.NoError(t, err)
	assert.False(t, resp.Success)
	assert.Equal(t, uint32(100), resp.ItemId)
	assert.Contains(t, resp.Error, "not found")
}

// notFoundWrapper exists so handler tests can return a wrapped
// inventorydomain.ErrItemNotFound without import-cycle headaches.
type notFoundWrapper struct{}

func (notFoundWrapper) Error() string { return "wrap: " + inventorydomain.ErrItemNotFound.Error() }
func (notFoundWrapper) Unwrap() error { return inventorydomain.ErrItemNotFound }

func TestEquipItem_InternalError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(svc)
	svc.EXPECT().EquipItem(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(errors.New("db write failed"))

	_, err := h.EquipItem(context.Background(), &identityv1.EquipItemRequest{
		AccountId: 7, CharId: 42, ItemId: 100, EquipPosition: 0x0002,
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestEquipItem_ZeroKey(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(svc)
	_, err := h.EquipItem(context.Background(), &identityv1.EquipItemRequest{
		AccountId: 0, CharId: 42, ItemId: 100, EquipPosition: 0x0002,
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestUnequipItem_HappyPath(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(svc)
	svc.EXPECT().UnequipItem(gomock.Any(), uint32(7), uint32(42), uint32(100)).Return(uint32(0x0002), nil)

	resp, err := h.UnequipItem(context.Background(), &identityv1.UnequipItemRequest{
		AccountId: 7, CharId: 42, ItemId: 100,
	})
	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Equal(t, uint32(100), resp.ItemId)
}

func TestUnequipItem_NotFound_EncodesAsSoftFailure(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(svc)
	svc.EXPECT().UnequipItem(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(uint32(0), notFoundWrapper{})

	resp, err := h.UnequipItem(context.Background(), &identityv1.UnequipItemRequest{
		AccountId: 7, CharId: 42, ItemId: 100,
	})
	require.NoError(t, err)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Error, "not found")
}

func TestUnequipItem_InternalError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(svc)
	svc.EXPECT().UnequipItem(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(uint32(0), errors.New("db down"))

	_, err := h.UnequipItem(context.Background(), &identityv1.UnequipItemRequest{
		AccountId: 7, CharId: 42, ItemId: 100,
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestUseItem_HappyPath(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(svc)
	svc.EXPECT().UseItem(gomock.Any(), uint32(7), uint32(42), uint32(100)).Return(uint32(2), nil)

	resp, err := h.UseItem(context.Background(), &identityv1.UseItemRequest{
		AccountId: 7, CharId: 42, ItemId: 100,
	})
	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Equal(t, uint32(100), resp.ItemId)
	assert.Equal(t, uint32(2), resp.RemainingAmount)
}

func TestUseItem_StackEmptied(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(svc)
	svc.EXPECT().UseItem(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(uint32(0), nil)

	resp, err := h.UseItem(context.Background(), &identityv1.UseItemRequest{
		AccountId: 7, CharId: 42, ItemId: 100,
	})
	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Equal(t, uint32(0), resp.RemainingAmount, "row deleted -> remaining must be 0")
}

func TestUseItem_NotFound_EncodesAsSoftFailure(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(svc)
	svc.EXPECT().UseItem(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(uint32(0), notFoundWrapper{})

	resp, err := h.UseItem(context.Background(), &identityv1.UseItemRequest{
		AccountId: 7, CharId: 42, ItemId: 100,
	})
	require.NoError(t, err)
	assert.False(t, resp.Success)
	assert.Contains(t, resp.Error, "not found")
}

func TestUseItem_InternalError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(svc)
	svc.EXPECT().UseItem(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(uint32(0), errors.New("db down"))

	_, err := h.UseItem(context.Background(), &identityv1.UseItemRequest{
		AccountId: 7, CharId: 42, ItemId: 100,
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.Internal, st.Code())
}
