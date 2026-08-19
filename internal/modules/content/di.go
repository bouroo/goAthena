// Package content is the content bounded-context module root (script VM ↔ dialog).
package content

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/modules/content/app"
	"github.com/bouroo/goAthena/internal/modules/content/domain"
	"github.com/bouroo/goAthena/internal/modules/content/infra"
	worldapp "github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/pkg/ro/mobdb"
	"github.com/bouroo/goAthena/pkg/ro/script"
)

// Register provisions the NPC and shop stores, the CompiledScriptSet, and the
// script Engine. The CompiledScriptSet is populated from cfg.Zone.ScriptPath
// when set (globbed .txt files in a directory, or a single .txt); on load
// failure it is nil and the server boots with the dev catalog only. The Engine
// and domain ports are exposed as normal. The WorldService is resolved lazily
// as the effect-builtins (warp/heal) world port; a resolve failure leaves
// world nil so the server still boots.
func Register(inj do.Injector, cfg *config.Config) {
	do.Provide(inj, func(_ do.Injector) (*infra.MemoryNPCStore, error) {
		return infra.NewMemoryNPCStore(), nil
	})
	do.Provide(inj, func(_ do.Injector) (*infra.MemoryShopStore, error) {
		return infra.NewMemoryShopStore(), nil
	})
	do.Provide(inj, func(i do.Injector) (*script.CompiledScriptSet, error) {
		return compileScripts(cfg, do.MustInvoke[*slog.Logger](i)), nil
	})
	do.Provide(inj, func(i do.Injector) (*app.Engine, error) {
		npcs := do.MustInvoke[*infra.MemoryNPCStore](i)
		log := do.MustInvoke[*slog.Logger](i)
		scripts := do.MustInvoke[*script.CompiledScriptSet](i)
		var world domain.ScriptWorld
		if ws, err := do.Invoke[*worldapp.WorldService](i); err == nil {
			world = ws
		}
		return app.NewEngine(scripts, npcs, world, log), nil
	})
	do.Provide(inj, func(i do.Injector) (domain.NPCStore, error) {
		return do.MustInvoke[*infra.MemoryNPCStore](i), nil
	})
	do.Provide(inj, func(i do.Injector) (domain.ShopStore, error) {
		return do.MustInvoke[*infra.MemoryShopStore](i), nil
	})
	// The world seeder places corpus NPCs/shops/mobs. It registers after
	// world+content providers exist; composition invokes it once at boot
	// (Seed), so a resolve failure here only means an unseeded world, which
	// composition logs.
	do.Provide(inj, func(i do.Injector) (*worldapp.WorldSeeder, error) {
		world := do.MustInvoke[*worldapp.WorldService](i)
		spawn := do.MustInvoke[*worldapp.SpawnService](i)
		var mobs *mobdb.Registry
		if m, err := do.Invoke[*mobdb.Registry](i); err == nil {
			mobs = m
		}
		log := do.MustInvoke[*slog.Logger](i)
		return worldapp.NewWorldSeeder(world, spawn, mobs, log), nil
	})
}

// SeedWorld places the script corpus into the world: dialog NPCs, shop NPCs,
// and mob spawns, with GID→name registrations in the content stores. It is
// invoked once from composition after every module has registered. A nil
// seeder/set is a no-op (the empty world stands).
func SeedWorld(inj do.Injector, log *slog.Logger) {
	seeder, err := do.Invoke[*worldapp.WorldSeeder](inj)
	if err != nil {
		log.Warn("world seeder unavailable; world stays unseeded", "err", err)
		return
	}
	set, err := do.Invoke[*script.CompiledScriptSet](inj)
	if err != nil || set == nil {
		log.Info("no script corpus; world stays unseeded")
		return
	}
	npcStore, _ := do.Invoke[domain.NPCStore](inj)
	shopStore, _ := do.Invoke[domain.ShopStore](inj)
	registerNPC := func(gid uint32, name string) {
		if npcStore != nil {
			npcStore.Register(gid, name)
		}
	}
	registerShop := func(gid uint32, shopName string) {
		if shopStore != nil {
			shopStore.RegisterShop(gid, shopName)
		}
	}
	npcs, mobs := seeder.Seed(set, registerNPC, registerShop)
	log.Info("world seeded", "npcs", npcs, "mobs", mobs)
}

// compileScripts reads NPC .txt files from cfg.Zone.ScriptPath and compiles
// them into a CompiledScriptSet. A directory is walked recursively (the
// rAthena corpus nests: npc/cities, npc/pre-re/mobs/fields, ...). Files the
// NPC grammar rejects are retried as mob-spawn files (their lines are
// `map,x,y monster Name class,amount` — not NPC scripts); a file yielding
// neither is skipped with a warning. Empty ScriptPath → empty set. Total
// load failure → nil so the server boots with the dev catalog standing.
func compileScripts(cfg *config.Config, log *slog.Logger) *script.CompiledScriptSet {
	if cfg.Zone.ScriptPath == "" {
		log.Info("script_path not configured; NPC shop catalog falls back to dev shop")
		return script.NewCompiledScriptSet()
	}
	info, err := os.Stat(cfg.Zone.ScriptPath)
	if err != nil {
		log.Warn("script_path stat failed; shop catalog falls back to dev shop",
			"script_path", cfg.Zone.ScriptPath, "err", err)
		return nil
	}
	var paths []string
	if info.IsDir() {
		paths, err = collectScripts(cfg.Zone.ScriptPath)
		if err != nil || len(paths) == 0 {
			log.Warn("script_path walk found no .txt files; falling back to dev shop",
				"script_path", cfg.Zone.ScriptPath, "err", err)
			return nil
		}
	} else {
		paths = []string{cfg.Zone.ScriptPath}
	}
	set := script.NewCompiledScriptSet()
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			log.Warn("script file read failed; skipping", "path", path, "err", err)
			continue
		}
		if err := script.CompileInto(data, set); err != nil {
			// Mob-spawn files are not NPC scripts: fall back to the line
			// scanner. Zero spawns = the file is genuinely bad → skip.
			if defs := script.ParseSpawnLines(data); len(defs) > 0 {
				set.Spawns = append(set.Spawns, defs...)
				continue
			}
			log.Warn("script compile failed; skipping", "path", path, "err", err)
			continue
		}
		log.Debug("scripts compiled", "path", path)
	}
	log.Info("script corpus loaded", "npcs", len(set.NPCs), "shops", len(set.Shops), "warps", len(set.Warps), "spawns", len(set.Spawns))
	return set
}

// collectScripts walks root recursively and returns every .txt path.
func collectScripts(root string) ([]string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".txt" {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk script root: %w", err)
	}
	return paths, nil
}
