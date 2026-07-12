package di

import (
	"context"
	"fmt"

	inventorydomain "github.com/bouroo/goAthena/internal/features/inventory/domain"
	"github.com/bouroo/goAthena/internal/features/storage/domain"
)

// inventoryRepositoryAdapter wraps the inventory feature's InventoryRepository
// to implement the storage feature's InventoryRepository interface.
type inventoryRepositoryAdapter struct {
	repo inventorydomain.InventoryRepository
}

func newInventoryRepositoryAdapter(repo inventorydomain.InventoryRepository) domain.InventoryRepository {
	return &inventoryRepositoryAdapter{repo: repo}
}

func (a *inventoryRepositoryAdapter) ListByChar(ctx context.Context, charID uint32) ([]domain.InventoryItem, error) {
	items, err := a.repo.ListByChar(ctx, charID)
	if err != nil {
		return nil, fmt.Errorf("list inventory by char (char %d): %w", charID, err)
	}

	result := make([]domain.InventoryItem, len(items))
	for i, item := range items {
		if item.Amount > uint32(1<<31-1) {
			return nil, fmt.Errorf("inventory item amount %d overflows int32", item.Amount)
		}

		result[i] = domain.InventoryItem{
			ID:     uint64(item.ID),
			CharID: item.CharID,
			NameID: item.NameID,
			Amount: int32(item.Amount),
		}
	}

	return result, nil
}
