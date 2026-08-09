// Package shop is the shop commerce sub-module root.
package shop

import (
	"context"
	"fmt"

	"github.com/samber/do/v2"

	contentdomain "github.com/bouroo/goAthena/internal/modules/content/domain"
	economyapp "github.com/bouroo/goAthena/internal/modules/economy/app"
	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"

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

// Register provisions the ShopService into the injector. The catalog is seeded
// with one dev/starter shop ("Tool Shop") so the buy/sell path transacts
// end-to-end; real shop catalogs populate from rAthena NPC shop blocks once the
// data loader lands (M10 futurework). The dev shop is bound to a known NPC GID
// (DevShopGID) in the content ShopStore so a click on that NPC resolves to it.
func Register(inj do.Injector) {
	do.Provide(inj, func(i do.Injector) (*domain.CatalogRegistry, error) {
		catalog := newDevCatalog()
		// Bind the dev shop to its NPC GID so a shop click resolves to it. The
		// ShopStore (a content port) is required for click resolution; a missing
		// store is a wiring error surfaced here as a resolve error (not swallowed).
		shops, err := do.Invoke[contentdomain.ShopStore](i)
		if err != nil {
			return nil, fmt.Errorf("resolve shop store: %w", err)
		}
		shops.RegisterShop(DevShopGID, devShopName)
		return catalog, nil
	})
	do.Provide(inj, func(i do.Injector) (*app.ShopService, error) {
		catalog := do.MustInvoke[*domain.CatalogRegistry](i)
		items := do.MustInvoke[invdomain.ItemRepository](i)
		econSvc := do.MustInvoke[*economyapp.EconomyService](i)
		econ := economyAdapter{
			deduct: econSvc.DeductZeny,
			credit: econSvc.CreditZeny,
		}
		return app.NewShopService(catalog, items, econ), nil
	})
}
