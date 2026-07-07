// Package gateway wires the gateway service composition root (DEL-01).
//
// The gateway is a stateless ingress layer: kRO TCP packet parse/decrypt,
// WebSocket for roBrowser, and gRPC routing to identity/zone services.
package gateway

import (
	"context"
	"fmt"

	"github.com/panjf2000/gnet/v2"
	"github.com/rs/zerolog"
	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/app/common"
	"github.com/bouroo/goAthena/internal/config"
	assetsdi "github.com/bouroo/goAthena/internal/features/assets"
	gatewaydi "github.com/bouroo/goAthena/internal/features/gateway/di"
	"github.com/bouroo/goAthena/internal/features/gateway/handler"
	"github.com/bouroo/goAthena/internal/shared/telemetry"
)

// Run builds and runs the gateway service until ctx is cancelled. The
// gateway runs both the gnet TCP listener (kRO native client) and the
// HTTP/WebSocket listener (roBrowser web client) concurrently and stops
// them together on context cancellation.
func Run(ctx context.Context, cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	injector := do.New()
	defer func() {
		_ = injector.Shutdown()
	}()

	tcpHandler, wsHandler, logger, err := bootstrapGateway(ctx, cfg, injector)
	if err != nil {
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.App.ShutdownTimeout)
	defer cancel()

	logger.Info().
		Str("version", common.Version).
		Str("commit", common.CommitSHA).
		Str("service", "gateway").
		Str("env", cfg.App.Environment).
		Str("tcp_addr", cfg.Gateway.TCP.Addr).
		Str("ws_addr", cfg.Gateway.WS.Addr).
		Str("ws_path", cfg.Gateway.WS.Path).
		Int("packetver", cfg.Gateway.Packetver).
		Msg("starting gateway service")

	if err := wsHandler.Start(ctx); err != nil {
		return fmt.Errorf("start ws server: %w", err)
	}

	gnetErrCh := make(chan error, 1)
	go func() {
		// gnet.Run blocks until the engine stops. After gnet.Stop it
		// returns ErrEngineInShutdown (or similar); we only surface
		// unexpected startup/binding errors here.
		tcpAddr := "tcp://" + cfg.Gateway.TCP.Addr
		if gnetErr := gnet.Run(tcpHandler, tcpAddr, gnet.WithTicker(false)); gnetErr != nil {
			gnetErrCh <- fmt.Errorf("gnet run: %w", gnetErr)
			return
		}
		gnetErrCh <- nil
	}()

	select {
	case <-ctx.Done():
		logger.Info().Msg("gateway service shutting down")
		// Best-effort shutdown: log and continue. gnet may never have
		// reached OnBoot (engine is empty) when ctx is cancelled before
		// the engine boots — that is not an error worth surfacing.
		if eng := tcpHandler.Engine(); eng != (gnet.Engine{}) {
			if stopErr := eng.Stop(context.Background()); stopErr != nil {
				logger.Warn().Err(stopErr).Msg("gnet Engine.Stop returned error")
			}
		}
		if stopErr := wsHandler.Stop(shutdownCtx); stopErr != nil {
			logger.Warn().Err(stopErr).Msg("ws server shutdown returned error")
		}
		return nil
	case runErr := <-gnetErrCh:
		// TCP engine failed (e.g. port bind error). Tear down the WS
		// server too so the process exits cleanly.
		_ = wsHandler.Stop(shutdownCtx)
		if runErr != nil {
			return runErr
		}
		return nil
	}
}

// bootstrapGateway wires telemetry, registers all gateway-side features,
// and resolves the TCP/WS handlers. Extracted from Run to keep the
// gocyclo budget under 15. The caller owns the injector lifecycle.
func bootstrapGateway(
	ctx context.Context,
	cfg *config.Config,
	injector do.Injector,
) (*handler.TCPHandler, *handler.WSHandler, *zerolog.Logger, error) {
	do.ProvideValue(injector, cfg)

	if err := telemetry.Register(ctx, injector); err != nil {
		return nil, nil, nil, fmt.Errorf("register telemetry: %w", err)
	}

	logger, err := do.Invoke[*zerolog.Logger](injector)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("resolve logger: %w", err)
	}

	if err := gatewaydi.Register(injector); err != nil {
		return nil, nil, nil, fmt.Errorf("register gateway feature: %w", err)
	}

	if err := assetsdi.Register(injector); err != nil {
		return nil, nil, nil, fmt.Errorf("register assets feature: %w", err)
	}

	tcpHandler, err := do.Invoke[*handler.TCPHandler](injector)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("resolve TCP handler: %w", err)
	}

	wsHandler, err := do.Invoke[*handler.WSHandler](injector)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("resolve WS handler: %w", err)
	}

	// Wire the asset handler onto the WS server when present. The DI
	// container only registers an *assets.AssetHandler when
	// assets.enabled is true; a not-found error here just means the
	// asset server is disabled — not a startup failure.
	if assetHandler, assetErr := do.Invoke[*assetsdi.AssetHandler](injector); assetErr == nil {
		wsHandler.WithAssetsHandler(assetHandler)
	}

	return tcpHandler, wsHandler, logger, nil
}
