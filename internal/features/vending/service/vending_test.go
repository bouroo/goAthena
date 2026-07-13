//go:build unit

package service_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	economymock "github.com/bouroo/goAthena/internal/features/economy/domain/mock"
	inventorydomain "github.com/bouroo/goAthena/internal/features/inventory/domain"
	inventorymock "github.com/bouroo/goAthena/internal/features/inventory/domain/mock"
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

// TestBuyItem_OwnerInventoryNotOverwritten is a regression test for the
// inventory-overwrite bug: reduceOwnerInventory must compute the new
// inventory amount from the owner's ACTUAL inventory quantity, not from
// the shop-listing amount. Without the fix, a player listing 5 of 100
// potions and selling 1 would have their inventory set to 4 (losing 95).
func TestBuyItem_OwnerInventoryNotOverwritten(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)

	invRepo := inventorymock.NewMockInventoryRepository(ctrl)
	zenyRepo := economymock.NewMockCharacterZenyRepository(ctrl)
	repo := repository.NewMemoryVendingRepository()
	locks := repository.NewMemoryLockStore()
	svc := service.NewVendingService(repo, locks, invRepo, zenyRepo, 0)

	// Owner (char 1) owns 100 of item 500 at inventory row id 10.
	ownerInv := []inventorydomain.InventoryItem{
		{ID: 10, CharID: 1, NameID: 500, Amount: 100},
	}
	// ListByChar is called twice for the owner: once during OpenShop's
	// validateItemOwnership, once during BuyItem's reduceOwnerInventory.
	invRepo.EXPECT().ListByChar(gomock.Any(), uint32(1)).Return(ownerInv, nil).Times(2)

	// Buyer (char 2) has plenty of zeny.
	zenyRepo.EXPECT().GetZeny(gomock.Any(), uint32(2)).Return(uint32(100000), nil)
	// totalCost = price(100) * amount(1) = 100.
	zenyRepo.EXPECT().ExecuteBuyTx(gomock.Any(), uint32(2), uint32(100), gomock.Any()).
		Return(uint32(99900), nil)
	// Owner is credited the sale.
	zenyRepo.EXPECT().ExecuteSellTx(gomock.Any(), uint32(1), uint32(100), gomock.Any()).
		Return(uint32(100), nil)

	// THE KEY ASSERTION: UpdateAmount must receive 99 (real inventory 100
	// minus 1 sold), NOT 4 (shop stock 5 minus 1). The mock fails the
	// test if any other value is passed.
	invRepo.EXPECT().UpdateAmount(gomock.Any(), uint32(10), uint32(99)).Return(nil)

	shop := domain.VendingShop{
		OwnerID: 1,
		Title:   "potions",
		Items: []domain.VendingItem{
			{ItemID: 500, InventoryID: 10, Amount: 5, Price: 100},
		},
	}
	created, err := svc.OpenShop(context.Background(), shop)
	require.NoError(t, err)

	newZeny, err := svc.BuyItem(context.Background(), 2, created.ID, 10, 1)
	require.NoError(t, err)
	assert.Equal(t, uint32(99900), newZeny)
}

// TestBuyItem_TotalCostOverflow is a regression test for the uint32
// overflow check bypass. A price near MaxItemPrice multiplied by a small
// amount can overflow uint32 to a value that still passes the old
// `totalCost < shopItem.Price` guard. The fix computes in uint64 and
// rejects any result exceeding 0xffffffff.
func TestBuyItem_TotalCostOverflow(t *testing.T) {
	t.Parallel()
	repo := repository.NewMemoryVendingRepository()
	locks := repository.NewMemoryLockStore()
	// nil inv/zeny: OpenShop skips ownership validation; BuyItem still
	// runs the overflow guard before the zenyRepo branch.
	svc := service.NewVendingService(repo, locks, nil, nil, 0)

	// Price 1,431,655,766 * amount 4 = 5,726,623,064, which overflows
	// uint32 to 1,431,655,768 — and the old guard `totalCost < Price`
	// (1431655768 < 1431655766) is false, so it would NOT detect it.
	const overflowPrice uint32 = 1_431_655_766
	shop := domain.VendingShop{
		OwnerID: 1,
		Title:   "overflow",
		Items: []domain.VendingItem{
			{ItemID: 500, InventoryID: 10, Amount: 10, Price: overflowPrice},
		},
	}
	created, err := svc.OpenShop(context.Background(), shop)
	require.NoError(t, err)

	_, err = svc.BuyItem(context.Background(), 2, created.ID, 10, 4)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "overflow")
}

// TestRepository_GetShop_NoSliceAliasing is a regression test for the
// data race on the Items slice. GetShop returns the shop struct by
// value, but the slice header shares the repository's backing array.
// Mutating a returned shop's Items must not affect a second GetShop
// result. The fix deep-copies the Items slice on every read.
func TestRepository_GetShop_NoSliceAliasing(t *testing.T) {
	t.Parallel()
	repo := repository.NewMemoryVendingRepository()

	shop := domain.VendingShop{
		ID:      "shop-alias-1",
		OwnerID: 1,
		Title:   "aliasing",
		Items: []domain.VendingItem{
			{ItemID: 500, InventoryID: 10, Amount: 10, Price: 100},
			{ItemID: 501, InventoryID: 11, Amount: 20, Price: 200},
		},
	}
	_, err := repo.CreateShop(context.Background(), shop)
	require.NoError(t, err)

	shop1, err := repo.GetShop(context.Background(), "shop-alias-1")
	require.NoError(t, err)
	shop2, err := repo.GetShop(context.Background(), "shop-alias-1")
	require.NoError(t, err)

	require.Len(t, shop1.Items, 2)
	require.Len(t, shop2.Items, 2)

	// Mutate the first returned copy. Without the deep-copy fix, this
	// also mutates shop2 because both slices share the backing array.
	shop1.Items[0].Amount = 999

	assert.Equal(t, int32(10), shop2.Items[0].Amount, "shop2 must be unaffected by shop1 mutation")
	assert.Len(t, shop1.Items, len(shop2.Items))
}

// TestBuyItem_ConcurrentBuyers_SingleWinner is a regression test for the
// missing shop-owner lock. With only a buyer lock, concurrent buyers
// racing on the same shop could each pass the stock check and oversell.
// The fix acquires the shop-owner lock too, serializing buys on the same
// shop. Exactly one buyer must win the single unit of stock.
func TestBuyItem_ConcurrentBuyers_SingleWinner(t *testing.T) {
	t.Parallel()
	repo := repository.NewMemoryVendingRepository()
	locks := repository.NewMemoryLockStore()
	// nil inv/zeny: exercise the updateShopStock path only.
	svc := service.NewVendingService(repo, locks, nil, nil, 0)

	shop := domain.VendingShop{
		OwnerID: 1,
		Title:   "one-unit",
		Items: []domain.VendingItem{
			{ItemID: 500, InventoryID: 10, Amount: 1, Price: 1},
		},
	}
	created, err := svc.OpenShop(context.Background(), shop)
	require.NoError(t, err)

	const numBuyers = 20
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		success  int
		failures int
	)
	wg.Add(numBuyers)
	for i := 0; i < numBuyers; i++ {
		buyerID := uint32(2 + i) // distinct buyers, all hitting the same shop
		go func() {
			defer wg.Done()
			_, buyErr := svc.BuyItem(context.Background(), buyerID, created.ID, 10, 1)
			mu.Lock()
			defer mu.Unlock()
			if buyErr == nil {
				success++
			} else {
				// The single stock unit is gone after the winner's buy;
				// updateShopStock auto-deletes the empty shop, so later
				// buyers may see ErrShopNotFound. Stock exhaustion,
				// lock contention, and shop-gone are all valid losses.
				if !errors.Is(buyErr, domain.ErrInsufficientItems) &&
					!errors.Is(buyErr, domain.ErrLockBusy) &&
					!errors.Is(buyErr, domain.ErrShopNotFound) {
					t.Errorf("unexpected buy error: %v", buyErr)
				}
				failures++
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, 1, success, "exactly one buyer must win the single stock unit")
	assert.Equal(t, numBuyers-1, failures, "all other buyers must lose")
}
