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
	"github.com/bouroo/goAthena/internal/modules/content"
	contentapp "github.com/bouroo/goAthena/internal/modules/content/app"
	"github.com/bouroo/goAthena/internal/modules/gateway"
	gwapp "github.com/bouroo/goAthena/internal/modules/gateway/app"
	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	gwinfra "github.com/bouroo/goAthena/internal/modules/gateway/infra"
	"github.com/bouroo/goAthena/internal/modules/inventory"
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
// attaches the gateway listeners and worker runnables to the application.
// Each step is extracted into a helper so the lifecycle function here stays
// readable and each piece stays under the gocyclo threshold.
func wire(ctx context.Context, injector do.Injector, cfg *config.Config, logger *zerolog.Logger, application *server.Application) error {
	if err := wirePersistence(ctx, injector); err != nil {
		return err
	}
	if err := wireFeatureModules(ctx, injector, cfg); err != nil {
		return err
	}
	if err := resolveGatewayHandlers(injector); err != nil {
		return err
	}
	if err := gateway.Register(ctx, injector); err != nil {
		return fmt.Errorf("register gateway: %w", err)
	}
	if err := wireRunnableListeners(ctx, injector, cfg, logger, application); err != nil {
		return err
	}
	return nil
}

// wirePersistence registers the cross-module infrastructure: GORM accounts,
// Valkey sessions, NATS bus. Each one owns its own readiness probe, so /readyz
// reflects real dependency health.
func wirePersistence(ctx context.Context, injector do.Injector) error {
	if err := db.Register(ctx, injector); err != nil {
		return fmt.Errorf("register db: %w", err)
	}
	if err := valkey.Register(ctx, injector); err != nil {
		return fmt.Errorf("register valkey: %w", err)
	}
	if err := nats.Register(ctx, injector); err != nil {
		return fmt.Errorf("register nats: %w", err)
	}
	return nil
}

// wireFeatureModules registers every bounded-context feature module in the
// dependency order the injector requires. Inventory must register before
// world so the pickup use case can resolve its port during world.Register;
// world must register before content because content depends on the world's
// NPCRegistry, MapStore, and PlayerRegistry.
func wireFeatureModules(ctx context.Context, injector do.Injector, cfg *config.Config) error {
	if err := account.Register(ctx, injector); err != nil {
		return fmt.Errorf("register account: %w", err)
	}
	if err := character.Register(ctx, injector); err != nil {
		return fmt.Errorf("register character: %w", err)
	}
	if err := inventory.Register(ctx, injector); err != nil {
		return fmt.Errorf("register inventory: %w", err)
	}
	if err := world.Register(ctx, injector); err != nil {
		return fmt.Errorf("register world: %w", err)
	}
	if err := content.Register(injector, cfg.Zone.ScriptDir); err != nil {
		return fmt.Errorf("register content: %w", err)
	}
	return nil
}

// wireRunnableListeners resolves the dispatcher + decoder factories and
// attaches every gateway listener and worker runnable to the application.
// Runnables share the run/signal context so SIGTERM cancels them and they
// drain on shutdown.
func wireRunnableListeners(ctx context.Context, injector do.Injector, cfg *config.Config, logger *zerolog.Logger, application *server.Application) error {
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
	// The map-role WebSocket listener — roBrowser's only way to reach the map
	// server, since a browser cannot open the raw TCP socket HC_NOTIFY_ZONESVR
	// otherwise points at. Every accepted connection starts at the map role
	// (NewMapWSServer), so CZ_ENTER routes through the map dispatch table exactly
	// as the TCP map listener does. It shares the dispatcher, decoder factory,
	// and origin policy with the login/char WS listener; only its initial role
	// and bind address differ. Wired with the dual-client e2e (M7e).
	application.RegisterRunnable("gateway-map-ws", func(runCtx context.Context) error {
		return gwinfra.NewMapWSServer(runCtx, logger, disp, newMapDec, cfg.Gateway.WS.AllowedOrigins).
			Run(cfg.Gateway.MapWSAddr)
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

// invokeOr resolves a typed dependency from the injector and wraps the
// not-found error with the supplied message so callers stay one line each.
// The generic argument is inferred from the assignment target.
func invokeOr[T any](c do.Injector, wrap string) (T, error) {
	v, err := do.Invoke[T](c)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("%s: %w", wrap, err)
	}
	return v, nil
}

func resolveAccountHandlers(i do.Injector) (*accountapp.CALoginHandler, error) {
	return invokeOr[*accountapp.CALoginHandler](i, "resolve CA_LOGIN handler")
}

func resolveCharacterHandlers(i do.Injector) (enter *characterapp.CharEnterHandler, sel *characterapp.CharSelectHandler, mk *characterapp.CharMakeHandler, err error) {
	enter, err = invokeOr[*characterapp.CharEnterHandler](i, "resolve CH_ENTER handler")
	if err != nil {
		return
	}
	sel, err = invokeOr[*characterapp.CharSelectHandler](i, "resolve CH_SELECT_CHAR handler")
	if err != nil {
		return
	}
	mk, err = invokeOr[*characterapp.CharMakeHandler](i, "resolve CH_MAKE_CHAR handler")
	return
}

func resolveWorldHandlers(i do.Injector) (
	mapEnter *worldapp.MapEnterHandler, move *worldapp.MoveHandler, action *worldapp.ActionHandler,
	timeH *worldapp.TimeHandler, changeDir *worldapp.ChangeDirHandler, emotion *worldapp.EmotionHandler,
	restart *worldapp.RestartHandler, pickup *worldapp.PickupHandler, equip *worldapp.EquipHandler,
	takeoff *worldapp.TakeoffHandler, err error,
) {
	mapEnter, err = invokeOr[*worldapp.MapEnterHandler](i, "resolve CZ_ENTER handler")
	if err != nil {
		return
	}
	move, err = invokeOr[*worldapp.MoveHandler](i, "resolve CZ_REQUEST_MOVE handler")
	if err != nil {
		return
	}
	action, err = invokeOr[*worldapp.ActionHandler](i, "resolve CZ_ACTION_REQUEST handler")
	if err != nil {
		return
	}
	timeH, err = invokeOr[*worldapp.TimeHandler](i, "resolve CZ_REQUEST_TIME handler")
	if err != nil {
		return
	}
	changeDir, err = invokeOr[*worldapp.ChangeDirHandler](i, "resolve CZ_CHANGE_DIR handler")
	if err != nil {
		return
	}
	emotion, err = invokeOr[*worldapp.EmotionHandler](i, "resolve CZ_REQ_EMOTION handler")
	if err != nil {
		return
	}
	restart, err = invokeOr[*worldapp.RestartHandler](i, "resolve CZ_RESTART handler")
	if err != nil {
		return
	}
	pickup, err = invokeOr[*worldapp.PickupHandler](i, "resolve CZ_ITEM_PICKUP handler")
	if err != nil {
		return
	}
	equip, err = invokeOr[*worldapp.EquipHandler](i, "resolve CZ_REQ_WEAR_EQUIP handler")
	if err != nil {
		return
	}
	takeoff, err = invokeOr[*worldapp.TakeoffHandler](i, "resolve CZ_REQ_TAKEOFF_EQUIP handler")
	return
}

func resolveContentHandlers(i do.Injector) (contact *contentapp.ContactNPCHandler, next *contentapp.ReqNextScriptHandler, menu *contentapp.ChooseMenuHandler, closeDlg *contentapp.CloseDialogHandler, err error) {
	contact, err = invokeOr[*contentapp.ContactNPCHandler](i, "resolve CZ_CONTACTNPC handler")
	if err != nil {
		return
	}
	next, err = invokeOr[*contentapp.ReqNextScriptHandler](i, "resolve CZ_REQ_NEXT_SCRIPT handler")
	if err != nil {
		return
	}
	menu, err = invokeOr[*contentapp.ChooseMenuHandler](i, "resolve CZ_CHOOSE_MENU handler")
	if err != nil {
		return
	}
	closeDlg, err = invokeOr[*contentapp.CloseDialogHandler](i, "resolve CZ_CLOSE_DIALOG handler")
	return
}

// resolveGatewayHandlers resolves the concrete feature-module handlers and
// provides them as a single gwapp.Handlers value (the contribution
// gateway.Register consumes to build the dispatch tables). The gateway cannot
// import account/app or character/app (the architecture guard forbids
// cross-module impl imports), so the composition root — the one place
// allowed to see all modules — resolves each concrete handler here and
// provides it as a gateway/domain.PacketHandler function value.
func resolveGatewayHandlers(injector do.Injector) error {
	login, err := resolveAccountHandlers(injector)
	if err != nil {
		return err
	}
	enter, sel, mk, err := resolveCharacterHandlers(injector)
	if err != nil {
		return err
	}
	mapEnter, move, action, timeH, changeDir, emotion, restart, pickup, equip, takeoff, err := resolveWorldHandlers(injector)
	if err != nil {
		return err
	}
	contact, next, menu, closeDlg, err := resolveContentHandlers(injector)
	if err != nil {
		return err
	}
	do.ProvideValue(injector, gwapp.Handlers{
		OnCALogin:           login.Handle,
		OnCHEnter:           enter.Handle,
		OnCHSelectChar:      sel.Handle,
		OnCHMakeChar:        mk.Handle,
		OnCZEnter:           mapEnter.Handle,
		OnCZRequestMove:     move.Handle,
		OnCZActionRequest:   action.Handle,
		OnCZRequestTime:     timeH.Handle,
		OnCZChangeDir:       changeDir.Handle,
		OnCZReqEmotion:      emotion.Handle,
		OnCZRestart:         restart.Handle,
		OnCZItemPickup:      pickup.Handle,
		OnCZReqWearEquip:    equip.Handle,
		OnCZReqTakeoffEquip: takeoff.Handle,
		OnCZContactNPC:      contact.Handle,
		OnCZReqNextScript:   next.Handle,
		OnCZChooseMenu:      menu.Handle,
		OnCZCloseDialog:     closeDlg.Handle,
	})
	return nil
}
