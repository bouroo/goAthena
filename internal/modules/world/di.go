// Package world is the world bounded-context module root. Register provisions
// the GORM world repo, the WorldService (entity registry + AOI + 50Hz tick), the
// mob_db registry, and the spawn/combat services built on it.
package world

import (
	"log/slog"
	"path/filepath"

	"github.com/samber/do/v2"
	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/internal/modules/world/infra"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
	"github.com/bouroo/goAthena/pkg/ro/mobdb"
)

// Register provisions the world module. The repo resolves the process-wide
// *gorm.DB; the WorldService is built with the configured tick rate. The mob_db
// registry is loaded best-effort from <dbPath>/pre-re (goAthena is pre-renewal):
// a missing or unreadable file degrades to an empty registry (mobs resolve 0
// DEF) rather than failing boot.
func Register(inj do.Injector, tickRateHz int, dbPath string) {
	do.Provide(inj, func(i do.Injector) (*infra.GORMWorldRepository, error) {
		gdb := do.MustInvoke[*gorm.DB](i)
		return infra.NewGORMWorldRepository(gdb), nil
	})
	do.Provide(inj, func(i do.Injector) (*app.WorldService, error) {
		repo := do.MustInvoke[*infra.GORMWorldRepository](i)
		log := do.MustInvoke[*slog.Logger](i)
		return app.NewWorldService(repo, log, tickRateHz), nil
	})
	do.Provide(inj, func(i do.Injector) (*mobdb.Registry, error) {
		log := do.MustInvoke[*slog.Logger](i)
		return loadMobDB(dbPath, log), nil
	})
	do.Provide(inj, func(i do.Injector) (*itemdb.Registry, error) {
		log := do.MustInvoke[*slog.Logger](i)
		return loadItemDB(dbPath, log), nil
	})
	do.Provide(inj, func(i do.Injector) (*app.SpawnService, error) {
		world := do.MustInvoke[*app.WorldService](i)
		mobs := do.MustInvoke[*mobdb.Registry](i)
		items := do.MustInvoke[*itemdb.Registry](i)
		return app.NewSpawnService(world, mobs, items), nil
	})
	do.Provide(inj, func(i do.Injector) (*app.CombatService, error) {
		world := do.MustInvoke[*app.WorldService](i)
		mobs := do.MustInvoke[*mobdb.Registry](i)
		return app.NewCombatService(world, mobs), nil
	})
}

// loadMobDB loads mob_db.yml from the pre-renewal db root. A load failure is
// non-fatal: the server still boots and mobs resolve 0 DEF until the operator
// points ZONE_DB_PATH at a rAthena checkout.
func loadMobDB(dbPath string, log *slog.Logger) *mobdb.Registry {
	path := filepath.Join(dbPath, "pre-re", "mob_db.yml")
	reg, err := mobdb.LoadFile(path)
	if err != nil {
		log.Warn("mob_db load failed; mobs resolve 0 DEF", "path", path, "err", err)
		return mobdb.NewRegistry()
	}
	log.Info("mob_db loaded", "path", path, "mobs", reg.Len())
	return reg
}

// loadItemDB loads item_db.yml from the pre-renewal db root. A load failure is
// non-fatal and symmetric with loadMobDB: the server still boots and mob drops
// resolve no items until the operator points ZONE_DB_PATH at a rAthena checkout.
func loadItemDB(dbPath string, log *slog.Logger) *itemdb.Registry {
	path := filepath.Join(dbPath, "pre-re", "item_db.yml")
	reg, err := itemdb.LoadFile(path)
	if err != nil {
		log.Warn("item_db load failed; mob drops resolve no items", "path", path, "err", err)
		return itemdb.NewRegistry()
	}
	log.Info("item_db loaded", "path", path, "items", reg.Len())
	return reg
}
