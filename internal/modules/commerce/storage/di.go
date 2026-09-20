// Package storage is the storage bounded-context module root.
//
// Storage is the per-account warehouse (the rAthena `storage` table). It is
// distinct from inventory (per-character bag): moving an item between them
// is a world-module orchestration verb, not a storage-internal operation.
package storage

import (
	"github.com/samber/do/v2"
	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/modules/commerce/storage/app"
	"github.com/bouroo/goAthena/internal/modules/commerce/storage/domain"
	"github.com/bouroo/goAthena/internal/modules/commerce/storage/infra"
)

// Register provisions the GORM storage repo and the StorageService into the
// injector. Composition root (composition.go) calls this once at boot.
func Register(inj do.Injector) {
	do.Provide(inj, func(i do.Injector) (*infra.GORMStorageRepository, error) {
		gdb := do.MustInvoke[*gorm.DB](i)
		return infra.NewGORMStorageRepository(gdb), nil
	})
	do.Provide(inj, func(i do.Injector) (domain.StorageRepository, error) {
		return do.MustInvoke[*infra.GORMStorageRepository](i), nil
	})
	do.Provide(inj, func(i do.Injector) (*app.StorageService, error) {
		repo := do.MustInvoke[*infra.GORMStorageRepository](i)
		return app.NewStorageService(repo), nil
	})
}
