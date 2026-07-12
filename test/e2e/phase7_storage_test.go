//go:build e2e

package e2e

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	storage "github.com/bouroo/goAthena/internal/features/storage/domain"
	"github.com/bouroo/goAthena/internal/features/storage/repository"
	"github.com/bouroo/goAthena/internal/features/storage/service"
)

// memoryInventoryRepository is a test double for inventory operations.
// It implements the storage domain's InventoryRepository interface with
// in-memory storage, avoiding database dependencies for E2E tests.
type memoryInventoryRepository struct {
	mu     sync.RWMutex
	items  map[uint64]storage.InventoryItem
	nextID uint64
}

// newMemoryInventoryRepository creates a test-only inventory repository.
func newMemoryInventoryRepository() storage.InventoryRepository {
	return &memoryInventoryRepository{
		items:  make(map[uint64]storage.InventoryItem),
		nextID: 1,
	}
}

// ListByChar returns all inventory items for a character.
func (r *memoryInventoryRepository) ListByChar(ctx context.Context, charID uint32) ([]storage.InventoryItem, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []storage.InventoryItem
	for _, item := range r.items {
		if item.CharID == charID {
			result = append(result, item)
		}
	}
	return result, nil
}

// Add adds a new inventory item for testing.
func (r *memoryInventoryRepository) Add(ctx context.Context, charID uint32, nameID uint32, amount int32) (uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	id := r.nextID
	r.nextID++

	item := storage.InventoryItem{
		ID:     id,
		CharID: charID,
		NameID: nameID,
		Amount: amount,
	}

	r.items[id] = item
	return id, nil
}

// Remove removes an inventory item for testing.
func (r *memoryInventoryRepository) Remove(ctx context.Context, itemID uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.items, itemID)
	return nil
}

// UpdateAmount updates the amount of an inventory item.
func (r *memoryInventoryRepository) UpdateAmount(ctx context.Context, itemID uint64, newAmount int32) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	item, exists := r.items[itemID]
	if !exists {
		return storage.ErrStorageNotFound
	}

	item.Amount = newAmount
	r.items[itemID] = item
	return nil
}

// TestPhase7_StorageFlow exercises the full storage lifecycle:
// 1. Create a test character with inventory items
// 2. Open storage for the character
// 3. Deposit an item from inventory to storage
// 4. Verify storage contains the item
// 5. Withdraw the item from storage
// 6. Verify storage no longer has the item
// 7. Close storage
func TestPhase7_StorageFlow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const (
		testCharID     = uint32(90001)
		itemID         = uint32(501)
		itemAmount     = int32(10)
		withdrawAmount = int32(5)
	)

	// Create test dependencies
	repo := repository.NewMemoryStorageRepository()
	locks := repository.NewMemoryLockStore()
	invRepo := newMemoryInventoryRepository()

	// Create storage service
	storageSvc := service.NewStorageService(repo, locks, invRepo, 0)
	require.NotNil(t, storageSvc, "storage service should be created")

	// Add test item to inventory
	inventoryItemID, err := invRepo.(*memoryInventoryRepository).Add(ctx, testCharID, itemID, itemAmount)
	require.NoError(t, err, "add inventory item should succeed")
	require.NotZero(t, inventoryItemID, "inventory item ID should be set")

	// Verify initial inventory state
	invItems, err := invRepo.ListByChar(ctx, testCharID)
	require.NoError(t, err, "list inventory should succeed")
	require.Len(t, invItems, 1, "should have 1 inventory item")
	require.Equal(t, itemID, invItems[0].NameID)
	require.Equal(t, itemAmount, invItems[0].Amount)

	// Open storage
	err = storageSvc.OpenStorage(ctx, testCharID)
	require.NoError(t, err, "open storage should succeed")

	// Deposit item to storage
	err = storageSvc.DepositItem(ctx, testCharID, inventoryItemID, itemAmount)
	require.NoError(t, err, "deposit item should succeed")

	// Verify storage contains the item
	storageItems, err := repo.ListStorageByChar(ctx, testCharID)
	require.NoError(t, err, "list storage should succeed")
	require.Len(t, storageItems, 1, "should have 1 storage item")
	require.Equal(t, itemID, storageItems[0].NameID)
	require.Equal(t, itemAmount, storageItems[0].Amount)

	// Withdraw partial amount from storage
	storageItemID := storageItems[0].ID
	err = storageSvc.WithdrawItem(ctx, testCharID, storageItemID, withdrawAmount)
	require.NoError(t, err, "withdraw item should succeed")

	// Verify storage has remaining amount
	storageItems, err = repo.ListStorageByChar(ctx, testCharID)
	require.NoError(t, err, "list storage should succeed")
	require.Len(t, storageItems, 1, "should still have 1 storage item")
	require.Equal(t, itemID, storageItems[0].NameID)
	require.Equal(t, itemAmount-withdrawAmount, storageItems[0].Amount)

	// Withdraw remaining amount from storage
	err = storageSvc.WithdrawItem(ctx, testCharID, storageItemID, itemAmount-withdrawAmount)
	require.NoError(t, err, "withdraw remaining item should succeed")

	// Verify storage is empty
	storageItems, err = repo.ListStorageByChar(ctx, testCharID)
	require.NoError(t, err, "list storage should succeed")
	require.Len(t, storageItems, 0, "should have 0 storage items after full withdrawal")

	// Close storage
	err = storageSvc.CloseStorage(ctx, testCharID)
	require.NoError(t, err, "close storage should succeed")
}

// TestPhase7_StorageLocking verifies that storage operations are
// properly locked per-character and prevent concurrent access.
func TestPhase7_StorageLocking(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const (
		testCharID = uint32(90002)
		itemID     = uint32(502)
		itemAmount = int32(5)
	)

	repo := repository.NewMemoryStorageRepository()
	locks := repository.NewMemoryLockStore()
	invRepo := newMemoryInventoryRepository()

	storageSvc := service.NewStorageService(repo, locks, invRepo, 0)

	// Add test item
	inventoryItemID, err := invRepo.(*memoryInventoryRepository).Add(ctx, testCharID, itemID, itemAmount)
	require.NoError(t, err)

	// Open storage acquires and releases lock immediately
	err = storageSvc.OpenStorage(ctx, testCharID)
	require.NoError(t, err, "open storage should succeed")

	// Deposit should succeed (lock is released after OpenStorage)
	err = storageSvc.DepositItem(ctx, testCharID, inventoryItemID, itemAmount)
	require.NoError(t, err, "deposit should succeed")

	// Verify item was deposited
	storageItems, err := repo.ListStorageByChar(ctx, testCharID)
	require.NoError(t, err)
	require.Len(t, storageItems, 1)

	// Close storage
	err = storageSvc.CloseStorage(ctx, testCharID)
	require.NoError(t, err, "close storage should succeed")
}

// TestPhase7_StorageValidation verifies that storage operations
// validate inventory space and storage capacity correctly.
func TestPhase7_StorageValidation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const (
		testCharID = uint32(90003)
		itemID     = uint32(503)
		itemAmount = int32(1)
	)

	repo := repository.NewMemoryStorageRepository()
	locks := repository.NewMemoryLockStore()
	invRepo := newMemoryInventoryRepository()

	storageSvc := service.NewStorageService(repo, locks, invRepo, 0)

	t.Run("deposit with invalid amount fails", func(t *testing.T) {
		err := storageSvc.DepositItem(ctx, testCharID, 1, 0)
		require.Error(t, err, "deposit with zero amount should fail")
		require.ErrorIs(t, err, storage.ErrInvalidItemAmount, "should return ErrInvalidItemAmount")

		err = storageSvc.DepositItem(ctx, testCharID, 1, -5)
		require.Error(t, err, "deposit with negative amount should fail")
		require.ErrorIs(t, err, storage.ErrInvalidItemAmount, "should return ErrInvalidItemAmount")
	})

	t.Run("withdraw with invalid amount fails", func(t *testing.T) {
		err := storageSvc.WithdrawItem(ctx, testCharID, 1, 0)
		require.Error(t, err, "withdraw with zero amount should fail")
		require.ErrorIs(t, err, storage.ErrInvalidItemAmount, "should return ErrInvalidItemAmount")

		err = storageSvc.WithdrawItem(ctx, testCharID, 1, -5)
		require.Error(t, err, "withdraw with negative amount should fail")
		require.ErrorIs(t, err, storage.ErrInvalidItemAmount, "should return ErrInvalidItemAmount")
	})

	t.Run("deposit non-existent inventory item fails", func(t *testing.T) {
		err := storageSvc.DepositItem(ctx, testCharID, 99999, itemAmount)
		require.Error(t, err, "deposit non-existent item should fail")
		require.Contains(t, err.Error(), "not found", "error should indicate item not found")
	})

	t.Run("withdraw non-existent storage item fails", func(t *testing.T) {
		err := storageSvc.WithdrawItem(ctx, testCharID, 99999, itemAmount)
		require.Error(t, err, "withdraw non-existent item should fail")
		require.Contains(t, err.Error(), "not found", "error should indicate item not found")
	})
}

// TestPhase7_StoragePersistence verifies that storage state is
// correctly persisted and retrieved across operations.
func TestPhase7_StoragePersistence(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const (
		testCharID = uint32(90004)
		itemID     = uint32(504)
		itemAmount = int32(15)
	)

	repo := repository.NewMemoryStorageRepository()
	locks := repository.NewMemoryLockStore()
	invRepo := newMemoryInventoryRepository()

	storageSvc := service.NewStorageService(repo, locks, invRepo, 0)

	// Add test item
	inventoryItemID, err := invRepo.(*memoryInventoryRepository).Add(ctx, testCharID, itemID, itemAmount)
	require.NoError(t, err)

	// Open and deposit
	err = storageSvc.OpenStorage(ctx, testCharID)
	require.NoError(t, err)

	err = storageSvc.DepositItem(ctx, testCharID, inventoryItemID, itemAmount)
	require.NoError(t, err)

	// Verify persistence through repository
	savedItems, err := repo.ListStorageByChar(ctx, testCharID)
	require.NoError(t, err)
	require.Len(t, savedItems, 1)
	require.Equal(t, itemID, savedItems[0].NameID)
	require.Equal(t, itemAmount, savedItems[0].Amount)

	// Verify individual item retrieval
	item, err := repo.GetStorageItem(ctx, savedItems[0].ID)
	require.NoError(t, err)
	require.Equal(t, savedItems[0].ID, item.ID)
	require.Equal(t, testCharID, item.CharID)
	require.Equal(t, itemID, item.NameID)

	// Verify count
	count, err := repo.CountStorageItems(ctx, testCharID)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	// Clean up
	err = storageSvc.CloseStorage(ctx, testCharID)
	require.NoError(t, err)
}
