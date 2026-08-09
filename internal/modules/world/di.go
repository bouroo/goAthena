// Package world is the world bounded-context module root. Register provisions
// the GORM world repo, the WorldService (entity registry + AOI + 50Hz tick), the
// mob_db registry, and the spawn/combat services built on it.
package world

import (
	"log/slog"
	"path/filepath"

	"github.com/samber/do/v2"
	"gorm.io/gorm"

	economyapp "github.com/bouroo/goAthena/internal/modules/economy/app"
	invapp "github.com/bouroo/goAthena/internal/modules/inventory/app"
	"github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/internal/modules/world/infra"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
	"github.com/bouroo/goAthena/pkg/ro/mobdb"
	"github.com/bouroo/goAthena/pkg/ro/skilldb"
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
		// EquipService registers below but resolves lazily here (inventory +
		// itemdb both precede world), so PC attackers feed equipped WeaponATK
		// into melee damage.
		equip := do.MustInvoke[*app.EquipService](i)
		return app.NewCombatService(world, mobs, equip), nil
	})
	do.Provide(inj, func(i do.Injector) (*app.EquipService, error) {
		// inventory registers before world (composition.go), so its service
		// resolves here and satisfies the world EquipService's inventory port.
		inv := do.MustInvoke[*invapp.InventoryService](i)
		items := do.MustInvoke[*itemdb.Registry](i)
		return app.NewEquipService(inv, items), nil
	})
	do.Provide(inj, func(i do.Injector) (*skilldb.Registry, error) {
		log := do.MustInvoke[*slog.Logger](i)
		return loadSkillDB(dbPath, log), nil
	})
	do.Provide(inj, func(i do.Injector) (*app.SkillService, error) {
		world := do.MustInvoke[*app.WorldService](i)
		combatSvc := do.MustInvoke[*app.CombatService](i)
		skills := do.MustInvoke[*skilldb.Registry](i)
		return app.NewSkillService(world, combatSvc, skills), nil
	})
	do.Provide(inj, func(i do.Injector) (*app.TradeService, error) {
		// inventory and economy register before world (composition.go), so their
		// services resolve here. Both satisfy the trade ports directly.
		world := do.MustInvoke[*app.WorldService](i)
		inv := do.MustInvoke[*invapp.InventoryService](i)
		econ := do.MustInvoke[*economyapp.EconomyService](i)
		return app.NewTradeService(world, inv, econ), nil
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

// loadItemDB loads the pre-renewal item database from the db root. Unlike
// mob_db/skill_db, item data ships across several sibling files
// (item_db.yml + item_db_usable.yml + item_db_equip.yml + item_db_etc.yml):
// item_db.yml is a Header/Footer import manifest with zero Body entries, the
// rest carry the real rows. LoadFiles merges them (dup-AegisName/id last-wins,
// tolerant of the rAthena duplicate-mapping-key quirk). A load failure is
// non-fatal and symmetric with loadMobDB: the server still boots and mob drops
// resolve no items until the operator points ZONE_DB_PATH at a rAthena checkout.
func loadItemDB(dbPath string, log *slog.Logger) *itemdb.Registry {
	pattern := filepath.Join(dbPath, "pre-re", "item_db*.yml")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		log.Warn("item_db glob failed; mob drops resolve no items", "pattern", pattern, "err", err)
		return itemdb.NewRegistry()
	}
	if len(paths) == 0 {
		log.Warn("item_db load found no files; mob drops resolve no items", "pattern", pattern)
		return itemdb.NewRegistry()
	}
	reg, err := itemdb.LoadFiles(paths...)
	if err != nil {
		log.Warn("item_db load failed; mob drops resolve no items", "pattern", pattern, "err", err)
		return itemdb.NewRegistry()
	}
	log.Info("item_db loaded", "pattern", pattern, "files", len(paths), "items", reg.Len())
	return reg
}

// loadSkillDB loads skill_db.yml from the pre-renewal db root. A load failure is
// non-fatal and symmetric with loadMobDB/loadItemDB: the server still boots and
// skill casts resolve unknown skills until the operator points ZONE_DB_PATH at a
// rAthena checkout.
func loadSkillDB(dbPath string, log *slog.Logger) *skilldb.Registry {
	path := filepath.Join(dbPath, "pre-re", "skill_db.yml")
	reg, err := skilldb.LoadFile(path)
	if err != nil {
		log.Warn("skill_db load failed; skills resolve unknown", "path", path, "err", err)
		return skilldb.NewRegistry()
	}
	log.Info("skill_db loaded", "path", path, "skills", reg.Len())
	return reg
}
