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
	economydomain "github.com/bouroo/goAthena/internal/features/economy/domain"
	economydomainmock "github.com/bouroo/goAthena/internal/features/economy/domain/mock"
	domainmock "github.com/bouroo/goAthena/internal/features/identity/domain/mock"
	"github.com/bouroo/goAthena/internal/features/identity/handler"
)

func TestBuyFromShop_HappyPath(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := economydomainmock.NewMockShopService(ctrl)
	// svc (IdentityService) is unused here but required by the constructor.
	idleSvc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(idleSvc, svc)

	svc.EXPECT().
		BuyFromShop(gomock.Any(), uint32(42), gomock.Any()).
		DoAndReturn(func(_ context.Context, charID uint32, orders []economydomain.ShopOrder) (uint32, economydomain.BuyResult, error) {
			require.Equal(t, uint32(42), charID)
			require.Len(t, orders, 1)
			assert.Equal(t, uint32(501), orders[0].ItemID)
			assert.Equal(t, uint32(5), orders[0].Amount)
			assert.Equal(t, uint32(10), orders[0].UnitPrice)
			return 5000, economydomain.BuyOK, nil
		})

	resp, err := h.BuyFromShop(context.Background(), &identityv1.BuyFromShopRequest{
		AccountId: 7,
		CharId:    42,
		Orders: []*identityv1.ShopOrder{
			{ItemId: 501, Amount: 5, UnitPrice: 10},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, identityv1.BuyResult_BUY_RESULT_OK, resp.GetResult())
	assert.Equal(t, uint32(5000), resp.GetNewZeny())
}

func TestBuyFromShop_InsufficientZeny(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := economydomainmock.NewMockShopService(ctrl)
	idleSvc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(idleSvc, svc)

	svc.EXPECT().
		BuyFromShop(gomock.Any(), uint32(42), gomock.Any()).
		Return(uint32(0), economydomain.BuyFailInsufficientZeny, nil)

	resp, err := h.BuyFromShop(context.Background(), &identityv1.BuyFromShopRequest{
		AccountId: 7,
		CharId:    42,
		Orders:    []*identityv1.ShopOrder{{ItemId: 501, Amount: 1, UnitPrice: 1000}},
	})
	require.NoError(t, err, "insufficient zeny is a wire-level outcome, not a gRPC error")
	require.NotNil(t, resp)
	assert.Equal(t, identityv1.BuyResult_BUY_RESULT_INSUFFICIENT_ZENY, resp.GetResult())
	assert.Equal(t, uint32(0), resp.GetNewZeny(), "new_zeny must be 0 on non-OK outcomes")
}

func TestBuyFromShop_LockBusy(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := economydomainmock.NewMockShopService(ctrl)
	idleSvc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(idleSvc, svc)

	svc.EXPECT().
		BuyFromShop(gomock.Any(), uint32(42), gomock.Any()).
		Return(uint32(0), economydomain.BuyFailLockBusy, nil)

	resp, err := h.BuyFromShop(context.Background(), &identityv1.BuyFromShopRequest{
		AccountId: 7,
		CharId:    42,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, identityv1.BuyResult_BUY_RESULT_LOCK_BUSY, resp.GetResult())
}

func TestBuyFromShop_InternalError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := economydomainmock.NewMockShopService(ctrl)
	idleSvc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(idleSvc, svc)

	svc.EXPECT().
		BuyFromShop(gomock.Any(), uint32(42), gomock.Any()).
		Return(uint32(0), economydomain.BuyOK, errors.New("valkey exploded"))

	resp, err := h.BuyFromShop(context.Background(), &identityv1.BuyFromShopRequest{
		AccountId: 7,
		CharId:    42,
		Orders:    []*identityv1.ShopOrder{{ItemId: 501, Amount: 1, UnitPrice: 10}},
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "buy from shop")
}

func TestBuyFromShop_NilRequest(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	idleSvc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(idleSvc, economydomainmock.NewMockShopService(ctrl))

	resp, err := h.BuyFromShop(context.Background(), nil)
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestBuyFromShop_ZeroKeys(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	shop := economydomainmock.NewMockShopService(ctrl)
	idleSvc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(idleSvc, shop)

	cases := []struct {
		name string
		req  *identityv1.BuyFromShopRequest
	}{
		{"zero account", &identityv1.BuyFromShopRequest{AccountId: 0, CharId: 42}},
		{"zero char", &identityv1.BuyFromShopRequest{AccountId: 7, CharId: 0}},
		{"both zero", &identityv1.BuyFromShopRequest{AccountId: 0, CharId: 0}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp, err := h.BuyFromShop(context.Background(), tc.req)
			require.Error(t, err)
			assert.Nil(t, resp)
			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, codes.InvalidArgument, st.Code())
		})
	}
}

func TestSellToShop_HappyPath(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := economydomainmock.NewMockShopService(ctrl)
	idleSvc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(idleSvc, svc)

	svc.EXPECT().
		SellToShop(gomock.Any(), uint32(42), gomock.Any()).
		DoAndReturn(func(_ context.Context, charID uint32, sales []economydomain.SellLine) (uint32, economydomain.SellResult, error) {
			require.Equal(t, uint32(42), charID)
			require.Len(t, sales, 1)
			assert.Equal(t, uint32(7), sales[0].InvID)
			assert.Equal(t, uint32(2), sales[0].Amount)
			assert.Equal(t, uint32(50), sales[0].UnitPrice)
			return 200, economydomain.SellOK, nil
		})

	resp, err := h.SellToShop(context.Background(), &identityv1.SellToShopRequest{
		AccountId: 7,
		CharId:    42,
		Sales: []*identityv1.SellLine{
			{InvId: 7, Amount: 2, UnitPrice: 50},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, identityv1.SellResult_SELL_RESULT_OK, resp.GetResult())
	assert.Equal(t, uint32(200), resp.GetNewZeny())
}

func TestSellToShop_InvalidItem(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := economydomainmock.NewMockShopService(ctrl)
	idleSvc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(idleSvc, svc)

	svc.EXPECT().
		SellToShop(gomock.Any(), uint32(42), gomock.Any()).
		Return(uint32(0), economydomain.SellFailInvalidItem, nil)

	resp, err := h.SellToShop(context.Background(), &identityv1.SellToShopRequest{
		AccountId: 7,
		CharId:    42,
		Sales:     []*identityv1.SellLine{{InvId: 999, Amount: 1, UnitPrice: 10}},
	})
	require.NoError(t, err, "invalid item is a wire-level outcome, not a gRPC error")
	require.NotNil(t, resp)
	assert.Equal(t, identityv1.SellResult_SELL_RESULT_INVALID_ITEM, resp.GetResult())
	assert.Equal(t, uint32(0), resp.GetNewZeny())
}

func TestSellToShop_LockBusy(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := economydomainmock.NewMockShopService(ctrl)
	idleSvc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(idleSvc, svc)

	svc.EXPECT().
		SellToShop(gomock.Any(), uint32(42), gomock.Any()).
		Return(uint32(0), economydomain.SellFailLockBusy, nil)

	resp, err := h.SellToShop(context.Background(), &identityv1.SellToShopRequest{
		AccountId: 7,
		CharId:    42,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, identityv1.SellResult_SELL_RESULT_LOCK_BUSY, resp.GetResult())
}

func TestSellToShop_ZenyFull(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := economydomainmock.NewMockShopService(ctrl)
	idleSvc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(idleSvc, svc)

	svc.EXPECT().
		SellToShop(gomock.Any(), uint32(42), gomock.Any()).
		Return(uint32(0), economydomain.SellFailZenyFull, nil)

	resp, err := h.SellToShop(context.Background(), &identityv1.SellToShopRequest{
		AccountId: 7,
		CharId:    42,
		Sales:     []*identityv1.SellLine{{InvId: 1, Amount: 1, UnitPrice: 1}},
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, identityv1.SellResult_SELL_RESULT_ZENY_FULL, resp.GetResult())
}

func TestSellToShop_InternalError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := economydomainmock.NewMockShopService(ctrl)
	idleSvc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(idleSvc, svc)

	svc.EXPECT().
		SellToShop(gomock.Any(), uint32(42), gomock.Any()).
		Return(uint32(0), economydomain.SellOK, errors.New("db down"))

	resp, err := h.SellToShop(context.Background(), &identityv1.SellToShopRequest{
		AccountId: 7,
		CharId:    42,
		Sales:     []*identityv1.SellLine{{InvId: 1, Amount: 1, UnitPrice: 1}},
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "sell to shop")
}

func TestSellToShop_NilRequest(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	idleSvc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(idleSvc, economydomainmock.NewMockShopService(ctrl))

	resp, err := h.SellToShop(context.Background(), nil)
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestSellToShop_ZeroKeys(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	shop := economydomainmock.NewMockShopService(ctrl)
	idleSvc := domainmock.NewMockIdentityService(ctrl)
	h := handler.NewGRPCHandler(idleSvc, shop)

	cases := []struct {
		name string
		req  *identityv1.SellToShopRequest
	}{
		{"zero account", &identityv1.SellToShopRequest{AccountId: 0, CharId: 42}},
		{"zero char", &identityv1.SellToShopRequest{AccountId: 7, CharId: 0}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp, err := h.SellToShop(context.Background(), tc.req)
			require.Error(t, err)
			assert.Nil(t, resp)
			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, codes.InvalidArgument, st.Code())
		})
	}
}
