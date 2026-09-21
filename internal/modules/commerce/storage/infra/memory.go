package infra

import (
	"cmp"
	"context"
	"slices"
	"sync"

	"github.com/bouroo/goAthena/internal/modules/commerce/storage/domain"
)

// MemoryStorageRepository is an in-memory storage store for unit tests.
// Same contract as GORMStorageRepository; ordered by id on read.
type MemoryStorageRepository struct {
	mu    sync.RWMutex
	items map[domain.StorageItemID]domain.StorageItem
	next  uint32
}

// NewMemoryStorageRepository creates an empty in-memory repo.
func NewMemoryStorageRepository() *MemoryStorageRepository {
	return &MemoryStorageRepository{items: make(map[domain.StorageItemID]domain.StorageItem)}
}

// LoadByAccount returns the account's items ordered by row id, mirroring the
// GORMStorageRepository's `ORDER BY id`. The ordering is load-bearing, not
// cosmetic: the warehouse index the client sends is an offset into this list.
func (r *MemoryStorageRepository) LoadByAccount(_ context.Context, accountID uint32) ([]domain.StorageItem, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.StorageItem, 0)
	for _, it := range r.items {
		if it.AccountID == accountID {
			out = append(out, it)
		}
	}
	slices.SortFunc(out, func(a, b domain.StorageItem) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

// Add inserts a new row. Equipment always inserts a new row; stackable items
// merge into an existing same-NameID row when one exists. Returns
// ErrStorageFull when the account is at the ceiling for a non-stacking insert.
//
//nolint:gosec // G115: amount validated >0 before the uint32 cast.
func (r *MemoryStorageRepository) Add(_ context.Context, accountID, nameID uint32, amount int) (domain.StorageItem, error) {
	if amount <= 0 {
		return domain.StorageItem{}, domain.ErrStorageInsufficientAmount
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Stack into an existing same-NameID row when the item is non-equipped.
	for id, it := range r.items {
		if it.AccountID == accountID && it.NameID == nameID && it.Equip == 0 {
			it.Amount += uint32(amount)
			r.items[id] = it
			return it, nil
		}
	}
	if r.accountCountLocked(accountID) >= domain.MaxStorage {
		return domain.StorageItem{}, domain.ErrStorageFull
	}
	r.next++
	item := domain.StorageItem{
		ID:        domain.StorageItemID(r.next),
		AccountID: accountID,
		NameID:    nameID,
		Amount:    uint32(amount),
		Identify:  1,
	}
	r.items[item.ID] = item
	return item, nil
}

// Remove decrements amount and deletes the row when it hits zero.
//
//nolint:gosec // G115: amount validated >0 before the uint32 cast.
func (r *MemoryStorageRepository) Remove(_ context.Context, id domain.StorageItemID, amount int) error {
	if amount <= 0 {
		return domain.ErrStorageInsufficientAmount
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.items[id]
	if !ok {
		return domain.ErrStorageItemNotFound
	}
	if int(item.Amount) < amount {
		return domain.ErrStorageInsufficientAmount
	}
	if int(item.Amount) == amount {
		delete(r.items, id)
		return nil
	}
	item.Amount -= uint32(amount)
	r.items[id] = item
	return nil
}

// SetEquip overwrites the equip bitmask of one storage row (0 to unequip).
func (r *MemoryStorageRepository) SetEquip(_ context.Context, id domain.StorageItemID, equip uint32) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.items[id]
	if !ok {
		return domain.ErrStorageItemNotFound
	}
	item.Equip = equip
	r.items[id] = item
	return nil
}

// accountCountLocked is the number of rows the account owns. Caller holds mu.
func (r *MemoryStorageRepository) accountCountLocked(accountID uint32) int {
	n := 0
	for _, it := range r.items {
		if it.AccountID == accountID {
			n++
		}
	}
	return n
}
