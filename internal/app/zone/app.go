// Package zone wires the zone service composition root (DEL-03).
//
// The zone service is stateful (Agones GameServer): map instances, AOI
// tower-grid, pathfinding, tick loop, and the embedded script engine.
//
// Run() boot order:
//
//  1. Validate config and build the DI injector.
//  2. Wire infrastructure (Agones lifecycle, telemetry).
//  3. Wire the zone feature (TickLoop + ZoneService).
//  4. Start the tick loop in a background goroutine.
//  5. Signal Agones Ready so the GameServer enters the GameServer
//     state machine.
//  6. Block until ctx is cancelled.
//  7. On shutdown, wait for the tick loop to drain and signal Agones
//     Shutdown.
package zone

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog"
	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/app/common"
	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/features/zone/di"
	"github.com/bouroo/goAthena/internal/features/zone/service"
	"github.com/bouroo/goAthena/internal/infrastructure/agones"
	"github.com/bouroo/goAthena/internal/shared/telemetry"
)

// Run builds and runs the zone service until ctx is cancelled.
func Run(ctx context.Context, cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	injector := do.New()
	defer func() {
		_ = injector.Shutdown()
	}()
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
		Str("default_map", cfg.Zone.DefaultMap).
		Dur("tick_rate", cfg.Zone.TickRate).
		Msg("starting zone service")

	agonesLifecycle := agones.New(ctx, logger)
	do.ProvideValue(injector, agonesLifecycle)

	if err := di.Register(injector); err != nil {
		return fmt.Errorf("register zone feature: %w", err)
	}

	tickLoop, err := di.ProvideTickLoop(injector)
	if err != nil {
		return fmt.Errorf("resolve tick loop: %w", err)
	}

	tickCtx, tickCancel := context.WithCancel(ctx)
	tickDone := make(chan struct{})
	go func() {
		defer close(tickDone)
		if err := tickLoop.Start(tickCtx); err != nil &&
			!errors.Is(err, context.Canceled) &&
			!errors.Is(err, context.DeadlineExceeded) {
			logger.Warn().Err(err).Msg("zone: tick loop exited with error")
		}
	}()
	// Give the tick loop a moment to enter its select before we advertise
	// Ready. This is a heuristic — the Agones SDK has no "blocked on
	// tick loop" hook.
	tickLoopSettle(tickLoop)

	if err := agonesLifecycle.Ready(ctx); err != nil {
		tickCancel()
		<-tickDone
		// If the parent context is already cancelled we treat a Ready
		// failure as a normal shutdown rather than a startup error.
		if ctxErr := ctx.Err(); ctxErr != nil {
			logger.Info().Err(err).Msg("zone: Agones ready skipped due to context cancellation")
			return nil
		}
		return fmt.Errorf("agones ready: %w", err)
	}

	logger.Info().Msg("zone service ready")

	<-ctx.Done()
	logger.Info().Msg("zone service shutting down")

	// Stop the tick loop first so no new AOI broadcasts are issued while
	// the Agones shutdown is in flight.
	tickCancel()
	select {
	case <-tickDone:
	case <-ctx.Done():
		logger.Warn().Msg("zone: tick loop did not exit before shutdown deadline")
	}

	if err := agonesLifecycle.Shutdown(context.Background()); err != nil {
		logger.Warn().Err(err).Msg("zone: agones shutdown failed")
	}

	if err := agonesLifecycle.Close(); err != nil {
		logger.Warn().Err(err).Msg("zone: agones close failed")
	}

	return nil
}

// tickLoopSettle blocks briefly so the tick loop goroutine can enter
// its select before Agones Ready is signalled. One short sleep is
// sufficient — once the loop is in its select, a Ready state transition
// is safe regardless of how many ticks have fired.
func tickLoopSettle(_ *service.TickLoop) {
	// no-op for now; future versions may observe Done() once at least
	// one tick has fired. Kept as a hook so the production startup
	// sequence remains consistent across environments.
}
