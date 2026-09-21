// Package app implements the storage bounded context use cases: load an
// account's warehouse (for the ZC_STORE_* init burst), and deposit / withdraw
// items across the bag↔warehouse boundary.
//
// Storage is a SEPARATE bounded context from inventory: it owns its own
// table, its own repo, and its own service. The world module is responsible
// for cross-context orchestration — a "move to warehouse" is a world verb
// that calls inventory.Remove + storage.Add atomically.
package app

import (
	"context"
	"fmt"

	"github.com/bouroo/goAthena/internal/modules/commerce/storage/domain"
)

// StorageService is the storage use-case service.
type StorageService struct {
	repos domain.StorageRepository
}

// NewStorageService builds a StorageService backed by repo.
func NewStorageService(repo domain.StorageRepository) *StorageService {
	return &StorageService{repos: repo}
}

// LoadByAccount returns the account's warehouse rows for the ZC_STORE_*
// init burst. The order is the warehouse index the client expects.
func (s *StorageService) LoadByAccount(ctx context.Context, accountID uint32) ([]domain.StorageItem, error) {
	items, err := s.repos.LoadByAccount(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("load storage: %w", err)
	}
	return items, nil
}

// Add places amount units of an item in the account's warehouse.
// Stackable items (Equip==0) merge into an existing same-NameID row;
// equipment always inserts a new row.
func (s *StorageService) Add(ctx context.Context, accountID, nameID uint32, amount int) (domain.StorageItem, error) {
	item, err := s.repos.Add(ctx, accountID, nameID, amount)
	if err != nil {
		return domain.StorageItem{}, fmt.Errorf("add to storage: %w", err)
	}
	return item, nil
}

// Remove withdraws amount units of a stored item. Withdrawing the full
// stack deletes the row; partial withdraws decrement.
func (s *StorageService) Remove(ctx context.Context, id domain.StorageItemID, amount int) error {
	if err := s.repos.Remove(ctx, id, amount); err != nil {
		return fmt.Errorf("remove from storage: %w", err)
	}
	return nil
}

// SetEquip overwrites the equip bitmask of one stored item (0 to unequip).
// Storage-side equip is for preview windows (client storage icon), not for
// wearing — wearing happens in the inventory domain via world EquipService.
func (s *StorageService) SetEquip(ctx context.Context, id domain.StorageItemID, equip uint32) error {
	if err := s.repos.SetEquip(ctx, id, equip); err != nil {
		return fmt.Errorf("set storage equip: %w", err)
	}
	return nil
}
