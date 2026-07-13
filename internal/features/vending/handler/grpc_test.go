//go:build unit

package handler_test

import (
	"context"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	"github.com/bouroo/goAthena/internal/features/vending/domain"
	domainmock "github.com/bouroo/goAthena/internal/features/vending/domain/mock"
	vendinghandler "github.com/bouroo/goAthena/internal/features/vending/handler"
)

func newTestHandler(t *testing.T, svc domain.VendingService) zonev1.ZoneServiceServer {
	t.Helper()
	logger := zerolog.Nop()
	return vendinghandler.NewVendingHandler(svc, &logger)
}

func TestOpenVendingShop_Success(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	mockSvc := domainmock.NewMockVendingService(ctrl)

	expectedShop := domain.VendingShop{
		ID:      "shop-123",
		OwnerID: 1,
		Title:   "Test Shop",
		MapName: "prontera",
		X:       100, Y: 200,
		Items: []domain.VendingItem{{InventoryID: 10, ItemID: 501, Amount: 5, Price: 100}},
	}

	mockSvc.EXPECT().OpenShop(gomock.Any(), gomock.Any()).Return(expectedShop, nil)

	h := newTestHandler(t, mockSvc)
	resp, err := h.OpenVendingShop(context.Background(), &zonev1.OpenVendingShopRequest{
		OwnerCharId: 1,
		Title:       "Test Shop",
		MapName:     "prontera",
		X:           100, Y: 200,
		Items: []*zonev1.VendingItemInfo{
			{InventoryId: 10, ItemId: 501, Amount: 5, Price: 100},
		},
	})

	require.NoError(t, err)
	assert.True(t, resp.GetSuccess())
	assert.Equal(t, "shop-123", resp.GetShop().GetShopId())
	assert.Equal(t, "Test Shop", resp.GetShop().GetTitle())
}

func TestOpenVendingShop_ValidationError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	mockSvc := domainmock.NewMockVendingService(ctrl)
	h := newTestHandler(t, mockSvc)

	// Missing owner
	_, err := h.OpenVendingShop(context.Background(), &zonev1.OpenVendingShopRequest{
		Title: "T",
	})
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	// Missing title
	_, err = h.OpenVendingShop(context.Background(), &zonev1.OpenVendingShopRequest{
		OwnerCharId: 1,
	})
	st, ok = status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestCloseVendingShop_Success(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	mockSvc := domainmock.NewMockVendingService(ctrl)
	mockSvc.EXPECT().CloseShop(gomock.Any(), uint32(1)).Return(nil)

	h := newTestHandler(t, mockSvc)
	resp, err := h.CloseVendingShop(context.Background(), &zonev1.CloseVendingShopRequest{
		OwnerCharId: 1,
	})
	require.NoError(t, err)
	assert.True(t, resp.GetSuccess())
}

func TestCloseVendingShop_Error(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	mockSvc := domainmock.NewMockVendingService(ctrl)
	mockSvc.EXPECT().CloseShop(gomock.Any(), uint32(1)).Return(domain.ErrShopClosed)

	h := newTestHandler(t, mockSvc)
	resp, err := h.CloseVendingShop(context.Background(), &zonev1.CloseVendingShopRequest{
		OwnerCharId: 1,
	})
	require.NoError(t, err)
	assert.False(t, resp.GetSuccess())
	assert.Contains(t, resp.GetErrorMessage(), "closed")
}

func TestBuyVendingItem_Success(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	mockSvc := domainmock.NewMockVendingService(ctrl)
	mockSvc.EXPECT().BuyItem(gomock.Any(), uint32(2), "shop-1", uint32(10), int32(3)).Return(uint32(9700), nil)

	h := newTestHandler(t, mockSvc)
	resp, err := h.BuyVendingItem(context.Background(), &zonev1.BuyVendingItemRequest{
		BuyerCharId: 2,
		ShopId:      "shop-1",
		InventoryId: 10,
		Amount:      3,
	})
	require.NoError(t, err)
	assert.True(t, resp.GetSuccess())
	assert.Equal(t, uint32(9700), resp.GetBuyerZeny())
}

func TestBuyVendingItem_ValidationError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	mockSvc := domainmock.NewMockVendingService(ctrl)
	h := newTestHandler(t, mockSvc)

	tests := []struct {
		name string
		req  *zonev1.BuyVendingItemRequest
	}{
		{"missing buyer", &zonev1.BuyVendingItemRequest{ShopId: "s", Amount: 1}},
		{"missing shop", &zonev1.BuyVendingItemRequest{BuyerCharId: 1, Amount: 1}},
		{"zero amount", &zonev1.BuyVendingItemRequest{BuyerCharId: 1, ShopId: "s"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := h.BuyVendingItem(context.Background(), tt.req)
			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, codes.InvalidArgument, st.Code())
		})
	}
}

func TestListVendingShops_Success(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	mockSvc := domainmock.NewMockVendingService(ctrl)
	mockSvc.EXPECT().ListShopsOnMap(gomock.Any(), "prontera").Return([]domain.VendingShop{
		{ID: "s1", OwnerID: 1, Title: "Shop1", MapName: "prontera"},
		{ID: "s2", OwnerID: 2, Title: "Shop2", MapName: "prontera"},
	}, nil)

	h := newTestHandler(t, mockSvc)
	resp, err := h.ListVendingShops(context.Background(), &zonev1.ListVendingShopsRequest{
		MapName: "prontera",
	})
	require.NoError(t, err)
	assert.True(t, resp.GetSuccess())
	assert.Len(t, resp.GetShops(), 2)
}

func TestListVendingShops_MissingMap(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	mockSvc := domainmock.NewMockVendingService(ctrl)
	h := newTestHandler(t, mockSvc)

	_, err := h.ListVendingShops(context.Background(), &zonev1.ListVendingShopsRequest{})
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestGetVendingShop_Success(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	mockSvc := domainmock.NewMockVendingService(ctrl)
	mockSvc.EXPECT().GetShop(gomock.Any(), uint32(1)).Return(domain.VendingShop{
		ID: "s1", OwnerID: 1, Title: "My Shop",
	}, nil)

	h := newTestHandler(t, mockSvc)
	resp, err := h.GetVendingShop(context.Background(), &zonev1.GetVendingShopRequest{
		OwnerCharId: 1,
	})
	require.NoError(t, err)
	assert.True(t, resp.GetSuccess())
	assert.Equal(t, "My Shop", resp.GetShop().GetTitle())
}

func TestGetVendingShop_MissingOwner(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	mockSvc := domainmock.NewMockVendingService(ctrl)
	h := newTestHandler(t, mockSvc)

	_, err := h.GetVendingShop(context.Background(), &zonev1.GetVendingShopRequest{})
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestOpenVendingShop_ServiceError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	mockSvc := domainmock.NewMockVendingService(ctrl)
	mockSvc.EXPECT().OpenShop(gomock.Any(), gomock.Any()).Return(domain.VendingShop{}, domain.ErrShopAlreadyOpen)

	h := newTestHandler(t, mockSvc)
	resp, err := h.OpenVendingShop(context.Background(), &zonev1.OpenVendingShopRequest{
		OwnerCharId: 1,
		Title:       "T",
		Items:       []*zonev1.VendingItemInfo{{ItemId: 1, Amount: 1, Price: 1}},
	})
	require.NoError(t, err)
	assert.False(t, resp.GetSuccess())
	assert.Contains(t, resp.GetErrorMessage(), "already open")
}
