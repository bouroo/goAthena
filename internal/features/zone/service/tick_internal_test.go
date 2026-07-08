//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/features/zone/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/pathfinding"
	"github.com/bouroo/goAthena/pkg/ro/romap"
)

func silentLogger() *zerolog.Logger {
	l := zerolog.Nop()
	return &l
}

func newTickLoop(t *testing.T, w, h int, tickRate time.Duration) *TickLoop {
	t.Helper()
	md := newSyntheticMapSized(w, h)
	tl := NewTickLoop(md, tickRate, silentLogger(), nopPublisher{})
	require.NotNil(t, tl)
	return tl
}

func newSyntheticMapSized(w, h int) *romap.MapData {
	md := &romap.MapData{
		Name:     "test",
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

func TestTickLoop_MovementFollowsPath(t *testing.T) {
	t.Parallel()
	// Tick rate 10ms; speed 150ms/cell → 15 ticks/cell.
	tl := newTickLoop(t, 100, 100, 10*time.Millisecond)
	runTickLoop(t, tl)

	e := &domain.Entity{ID: 1, Type: domain.EntityPlayer, X: 10, Y: 10, MoveSpeed: 150}
	ctx := context.Background()
	_, err := tl.addEntity(ctx, e)
	require.NoError(t, err)
	require.NoError(t, tl.moveEntity(ctx, 1, 15, 10))

	require.Eventually(t, func() bool {
		got, _ := tl.getEntity(ctx, 1)
		return got != nil && got.X == 15 && got.Y == 10
	}, 2*time.Second, 20*time.Millisecond, "entity should reach (15,10)")
}

func TestTickLoop_SpeedGatingSlowsMovement(t *testing.T) {
	t.Parallel()
	// Tick rate 10ms; speed 500ms/cell → 50 ticks/cell.
	tl := newTickLoop(t, 100, 100, 10*time.Millisecond)
	runTickLoop(t, tl)

	e := &domain.Entity{ID: 1, Type: domain.EntityMob, X: 10, Y: 10, MoveSpeed: 500}
	ctx := context.Background()
	_, err := tl.addEntity(ctx, e)
	require.NoError(t, err)
	require.NoError(t, tl.moveEntity(ctx, 1, 30, 10))

	require.Eventually(t, func() bool {
		got, _ := tl.getEntity(ctx, 1)
		return got != nil && got.X > 10
	}, 5*time.Second, 50*time.Millisecond, "entity should eventually move")

	got, err := tl.getEntity(ctx, 1)
	require.NoError(t, err)
	assert.Greater(t, got.X, 10)
	assert.LessOrEqual(t, got.X, 30)
}

func TestTickLoop_AOITowerCrossUpdates(t *testing.T) {
	t.Parallel()
	// Move across an 18-cell tower boundary (TowerSize=18).
	tl := newTickLoop(t, 200, 200, 10*time.Millisecond)
	runTickLoop(t, tl)

	e := &domain.Entity{ID: 1, Type: domain.EntityPlayer, X: 5, Y: 5, MoveSpeed: 100}
	ctx := context.Background()
	_, err := tl.addEntity(ctx, e)
	require.NoError(t, err)
	require.NoError(t, tl.moveEntity(ctx, 1, 50, 50))

	// Origin tower (5,5 → tower 0). Destination tower after move: (50,50 → tower (2,2)).
	gw := tl.Grid().GridWidth()
	expectedTID := 2*gw + 2
	require.Eventually(t, func() bool {
		_, _, tid, ok := tl.Grid().EntityLocation(aoi.EntityID(1))
		return ok && tid == expectedTID
	}, 5*time.Second, 20*time.Millisecond, "entity should reach tower (2,2)")
}

func TestTickLoop_StartStopOnContextCancel(t *testing.T) {
	t.Parallel()
	tl := newTickLoop(t, 50, 50, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())

	go func() { _ = tl.Start(ctx) }()

	// Give the loop time to start.
	time.Sleep(20 * time.Millisecond)

	cancel()

	select {
	case <-tl.Done():
	case <-time.After(200 * time.Millisecond):
		t.Fatal("tick loop did not stop within 200ms of context cancellation")
	}
}

func TestTickLoop_StartIsIdempotent(t *testing.T) {
	t.Parallel()
	tl := newTickLoop(t, 50, 50, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	require.NoError(t, tl.Start(ctx), "first Start")
	require.NoError(t, tl.Start(ctx), "second Start should be a no-op")
	require.NoError(t, tl.Start(ctx), "third Start should be a no-op")

	cancel()
	select {
	case <-tl.Done():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("tick loop did not stop")
	}
}

func TestTickLoop_PathComputation(t *testing.T) {
	t.Parallel()
	md := newSyntheticMapSized(50, 50)
	tl := NewTickLoop(md, 50*time.Millisecond, silentLogger(), nopPublisher{})

	e := &domain.Entity{ID: 1, Type: domain.EntityMob, X: 5, Y: 5, MoveSpeed: 150}
	ctx := context.Background()
	_, err := tl.addEntity(ctx, e)
	require.NoError(t, err)
	require.NoError(t, tl.moveEntity(ctx, 1, 45, 45))

	got, err := tl.getEntity(ctx, 1)
	require.NoError(t, err)
	assert.NotEmpty(t, got.Path, "path should be computed")

	// 4-connected Manhattan distance from (5,5) to (45,45) is 80, but
	// the pathfinder caps at MaxWalkPath=32 and trims diagonally.
	assert.LessOrEqual(t, len(got.Path), 32)
}

func TestTickLoop_ConcurrentMoveAndQuery(t *testing.T) {
	t.Parallel()
	tl := newTickLoop(t, 200, 200, 5*time.Millisecond)
	runTickLoop(t, tl)

	ctx := context.Background()
	_, err := tl.addEntity(ctx, &domain.Entity{
		ID: 1, Type: domain.EntityPlayer, X: 50, Y: 50, MoveSpeed: 100,
	})
	require.NoError(t, err)

	var wg sync.WaitGroup
	wg.Add(4)

	// Writer: keep assigning new targets.
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			x := 10 + (i*3)%180
			y := 10 + (i*5)%180
			_ = tl.moveEntity(ctx, 1, x, y)
			time.Sleep(time.Millisecond)
		}
	}()

	// Readers: query visible + entity concurrently.
	for r := 0; r < 3; r++ {
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_, _ = tl.getEntity(ctx, 1)
				_, _ = tl.getVisible(ctx, 1)
				time.Sleep(time.Millisecond)
			}
		}()
	}

	wg.Wait()
}

// runTickLoop starts the tick loop in a background goroutine and registers
// cleanup that cancels the loop on test completion.
func runTickLoop(t *testing.T, tl *TickLoop) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = tl.Start(ctx) }()
	// Brief settle so the loop enters its select.
	time.Sleep(5 * time.Millisecond)
}

// waitDone blocks until done is closed or the deadline channel fires.
// Returns nil on done closed, context error otherwise.
func waitDone(done, deadline <-chan struct{}) error {
	select {
	case <-done:
		return nil
	case <-deadline:
		return context.DeadlineExceeded
	}
}

// barrierGrid wraps a pathfinding.Grid and blocks the first Walkable call
// for a specific cell until the test releases it. Used to inject a
// deterministic delay inside moveEntity's RLock→compute→Lock window so
// tests can mutate the entity while computePath is running.
type barrierGrid struct {
	inner   pathfinding.Grid
	blockX  int
	blockY  int
	arrived chan struct{}
	release chan struct{}
	mu      sync.Mutex
	blocked bool
}

func newBarrierGrid(inner pathfinding.Grid, x, y int) *barrierGrid {
	return &barrierGrid{
		inner:   inner,
		blockX:  x,
		blockY:  y,
		arrived: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (g *barrierGrid) Width() int  { return g.inner.Width() }
func (g *barrierGrid) Height() int { return g.inner.Height() }

func (g *barrierGrid) Walkable(x, y int) bool {
	if x == g.blockX && y == g.blockY {
		g.mu.Lock()
		if g.blocked {
			g.mu.Unlock()
			return g.inner.Walkable(x, y)
		}
		g.blocked = true
		g.mu.Unlock()
		g.arrived <- struct{}{}
		<-g.release
	}
	return g.inner.Walkable(x, y)
}

func TestMoveEntity_DiscardStalePathWhenEntityMovedDuringCompute(t *testing.T) {
	t.Parallel()
	tl := newTickLoop(t, 100, 100, 10*time.Millisecond)
	ctx := context.Background()

	e := &domain.Entity{ID: 1, Type: domain.EntityPlayer, X: 10, Y: 10, MoveSpeed: 150}
	_, err := tl.addEntity(ctx, e)
	require.NoError(t, err)

	// Install a barrier on the pathfinder's grid that blocks the first
	// Walkable(10,10) call. That call happens inside computePath, which
	// runs AFTER moveEntity's RLock release and BEFORE its write Lock —
	// the exact window where the entity's position can change.
	bg := newBarrierGrid(pathfinding.FromMapData(tl.mapData), 10, 10)
	tl.pf = pathfinding.New(bg)

	moveDone := make(chan error, 1)
	go func() { moveDone <- tl.moveEntity(ctx, 1, 20, 10) }()

	select {
	case <-bg.arrived:
	case <-time.After(2 * time.Second):
		t.Fatal("moveEntity did not reach the barrier within 2s")
	}

	// MoveEntity has released its RLock and is blocked inside computePath.
	// Mutate the entity's position to simulate a concurrent tick advance.
	tl.mu.Lock()
	e.X = 15
	e.Y = 10
	tl.mu.Unlock()

	// Release moveEntity; it will re-acquire the Lock, see the position
	// mismatch, and discard the stale path.
	close(bg.release)

	select {
	case err := <-moveDone:
		require.NoError(t, err, "discarded stale path should not be an error")
	case <-time.After(2 * time.Second):
		t.Fatal("moveEntity did not return within 2s after barrier release")
	}

	tl.mu.RLock()
	targetX, targetY, pathLen := e.TargetX, e.TargetY, len(e.Path)
	tl.mu.RUnlock()

	assert.Equal(t, 0, targetX, "stale TargetX should not be assigned")
	assert.Equal(t, 0, targetY, "stale TargetY should not be assigned")
	assert.Equal(t, 0, pathLen, "stale Path should not be assigned")
}

func TestMoveEntity_ReturnsErrorAfterEntityRemoved(t *testing.T) {
	t.Parallel()
	tl := newTickLoop(t, 100, 100, 10*time.Millisecond)
	ctx := context.Background()

	e := &domain.Entity{ID: 42, Type: domain.EntityPlayer, X: 10, Y: 10, MoveSpeed: 150}
	_, err := tl.addEntity(ctx, e)
	require.NoError(t, err)

	require.NoError(t, tl.removeEntity(ctx, 42))

	err = tl.moveEntity(ctx, 42, 20, 10)
	require.Error(t, err, "moveEntity after removeEntity should return an error")
	assert.True(t, errors.Is(err, ErrEntityMissing),
		"expected ErrEntityMissing, got: %v", err)
}

func TestAddRemoveEntity_NoGridLeakUnderConcurrency(t *testing.T) {
	t.Parallel()
	tl := newTickLoop(t, 100, 100, 10*time.Millisecond)
	ctx := context.Background()

	const id domain.EntityID = 7
	const iterations = 100

	var wg sync.WaitGroup
	for i := 0; i < iterations; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			e := &domain.Entity{
				ID:        id,
				Type:      domain.EntityPlayer,
				X:         5 + (i % 10),
				Y:         5 + (i % 10),
				MoveSpeed: 150,
			}
			_, _ = tl.addEntity(ctx, e)
		}()
		go func() {
			defer wg.Done()
			_ = tl.removeEntity(ctx, id)
		}()
	}
	wg.Wait()

	tl.mu.RLock()
	inMap := tl.entities[id] != nil
	tl.mu.RUnlock()

	_, _, _, inGrid := tl.Grid().EntityLocation(aoi.EntityID(id))

	assert.Equal(t, inMap, inGrid,
		"grid/map desync: inMap=%v, inGrid=%v", inMap, inGrid)
}

func TestAddEntity_HoldsLockThroughGridInsert(t *testing.T) {
	t.Parallel()
	tl := newTickLoop(t, 100, 100, 10*time.Millisecond)
	ctx := context.Background()

	e := &domain.Entity{ID: 99, Type: domain.EntityMob, X: 20, Y: 20, MoveSpeed: 150}
	_, err := tl.addEntity(ctx, e)
	require.NoError(t, err)

	got, err := tl.getEntity(ctx, 99)
	require.NoError(t, err, "entity should be in tl.entities after addEntity")
	assert.Equal(t, domain.EntityID(99), got.ID)

	_, _, _, ok := tl.Grid().EntityLocation(aoi.EntityID(99))
	assert.True(t, ok, "entity should be in AOI grid after addEntity")

	require.NoError(t, tl.removeEntity(ctx, 99))

	_, err = tl.getEntity(ctx, 99)
	require.Error(t, err, "entity should not be in tl.entities after removeEntity")
	assert.True(t, errors.Is(err, ErrEntityMissing))

	_, _, _, ok = tl.Grid().EntityLocation(aoi.EntityID(99))
	assert.False(t, ok, "entity should not be in AOI grid after removeEntity")
}
