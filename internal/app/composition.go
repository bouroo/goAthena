// Package app is the modular-monolith composition root: the one place that
// knows how to build and start the whole process. It wires infrastructure
// providers into a single samber/do injector and runs the application
// orchestrator until shutdown.
package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog"
	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/shared/server"
	"github.com/bouroo/goAthena/internal/shared/telemetry"
)

// Serve builds and runs the modular-monolith process: config → logger →
// telemetry → HTTP/gRPC servers. It blocks until ctx is cancelled (SIGINT/
// SIGTERM are handled inside Application.Run) or a fatal server error occurs.
//
// M0 scope: only the health-serving skeleton is wired — no persistence
// (DB/Valkey/NATS) and no feature modules yet. /healthz and /readyz return 200
// because the health registry starts empty (an empty checker set is healthy by
// definition). Persistence and the gateway ingress land in M1 alongside
// account/auth.
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

	// TODO(M1): register persistence (db, valkey, nats) here, before the gateway
	// ingress, so account/auth has its repositories available.
	// TODO(M1): wire the gateway ingress (codec + table-driven dispatch) and the
	// account/auth module feature DI here.

	application := server.NewApplication(injector, cfg, logger)
	if err := application.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("run application: %w", err)
	}
	return nil
}
