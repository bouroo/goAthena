// Package world is the world bounded-context module root. Register provisions
// the GORM world repo and the WorldService (entity registry + AOI + 50Hz tick).
package world

import (
	"log/slog"

	"github.com/samber/do/v2"
	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/internal/modules/world/infra"
)

// Register provisions the world module. The repo resolves the process-wide
// *gorm.DB; the WorldService is built with the configured tick rate.
func Register(inj do.Injector, tickRateHz int) {
	do.Provide(inj, func(i do.Injector) (*infra.GORMWorldRepository, error) {
		gdb := do.MustInvoke[*gorm.DB](i)
		return infra.NewGORMWorldRepository(gdb), nil
	})
	do.Provide(inj, func(i do.Injector) (*app.WorldService, error) {
		repo := do.MustInvoke[*infra.GORMWorldRepository](i)
		log := do.MustInvoke[*slog.Logger](i)
		return app.NewWorldService(repo, log, tickRateHz), nil
	})
}
