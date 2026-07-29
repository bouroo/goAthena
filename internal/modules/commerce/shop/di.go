// Package shop is the composition point for the commerce/shop bounded context.
// It resolves the cross-module domain ports the buy use case depends on — the
// shop catalog (provided by content), the character repository, the inventory
// repository, the live player registry, and the item database — and provides
// the BuyService on the injector for the composition root to thread into the
// gateway's map-role dispatch table.
//
// The file lives at the module root (not under app/ or infra/) because the
// clean-architecture guard forbids an app-layer file from importing another
// module — and the wiring seam legitimately needs cross-module domain imports.
// Cross-module domain imports are permitted anywhere; only impl-layer imports
// are blocked.
package shop

import (
	"fmt"

	"github.com/samber/do/v2"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	shopapp "github.com/bouroo/goAthena/internal/modules/commerce/shop/app"
	shopdomain "github.com/bouroo/goAthena/internal/modules/commerce/shop/domain"
	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	worlddomain "github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
)

// Register wires the buy use case. It depends on five singleton collaborators
// each module registers (catalog from content, the others from inventory /
// character / world). A missing dependency is a wiring bug and fails boot with
// a clear error rather than disabling the buy loop silently.
func Register(c do.Injector) error {
	catalog, err := do.Invoke[shopdomain.ShopCatalog](c)
	if err != nil {
		return fmt.Errorf("shop: resolve shop catalog: %w", err)
	}
	chars, err := do.Invoke[chardomain.CharacterRepository](c)
	if err != nil {
		return fmt.Errorf("shop: resolve character repository: %w", err)
	}
	items, err := do.Invoke[invdomain.InventoryRepository](c)
	if err != nil {
		return fmt.Errorf("shop: resolve inventory repository: %w", err)
	}
	players, err := do.Invoke[*worlddomain.PlayerRegistry](c)
	if err != nil {
		return fmt.Errorf("shop: resolve player registry: %w", err)
	}
	itemDB, err := do.Invoke[*itemdb.Registry](c)
	if err != nil {
		return fmt.Errorf("shop: resolve item database: %w", err)
	}
	do.ProvideValue(c, shopapp.NewBuyService(catalog, chars, items, players, itemDB))
	return nil
}

// Compile-time assertion that the gateway handler signature matches the
// BuyService methods the composition root threads in, so a future signature
// drift surfaces at build time rather than at runtime.
var (
	_ gwdomain.PacketHandler = (*shopapp.BuyService)(nil).HandleAckSelectDealtype
	_ gwdomain.PacketHandler = (*shopapp.BuyService)(nil).HandlePurchaseItemList
)
