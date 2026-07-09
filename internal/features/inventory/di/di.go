// Package di wires the inventory feature into the DI container.
//
// The inventory feature owns the persistence of per-character items
// and equipment (the rows backing rAthena's `inventory` table). The
// repository is a thin GORM adapter; this package only registers it
// into the injector so service-layer code can resolve it by interface.
//
// Bootstrap order: *gorm.DB (provided by upstream Register calls) →
// repository. No service or handler layer is wired yet — those arrive
// with the inventory use-case (WS-C) implementation.
package di

import (
	"fmt"

	"github.com/samber/do/v2"
	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/features/inventory/domain"
	"github.com/bouroo/goAthena/internal/features/inventory/repository"
)

// Register wires the inventory feature's persistence layer into the
// DI container. It depends on *gorm.DB being already registered by
// db.Register.
func Register(c do.Injector) error {
	db := do.MustInvoke[*gorm.DB](c)

	invRepo := repository.NewInventoryRepository(db)

	do.ProvideValue(c, invRepo)

	return nil
}

// ProvideInventoryRepository resolves the wired InventoryRepository.
// Other features (zone, future inventory service) call this to access
// inventory persistence without depending on the repository package.
func ProvideInventoryRepository(c do.Injector) (domain.InventoryRepository, error) {
	repo, err := do.Invoke[domain.InventoryRepository](c)
	if err != nil {
		return nil, fmt.Errorf("resolve inventory repository: %w", err)
	}
	return repo, nil
}
