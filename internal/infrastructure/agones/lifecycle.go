package agones

import (
	"context"

	"github.com/rs/zerolog"
)

// Lifecycle manages GameServer state transitions.
//
// All methods are idempotent — safe to call multiple times. Ready and
// Allocate are guarded internally so only the first call performs the side
// effect; subsequent calls are no-ops.
type Lifecycle interface {
	// Ready signals that the GameServer is ready to accept players.
	// Called after map data is loaded and the tick loop is running.
	Ready(ctx context.Context) error

	// Allocate signals that the GameServer should transition from Ready to
	// Allocated. Called when the first player is assigned to this zone.
	Allocate(ctx context.Context) error

	// Shutdown signals that the GameServer should be deleted. Called when the
	// zone is empty and should be reclaimed.
	Shutdown(ctx context.Context) error

	// Health sends a periodic health check ping. Called on every tick (or
	// every N ticks).
	Health(ctx context.Context) error

	// Close releases SDK resources. Safe to call multiple times.
	Close() error
}

// New returns the appropriate Lifecycle for the current environment.
//
// If the Agones sidecar is reachable within sdkDialTimeout, an Agones-backed
// Lifecycle is returned. Otherwise (dev, CI, unit tests) a no-op Local
// Lifecycle is returned so the zone service can run unchanged without Agones
// infrastructure. The fallback is logged once.
func New(ctx context.Context, logger *zerolog.Logger) Lifecycle {
	a, err := NewAgones(ctx, logger)
	if err != nil {
		logger.Info().Err(err).Msg("agones: sidecar unavailable, using local lifecycle")
		return NewLocal(logger)
	}
	logger.Info().Msg("agones: sidecar connected, using Agones lifecycle")
	return a
}
