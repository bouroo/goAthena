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

	// Thread the feature-module handlers into the gateway dispatch tables. The
	// gateway cannot import account/app or character/app (the architecture guard
	// forbids cross-module impl imports), so the composition root — the one
	// place allowed to see all modules — provides each handler contribution as
	// a gateway/domain.PacketHandler function value.
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
	do.ProvideValue(injector, gwapp.Handlers{
		OnCALogin:      loginHandler.Handle,
		OnCHEnter:      enterHandler.Handle,
		OnCHSelectChar: selectHandler.Handle,
		OnCHMakeChar:   makeHandler.Handle,
	})

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
	application.RegisterRunnable("gateway-tcp", func(runCtx context.Context) error {
		return gwinfra.NewTCPHandler(runCtx, logger, disp, newDec).Run("tcp://" + cfg.Gateway.TCP.Addr)
	})
	application.RegisterRunnable("gateway-ws", func(runCtx context.Context) error {
		return gwinfra.NewWSServer(runCtx, logger, disp, newDec, cfg.Gateway.WS.AllowedOrigins).Run(cfg.Gateway.WS.Addr)
	})
	return nil
}
