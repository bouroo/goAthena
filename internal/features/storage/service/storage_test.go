//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gomock "go.uber.org/mock/gomock"

	storagedomain "github.com/bouroo/goAthena/internal/features/storage/domain"
	storagedomainmock "github.com/bouroo/goAthena/internal/features/storage/domain/mock"
)

func setupTestService(t *testing.T) (*storageService, *storagedomainmock.MockStorageRepository, *storagedomainmock.MockLockStore) {
	ctrl := gomock.NewController(t)
	repo := storagedomainmock.NewMockStorageRepository(ctrl)
	locks := storagedomainmock.NewMockLockStore(ctrl)
	svc := NewStorageService(repo, locks, nil, 0)
	return svc.(*storageService), repo, locks
}

func setupTestServiceWithInventory(t *testing.T) (*storageService, *storagedomainmock.MockStorageRepository, *storagedomainmock.MockLockStore, *storagedomainmock.MockInventoryRepository) {
	ctrl := gomock.NewController(t)
	repo := storagedomainmock.NewMockStorageRepository(ctrl)
	locks := storagedomainmock.NewMockLockStore(ctrl)
	invRepo := storagedomainmock.NewMockInventoryRepository(ctrl)
	svc := NewStorageService(repo, locks, invRepo, 0)
	return svc.(*storageService), repo, locks, invRepo
}

func TestOpenStorage_Success(t *testing.T) {
	svc, repo, locks := setupTestService(t)
	ctx := context.Background()
	charID := uint32(1001)

	token := "test-lock-token"
	lockKey := storagedomain.StorageLockKey(charID)

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return(token, nil)
	repo.EXPECT().ListStorageByChar(ctx, charID).Return([]storagedomain.StorageItem{}, nil)
	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(nil)

	err := svc.OpenStorage(ctx, charID)

	require.NoError(t, err)
}

func TestOpenStorage_LockBusy(t *testing.T) {
	svc, _, locks := setupTestService(t)
	ctx := context.Background()
	charID := uint32(1001)

	lockKey := storagedomain.StorageLockKey(charID)

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return("", storagedomain.ErrStorageLocked)

	err := svc.OpenStorage(ctx, charID)

	assert.Error(t, err)
	assert.ErrorIs(t, err, storagedomain.ErrStorageLocked)
}

func TestDepositItem_Success(t *testing.T) {
	svc, repo, locks, invRepo := setupTestServiceWithInventory(t)
	ctx := context.Background()
	charID := uint32(1001)
	inventoryItemID := uint64(501)
	amount := int32(5)

	token := "test-lock-token"
	lockKey := storagedomain.StorageLockKey(charID)

	invItems := []storagedomain.InventoryItem{
		{ID: inventoryItemID, CharID: charID, NameID: 501, Amount: 10},
	}

	storageItems := []storagedomain.StorageItem{}

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return(token, nil)
	invRepo.EXPECT().ListByChar(ctx, charID).Return(invItems, nil)
	repo.EXPECT().CountStorageItems(ctx, charID).Return(0, nil)
	repo.EXPECT().ListStorageByChar(ctx, charID).Return(storageItems, nil)
	repo.EXPECT().CreateStorageItem(ctx, gomock.Any()).Return(nil)
	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(nil)

	err := svc.DepositItem(ctx, charID, inventoryItemID, amount)

	require.NoError(t, err)
}

func TestDepositItem_InventoryItemNotFound(t *testing.T) {
	svc, _, locks, invRepo := setupTestServiceWithInventory(t)
	ctx := context.Background()
	charID := uint32(1001)
	inventoryItemID := uint64(999)
	amount := int32(5)

	token := "test-lock-token"
	lockKey := storagedomain.StorageLockKey(charID)

	invItems := []storagedomain.InventoryItem{
		{ID: 501, CharID: charID, NameID: 501, Amount: 10},
		{ID: 502, CharID: charID, NameID: 502, Amount: 5},
	}

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return(token, nil)
	invRepo.EXPECT().ListByChar(ctx, charID).Return(invItems, nil)
	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(nil)

	err := svc.DepositItem(ctx, charID, inventoryItemID, amount)
	require.Error(t, err)
}

func TestDepositItem_StorageFull(t *testing.T) {
	svc, repo, locks, invRepo := setupTestServiceWithInventory(t)
	ctx := context.Background()
	charID := uint32(1001)
	inventoryItemID := uint64(501)
	amount := int32(5)

	token := "test-lock-token"
	lockKey := storagedomain.StorageLockKey(charID)

	invItems := []storagedomain.InventoryItem{
		{ID: inventoryItemID, CharID: charID, NameID: 501, Amount: 10},
	}

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return(token, nil)
	invRepo.EXPECT().ListByChar(ctx, charID).Return(invItems, nil)
	repo.EXPECT().CountStorageItems(ctx, charID).Return(300, nil)
	repo.EXPECT().ListStorageByChar(ctx, charID).Return([]storagedomain.StorageItem{}, nil)
	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(nil)

	err := svc.DepositItem(ctx, charID, inventoryItemID, amount)

	assert.Error(t, err)
	assert.ErrorIs(t, err, storagedomain.ErrStorageFull)
}

func TestDepositItem_InvalidAmount(t *testing.T) {
	svc, _, _, _ := setupTestServiceWithInventory(t)
	ctx := context.Background()
	charID := uint32(1001)
	inventoryItemID := uint64(501)
	amount := int32(0)

	err := svc.DepositItem(ctx, charID, inventoryItemID, amount)

	assert.Error(t, err)
	assert.ErrorIs(t, err, storagedomain.ErrInvalidItemAmount)
}

func TestDepositItem_NegativeAmount(t *testing.T) {
	svc, _, _, _ := setupTestServiceWithInventory(t)
	ctx := context.Background()
	charID := uint32(1001)
	inventoryItemID := uint64(501)
	amount := int32(-5)

	err := svc.DepositItem(ctx, charID, inventoryItemID, amount)

	assert.Error(t, err)
	assert.ErrorIs(t, err, storagedomain.ErrInvalidItemAmount)
}

func TestDepositItem_UpdateExisting(t *testing.T) {
	svc, repo, locks, invRepo := setupTestServiceWithInventory(t)
	ctx := context.Background()
	charID := uint32(1001)
	inventoryItemID := uint64(501)
	amount := int32(5)

	token := "test-lock-token"
	lockKey := storagedomain.StorageLockKey(charID)

	invItems := []storagedomain.InventoryItem{
		{ID: inventoryItemID, CharID: charID, NameID: 501, Amount: 10},
	}

	existingStorageItem := storagedomain.StorageItem{
		ID:     1,
		CharID: charID,
		NameID: 501,
		Amount: 10,
	}

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return(token, nil)
	invRepo.EXPECT().ListByChar(ctx, charID).Return(invItems, nil)
	repo.EXPECT().CountStorageItems(ctx, charID).Return(1, nil)
	repo.EXPECT().ListStorageByChar(ctx, charID).Return([]storagedomain.StorageItem{existingStorageItem}, nil)
	repo.EXPECT().UpdateStorageItem(ctx, gomock.Any()).Return(nil)
	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(nil)

	err := svc.DepositItem(ctx, charID, inventoryItemID, amount)

	require.NoError(t, err)
}

func TestDepositItem_InsufficientInventory(t *testing.T) {
	svc, _, locks, invRepo := setupTestServiceWithInventory(t)
	ctx := context.Background()
	charID := uint32(1001)
	inventoryItemID := uint64(501)
	amount := int32(15)

	token := "test-lock-token"
	lockKey := storagedomain.StorageLockKey(charID)

	invItems := []storagedomain.InventoryItem{
		{ID: inventoryItemID, CharID: charID, NameID: 501, Amount: 10},
	}

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return(token, nil)
	invRepo.EXPECT().ListByChar(ctx, charID).Return(invItems, nil)
	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(nil)

	err := svc.DepositItem(ctx, charID, inventoryItemID, amount)

	assert.Error(t, err)
	assert.ErrorIs(t, err, storagedomain.ErrInsufficientStorageItem)
}

func TestWithdrawItem_Success(t *testing.T) {
	svc, repo, locks, invRepo := setupTestServiceWithInventory(t)
	ctx := context.Background()
	charID := uint32(1001)
	storageItemID := uint64(1)
	amount := int32(5)

	token := "test-lock-token"
	lockKey := storagedomain.StorageLockKey(charID)

	storageItem := storagedomain.StorageItem{
		ID:     storageItemID,
		CharID: charID,
		NameID: 501,
		Amount: 10,
	}

	invItems := []storagedomain.InventoryItem{}

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return(token, nil)
	repo.EXPECT().GetStorageItem(ctx, storageItemID).Return(storageItem, nil)
	invRepo.EXPECT().ListByChar(ctx, charID).Return(invItems, nil)
	repo.EXPECT().UpdateStorageItem(ctx, gomock.Any()).Return(nil)
	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(nil)

	err := svc.WithdrawItem(ctx, charID, storageItemID, amount)

	require.NoError(t, err)
}

func TestWithdrawItem_StorageItemNotFound(t *testing.T) {
	svc, repo, locks, _ := setupTestServiceWithInventory(t)
	ctx := context.Background()
	charID := uint32(1001)
	storageItemID := uint64(1)
	amount := int32(5)

	token := "test-lock-token"
	lockKey := storagedomain.StorageLockKey(charID)

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return(token, nil)
	repo.EXPECT().GetStorageItem(ctx, storageItemID).Return(storagedomain.StorageItem{}, storagedomain.ErrStorageNotFound)
	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(nil)

	err := svc.WithdrawItem(ctx, charID, storageItemID, amount)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "storage item 1 not found")
}

func TestWithdrawItem_InsufficientAmount(t *testing.T) {
	svc, repo, locks, _ := setupTestServiceWithInventory(t)
	ctx := context.Background()
	charID := uint32(1001)
	storageItemID := uint64(1)
	amount := int32(15)

	token := "test-lock-token"
	lockKey := storagedomain.StorageLockKey(charID)

	storageItem := storagedomain.StorageItem{
		ID:     storageItemID,
		CharID: charID,
		NameID: 501,
		Amount: 10,
	}

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return(token, nil)
	repo.EXPECT().GetStorageItem(ctx, storageItemID).Return(storageItem, nil)
	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(nil)

	err := svc.WithdrawItem(ctx, charID, storageItemID, amount)

	assert.Error(t, err)
	assert.ErrorIs(t, err, storagedomain.ErrInsufficientStorageItem)
}

func TestWithdrawItem_InvalidAmount(t *testing.T) {
	svc, _, _, _ := setupTestServiceWithInventory(t)
	ctx := context.Background()
	charID := uint32(1001)
	storageItemID := uint64(1)
	amount := int32(0)

	err := svc.WithdrawItem(ctx, charID, storageItemID, amount)

	assert.Error(t, err)
	assert.ErrorIs(t, err, storagedomain.ErrInvalidItemAmount)
}

func TestWithdrawItem_NegativeAmount(t *testing.T) {
	svc, _, _, _ := setupTestServiceWithInventory(t)
	ctx := context.Background()
	charID := uint32(1001)
	storageItemID := uint64(1)
	amount := int32(-5)

	err := svc.WithdrawItem(ctx, charID, storageItemID, amount)

	assert.Error(t, err)
	assert.ErrorIs(t, err, storagedomain.ErrInvalidItemAmount)
}

func TestWithdrawItem_InventoryFull(t *testing.T) {
	svc, repo, locks, invRepo := setupTestServiceWithInventory(t)
	ctx := context.Background()
	charID := uint32(1001)
	storageItemID := uint64(1)
	amount := int32(5)

	token := "test-lock-token"
	lockKey := storagedomain.StorageLockKey(charID)

	storageItem := storagedomain.StorageItem{
		ID:     storageItemID,
		CharID: charID,
		NameID: 501,
		Amount: 10,
	}

	invItems := make([]storagedomain.InventoryItem, 100)

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return(token, nil)
	repo.EXPECT().GetStorageItem(ctx, storageItemID).Return(storageItem, nil)
	invRepo.EXPECT().ListByChar(ctx, charID).Return(invItems, nil)
	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(nil)

	err := svc.WithdrawItem(ctx, charID, storageItemID, amount)

	assert.Error(t, err)
	assert.ErrorIs(t, err, storagedomain.ErrInventoryFull)
}

func TestWithdrawItem_DeleteFullAmount(t *testing.T) {
	svc, repo, locks, invRepo := setupTestServiceWithInventory(t)
	ctx := context.Background()
	charID := uint32(1001)
	storageItemID := uint64(1)
	amount := int32(10)

	token := "test-lock-token"
	lockKey := storagedomain.StorageLockKey(charID)

	storageItem := storagedomain.StorageItem{
		ID:     storageItemID,
		CharID: charID,
		NameID: 501,
		Amount: 10,
	}

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return(token, nil)
	repo.EXPECT().GetStorageItem(ctx, storageItemID).Return(storageItem, nil)
	invRepo.EXPECT().ListByChar(ctx, charID).Return([]storagedomain.InventoryItem{}, nil)
	repo.EXPECT().DeleteStorageItem(ctx, storageItemID).Return(nil)
	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(nil)

	err := svc.WithdrawItem(ctx, charID, storageItemID, amount)

	require.NoError(t, err)
}

func TestWithdrawItem_ItemExistsInInventory(t *testing.T) {
	svc, repo, locks, invRepo := setupTestServiceWithInventory(t)
	ctx := context.Background()
	charID := uint32(1001)
	storageItemID := uint64(1)
	amount := int32(5)

	token := "test-lock-token"
	lockKey := storagedomain.StorageLockKey(charID)

	storageItem := storagedomain.StorageItem{
		ID:     storageItemID,
		CharID: charID,
		NameID: 501,
		Amount: 10,
	}

	invItems := make([]storagedomain.InventoryItem, 100)
	invItems[0] = storagedomain.InventoryItem{CharID: charID, NameID: 501, Amount: 5}

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return(token, nil)
	repo.EXPECT().GetStorageItem(ctx, storageItemID).Return(storageItem, nil)
	invRepo.EXPECT().ListByChar(ctx, charID).Return(invItems, nil)
	repo.EXPECT().UpdateStorageItem(ctx, gomock.Any()).Return(nil)
	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(nil)

	err := svc.WithdrawItem(ctx, charID, storageItemID, amount)

	require.NoError(t, err)
}

func TestCloseStorage_Success(t *testing.T) {
	svc, _, locks := setupTestService(t)
	ctx := context.Background()
	charID := uint32(1001)

	lockKey := storagedomain.StorageLockKey(charID)
	token := "close-1001"

	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(nil)

	err := svc.CloseStorage(ctx, charID)

	require.NoError(t, err)
}

func TestCloseStorage_ReleaseError(t *testing.T) {
	svc, _, locks := setupTestService(t)
	ctx := context.Background()
	charID := uint32(1001)

	lockKey := storagedomain.StorageLockKey(charID)
	token := "close-1001"

	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(assert.AnError)

	err := svc.CloseStorage(ctx, charID)

	assert.Error(t, err)
}

func TestDepositItem_LockBusy(t *testing.T) {
	svc, _, locks, _ := setupTestServiceWithInventory(t)
	ctx := context.Background()
	charID := uint32(1001)
	inventoryItemID := uint64(501)
	amount := int32(5)

	lockKey := storagedomain.StorageLockKey(charID)

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return("", storagedomain.ErrStorageLocked)

	err := svc.DepositItem(ctx, charID, inventoryItemID, amount)

	assert.Error(t, err)
	assert.ErrorIs(t, err, storagedomain.ErrStorageLocked)
}

func TestWithdrawItem_LockBusy(t *testing.T) {
	svc, _, locks, _ := setupTestServiceWithInventory(t)
	ctx := context.Background()
	charID := uint32(1001)
	storageItemID := uint64(1)
	amount := int32(5)

	lockKey := storagedomain.StorageLockKey(charID)

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return("", storagedomain.ErrStorageLocked)

	err := svc.WithdrawItem(ctx, charID, storageItemID, amount)

	assert.Error(t, err)
	assert.ErrorIs(t, err, storagedomain.ErrStorageLocked)
}

func TestWithdrawItem_WrongOwner(t *testing.T) {
	svc, repo, locks, _ := setupTestServiceWithInventory(t)
	ctx := context.Background()
	charID := uint32(1001)
	storageItemID := uint64(1)
	amount := int32(5)

	token := "test-lock-token"
	lockKey := storagedomain.StorageLockKey(charID)

	storageItem := storagedomain.StorageItem{
		ID:     storageItemID,
		CharID: 1002,
		NameID: 501,
		Amount: 10,
	}

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return(token, nil)
	repo.EXPECT().GetStorageItem(ctx, storageItemID).Return(storageItem, nil)
	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(nil)

	err := svc.WithdrawItem(ctx, charID, storageItemID, amount)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "does not belong to char")
}

func TestDefaultLockTTL(t *testing.T) {
	svc, _, _ := setupTestService(t)
	assert.Equal(t, 5*time.Second, svc.lockTTL)
}

func TestCustomLockTTL(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := storagedomainmock.NewMockStorageRepository(ctrl)
	locks := storagedomainmock.NewMockLockStore(ctrl)
	invRepo := storagedomainmock.NewMockInventoryRepository(ctrl)
	customTTL := 10 * time.Second
	svc := NewStorageService(repo, locks, invRepo, customTTL).(*storageService)

	assert.Equal(t, customTTL, svc.lockTTL)
}

func TestNewStorageService_ZeroTTL(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := storagedomainmock.NewMockStorageRepository(ctrl)
	locks := storagedomainmock.NewMockLockStore(ctrl)
	invRepo := storagedomainmock.NewMockInventoryRepository(ctrl)
	_ = invRepo
	svc := NewStorageService(repo, locks, invRepo, 0).(*storageService)

	assert.Equal(t, 5*time.Second, svc.lockTTL)
}

func TestDepositItem_WithoutInventoryRepo(t *testing.T) {
	svc, repo, locks := setupTestService(t)
	ctx := context.Background()
	charID := uint32(1001)
	inventoryItemID := uint64(501)
	amount := int32(5)

	token := "test-lock-token"
	lockKey := storagedomain.StorageLockKey(charID)

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return(token, nil)
	repo.EXPECT().CreateStorageItem(ctx, gomock.Any()).Return(nil)
	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(nil)

	err := svc.DepositItem(ctx, charID, inventoryItemID, amount)

	require.NoError(t, err)
}

func TestWithdrawItem_WithoutInventoryRepo(t *testing.T) {
	svc, repo, locks := setupTestService(t)
	ctx := context.Background()
	charID := uint32(1001)
	storageItemID := uint64(1)
	amount := int32(5)

	token := "test-lock-token"
	lockKey := storagedomain.StorageLockKey(charID)

	storageItem := storagedomain.StorageItem{
		ID:     storageItemID,
		CharID: charID,
		NameID: 501,
		Amount: 10,
	}

	locks.EXPECT().Acquire(ctx, lockKey, svc.lockTTL).Return(token, nil)
	repo.EXPECT().GetStorageItem(ctx, storageItemID).Return(storageItem, nil)
	repo.EXPECT().UpdateStorageItem(ctx, gomock.Any()).Return(nil)
	locks.EXPECT().Release(gomock.Any(), lockKey, token).Return(nil)

	err := svc.WithdrawItem(ctx, charID, storageItemID, amount)

	require.NoError(t, err)
}
