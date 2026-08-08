package infra

import (
	"context"
	"sync"

	"github.com/bouroo/goAthena/internal/modules/inventory/domain"
)

// MemoryItemRepository is an in-memory inventory store for unit tests.
type MemoryItemRepository struct {
	mu    sync.Mutex
	items map[domain.ItemID]domain.Item
	next  uint32
}

// NewMemoryItemRepository creates an empty in-memory repo.
func NewMemoryItemRepository() *MemoryItemRepository {
	return &MemoryItemRepository{items: make(map[domain.ItemID]domain.Item)}
}

// LoadByChar returns the character's items.
func (r *MemoryItemRepository) LoadByChar(_ context.Context, _, charID uint32) ([]domain.Item, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.Item, 0)
	for _, it := range r.items {
		if it.CharID == charID {
			out = append(out, it)
		}
	}
	return out, nil
}

// Add inserts a new item row.
//
//nolint:gosec // G115: amount validated >0 before the uint32 cast.
func (r *MemoryItemRepository) Add(_ context.Context, charID, nameID uint32, amount int) (domain.Item, error) {
	if amount <= 0 {
		return domain.Item{}, domain.ErrInsufficientAmount
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	item := domain.Item{ID: domain.ItemID(r.next), CharID: charID, NameID: nameID, Amount: uint32(amount), Identify: 1}
	r.items[item.ID] = item
	return item, nil
}

// Remove decrements amount and deletes the row when it hits zero.
//
//nolint:gosec // G115: amount validated >0 before the uint32 cast.
func (r *MemoryItemRepository) Remove(_ context.Context, id domain.ItemID, amount int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.items[id]
	if !ok {
		return domain.ErrItemNotFound
	}
	if int(item.Amount) < amount {
		return domain.ErrInsufficientAmount
	}
	if int(item.Amount) == amount {
		delete(r.items, id)
		return nil
	}
	item.Amount -= uint32(amount)
	r.items[id] = item
	return nil
}
