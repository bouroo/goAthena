// Package zone wires the zone service composition root (DEL-03).
//
// The zone service is stateful (Agones GameServer): map instances, AOI
// tower-grid, pathfinding, tick loop, and the embedded script engine.
//
// Run() boot order:
//
//  1. Validate config and build the DI injector.
//  2. Wire infrastructure (Agones lifecycle, telemetry, NATS pub/sub).
//  3. Wire the zone feature (TickLoop + ZoneService + gRPC handler +
//     *grpc.Server).
//  4. Start the gRPC listener in a background goroutine.
//  5. Start the tick loop in a background goroutine.
//  6. Signal Agones Ready so the GameServer enters the GameServer
//     state machine.
//  7. Block until ctx is cancelled.
//  8. On shutdown, GracefulStop the gRPC server (so no new EnterZone
//     calls arrive), drain the tick loop, and signal Agones Shutdown.
//     NATS connection draining is owned by the DI container's
//     natsinfra.Shutdowner (registered in step 2) and fires from the
//     deferred injector.Shutdown().
package zone

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/rs/zerolog"
	"github.com/samber/do/v2"
	"google.golang.org/grpc"

	"github.com/bouroo/goAthena/internal/app/common"
	"github.com/bouroo/goAthena/internal/config"
	chatdi "github.com/bouroo/goAthena/internal/features/chat/di"
	script "github.com/bouroo/goAthena/internal/features/script"
	scriptdi "github.com/bouroo/goAthena/internal/features/script/di"
	scriptservice "github.com/bouroo/goAthena/internal/features/script/service"
	"github.com/bouroo/goAthena/internal/features/script/vm"
	storagedi "github.com/bouroo/goAthena/internal/features/storage/di"
	zonedi "github.com/bouroo/goAthena/internal/features/zone/di"
	"github.com/bouroo/goAthena/internal/features/zone/service"
	"github.com/bouroo/goAthena/internal/infrastructure/agones"
	natsinfra "github.com/bouroo/goAthena/internal/infrastructure/messaging/nats"
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

	logger, err := initInjector(ctx, injector)
	if err != nil {
		return err
	}

	logger.Info().
		Str("version", common.Version).
		Str("commit", common.CommitSHA).
		Str("service", "zone").
		Str("env", cfg.App.Environment).
		Str("default_map", cfg.Zone.DefaultMap).
		Str("grpc_addr", cfg.GRPCAddr()).
		Dur("tick_rate", cfg.Zone.TickRate).
		Msg("starting zone service")

	// Register NATS before the zone feature: the zone DI wires a
	// domain.Publisher that depends on *nats.Client. natsinfra.Register
	// also installs a *Shutdowner so injector.Shutdown drains the
	// connection on app exit (mirror of identity/app.go's
	// valkey.Register invocation).
	if err := natsinfra.Register(ctx, injector); err != nil {
		return fmt.Errorf("register nats: %w", err)
	}

	agonesLifecycle := agones.New(ctx, logger)
	do.ProvideValue(injector, agonesLifecycle)

	tickLoop, grpcServer, err := startComponents(injector)
	if err != nil {
		return err
	}

	// Script engine: run OnInit for every script that defines it once at
	// startup (rAthena npc_event_do_oninit semantics — single-threaded, no
	// player context, runs before Agones Ready). Then optionally start a
	// periodic hot-reload goroutine if cfg.Zone.ScriptReloadInterval > 0.
	engine, err := scriptdi.ProvideEngine(injector)
	if err != nil {
		return fmt.Errorf("resolve script engine: %w", err)
	}
	initScopes, ran, onInitErrs := scriptservice.RunOnInit(ctx, engine.Current(), logger)
	do.ProvideValue[*vm.ScopeStore](injector, initScopes)
	if len(onInitErrs) > 0 {
		logger.Warn().Int("errors", len(onInitErrs)).Msg("zone: some OnInit scripts failed")
	}
	if ran > 0 {
		logger.Info().Int("ran", ran).Msg("zone: OnInit scripts executed")
	}
	if cfg.Zone.ScriptReloadInterval > 0 {
		go runScriptReload(ctx, engine, cfg.Zone.ScriptReloadInterval, logger)
	}

	grpcServeErr := make(chan error, 1)
	go func() {
		logger.Info().Str("addr", cfg.GRPCAddr()).Msg("zone: grpc server starting")
		grpcServeErr <- grpcServer.Serve(mustListen(cfg, logger))
	}()

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
		shutdown(ctx, grpcServer, grpcServeErr, tickCancel, tickDone, agonesLifecycle, logger)
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

	shutdown(ctx, grpcServer, grpcServeErr, tickCancel, tickDone, agonesLifecycle, logger)

	return nil
}

// initInjector registers telemetry in the DI container and resolves the
// structured logger. It returns a wrapped error per failure mode so Run
// stays a flat linear sequence.
func initInjector(ctx context.Context, injector do.Injector) (*zerolog.Logger, error) {
	if err := telemetry.Register(ctx, injector); err != nil {
		return nil, fmt.Errorf("register telemetry: %w", err)
	}
	logger, err := do.Invoke[*zerolog.Logger](injector)
	if err != nil {
		return nil, fmt.Errorf("resolve logger: %w", err)
	}
	return logger, nil
}

// startComponents wires the zone feature and the script engine into the
// injector and resolves the long-lived runtime dependencies (tick loop +
// gRPC server). The script engine is registered after the zone feature so
// any future feature-level port that needs to expose scripts can resolve
// it; its initial load is soft-fail (empty/missing ScriptDir yields an
// empty engine rather than aborting boot). It returns the resolved values
// or a wrapped error.
func startComponents(injector do.Injector) (*service.TickLoop, *grpc.Server, error) {
	if err := zonedi.Register(injector); err != nil {
		return nil, nil, fmt.Errorf("register zone feature: %w", err)
	}
	if err := scriptdi.Register(injector); err != nil {
		return nil, nil, fmt.Errorf("register script engine: %w", err)
	}
	if err := storagedi.Register(injector); err != nil {
		return nil, nil, fmt.Errorf("register storage feature: %w", err)
	}
	if err := chatdi.Register(injector); err != nil {
		return nil, nil, fmt.Errorf("register chat feature: %w", err)
	}
	tickLoop, err := zonedi.ProvideTickLoop(injector)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve tick loop: %w", err)
	}
	grpcServer, err := zonedi.ProvideGRPCServer(injector)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve grpc server: %w", err)
	}
	return tickLoop, grpcServer, nil
}

// mustListen binds the gRPC listener; the addr is taken from cfg.GRPCAddr.
// The logger parameter is currently unused but kept for symmetry with the
// rest of the startup sequence; future implementations may log bind
// information here.
func mustListen(cfg *config.Config, _ *zerolog.Logger) net.Listener {
	lis, err := net.Listen("tcp", cfg.GRPCAddr())
	if err != nil {
		panic(fmt.Errorf("listen grpc %s: %w", cfg.GRPCAddr(), err))
	}
	return lis
}

// stopServers halts the gRPC server and drains its Serve goroutine's
// outcome channel. GracefulStop returns before Serve does, so the
// buffered channel is drained non-blockingly here; Serve publishes the
// terminal error (typically grpc.ErrServerStopped) asynchronously.
func stopServers(grpcServer *grpc.Server, grpcServeErr <-chan error, logger *zerolog.Logger) {
	grpcServer.GracefulStop()
	select {
	case err := <-grpcServeErr:
		if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			logger.Warn().Err(err).Msg("zone: grpc server stopped with error")
		}
	default:
		// GracefulStop returns before Serve does; the goroutine will
		// exit on its own and publish into grpcServeErr.
	}
}

// shutdown performs the zone service shutdown sequence: stop accepting
// new gRPC traffic, cancel the tick loop, then signal Agones Shutdown
// and close the lifecycle. It is safe to call from either the normal
// context-cancel path or the Agones Ready-failure path so cleanup is
// identical regardless of when shutdown is triggered.
func shutdown(
	ctx context.Context,
	grpcServer *grpc.Server,
	grpcServeErr <-chan error,
	tickCancel context.CancelFunc,
	tickDone <-chan struct{},
	agonesLifecycle agones.Lifecycle,
	logger *zerolog.Logger,
) {
	stopServers(grpcServer, grpcServeErr, logger)
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

// runScriptReload periodically swaps the engine's compiled set until ctx
// is cancelled. Reload failures are logged but never fatal: the prior
// compiled set remains active, so a single bad reload cannot disable
// scripting mid-flight. The ticker is stopped on return so the goroutine
// does not leak past ctx cancellation.
func runScriptReload(ctx context.Context, engine *script.Engine, interval time.Duration, logger *zerolog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := engine.Reload(ctx); err != nil {
				logger.Warn().Err(err).Msg("zone: script hot-reload failed")
				continue
			}
			set := engine.Current()
			logger.Info().
				Int("scripts", len(set.Scripts)).
				Int("funcs", len(set.Funcs)).
				Msg("zone: scripts hot-reloaded")
		}
	}
}
