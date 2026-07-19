package repository

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/bouroo/goAthena/internal/features/storage/domain"
)

type memoryStorageRepository struct {
	mu         sync.RWMutex
	items      map[uint64]domain.StorageItem
	accountIdx map[uint32][]uint64
	nextID     uint64
}

// NewMemoryStorageRepository creates a new in-memory storage repository for testing.
func NewMemoryStorageRepository() domain.StorageRepository {
	return &memoryStorageRepository{
		items:      make(map[uint64]domain.StorageItem),
		accountIdx: make(map[uint32][]uint64),
		nextID:     1,
	}
}

func (r *memoryStorageRepository) CreateStorageItem(ctx context.Context, item domain.StorageItem) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	newID := r.nextID
	r.nextID++

	item.ID = newID
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = time.Now()
	}

	r.items[newID] = item
	r.accountIdx[item.AccountID] = append(r.accountIdx[item.AccountID], newID)

	return nil
}

func (r *memoryStorageRepository) ListStorageByAccount(ctx context.Context, accountID uint32) ([]domain.StorageItem, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids, exists := r.accountIdx[accountID]
	if !exists {
		return []domain.StorageItem{}, nil
	}

	items := make([]domain.StorageItem, 0, len(ids))
	for _, id := range ids {
		if item, ok := r.items[id]; ok {
			items = append(items, item)
		}
	}

	return items, nil
}

func (r *memoryStorageRepository) GetStorageItem(ctx context.Context, itemID uint64) (domain.StorageItem, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	item, exists := r.items[itemID]
	if !exists {
		return domain.StorageItem{}, fmt.Errorf("%w: storage item %d not found", domain.ErrStorageNotFound, itemID)
	}

	return item, nil
}

func (r *memoryStorageRepository) UpdateStorageItem(ctx context.Context, item domain.StorageItem) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.items[item.ID]; !exists {
		return fmt.Errorf("%w: storage item %d not found", domain.ErrStorageNotFound, item.ID)
	}

	item.UpdatedAt = time.Now()
	r.items[item.ID] = item

	return nil
}

func (r *memoryStorageRepository) DeleteStorageItem(ctx context.Context, itemID uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	item, exists := r.items[itemID]
	if !exists {
		return fmt.Errorf("%w: storage item %d not found", domain.ErrStorageNotFound, itemID)
	}

	delete(r.items, itemID)

	accountIDs := r.accountIdx[item.AccountID]
	filtered := make([]uint64, 0, len(accountIDs))
	for _, id := range accountIDs {
		if id != itemID {
			filtered = append(filtered, id)
		}
	}
	r.accountIdx[item.AccountID] = filtered

	return nil
}

func (r *memoryStorageRepository) CountStorageItems(ctx context.Context, accountID uint32) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids, exists := r.accountIdx[accountID]
	if !exists {
		return 0, nil
	}

	return len(ids), nil
}
