// Package nats wires the NATS pub/sub client into the DI container
// for inter-service communication (transit, social, broadcast).
//
// The package exposes:
//
//   - Client — a thin wrapper over *nats.Conn with type-safe
//     Publish / PublishRequest / Subscribe / QueueSubscribe / Close.
//   - Shutdowner — samber/do adapter that drains the connection on
//     injector.Shutdown().
//   - Subject helpers (TransitRequestSubject, PartySubject, etc.) that
//     produce versioned, sanitized subject strings.
//
// Configuration is read from config.NATSConfig (URL, ConnectTimeout).
// JetStream support is intentionally absent in Phase 5 — the hot
// paths use core NATS, and JetStream is added only when persistence is
// required (see Scout findings §1.1).
package nats

import (
	"context"
	"fmt"

	"github.com/rs/zerolog"
	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/shared/telemetry"
)

// ProvideClient resolves the shared zerolog logger, builds a Client
// from cfg.NATS, and registers the *Client on the injector. Callers
// that want the client should Invoke[*Client] rather than construct
// their own.
func ProvideClient(c do.Injector) (*Client, error) {
	cfg, err := do.Invoke[*config.Config](c)
	if err != nil {
		return nil, fmt.Errorf("resolve config: %w", err)
	}
	logger, err := do.Invoke[*zerolog.Logger](c)
	if err != nil {
		return nil, fmt.Errorf("resolve logger: %w", err)
	}
	return NewClientFromConfig(context.Background(), cfg, logger)
}

// Register provides *Client and *Shutdowner on the injector, and
// registers the readiness checker. ctx is unused at construction time
// (NewClientFromConfig uses Background so that long-lived connections
// outlive caller cancellation) but is accepted to match the samber/do
// v2 signature convention used elsewhere (e.g. valkey.Register).
func Register(_ context.Context, c do.Injector) error {
	cfg, err := do.Invoke[*config.Config](c)
	if err != nil {
		return fmt.Errorf("resolve config: %w", err)
	}

	registry, err := do.Invoke[*telemetry.Registry](c)
	if err != nil {
		return fmt.Errorf("resolve health registry: %w", err)
	}

	logger, err := do.Invoke[*zerolog.Logger](c)
	if err != nil {
		return fmt.Errorf("resolve logger: %w", err)
	}

	client, err := NewClientFromConfig(context.Background(), cfg, logger)
	if err != nil {
		return err
	}
	do.ProvideValue(c, client)
	do.ProvideValue(c, NewShutdowner(client))

	registry.AddReadiness(natsChecker{client: client})

	return nil
}
