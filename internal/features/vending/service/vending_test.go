//go:build unit

package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/features/vending/domain"
	"github.com/bouroo/goAthena/internal/features/vending/repository"
	"github.com/bouroo/goAthena/internal/features/vending/service"
)

func TestOpenShop_Success(t *testing.T) {
	t.Parallel()
	repo := repository.NewMemoryVendingRepository()
	locks := repository.NewMemoryLockStore()
	svc := service.NewVendingService(repo, locks, nil, nil, 0)

	shop := domain.VendingShop{
		OwnerID: 1,
		Title:   "My Shop",
		MapName: "prontera",
		X:       100,
		Y:       200,
		Items: []domain.VendingItem{
			{InventoryID: 10, ItemID: 501, Amount: 5, Price: 100},
		},
	}

	created, err := svc.OpenShop(context.Background(), shop)
	require.NoError(t, err)
	assert.NotEmpty(t, created.ID)
	assert.Equal(t, "My Shop", created.Title)
	assert.Len(t, created.Items, 1)
}

func TestOpenShop_AlreadyOpen(t *testing.T) {
	t.Parallel()
	repo := repository.NewMemoryVendingRepository()
	locks := repository.NewMemoryLockStore()
	svc := service.NewVendingService(repo, locks, nil, nil, 0)

	shop := domain.VendingShop{
		OwnerID: 1,
		Title:   "Shop 1",
		Items:   []domain.VendingItem{{InventoryID: 10, ItemID: 501, Amount: 5, Price: 100}},
	}

	_, err := svc.OpenShop(context.Background(), shop)
	require.NoError(t, err)

	// Opening again should fail
	_, err = svc.OpenShop(context.Background(), shop)
	assert.ErrorIs(t, err, domain.ErrShopAlreadyOpen)
}

func TestOpenShop_ValidationErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		shop      domain.VendingShop
		wantError string
	}{
		{
			name:      "empty owner",
			shop:      domain.VendingShop{Title: "T", Items: []domain.VendingItem{{ItemID: 1, Amount: 1, Price: 1}}},
			wantError: "owner_id",
		},
		{
			name:      "empty title",
			shop:      domain.VendingShop{OwnerID: 1, Items: []domain.VendingItem{{ItemID: 1, Amount: 1, Price: 1}}},
			wantError: "title is required",
		},
		{
			name:      "no items",
			shop:      domain.VendingShop{OwnerID: 1, Title: "T"},
			wantError: "at least one item",
		},
		{
			name: "zero price",
			shop: domain.VendingShop{
				OwnerID: 1, Title: "T",
				Items: []domain.VendingItem{{ItemID: 1, Amount: 1, Price: 0}},
			},
			wantError: "zero price",
		},
		{
			name: "negative amount",
			shop: domain.VendingShop{
				OwnerID: 1, Title: "T",
				Items: []domain.VendingItem{{ItemID: 1, Amount: -1, Price: 100}},
			},
			wantError: "non-positive amount",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			repo := repository.NewMemoryVendingRepository()
			locks := repository.NewMemoryLockStore()
			svc := service.NewVendingService(repo, locks, nil, nil, 0)

			_, err := svc.OpenShop(context.Background(), tt.shop)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantError)
		})
	}
}

func TestCloseShop_Success(t *testing.T) {
	t.Parallel()
	repo := repository.NewMemoryVendingRepository()
	locks := repository.NewMemoryLockStore()
	svc := service.NewVendingService(repo, locks, nil, nil, 0)

	// Open a shop first
	shop := domain.VendingShop{
		OwnerID: 1,
		Title:   "Test",
		Items:   []domain.VendingItem{{InventoryID: 10, ItemID: 501, Amount: 5, Price: 100}},
	}
	created, err := svc.OpenShop(context.Background(), shop)
	require.NoError(t, err)

	// Close it
	err = svc.CloseShop(context.Background(), 1)
	require.NoError(t, err)

	// Verify it's gone
	_, err = repo.GetShop(context.Background(), created.ID)
	assert.ErrorIs(t, err, domain.ErrShopNotFound)
}

func TestCloseShop_NoExistingShop(t *testing.T) {
	t.Parallel()
	repo := repository.NewMemoryVendingRepository()
	locks := repository.NewMemoryLockStore()
	svc := service.NewVendingService(repo, locks, nil, nil, 0)

	err := svc.CloseShop(context.Background(), 999)
	assert.ErrorIs(t, err, domain.ErrShopClosed)
}

func TestBuyItem_Success(t *testing.T) {
	t.Parallel()
	repo := repository.NewMemoryVendingRepository()
	locks := repository.NewMemoryLockStore()
	svc := service.NewVendingService(repo, locks, nil, nil, 0)

	// Open a shop
	shop := domain.VendingShop{
		OwnerID: 1,
		Title:   "Test",
		Items:   []domain.VendingItem{{InventoryID: 10, ItemID: 501, Amount: 5, Price: 100}},
	}
	created, err := svc.OpenShop(context.Background(), shop)
	require.NoError(t, err)

	// Buy 2 items
	buyerZeny, err := svc.BuyItem(context.Background(), 2, created.ID, 10, 2)
	require.NoError(t, err)
	assert.Equal(t, uint32(0), buyerZeny) // 0 because no zeny repo in test mode

	// Verify stock reduced
	items, err := svc.ListShopItems(context.Background(), created.ID)
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.Equal(t, int32(3), items[0].Amount)
}

func TestBuyItem_BuyAll_RemovesItem(t *testing.T) {
	t.Parallel()
	repo := repository.NewMemoryVendingRepository()
	locks := repository.NewMemoryLockStore()
	svc := service.NewVendingService(repo, locks, nil, nil, 0)

	shop := domain.VendingShop{
		OwnerID: 1,
		Title:   "Test",
		Items: []domain.VendingItem{
			{InventoryID: 10, ItemID: 501, Amount: 5, Price: 100},
			{InventoryID: 11, ItemID: 502, Amount: 3, Price: 200},
		},
	}
	created, err := svc.OpenShop(context.Background(), shop)
	require.NoError(t, err)

	// Buy all of item 10
	_, err = svc.BuyItem(context.Background(), 2, created.ID, 10, 5)
	require.NoError(t, err)

	// Item should be removed, but shop still has one item left
	items, err := svc.ListShopItems(context.Background(), created.ID)
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.Equal(t, uint32(11), items[0].InventoryID)
}

func TestBuyItem_BuyAll_AutoClosesShop(t *testing.T) {
	t.Parallel()
	repo := repository.NewMemoryVendingRepository()
	locks := repository.NewMemoryLockStore()
	svc := service.NewVendingService(repo, locks, nil, nil, 0)

	shop := domain.VendingShop{
		OwnerID: 1,
		Title:   "Test",
		Items:   []domain.VendingItem{{InventoryID: 10, ItemID: 501, Amount: 5, Price: 100}},
	}
	created, err := svc.OpenShop(context.Background(), shop)
	require.NoError(t, err)

	// Buy all 5 (the only item in the shop)
	_, err = svc.BuyItem(context.Background(), 2, created.ID, 10, 5)
	require.NoError(t, err)

	// Shop should be auto-deleted since it had only one item and all were bought
	_, err = svc.GetShop(context.Background(), 1)
	assert.ErrorIs(t, err, domain.ErrShopNotFound)
}

func TestBuyItem_InvalidItem(t *testing.T) {
	t.Parallel()
	repo := repository.NewMemoryVendingRepository()
	locks := repository.NewMemoryLockStore()
	svc := service.NewVendingService(repo, locks, nil, nil, 0)

	shop := domain.VendingShop{
		OwnerID: 1,
		Title:   "Test",
		Items:   []domain.VendingItem{{InventoryID: 10, ItemID: 501, Amount: 5, Price: 100}},
	}
	created, err := svc.OpenShop(context.Background(), shop)
	require.NoError(t, err)

	_, err = svc.BuyItem(context.Background(), 2, created.ID, 999, 1)
	assert.ErrorIs(t, err, domain.ErrInvalidItem)
}

func TestBuyItem_InsufficientItems(t *testing.T) {
	t.Parallel()
	repo := repository.NewMemoryVendingRepository()
	locks := repository.NewMemoryLockStore()
	svc := service.NewVendingService(repo, locks, nil, nil, 0)

	shop := domain.VendingShop{
		OwnerID: 1,
		Title:   "Test",
		Items:   []domain.VendingItem{{InventoryID: 10, ItemID: 501, Amount: 5, Price: 100}},
	}
	created, err := svc.OpenShop(context.Background(), shop)
	require.NoError(t, err)

	_, err = svc.BuyItem(context.Background(), 2, created.ID, 10, 10)
	assert.ErrorIs(t, err, domain.ErrInsufficientItems)
}

func TestBuyItem_ShopNotFound(t *testing.T) {
	t.Parallel()
	repo := repository.NewMemoryVendingRepository()
	locks := repository.NewMemoryLockStore()
	svc := service.NewVendingService(repo, locks, nil, nil, 0)

	_, err := svc.BuyItem(context.Background(), 2, "nonexistent", 10, 1)
	require.Error(t, err)
}

func TestListShopsOnMap(t *testing.T) {
	t.Parallel()
	repo := repository.NewMemoryVendingRepository()
	locks := repository.NewMemoryLockStore()
	svc := service.NewVendingService(repo, locks, nil, nil, 0)

	// Open shops on different maps
	shop1 := domain.VendingShop{OwnerID: 1, Title: "S1", MapName: "prontera", Items: []domain.VendingItem{{ItemID: 1, Amount: 1, Price: 1}}}
	shop2 := domain.VendingShop{OwnerID: 2, Title: "S2", MapName: "geffen", Items: []domain.VendingItem{{ItemID: 1, Amount: 1, Price: 1}}}
	shop3 := domain.VendingShop{OwnerID: 3, Title: "S3", MapName: "prontera", Items: []domain.VendingItem{{ItemID: 1, Amount: 1, Price: 1}}}

	_, err := svc.OpenShop(context.Background(), shop1)
	require.NoError(t, err)
	_, err = svc.OpenShop(context.Background(), shop2)
	require.NoError(t, err)
	_, err = svc.OpenShop(context.Background(), shop3)
	require.NoError(t, err)

	shops, err := svc.ListShopsOnMap(context.Background(), "prontera")
	require.NoError(t, err)
	assert.Len(t, shops, 2)

	shops, err = svc.ListShopsOnMap(context.Background(), "geffen")
	require.NoError(t, err)
	assert.Len(t, shops, 1)
}

func TestGetShop(t *testing.T) {
	t.Parallel()
	repo := repository.NewMemoryVendingRepository()
	locks := repository.NewMemoryLockStore()
	svc := service.NewVendingService(repo, locks, nil, nil, 0)

	shop := domain.VendingShop{OwnerID: 1, Title: "S", Items: []domain.VendingItem{{ItemID: 1, Amount: 1, Price: 1}}}
	_, err := svc.OpenShop(context.Background(), shop)
	require.NoError(t, err)

	result, err := svc.GetShop(context.Background(), 1)
	require.NoError(t, err)
	assert.Equal(t, "S", result.Title)
}

func TestOpenShop_TooManyItems(t *testing.T) {
	t.Parallel()
	repo := repository.NewMemoryVendingRepository()
	locks := repository.NewMemoryLockStore()
	svc := service.NewVendingService(repo, locks, nil, nil, 0)

	// Create more than MaxItemsPerShop (12) items
	items := make([]domain.VendingItem, service.MaxItemsPerShop+1)
	for i := range items {
		items[i] = domain.VendingItem{ItemID: uint32(i + 1), Amount: 1, Price: 100}
	}

	shop := domain.VendingShop{OwnerID: 1, Title: "T", Items: items}
	_, err := svc.OpenShop(context.Background(), shop)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")
}

func TestBuyItem_InvalidAmount(t *testing.T) {
	t.Parallel()
	repo := repository.NewMemoryVendingRepository()
	locks := repository.NewMemoryLockStore()
	svc := service.NewVendingService(repo, locks, nil, nil, 0)

	_, err := svc.BuyItem(context.Background(), 2, "shop", 10, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "amount must be positive")

	_, err = svc.BuyItem(context.Background(), 2, "shop", 10, -1)
	require.Error(t, err)
}
