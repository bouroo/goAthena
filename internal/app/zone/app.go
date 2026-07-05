// Package zone wires the zone service composition root (DEL-03).
//
// The zone service is stateful (Agones GameServer): map instances, AOI
// tower-grid, pathfinding, tick loop, and the embedded script engine.
package zone

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"
	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/app/common"
	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/shared/telemetry"
)

// Run builds and runs the zone service until ctx is cancelled.
func Run(ctx context.Context, cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	injector := do.New()
	do.ProvideValue(injector, cfg)

	if err := telemetry.Register(ctx, injector); err != nil {
		return fmt.Errorf("register telemetry: %w", err)
	}

	logger, err := do.Invoke[*zerolog.Logger](injector)
	if err != nil {
		return fmt.Errorf("resolve logger: %w", err)
	}

	logger.Info().
		Str("version", common.Version).
		Str("commit", common.CommitSHA).
		Str("service", "zone").
		Str("env", cfg.App.Environment).
		Msg("starting zone service")

	// TODO(DEL-03): wire Agones SDK lifecycle, NATS, Valkey, DB,
	// tick loop, AOI tower-grid, pathfinding, and script engine.

	<-ctx.Done()
	logger.Info().Msg("zone service shutting down")
	return nil
}
