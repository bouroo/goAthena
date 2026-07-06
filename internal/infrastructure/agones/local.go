package agones

import (
	"context"
	"errors"
	"sync"

	"github.com/rs/zerolog"
)

// Local is a no-op Lifecycle used in dev/CI when no Agones sidecar is
// available. Every transition is logged and returns nil. State flags are
// tracked only for diagnostic logging.
type Local struct {
	mu       sync.Mutex
	ready    bool
	alloc    bool
	shutdown bool
	closed   bool
	logger   *zerolog.Logger
}

// NewLocal returns a no-op Lifecycle that logs each transition.
func NewLocal(logger *zerolog.Logger) *Local {
	if logger == nil {
		panic(errors.New("agones: logger is required"))
	}
	lg := *logger
	return &Local{logger: &lg}
}

// Ready records the transition and returns nil.
func (l *Local) Ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return errors.Join(errors.New("agones: ready"), err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return errors.New("agones: ready: lifecycle closed")
	}
	if l.ready {
		l.logger.Debug().Msg("agones(local): Ready already sent, skipping")
		return nil
	}
	l.ready = true
	l.logger.Info().Msg("agones(local): Ready")
	return nil
}

// Allocate records the transition and returns nil.
func (l *Local) Allocate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return errors.Join(errors.New("agones: allocate"), err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return errors.New("agones: allocate: lifecycle closed")
	}
	if l.alloc {
		l.logger.Debug().Msg("agones(local): Allocate already sent, skipping")
		return nil
	}
	l.alloc = true
	l.logger.Info().Msg("agones(local): Allocate")
	return nil
}

// Shutdown records the transition and returns nil.
func (l *Local) Shutdown(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return errors.Join(errors.New("agones: shutdown"), err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.shutdown {
		return nil
	}
	l.shutdown = true
	l.logger.Info().Msg("agones(local): Shutdown")
	return nil
}

// Health is a no-op.
func (l *Local) Health(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return errors.Join(errors.New("agones: health"), err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return errors.New("agones: health: lifecycle closed")
	}
	return nil
}

// Close marks the lifecycle closed. Idempotent.
func (l *Local) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	l.logger.Info().Msg("agones(local): closed")
	return nil
}
