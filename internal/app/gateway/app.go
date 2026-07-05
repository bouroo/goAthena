// Package gateway wires the gateway service composition root (DEL-01).
//
// The gateway is a stateless ingress layer: kRO TCP packet parse/decrypt,
// WebSocket for roBrowser, and gRPC routing to identity/zone services.
package gateway

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"
	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/app/common"
	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/shared/telemetry"
)

// Run builds and runs the gateway service until ctx is cancelled.
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
		Str("service", "gateway").
		Str("env", cfg.App.Environment).
		Msg("starting gateway service")

	// TODO(DEL-01): wire gnet TCP listener, WebSocket ingress, packet codec,
	// stream decryption, and gRPC routing to identity/zone.

	<-ctx.Done()
	logger.Info().Msg("gateway service shutting down")
	return nil
}
