package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bouroo/goAthena/internal/features/storage/domain"
)

const (
	// DefaultLockTTL is the default time-to-live for storage locks.
	DefaultLockTTL = 5 * time.Second
	releaseTimeout = 2 * time.Second
)

type storageService struct {
	repo    domain.StorageRepository
	locks   domain.LockStore
	invRepo domain.InventoryRepository
	lockTTL time.Duration
}

// NewStorageService creates a new storage service with the given repository, lock store, inventory repository, and lock TTL.
func NewStorageService(repo domain.StorageRepository, locks domain.LockStore, invRepo domain.InventoryRepository, lockTTL time.Duration) domain.StorageService {
	if lockTTL <= 0 {
		lockTTL = DefaultLockTTL
	}
	return &storageService{
		repo:    repo,
		locks:   locks,
		invRepo: invRepo,
		lockTTL: lockTTL,
	}
}

func (s *storageService) OpenStorage(ctx context.Context, charID uint32) error {
	token, res, err := s.acquire(ctx, charID)
	if err != nil {
		return err
	}
	if res == acquireLockBusy {
		return domain.ErrStorageLocked
	}
	defer s.release(ctx, charID, token)

	_, err = s.repo.ListStorageByChar(ctx, charID)
	if err != nil {
		return fmt.Errorf("list storage (char %d): %w", charID, err)
	}

	return nil
}

func (s *storageService) DepositItem(ctx context.Context, charID uint32, inventoryItemID uint64, amount int32) error {
	if amount <= 0 {
		return domain.ErrInvalidItemAmount
	}

	token, res, err := s.acquire(ctx, charID)
	if err != nil {
		return err
	}
	if res == acquireLockBusy {
		return domain.ErrStorageLocked
	}
	defer s.release(ctx, charID, token)

	if s.invRepo == nil {
		return s.depositItemWithoutValidation(ctx, charID, inventoryItemID, amount)
	}

	return s.depositItemWithValidation(ctx, charID, inventoryItemID, amount)
}

func (s *storageService) depositItemWithValidation(ctx context.Context, charID uint32, inventoryItemID uint64, amount int32) error {
	targetItem, invItems, err := s.findInventoryItem(ctx, charID, inventoryItemID)
	if err != nil {
		return err
	}

	if err := s.validateItemAmount(ctx, charID, inventoryItemID, amount, targetItem, invItems); err != nil {
		return err
	}

	existingItems, err := s.repo.ListStorageByChar(ctx, charID)
	if err != nil {
		return fmt.Errorf("list storage (char %d): %w", charID, err)
	}

	count, err := s.repo.CountStorageItems(ctx, charID)
	if err != nil {
		return fmt.Errorf("count storage items (char %d): %w", charID, err)
	}

	itemExists := s.findItemByNameID(existingItems, targetItem.NameID)

	if !itemExists {
		const maxStorageSlots = 300
		if count >= maxStorageSlots {
			return domain.ErrStorageFull
		}
	}

	return s.addOrUpdateStorageItem(ctx, charID, targetItem.NameID, amount, existingItems, itemExists)
}

func (s *storageService) findInventoryItem(ctx context.Context, charID uint32, inventoryItemID uint64) (*domain.InventoryItem, []domain.InventoryItem, error) {
	invItems, err := s.invRepo.ListByChar(ctx, charID)
	if err != nil {
		return nil, nil, fmt.Errorf("list inventory (char %d): %w", charID, err)
	}

	for i := range invItems {
		if invItems[i].ID == inventoryItemID {
			return &invItems[i], invItems, nil
		}
	}

	return nil, nil, fmt.Errorf("inventory item %d not found for char %d: %w", inventoryItemID, charID, domain.ErrInsufficientStorageItem)
}

func (s *storageService) validateItemAmount(ctx context.Context, charID uint32, inventoryItemID uint64, amount int32, targetItem *domain.InventoryItem, invItems []domain.InventoryItem) error {
	var totalAvailable int64
	for _, item := range invItems {
		if item.NameID == targetItem.NameID {
			totalAvailable += int64(item.Amount)
		}
	}

	if int64(amount) > totalAvailable {
		return domain.ErrInsufficientStorageItem
	}
	return nil
}

func (s *storageService) findItemByNameID(items []domain.StorageItem, nameID uint32) bool {
	for _, item := range items {
		if item.NameID == nameID {
			return true
		}
	}
	return false
}

func (s *storageService) addOrUpdateStorageItem(ctx context.Context, charID uint32, nameID uint32, amount int32, existingItems []domain.StorageItem, itemExists bool) error {
	now := time.Now()

	if itemExists {
		for _, item := range existingItems {
			if item.NameID == nameID {
				item.Amount += amount
				item.UpdatedAt = now
				if err := s.repo.UpdateStorageItem(ctx, item); err != nil {
					return fmt.Errorf("update storage item (id %d): %w", item.ID, err)
				}
				return nil
			}
		}
	}

	newItem := domain.StorageItem{
		AccountID: charID,
		NameID:    nameID,
		Amount:    amount,
		Identify:  1,
		Refine:    0,
		Attribute: 0,
		Card0:     0,
		Card1:     0,
		Card2:     0,
		Card3:     0,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.repo.CreateStorageItem(ctx, newItem); err != nil {
		return fmt.Errorf("create storage item (char %d, nameid %d): %w", charID, nameID, err)
	}

	return nil
}

func (s *storageService) depositItemWithoutValidation(ctx context.Context, charID uint32, inventoryItemID uint64, amount int32) error {
	now := time.Now()

	if inventoryItemID > uint64(^uint32(0)) {
		return fmt.Errorf("inventory item ID %d overflows uint32", inventoryItemID)
	}

	newItem := domain.StorageItem{
		AccountID: charID,
		NameID:    uint32(inventoryItemID),
		Amount:    amount,
		Identify:  1,
		Refine:    0,
		Attribute: 0,
		Card0:     0,
		Card1:     0,
		Card2:     0,
		Card3:     0,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.repo.CreateStorageItem(ctx, newItem); err != nil {
		return fmt.Errorf("create storage item (char %d, nameid %d): %w", charID, uint32(inventoryItemID), err)
	}

	return nil
}

func (s *storageService) WithdrawItem(ctx context.Context, charID uint32, storageItemID uint64, amount int32) error {
	if amount <= 0 {
		return domain.ErrInvalidItemAmount
	}

	token, res, err := s.acquire(ctx, charID)
	if err != nil {
		return err
	}
	if res == acquireLockBusy {
		return domain.ErrStorageLocked
	}
	defer s.release(ctx, charID, token)

	item, err := s.repo.GetStorageItem(ctx, storageItemID)
	if err != nil {
		if errors.Is(err, domain.ErrStorageNotFound) {
			return fmt.Errorf("storage item %d not found for char %d", storageItemID, charID)
		}
		return fmt.Errorf("get storage item (id %d): %w", storageItemID, err)
	}

	if item.AccountID != charID {
		return fmt.Errorf("storage item %d does not belong to char %d", storageItemID, charID)
	}

	if amount > item.Amount {
		return domain.ErrInsufficientStorageItem
	}

	if err := s.checkInventoryCapacity(ctx, charID, item); err != nil {
		return err
	}

	return s.updateOrDeleteStorageItem(ctx, storageItemID, item, amount)
}

func (s *storageService) checkInventoryCapacity(ctx context.Context, charID uint32, item domain.StorageItem) error {
	if s.invRepo == nil {
		return nil
	}

	invItems, err := s.invRepo.ListByChar(ctx, charID)
	if err != nil {
		return fmt.Errorf("list inventory (char %d): %w", charID, err)
	}

	const maxInventorySlots = 100
	if len(invItems) < maxInventorySlots {
		return nil
	}

	itemExists := s.findInventoryItemByNameID(invItems, item.NameID)
	if !itemExists {
		return domain.ErrInventoryFull
	}

	return nil
}

func (s *storageService) findInventoryItemByNameID(items []domain.InventoryItem, nameID uint32) bool {
	for _, item := range items {
		if item.NameID == nameID {
			return true
		}
	}
	return false
}

func (s *storageService) updateOrDeleteStorageItem(ctx context.Context, storageItemID uint64, item domain.StorageItem, amount int32) error {
	if amount == item.Amount {
		if err := s.repo.DeleteStorageItem(ctx, storageItemID); err != nil {
			return fmt.Errorf("delete storage item (id %d): %w", storageItemID, err)
		}
		return nil
	}

	item.Amount -= amount
	item.UpdatedAt = time.Now()

	if err := s.repo.UpdateStorageItem(ctx, item); err != nil {
		return fmt.Errorf("update storage item (id %d): %w", storageItemID, err)
	}

	return nil
}

func (s *storageService) CloseStorage(ctx context.Context, charID uint32) error {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()

	token := fmt.Sprintf("close-%d", charID)
	if err := s.locks.Release(releaseCtx, domain.StorageLockKey(charID), token); err != nil {
		return fmt.Errorf("release storage lock (char %d): %w", charID, err)
	}

	return nil
}

type acquireResult uint8

const (
	acquireOK acquireResult = iota
	acquireLockBusy
)

func (s *storageService) acquire(ctx context.Context, charID uint32) (string, acquireResult, error) {
	token, err := s.locks.Acquire(ctx, domain.StorageLockKey(charID), s.lockTTL)
	switch {
	case err == nil:
		return token, acquireOK, nil
	case errors.Is(err, domain.ErrStorageLocked):
		return "", acquireLockBusy, nil
	default:
		return "", 0, fmt.Errorf("storage lock acquire (char %d): %w", charID, err)
	}
}

func (s *storageService) release(ctx context.Context, charID uint32, token string) {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseTimeout)
	defer cancel()
	_ = s.locks.Release(releaseCtx, domain.StorageLockKey(charID), token)
}
