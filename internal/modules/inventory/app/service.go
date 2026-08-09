// Package app implements the inventory bounded context use cases: load a char's
// items (for the LoadEndAck init burst), and add/remove items.
package app

import (
	"context"
	"fmt"

	"github.com/bouroo/goAthena/internal/modules/inventory/domain"
)

// InventoryService is the inventory use-case service.
type InventoryService struct {
	repos domain.ItemRepository
}

// NewInventoryService builds an InventoryService backed by repo.
func NewInventoryService(repo domain.ItemRepository) *InventoryService {
	return &InventoryService{repos: repo}
}

// LoadByChar returns the character's inventory for the LoadEndAck init burst.
func (s *InventoryService) LoadByChar(ctx context.Context, accountID, charID uint32) ([]domain.Item, error) {
	items, err := s.repos.LoadByChar(ctx, accountID, charID)
	if err != nil {
		return nil, fmt.Errorf("load inventory: %w", err)
	}
	return items, nil
}

// Add grants an item to a character (amount > 0).
func (s *InventoryService) Add(ctx context.Context, charID, nameID uint32, amount int) (domain.Item, error) {
	item, err := s.repos.Add(ctx, charID, nameID, amount)
	if err != nil {
		return domain.Item{}, fmt.Errorf("add item: %w", err)
	}
	return item, nil
}

// Remove consumes amount units of an item.
func (s *InventoryService) Remove(ctx context.Context, id domain.ItemID, amount int) error {
	if err := s.repos.Remove(ctx, id, amount); err != nil {
		return fmt.Errorf("remove item: %w", err)
	}
	return nil
}

// SetEquip overwrites the equip bitmask of one item row (0 to unequip). Used by
// the world EquipService to wear/remove gear.
func (s *InventoryService) SetEquip(ctx context.Context, id domain.ItemID, equip uint32) error {
	if err := s.repos.SetEquip(ctx, id, equip); err != nil {
		return fmt.Errorf("set equip: %w", err)
	}
	return nil
}
