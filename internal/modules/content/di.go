// Package content is the content bounded-context module root (script VM ↔ dialog).
package content

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/modules/content/app"
	"github.com/bouroo/goAthena/internal/modules/content/domain"
	"github.com/bouroo/goAthena/internal/modules/content/infra"
	worldapp "github.com/bouroo/goAthena/internal/modules/world/app"
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
}

// compileScripts reads NPC .txt files from cfg.Zone.ScriptPath and compiles
// them into a CompiledScriptSet. Empty ScriptPath → returns an empty set.
// Load failure → logged warning, returns nil so the server boots with the dev
// catalog standing.
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
		m, err := filepath.Glob(filepath.Join(cfg.Zone.ScriptPath, "*.txt"))
		if err != nil || len(m) == 0 {
			log.Warn("script_path glob found no .txt files; falling back to dev shop",
				"script_path", cfg.Zone.ScriptPath, "err", err)
			return nil
		}
		paths = m
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
			log.Warn("script compile failed; skipping", "path", path, "err", err)
			continue
		}
		log.Info("scripts compiled", "path", path, "shops", len(set.Shops))
	}
	return set
}
