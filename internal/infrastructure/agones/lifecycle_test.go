//go:build unit

package agones

import (
	"context"
	"sync"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestLogger() *zerolog.Logger {
	l := zerolog.Nop()
	return &l
}

func TestLocal_ReadyIsIdempotent(t *testing.T) {
	t.Parallel()

	l := NewLocal(newTestLogger())

	require.NoError(t, l.Ready(context.Background()))
	require.NoError(t, l.Ready(context.Background()))
	require.NoError(t, l.Ready(context.Background()))
}

func TestLocal_AllocateIsIdempotent(t *testing.T) {
	t.Parallel()

	l := NewLocal(newTestLogger())

	require.NoError(t, l.Allocate(context.Background()))
	require.NoError(t, l.Allocate(context.Background()))
}

func TestLocal_ShutdownIsIdempotent(t *testing.T) {
	t.Parallel()

	l := NewLocal(newTestLogger())

	require.NoError(t, l.Shutdown(context.Background()))
	require.NoError(t, l.Shutdown(context.Background()))
}

func TestLocal_CloseIsIdempotent(t *testing.T) {
	t.Parallel()

	l := NewLocal(newTestLogger())

	require.NoError(t, l.Close())
	require.NoError(t, l.Close())
}

func TestLocal_HealthIsNoOp(t *testing.T) {
	t.Parallel()

	l := NewLocal(newTestLogger())

	require.NoError(t, l.Health(context.Background()))
	require.NoError(t, l.Health(context.Background()))
}

func TestLocal_StateTransitionsAllowedInOrder(t *testing.T) {
	t.Parallel()

	l := NewLocal(newTestLogger())

	require.NoError(t, l.Ready(context.Background()))
	require.NoError(t, l.Allocate(context.Background()))
	require.NoError(t, l.Health(context.Background()))
	require.NoError(t, l.Shutdown(context.Background()))
	require.NoError(t, l.Close())
}

func TestLocal_RefusesAfterClose(t *testing.T) {
	t.Parallel()

	l := NewLocal(newTestLogger())
	require.NoError(t, l.Close())

	assert.ErrorContains(t, l.Ready(context.Background()), "lifecycle closed")
	assert.ErrorContains(t, l.Allocate(context.Background()), "lifecycle closed")
	assert.ErrorContains(t, l.Health(context.Background()), "lifecycle closed")
}

func TestLocal_RespectsContextCancellation(t *testing.T) {
	t.Parallel()

	l := NewLocal(newTestLogger())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Local has no async work but should still surface the cancelled context
	// to the caller — the wrapper is context-aware end-to-end.
	assert.ErrorIs(t, l.Ready(ctx), context.Canceled)
	assert.ErrorIs(t, l.Allocate(ctx), context.Canceled)
	assert.ErrorIs(t, l.Shutdown(ctx), context.Canceled)
	assert.ErrorIs(t, l.Health(ctx), context.Canceled)
}

func TestLocal_ConcurrentCallsAreSafe(t *testing.T) {
	t.Parallel()

	l := NewLocal(newTestLogger())

	const goroutines = 32
	var wg sync.WaitGroup
	wg.Add(goroutines * 4)

	for range goroutines {
		go func() {
			defer wg.Done()
			_ = l.Ready(context.Background())
		}()
		go func() {
			defer wg.Done()
			_ = l.Allocate(context.Background())
		}()
		go func() {
			defer wg.Done()
			_ = l.Health(context.Background())
		}()
		go func() {
			defer wg.Done()
			_ = l.Shutdown(context.Background())
		}()
	}

	wg.Wait()

	require.NoError(t, l.Close())
}

func TestLocal_NilLoggerPanics(t *testing.T) {
	t.Parallel()

	assert.Panics(t, func() { _ = NewLocal(nil) })
}

func TestNew_FallsBackToLocalWhenNoSidecar(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	l := New(ctx, newTestLogger())

	// Should be the local implementation since no Agones sidecar is reachable.
	_, ok := l.(*Local)
	assert.True(t, ok, "expected *Local fallback, got %T", l)
	assert.NoError(t, l.Ready(ctx))
	assert.NoError(t, l.Allocate(ctx))
	assert.NoError(t, l.Shutdown(ctx))
	assert.NoError(t, l.Close())
}

func TestNewAgones_NilLoggerErrors(t *testing.T) {
	t.Parallel()

	_, err := NewAgones(context.Background(), nil)
	require.Error(t, err)
}
