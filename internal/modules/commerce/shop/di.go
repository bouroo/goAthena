// Package shop is the shop commerce sub-module root.
package shop

import (
	"context"

	"github.com/samber/do/v2"

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

// Register provisions the ShopService into the injector. The catalog starts
// empty (NPC shops populate from script data in M10); buy/sell services are
// ready to be called by NPC dialog handlers.
func Register(inj do.Injector) {
	do.Provide(inj, func(_ do.Injector) (*domain.CatalogRegistry, error) {
		return domain.NewCatalogRegistry(), nil
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
