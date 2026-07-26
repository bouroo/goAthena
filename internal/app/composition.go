// Package app is the modular-monolith composition root: the one place that
// knows how to build and start the whole process. It wires infrastructure
// providers and feature modules into a single samber/do injector and runs the
// application orchestrator until shutdown.
package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog"
	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/infrastructure/db"
	"github.com/bouroo/goAthena/internal/infrastructure/messaging/nats"
	"github.com/bouroo/goAthena/internal/infrastructure/messaging/valkey"
	"github.com/bouroo/goAthena/internal/modules/account"
	accountapp "github.com/bouroo/goAthena/internal/modules/account/app"
	"github.com/bouroo/goAthena/internal/modules/character"
	characterapp "github.com/bouroo/goAthena/internal/modules/character/app"
	"github.com/bouroo/goAthena/internal/modules/gateway"
	gwapp "github.com/bouroo/goAthena/internal/modules/gateway/app"
	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	gwinfra "github.com/bouroo/goAthena/internal/modules/gateway/infra"
	"github.com/bouroo/goAthena/internal/modules/world"
	worldapp "github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/internal/shared/server"
	"github.com/bouroo/goAthena/internal/shared/telemetry"
)

// Serve builds and runs the modular-monolith process: config → logger →
// telemetry → persistence → feature modules → HTTP/gRPC/gateway servers. It
// blocks until ctx is cancelled (SIGINT/SIGTERM are handled inside
// Application.Run) or a fatal server error occurs.
func Serve(ctx context.Context, cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	injector := do.New()
	var logger *zerolog.Logger
	// injector.Shutdown runs every registered shutdown hook in reverse order;
	// run it on every return path so a failed startup still releases whatever
	// was already constructed.
	defer func() {
		report := injector.Shutdown()
		if report != nil && !report.Succeed && logger != nil {
			logger.Error().Err(report).Msg("injector shutdown error")
		}
	}()

	do.ProvideValue(injector, cfg)

	if err := telemetry.Register(ctx, injector); err != nil {
		return fmt.Errorf("register telemetry: %w", err)
	}

	var err error
	logger, err = do.Invoke[*zerolog.Logger](injector)
	if err != nil {
		return fmt.Errorf("resolve logger: %w", err)
	}

	logger.Info().
		Str("service", cfg.App.Name).
		Str("env", cfg.App.Environment).
		Msg("starting goathena modular monolith")

	if err := server.Register(injector); err != nil {
		return fmt.Errorf("register shared servers: %w", err)
	}

	application := server.NewApplication(injector, cfg, logger)
	if err := wire(ctx, injector, cfg, logger, application); err != nil {
		return err
	}

	if err := application.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("run application: %w", err)
	}
	return nil
}

// wire registers persistence and the feature modules on the injector and
// attaches the gateway listeners to the application as supervised runnables.
// It is extracted from Serve to keep the lifecycle top-level readable; every
// step here is a dependency the injector or the application needs before Run.
func wire(ctx context.Context, injector do.Injector, cfg *config.Config, logger *zerolog.Logger, application *server.Application) error {
	// Persistence (real DB from the start): GORM accounts/chars, Valkey
	// sessions, NATS (future scale-out seam). Each registers its own readiness
	// probe, so /readyz reflects real dependency health.
	if err := db.Register(ctx, injector); err != nil {
		return fmt.Errorf("register db: %w", err)
	}
	if err := valkey.Register(ctx, injector); err != nil {
		return fmt.Errorf("register valkey: %w", err)
	}
	if err := nats.Register(ctx, injector); err != nil {
		return fmt.Errorf("register nats: %w", err)
	}

	// Account/auth: AuthService + CA_LOGIN handler over the GORM account repo
	// and the Valkey session store. Provides domain.Authenticator and
	// *accountapp.CALoginHandler.
	if err := account.Register(ctx, injector); err != nil {
		return fmt.Errorf("register account: %w", err)
	}

	// Character: CH_ENTER (char list) + CH_SELECT_CHAR (zone redirect) +
	// CH_MAKE_CHAR (create) handlers over the GORM char repo. Provides the
	// concrete character-app handlers.
	if err := character.Register(ctx, injector); err != nil {
		return fmt.Errorf("register character: %w", err)
	}

	// World: CZ_ENTER (map-enter trust gate) over the account Authenticator.
	// M3 ships only this gate; M4 layers the entity registry, AOI, and tick. The
	// handler lives on the dedicated map listener, whose fresh connections start
	// at the map role and route CZ_ENTER through the map dispatch table.
	if err := world.Register(ctx, injector); err != nil {
		return fmt.Errorf("register world: %w", err)
	}

	// Thread the feature-module handlers into the gateway dispatch tables. The
	// gateway cannot import account/app or character/app (the architecture guard
	// forbids cross-module impl imports), so the composition root — the one
	// place allowed to see all modules — resolves each concrete handler and
	// provides it as a gateway/domain.PacketHandler function value.
	if err := resolveGatewayHandlers(injector); err != nil {
		return err
	}

	if err := gateway.Register(ctx, injector); err != nil {
		return fmt.Errorf("register gateway: %w", err)
	}

	// Register the gateway listeners as supervised runnables so a fatal
	// listener error tears the process down and SIGTERM reaches them. They are
	// constructed with the runnable's context (the run/signal context) as
	// their base context — TCPHandler.Run and WSServer.Run block on it — so a
	// signal cancels it and they drain on shutdown. The TCP address needs the
	// gnet "tcp://" scheme prefix; the WS listener takes a bare host:port.
	disp, err := do.Invoke[*gwdomain.Dispatcher](injector)
	if err != nil {
		return fmt.Errorf("resolve gateway dispatcher: %w", err)
	}
	newDec, err := do.Invoke[gwinfra.DecoderFactory](injector)
	if err != nil {
		return fmt.Errorf("resolve gateway decoder factory: %w", err)
	}
	newMapDec, err := do.Invoke[gwinfra.MapDecoderFactory](injector)
	if err != nil {
		return fmt.Errorf("resolve map decoder factory: %w", err)
	}
	application.RegisterRunnable("gateway-tcp", func(runCtx context.Context) error {
		return gwinfra.NewTCPHandler(runCtx, logger, disp, newDec).Run("tcp://" + cfg.Gateway.TCP.Addr)
	})
	application.RegisterRunnable("gateway-ws", func(runCtx context.Context) error {
		return gwinfra.NewWSServer(runCtx, logger, disp, newDec, cfg.Gateway.WS.AllowedOrigins).Run(cfg.Gateway.WS.Addr)
	})
	// The map listener is the separate endpoint HC_NOTIFY_ZONESVR redirects to
	// after CH_SELECT_CHAR: every connection it accepts starts at the map role
	// (NewMapTCPHandler), so CZ_ENTER routes to the map table. It shares the same
	// dispatcher and differs from the login/char listener only in its initial
	// role and map-framed decoder. M3 ships TCP only; the WS variant (NewMapWSServer)
	// is wired with the dual-client e2e at M7.
	application.RegisterRunnable("gateway-map-tcp", func(runCtx context.Context) error {
		return gwinfra.NewMapTCPHandler(runCtx, logger, disp, newMapDec).Run("tcp://" + cfg.Gateway.MapAddr)
	})
	// M4c: the movement worker. A single goroutine owns every map's pathfinder
	// (the single-goroutine contract world/domain/map.go documents), so CZ_REQUEST_MOVE
	// handlers enqueue and this runnable resolves moves in arrival order. Registering
	// it as a supervised runnable means SIGTERM cancels its context and Run drains
	// (returns nil); a panic or future unrecoverable fault would fan into the error
	// channel and tear the process down alongside the listeners.
	mover, err := do.Invoke[*worldapp.MoveService](injector)
	if err != nil {
		return fmt.Errorf("resolve world move service: %w", err)
	}
	application.RegisterRunnable("world-move", mover.Run)

	// M5: the mob wander tick. A second supervised runnable drives idle wander
	// per mob on the zone tick cadence. It never calls FindPath (the move worker
	// remains the sole pathfinder owner — single-step wander checks only the
	// immutable MapData), so the two runnables do not contend on the shared A*
	// scratch buffers. Like world-move, a panic here fans into the error channel
	// and tears the process down.
	mobSvc, err := do.Invoke[*worldapp.MobService](injector)
	if err != nil {
		return fmt.Errorf("resolve world mob service: %w", err)
	}
	application.RegisterRunnable("world-mob-tick", mobSvc.Run)
	return nil
}

// resolveGatewayHandlers resolves the concrete feature-module handlers and
// provides them as a single gwapp.Handlers value (the contribution
// gateway.Register consumes to build the dispatch tables). Extracted from wire
// so the lifecycle function stays readable: each module's handler is one invoke
// + one error wrap, and threading them into the dispatch tables is a distinct
// concern from binding the listeners.
func resolveGatewayHandlers(injector do.Injector) error {
	loginHandler, err := do.Invoke[*accountapp.CALoginHandler](injector)
	if err != nil {
		return fmt.Errorf("resolve CA_LOGIN handler: %w", err)
	}
	enterHandler, err := do.Invoke[*characterapp.CharEnterHandler](injector)
	if err != nil {
		return fmt.Errorf("resolve CH_ENTER handler: %w", err)
	}
	selectHandler, err := do.Invoke[*characterapp.CharSelectHandler](injector)
	if err != nil {
		return fmt.Errorf("resolve CH_SELECT_CHAR handler: %w", err)
	}
	makeHandler, err := do.Invoke[*characterapp.CharMakeHandler](injector)
	if err != nil {
		return fmt.Errorf("resolve CH_MAKE_CHAR handler: %w", err)
	}
	mapEnterHandler, err := do.Invoke[*worldapp.MapEnterHandler](injector)
	if err != nil {
		return fmt.Errorf("resolve CZ_ENTER handler: %w", err)
	}
	moveHandler, err := do.Invoke[*worldapp.MoveHandler](injector)
	if err != nil {
		return fmt.Errorf("resolve CZ_REQUEST_MOVE handler: %w", err)
	}
	do.ProvideValue(injector, gwapp.Handlers{
		OnCALogin:       loginHandler.Handle,
		OnCHEnter:       enterHandler.Handle,
		OnCHSelectChar:  selectHandler.Handle,
		OnCHMakeChar:    makeHandler.Handle,
		OnCZEnter:       mapEnterHandler.Handle,
		OnCZRequestMove: moveHandler.Handle,
	})
	return nil
}
