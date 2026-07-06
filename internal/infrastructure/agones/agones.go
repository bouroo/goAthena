package agones

import (
	"context"
	"errors"
	"sync"
	"time"

	agones "agones.dev/agones/sdks/go"
	"github.com/rs/zerolog"
)

// sdkDialTimeout caps how long New waits for the Agones sidecar to come up
// before falling back to the local no-op implementation. sdk.NewSDK itself
// blocks up to 30s internally; we run it in a goroutine so we can give up
// faster in dev/CI where the sidecar is not running.
const sdkDialTimeout = 3 * time.Second

// Agones is the Agones-backed Lifecycle implementation. It holds a connection
// to the Agones sidecar and tracks which state transitions have been
// performed so they are idempotent.
type Agones struct {
	sdk    *agones.SDK
	cancel context.CancelFunc

	mu     sync.Mutex
	ready  bool
	alloc  bool
	closed bool
	logger *zerolog.Logger
}

// NewAgones dials the Agones sidecar with a bounded timeout and returns an
// Agones-backed Lifecycle. Returns an error if the sidecar cannot be reached
// within sdkDialTimeout so callers can decide whether to fall back.
func NewAgones(parent context.Context, logger *zerolog.Logger) (*Agones, error) {
	if logger == nil {
		return nil, errors.New("agones: logger is required")
	}

	type result struct {
		s   *agones.SDK
		err error
	}

	resCh := make(chan result, 1)
	go func() {
		s, err := agones.NewSDK()
		resCh <- result{s: s, err: err}
	}()

	var s *agones.SDK
	select {
	case <-parent.Done():
		return nil, errors.Join(errors.New("agones: dial cancelled"), parent.Err())
	case r := <-resCh:
		if r.err != nil {
			return nil, errors.Join(errors.New("agones: dial sidecar"), r.err)
		}
		s = r.s
	case <-time.After(sdkDialTimeout):
		return nil, errors.New("agones: sidecar not reachable within timeout")
	}

	_, cancel := context.WithCancel(context.Background())
	lg := *logger
	return &Agones{
		sdk:    s,
		cancel: cancel,
		logger: &lg,
	}, nil
}

// Ready is idempotent; only the first call hits the SDK.
func (a *Agones) Ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return errors.Join(errors.New("agones: ready"), err)
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return errors.New("agones: ready: lifecycle closed")
	}
	if a.ready {
		a.mu.Unlock()
		a.logger.Debug().Msg("agones: Ready already sent, skipping")
		return nil
	}
	a.ready = true
	a.mu.Unlock()

	if err := a.sdk.Ready(); err != nil {
		a.mu.Lock()
		a.ready = false
		a.mu.Unlock()
		return errors.Join(errors.New("agones: ready"), err)
	}
	a.logger.Info().Msg("agones: Ready sent")
	return nil
}

// Allocate is idempotent; only the first call hits the SDK.
func (a *Agones) Allocate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return errors.Join(errors.New("agones: allocate"), err)
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return errors.New("agones: allocate: lifecycle closed")
	}
	if a.alloc {
		a.mu.Unlock()
		a.logger.Debug().Msg("agones: Allocate already sent, skipping")
		return nil
	}
	a.alloc = true
	a.mu.Unlock()

	if err := a.sdk.Allocate(); err != nil {
		a.mu.Lock()
		a.alloc = false
		a.mu.Unlock()
		return errors.Join(errors.New("agones: allocate"), err)
	}
	a.logger.Info().Msg("agones: Allocate sent")
	return nil
}

// Shutdown signals deletion to Agones. Unlike Ready/Allocate it is *not*
// idempotent at the SDK level (sending it twice is harmless but wastes a
// round-trip); we send it once and subsequent calls are no-ops.
func (a *Agones) Shutdown(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return errors.Join(errors.New("agones: shutdown"), err)
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	a.mu.Unlock()

	if err := a.sdk.Shutdown(); err != nil {
		// Roll back the closed flag so retries are possible if Shutdown
		// is called again from a deferred recovery path.
		a.mu.Lock()
		a.closed = false
		a.mu.Unlock()
		return errors.Join(errors.New("agones: shutdown"), err)
	}
	a.logger.Info().Msg("agones: Shutdown sent")
	return nil
}

// Health pings the sidecar health stream.
func (a *Agones) Health(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return errors.Join(errors.New("agones: health"), err)
	}
	a.mu.Lock()
	closed := a.closed
	a.mu.Unlock()
	if closed {
		return errors.New("agones: health: lifecycle closed")
	}
	if err := a.sdk.Health(); err != nil {
		return errors.Join(errors.New("agones: health"), err)
	}
	return nil
}

// Close cancels the internal context. The Agones SDK does not expose a Close
// on its SDK struct, so we cannot close the underlying gRPC connection here;
// process exit handles that. Idempotent.
func (a *Agones) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil
	}
	a.closed = true
	if a.cancel != nil {
		a.cancel()
	}
	a.logger.Info().Msg("agones: lifecycle closed")
	return nil
}
