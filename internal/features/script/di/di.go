// Package di wires the script engine into the zone service's DI container.
package di

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"
	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/features/script"
)

// Register wires the script engine (lexer, parser, VM, hot-reload) into
// the DI container. It constructs the engine from cfg.Zone.ScriptDir,
// loads and compiles all scripts once at startup, and provides the
// *script.Engine for downstream consumers (the OnInit runner, dialog
// handlers). A missing or unreadable script directory logs a warning and
// leaves the engine holding an empty compiled set so the zone still boots.
func Register(c do.Injector) error {
	cfg := do.MustInvoke[*config.Config](c)
	logger := do.MustInvoke[*zerolog.Logger](c)

	engine := script.NewEngine(logger, cfg.Zone.ScriptDir)

	dir := cfg.Zone.ScriptDir

	switch dir {
	case "":
		logger.Warn().Msg("script: no script_dir configured; script engine holds an empty set")
	default:
		if loadErr := engine.Reload(context.Background()); loadErr != nil {
			// Soft-fail: a broken/absent script dir must not stop the
			// zone from booting (mirrors the mob_db soft-fail in
			// zone/di).
			logger.Warn().Err(loadErr).
				Str("script_dir", dir).
				Msg("script: initial load failed; script engine holds an empty set")
			break
		}
		set := engine.Current()
		logger.Info().
			Int("scripts", len(set.Scripts)).
			Int("funcs", len(set.Funcs)).
			Int("warps", len(set.Warps)).
			Int("shops", len(set.Shops)).
			Str("script_dir", dir).
			Msg("script engine loaded")
	}

	do.ProvideValue(c, engine)
	return nil
}

// ProvideEngine resolves the wired *script.Engine. Consumers (OnInit
// runner, dialog handlers) call this to execute compiled scripts.
func ProvideEngine(c do.Injector) (*script.Engine, error) {
	e, err := do.Invoke[*script.Engine](c)
	if err != nil {
		return nil, fmt.Errorf("resolve script engine: %w", err)
	}
	return e, nil
}
