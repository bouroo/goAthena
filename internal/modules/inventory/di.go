// Package inventory is the inventory bounded-context module root.
package inventory

import (
	"github.com/samber/do/v2"
	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/modules/inventory/app"
	"github.com/bouroo/goAthena/internal/modules/inventory/domain"
	"github.com/bouroo/goAthena/internal/modules/inventory/infra"
)

// Register provisions the GORM item repo and the InventoryService into the
// injector so the gateway LoadEndAck handler can send the inventory init burst.
func Register(inj do.Injector) {
	do.Provide(inj, func(i do.Injector) (*infra.GORMItemRepository, error) {
		gdb := do.MustInvoke[*gorm.DB](i)
		return infra.NewGORMItemRepository(gdb), nil
	})
	do.Provide(inj, func(i do.Injector) (domain.ItemRepository, error) {
		return do.MustInvoke[*infra.GORMItemRepository](i), nil
	})
	do.Provide(inj, func(i do.Injector) (*app.InventoryService, error) {
		repo := do.MustInvoke[*infra.GORMItemRepository](i)
		return app.NewInventoryService(repo), nil
	})
}
