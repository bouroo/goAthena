// Package inventory is the composition point for the inventory bounded
// context's DI. It wires the GORM inventory repository (the `inventory` table,
// migration 000003) and provides it under its domain port so the world use
// cases (pickup, equip) can resolve it without importing inventory/infra.
//
// This file lives at the module root rather than under app/ or infra/ because it
// must import its own infra layer. The clean-architecture guard
// (internal/app/arch_test.go) forbids an app-layer file from importing infra, but
// a module-root file is the designated wiring seam and is exempt.
package inventory

import (
	"context"
	"fmt"

	"github.com/samber/do/v2"
	"gorm.io/gorm"

	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	"github.com/bouroo/goAthena/internal/modules/inventory/infra"
)

// Register builds the inventory bounded context over the GORM inventory
// repository and provides it as the domain port. The world module resolves the
// port via do.Invoke (the same widen-the-injector's-view pattern character/di.go
// uses) so pickup/equip use cases never import inventory/infra. ctx is accepted
// to match the samber/do v2 Register convention but is unused — the repository
// derives a per-request context from the use-case call.
func Register(_ context.Context, c do.Injector) error {
	db, err := do.Invoke[*gorm.DB](c)
	if err != nil {
		return fmt.Errorf("inventory: resolve gorm db: %w", err)
	}
	repo := infra.NewGORMInventoryRepository(db)
	// Provide the repository as the domain port so other bounded contexts (the
	// world module's pickup/equip flows) can resolve it via structural
	// satisfaction without importing inventory/infra.
	do.ProvideValue(c, invdomain.InventoryRepository(repo))
	return nil
}
