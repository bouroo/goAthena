// Package shop is the shop commerce sub-module root.
package shop

import (
	"context"
	"fmt"

	"github.com/samber/do/v2"

	contentdomain "github.com/bouroo/goAthena/internal/modules/content/domain"
	economyapp "github.com/bouroo/goAthena/internal/modules/economy/app"
	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	itemdb "github.com/bouroo/goAthena/pkg/ro/itemdb"
	"github.com/bouroo/goAthena/pkg/ro/script"

	"github.com/bouroo/goAthena/internal/modules/commerce/shop/app"
	"github.com/bouroo/goAthena/internal/modules/commerce/shop/domain"
)

// economyAdapter bridges economy.EconomyService → shop's EconomyPort.
type economyAdapter struct {
	deduct func(context.Context, uint32, int32) error
	credit func(context.Context, uint32, int32) error
}

func (a economyAdapter) DeductZeny(ctx context.Context, charID uint32, amount int32) error {
	return a.deduct(ctx, charID, amount)
}

func (a economyAdapter) CreditZeny(ctx context.Context, charID uint32, amount int32) error {
	return a.credit(ctx, charID, amount)
}

// Register provisions the ShopService into the injector.
//
// Catalog population strategy:
//   - If the content module's CompiledScriptSet has shops (ZONE_SCRIPT_PATH was
//     configured and scripts were compiled), those shops form the catalog.
//   - item_db buy/sell prices are resolved at load time; price < 0 in the script
//     table means "use item_db default" — resolved here.
//   - If no scripts are available (empty ScriptPath or load failure), the dev
//     catalog ("Tool Shop") stands as fallback and remains bound to DevShopGID.
//
// GID binding (NPC entity click resolution) is NOT implemented in this phase:
// production shops need NPC entities to be spawned and registered in the content
// ShopStore. The dev shop retains its DevShopGID binding so existing tests pass.
func Register(inj do.Injector) {
	// Lazy factory: resolves the CompiledScriptSet from the content module
	// (which is registered before shop in composition.go) and the itemdb.Registry
	// from the world module. If either is nil/unavailable the dev catalog serves.
	do.Provide(inj, func(i do.Injector) (*domain.CatalogRegistry, error) {
		var scripts *script.CompiledScriptSet
		if s, err := do.Invoke[*script.CompiledScriptSet](i); err == nil {
			scripts = s
		}
		var items *itemdb.Registry
		if r, err := do.Invoke[*itemdb.Registry](i); err == nil {
			items = r
		}
		catalog := buildCatalog(scripts, items)
		// Bind the dev shop to its NPC GID so a shop click resolves to it.
		// The ShopStore is a content port; resolve failure is a wiring error
		// surfaced here (not swallowed).
		shops, err := do.Invoke[contentdomain.ShopStore](i)
		if err != nil {
			return nil, fmt.Errorf("resolve shop store: %w", err)
		}
		shops.RegisterShop(DevShopGID, devShopName)
		return catalog, nil
	})
	do.Provide(inj, func(i do.Injector) (*app.ShopService, error) {
		catalog := do.MustInvoke[*domain.CatalogRegistry](i)
		itemRepo := do.MustInvoke[invdomain.ItemRepository](i)
		econSvc := do.MustInvoke[*economyapp.EconomyService](i)
		econ := economyAdapter{
			deduct: econSvc.DeductZeny,
			credit: econSvc.CreditZeny,
		}
		return app.NewShopService(catalog, itemRepo, econ), nil
	})
}

// buildCatalog constructs the CatalogRegistry from compiled shop definitions.
// If scripts is nil or has no shops, the dev catalog is returned unchanged.
func buildCatalog(scripts *script.CompiledScriptSet, items *itemdb.Registry) *domain.CatalogRegistry {
	if scripts == nil || len(scripts.Shops) == 0 {
		return newDevCatalog()
	}
	var shops []domain.Shop
	for _, sd := range scripts.Shops {
		var domainItems []domain.ShopItem
		for _, si := range sd.Items {
			// rAthena: an explicit table price overrides the buy price, but
			// the SELL price is always item_db's Sell value (shops buy back
			// at the item's sell price). item_db absent → Sell = Buy/2.
			price := si.Price
			sellPrice := price / 2
			if items != nil {
				if entry := items.Get(si.ItemID); entry != nil {
					if price < 0 {
						price = entry.Buy
					}
					sellPrice = entry.Sell
				}
			}
			if price < 0 {
				// No item_db row for this entry and no explicit price: the
				// item cannot be priced; drop it rather than sell it free.
				continue
			}
			domainItems = append(domainItems, domain.ShopItem{
				NameID:    uint32(si.ItemID), //nolint:gosec // item ids fit uint32
				Price:     price,
				SellPrice: sellPrice,
			})
		}
		shops = append(shops, domain.Shop{Name: sd.Name, Items: domainItems})
	}
	if len(shops) == 0 {
		return newDevCatalog()
	}
	return domain.NewCatalogRegistry(shops...)
}
