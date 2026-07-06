//go:build unit

package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/features/zone/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/romap"
)

func silentLogger() *zerolog.Logger {
	l := zerolog.Nop()
	return &l
}

func newTickLoop(t *testing.T, w, h int, tickRate time.Duration) *TickLoop {
	t.Helper()
	md := newSyntheticMapSized(w, h)
	tl := NewTickLoop(md, tickRate, silentLogger())
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
	tl := NewTickLoop(md, 50*time.Millisecond, silentLogger())

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
