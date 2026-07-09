//go:build unit

package service_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/bouroo/goAthena/internal/features/zone/domain"
	"github.com/bouroo/goAthena/internal/features/zone/service"
	"github.com/bouroo/goAthena/pkg/ro/romap"
)

func silentLogger() *zerolog.Logger {
	l := zerolog.Nop()
	return &l
}

// nopPublisher is a no-op domain.Publisher used by external tests in
// this file. It mirrors the helper in publisher_testhelper_test.go but
// lives here because the external test package cannot reference an
// unexported type from the internal test package.
type nopPublisher struct{}

func (nopPublisher) PublishEvent(_ context.Context, _ string, _ proto.Message) error {
	return nil
}

var _ domain.Publisher = nopPublisher{}

func newSyntheticMap(name string, w, h int) *romap.MapData {
	md := &romap.MapData{
		Name:     name,
		Width:    w,
		Height:   h,
		Walkable: make([]bool, w*h),
		Heights:  make([]float32, w*h),
	}
	for i := range md.Walkable {
		md.Walkable[i] = true
	}
	return md
}

func newZoneService(t *testing.T, tickRate time.Duration) (*service.ZoneService, *service.TickLoop) {
	t.Helper()
	md := newSyntheticMap("test", 100, 100)
	tl := service.NewTickLoop(md, tickRate, silentLogger(), nopPublisher{})
	require.NotNil(t, tl)
	ag := newCountingLifecycle()
	zs := service.NewZoneService(tl, ag, 150, 0, silentLogger())
	require.NotNil(t, zs)
	return zs, tl
}

// countingLifecycle records lifecycle transitions for assertions.
type countingLifecycle struct {
	mu         sync.Mutex
	ready      int32
	alloc      int32
	shutdown   int32
	health     int32
	allocFn    func(ctx context.Context) error
	shutdownFn func(ctx context.Context) error
}

func newCountingLifecycle() *countingLifecycle { return &countingLifecycle{} }

func (c *countingLifecycle) Ready(_ context.Context) error {
	atomic.AddInt32(&c.ready, 1)
	return nil
}

func (c *countingLifecycle) Allocate(ctx context.Context) error {
	atomic.AddInt32(&c.alloc, 1)
	if c.allocFn != nil {
		return c.allocFn(ctx)
	}
	return nil
}

func (c *countingLifecycle) Shutdown(ctx context.Context) error {
	atomic.AddInt32(&c.shutdown, 1)
	if c.shutdownFn != nil {
		return c.shutdownFn(ctx)
	}
	return nil
}

func (c *countingLifecycle) Health(_ context.Context) error {
	atomic.AddInt32(&c.health, 1)
	return nil
}
func (c *countingLifecycle) Close() error { return nil }

func TestZoneService_AddRemoveEntity(t *testing.T) {
	t.Parallel()
	zs, _ := newZoneService(t, 50*time.Millisecond)
	ctx := context.Background()

	e := &domain.Entity{ID: 1, Type: domain.EntityPlayer, X: 10, Y: 10, MoveSpeed: 150}
	require.NoError(t, zs.AddEntity(ctx, e))
	assert.Equal(t, 1, zs.EntityCount(ctx))

	require.NoError(t, zs.RemoveEntity(ctx, 1))
	assert.Equal(t, 0, zs.EntityCount(ctx))
}

func TestZoneService_AddEntityDuplicateErrors(t *testing.T) {
	t.Parallel()
	zs, _ := newZoneService(t, 50*time.Millisecond)
	ctx := context.Background()

	e := &domain.Entity{ID: 1, Type: domain.EntityPlayer, X: 10, Y: 10, MoveSpeed: 150}
	require.NoError(t, zs.AddEntity(ctx, e))
	require.Error(t, zs.AddEntity(ctx, e))
}

func TestZoneService_AddEntityOutOfBounds(t *testing.T) {
	t.Parallel()
	zs, _ := newZoneService(t, 50*time.Millisecond)
	ctx := context.Background()

	e := &domain.Entity{ID: 1, Type: domain.EntityMob, X: 999, Y: 999, MoveSpeed: 150}
	err := zs.AddEntity(ctx, e)
	require.Error(t, err)
	assert.ErrorIs(t, err, service.ErrInvalidCoordinates)
}

func TestZoneService_RemoveEntityUnknownErrors(t *testing.T) {
	t.Parallel()
	zs, _ := newZoneService(t, 50*time.Millisecond)
	ctx := context.Background()

	err := zs.RemoveEntity(ctx, 9999)
	require.Error(t, err)
	assert.ErrorIs(t, err, service.ErrEntityMissing)
}

func TestZoneService_MoveEntityComputesPath(t *testing.T) {
	t.Parallel()
	zs, _ := newZoneService(t, 50*time.Millisecond)
	ctx := context.Background()

	e := &domain.Entity{ID: 1, Type: domain.EntityPlayer, X: 10, Y: 10, MoveSpeed: 150}
	require.NoError(t, zs.AddEntity(ctx, e))

	require.NoError(t, zs.MoveEntity(ctx, 1, 20, 20))

	got, err := zs.GetEntity(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, 20, got.TargetX)
	assert.Equal(t, 20, got.TargetY)
	assert.NotEmpty(t, got.Path, "path should be set after MoveEntity")
}

func TestZoneService_MoveEntityDestinationBlocked(t *testing.T) {
	t.Parallel()
	md := newSyntheticMap("test", 50, 50)
	md.Walkable[25*50+25] = false // (25,25) is a wall
	tl := service.NewTickLoop(md, 50*time.Millisecond, silentLogger(), nopPublisher{})
	zs := service.NewZoneService(tl, newCountingLifecycle(), 150, 0, silentLogger())
	ctx := context.Background()

	e := &domain.Entity{ID: 1, Type: domain.EntityMob, X: 10, Y: 10, MoveSpeed: 150}
	require.NoError(t, zs.AddEntity(ctx, e))

	err := zs.MoveEntity(ctx, 1, 25, 25)
	require.Error(t, err)
	assert.ErrorIs(t, err, service.ErrDestinationNotWalkable)
}

func TestZoneService_GetVisibleReturnsEntities(t *testing.T) {
	t.Parallel()
	zs, _ := newZoneService(t, 50*time.Millisecond)
	ctx := context.Background()

	require.NoError(t, zs.AddEntity(ctx, &domain.Entity{ID: 1, Type: domain.EntityPlayer, X: 10, Y: 10, MoveSpeed: 150}))
	require.NoError(t, zs.AddEntity(ctx, &domain.Entity{ID: 2, Type: domain.EntityMob, X: 12, Y: 12, MoveSpeed: 150}))

	vis, err := zs.GetVisible(ctx, 1)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(vis), 2, "expected both entities to be visible from (10,10)")
}

func TestZoneService_GetEntityReturnsSnapshot(t *testing.T) {
	t.Parallel()
	zs, _ := newZoneService(t, 50*time.Millisecond)
	ctx := context.Background()

	require.NoError(t, zs.AddEntity(ctx, &domain.Entity{ID: 1, Type: domain.EntityPlayer, X: 5, Y: 5, MoveSpeed: 150}))
	require.NoError(t, zs.MoveEntity(ctx, 1, 30, 30))

	got, err := zs.GetEntity(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, 30, got.TargetX)
	assert.NotEmpty(t, got.Path)
}

func TestZoneService_GetEntityUnknownErrors(t *testing.T) {
	t.Parallel()
	zs, _ := newZoneService(t, 50*time.Millisecond)
	_, err := zs.GetEntity(context.Background(), 999)
	require.Error(t, err)
	assert.ErrorIs(t, err, service.ErrEntityMissing)
}

func TestZoneService_AgonesAllocateOnFirstPlayer(t *testing.T) {
	t.Parallel()
	md := newSyntheticMap("test", 50, 50)
	tl := service.NewTickLoop(md, 50*time.Millisecond, silentLogger(), nopPublisher{})
	ag := newCountingLifecycle()
	zs := service.NewZoneService(tl, ag, 150, 0, silentLogger())
	ctx := context.Background()

	// First entity is a player → Allocate should fire.
	require.NoError(t, zs.AddEntity(ctx, &domain.Entity{ID: 1, Type: domain.EntityPlayer, X: 10, Y: 10, MoveSpeed: 150}))
	// Second entity is a mob → Allocate should NOT fire again.
	require.NoError(t, zs.AddEntity(ctx, &domain.Entity{ID: 2, Type: domain.EntityMob, X: 20, Y: 20, MoveSpeed: 150}))

	assert.Equal(t, int32(1), atomic.LoadInt32(&ag.alloc),
		"Agones.Allocate should fire exactly once on the first player")
}

func TestZoneService_AgonesShutdownOnLastPlayerRemoved(t *testing.T) {
	t.Parallel()
	md := newSyntheticMap("test", 50, 50)
	tl := service.NewTickLoop(md, 50*time.Millisecond, silentLogger(), nopPublisher{})
	ag := newCountingLifecycle()
	zs := service.NewZoneService(tl, ag, 150, 0, silentLogger()) // grace=0 → immediate
	ctx := context.Background()

	require.NoError(t, zs.AddEntity(ctx, &domain.Entity{ID: 1, Type: domain.EntityPlayer, X: 10, Y: 10, MoveSpeed: 150}))
	require.NoError(t, zs.AddEntity(ctx, &domain.Entity{ID: 2, Type: domain.EntityMob, X: 20, Y: 20, MoveSpeed: 150}))

	require.NoError(t, zs.RemoveEntity(ctx, 2))
	// Mob removal alone should NOT trigger shutdown (players remain).
	assert.Equal(t, int32(0), atomic.LoadInt32(&ag.shutdown))

	require.NoError(t, zs.RemoveEntity(ctx, 1))

	// Wait for the shutdown goroutine to fire (grace=0).
	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&ag.shutdown) == 1
	}, 200*time.Millisecond, 5*time.Millisecond,
		"Agones.Shutdown should fire after the last player leaves")
}

func TestZoneService_AgonesShutdownSkippedWhenMobOnly(t *testing.T) {
	t.Parallel()
	md := newSyntheticMap("test", 50, 50)
	tl := service.NewTickLoop(md, 50*time.Millisecond, silentLogger(), nopPublisher{})
	ag := newCountingLifecycle()
	zs := service.NewZoneService(tl, ag, 150, 0, silentLogger())
	ctx := context.Background()

	require.NoError(t, zs.AddEntity(ctx, &domain.Entity{ID: 1, Type: domain.EntityMob, X: 10, Y: 10, MoveSpeed: 150}))
	require.NoError(t, zs.RemoveEntity(ctx, 1))

	// No player was ever present → onEmpty shouldn't fire Shutdown.
	// Allow some time for any potential spurious shutdown to manifest.
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(0), atomic.LoadInt32(&ag.shutdown))
}

func TestZoneService_ConcurrentAddRemove(t *testing.T) {
	t.Parallel()
	zs, _ := newZoneService(t, 50*time.Millisecond)
	ctx := context.Background()

	const workers = 16
	const ops = 50

	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				id := domain.EntityID(uint32(w)*1000 + uint32(i) + 1)
				e := &domain.Entity{
					ID:        id,
					Type:      domain.EntityMob,
					X:         (i % 50) + 1,
					Y:         ((i * 3) % 50) + 1,
					MoveSpeed: 150,
				}
				if err := zs.AddEntity(ctx, e); err != nil {
					t.Errorf("add: %v", err)
					return
				}
				if err := zs.RemoveEntity(ctx, id); err != nil {
					t.Errorf("remove: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	assert.Equal(t, 0, zs.EntityCount(ctx))
}

func TestZoneService_ContextCancelledPropagates(t *testing.T) {
	t.Parallel()
	zs, _ := newZoneService(t, 50*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := zs.AddEntity(ctx, &domain.Entity{ID: 1, Type: domain.EntityMob, X: 10, Y: 10})
	require.Error(t, err)
}
