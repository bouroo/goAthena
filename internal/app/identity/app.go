// Package identity wires the identity service composition root (DEL-02).
//
// The identity service handles login, character CRUD, and warehouse locking
// via HTTP (echo) and gRPC, backed by MariaDB and Valkey.
package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog"
	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/app/common"
	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/infrastructure/db"
	"github.com/bouroo/goAthena/internal/infrastructure/messaging/valkey"
	"github.com/bouroo/goAthena/internal/shared/server"
	"github.com/bouroo/goAthena/internal/shared/telemetry"
)

// Run builds and runs the identity service until ctx is cancelled or a
// server error occurs.
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
		Str("service", "identity").
		Str("env", cfg.App.Environment).
		Msg("starting identity service")

	if err := db.Register(ctx, injector); err != nil {
		return fmt.Errorf("register database: %w", err)
	}

	if err := valkey.Register(ctx, injector); err != nil {
		return fmt.Errorf("register valkey: %w", err)
	}

	if err := server.Register(injector); err != nil {
		return fmt.Errorf("register shared servers: %w", err)
	}

	// TODO(DEL-02): wire identity feature (login, char CRUD, warehouse locking).

	application := server.NewApplication(injector, cfg, logger)

	defer func() {
		report := injector.Shutdown()
		if report != nil && !report.Succeed {
			logger.Error().Err(report).Msg("injector shutdown error")
		}
	}()

	if err := application.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("run application: %w", err)
	}
	return nil
}
