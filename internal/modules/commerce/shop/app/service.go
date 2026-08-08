// Package app implements shop buy/sell use cases. Buy deducts zeny via the
// economy port and adds the item via the inventory port; sell does the reverse.
// Both coordinate two bounded contexts through injected ports.
package app

import (
	"context"
	"fmt"

	"github.com/bouroo/goAthena/internal/modules/commerce/shop/domain"
	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
)

// EconomyPort is the narrow port the shop service needs from the economy
// bounded context (credit/deduct zeny). Defining it locally avoids importing
// economy/app (clean-architecture: commerce imports only ports, not services).
type EconomyPort interface {
	DeductZeny(ctx context.Context, charID uint32, amount int32) error
	CreditZeny(ctx context.Context, charID uint32, amount int32) error
}

// ShopService resolves buy/sell transactions against an NPC shop catalog.
type ShopService struct {
	catalog *domain.CatalogRegistry
	items   invdomain.ItemRepository
	econ    EconomyPort
}

// NewShopService builds a ShopService.
func NewShopService(catalog *domain.CatalogRegistry, items invdomain.ItemRepository, econ EconomyPort) *ShopService {
	return &ShopService{catalog: catalog, items: items, econ: econ}
}

// Buy charges zeny and grants amount units of nameID from the named shop.
func (s *ShopService) Buy(ctx context.Context, charID uint32, shopName string, nameID uint32, amount int) error {
	shop, ok := s.catalog.Get(shopName)
	if !ok {
		return fmt.Errorf("shop %q: not found", shopName)
	}
	item, ok := shop.FindBuy(nameID)
	if !ok {
		return fmt.Errorf("item %d not in shop %q", nameID, shopName)
	}
	total := item.Price * int32(amount) //nolint:gosec // G115: amount is player-bounded stack count.
	if err := s.econ.DeductZeny(ctx, charID, total); err != nil {
		return fmt.Errorf("buy deduct zeny: %w", err)
	}
	if _, err := s.items.Add(ctx, charID, nameID, amount); err != nil {
		return fmt.Errorf("buy add item: %w", err)
	}
	return nil
}

// Sell removes amount units of the item and credits zeny at the shop's sell price.
func (s *ShopService) Sell(ctx context.Context, charID uint32, itemID invdomain.ItemID, _ uint32, amount int, sellPrice int32) error {
	if err := s.items.Remove(ctx, itemID, amount); err != nil {
		return fmt.Errorf("sell remove item: %w", err)
	}
	total := sellPrice * int32(amount) //nolint:gosec // G115: amount is player-bounded stack count.
	if err := s.econ.CreditZeny(ctx, charID, total); err != nil {
		return fmt.Errorf("sell credit zeny: %w", err)
	}
	return nil
}
