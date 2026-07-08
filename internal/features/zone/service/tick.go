package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"

	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	"github.com/bouroo/goAthena/internal/features/zone/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/pathfinding"
	"github.com/bouroo/goAthena/pkg/ro/romap"
)

// TickLoop is the physics simulation thread. It owns the entity map, the
// AOI grid, and the pathfinder, and runs a fixed-rate ticker that
// advances every entity along its path.
//
// Concurrency model:
//
//   - The TickLoop is the only writer of entity positions (Path/X/Y).
//   - Service methods (AddEntity, RemoveEntity, MoveEntity, GetVisible,
//     GetEntity) take the RWMutex write lock for mutations and read
//     lock for queries.
//   - The tick goroutine holds no lock while calling the move callback
//     but takes the read lock for the entity iteration phase.
//   - AOI grid operations (aoi.GridManager) are themselves thread-safe
//     and lock-internally; the TickLoop's outer lock only protects the
//     entity map and per-entity path/position fields.
type TickLoop struct {
	mu       sync.RWMutex
	entities map[domain.EntityID]*domain.Entity
	grid     *aoi.GridManager
	pf       *pathfinding.Pathfinder
	mapData  *romap.MapData

	tickNum  uint64
	tickRate time.Duration
	logger   *zerolog.Logger

	startOnce sync.Once
	done      chan struct{}

	// onEmpty is invoked once when the entity map transitions to empty
	// AND no players remain. Wired by NewZoneService.
	onEmpty func()

	publisher domain.Publisher
}

// NewTickLoop builds a TickLoop for the given map. The grid manager and
// pathfinder are derived from the map data; both are single-instance —
// the pathfinder is not safe for concurrent use but the tick loop runs
// single-threaded so this is fine.
func NewTickLoop(
	mapData *romap.MapData,
	tickRate time.Duration,
	logger *zerolog.Logger,
	publisher domain.Publisher,
) *TickLoop {
	if mapData == nil {
		panic(errors.New("zone: tick loop requires non-nil map data"))
	}
	if tickRate <= 0 {
		tickRate = 50 * time.Millisecond
	}
	if logger == nil {
		panic(errors.New("zone: tick loop requires a logger"))
	}
	if publisher == nil {
		panic(errors.New("zone: tick loop requires a publisher"))
	}

	grid := aoi.NewGridManager(mapData.Width, mapData.Height)
	pf := pathfinding.New(pathfinding.FromMapData(mapData))

	tl := &TickLoop{
		entities:  make(map[domain.EntityID]*domain.Entity),
		grid:      grid,
		pf:        pf,
		mapData:   mapData,
		tickRate:  tickRate,
		logger:    logger,
		done:      make(chan struct{}),
		publisher: publisher,
	}

	return tl
}

// setOnEmpty wires the empty-zone callback. Called by NewZoneService.
func (tl *TickLoop) setOnEmpty(fn func()) {
	tl.mu.Lock()
	tl.onEmpty = fn
	tl.mu.Unlock()
}

// MapData returns the loaded map. Useful for tests and diagnostic dumps.
func (tl *TickLoop) MapData() *romap.MapData { return tl.mapData }

// Grid returns the AOI grid. Exposed for tests and debug endpoints.
func (tl *TickLoop) Grid() *aoi.GridManager { return tl.grid }

// TickRate returns the configured tick period.
func (tl *TickLoop) TickRate() time.Duration { return tl.tickRate }

// Start runs the tick loop until ctx is cancelled. Returns nil on clean
// shutdown, ctx.Err() if context cancelled mid-loop. Safe to call once;
// subsequent calls return nil without starting a new goroutine.
func (tl *TickLoop) Start(ctx context.Context) error {
	var started bool
	tl.startOnce.Do(func() {
		started = true
		go tl.run(ctx)
	})
	if !started {
		return nil
	}
	return nil
}

// run is the tick loop body. Separated from Start so the once-only
// invariant is enforced by sync.Once without leaking the running goroutine.
func (tl *TickLoop) run(ctx context.Context) {
	tl.logger.Info().
		Dur("tick_rate", tl.tickRate).
		Str("map", tl.mapData.Name).
		Int("width", tl.mapData.Width).
		Int("height", tl.mapData.Height).
		Msg("zone: tick loop starting")

	defer func() {
		close(tl.done)
		tl.logger.Info().Msg("zone: tick loop stopped")
	}()

	ticker := time.NewTicker(tl.tickRate)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t0 := time.Now()
			if err := tl.tick(ctx); err != nil {
				if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
					tl.logger.Warn().Err(err).Msg("zone: tick step error")
				}
			}
			elapsed := time.Since(t0)
			if elapsed > 5*time.Millisecond {
				tl.logger.Warn().
					Dur("elapsed", elapsed).
					Msg("zone: tick exceeded 5ms gate")
			}
		}
	}
}

// Done returns a channel closed when the tick loop exits. The app
// composition root waits on this during shutdown to ensure all
// in-flight ticks have flushed before releasing resources.
func (tl *TickLoop) Done() <-chan struct{} { return tl.done }

// tick advances the simulation by one step. Acquires the read lock to
// iterate entities; calls into the AOI grid (thread-safe) for
// position updates. Per-cell AOI broadcasts are out of scope for
// Phase 4 (the TickLoop moves positions; the broadcast happens when a
// gRPC handler reads GetVisible).
func (tl *TickLoop) tick(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("zone: tick: %w", err)
	}

	tl.mu.Lock()
	tl.tickNum++
	currentTick := tl.tickNum
	tickRateMs := max(int(tl.tickRate/time.Millisecond), 1)

	type pending struct {
		id   domain.EntityID
		x, y int
		srcX int
		srcY int
		typ  domain.EntityType
	}
	var moves []pending

	for id, e := range tl.entities {
		if len(e.Path) == 0 {
			continue
		}
		if currentTick < e.NextMoveTick {
			continue
		}

		next := e.Path[0]
		e.Path = e.Path[1:]
		srcX, srcY := e.X, e.Y
		e.X = next.X
		e.Y = next.Y
		e.NextMoveTick = currentTick + moveInterval(e.MoveSpeed, tickRateMs)
		if len(e.Path) == 0 {
			e.TargetX = 0
			e.TargetY = 0
		}

		moves = append(moves, pending{
			id:   id,
			x:    e.X,
			y:    e.Y,
			srcX: srcX,
			srcY: srcY,
			typ:  e.Type,
		})
	}
	tl.mu.Unlock()

	for _, m := range moves {
		if err := tl.grid.MoveEntity(aoi.EntityID(m.id), m.x, m.y); err != nil {
			tl.logger.Warn().Err(err).
				Uint32("entity_id", uint32(m.id)).
				Int("x", m.x).Int("y", m.y).
				Msg("zone: AOI move failed")
			continue
		}
		//nolint:gosec // uint32↔int map-coord casts are bounded by map dimensions; overflow impossible in practice.
		tl.publish(ctx, &zonev1.ZoneEvent{
			Event: &zonev1.ZoneEvent_Moved{
				Moved: &zonev1.EntityMoved{
					EntityId:      uint32(m.id),
					DestX:         uint32(m.x),
					DestY:         uint32(m.y),
					SrcX:          uint32(m.srcX),
					SrcY:          uint32(m.srcY),
					MoveStartTime: uint64(time.Now().UnixMilli()),
				},
			},
		}, "move")
	}

	return nil
}

// addEntity registers a new entity. Returns (isFirst, err) where
// isFirst indicates the entity map transitioned from empty (used to
// trigger Agones.Allocate on the first player). The ZoneEvent_Spawned
// notification is published AFTER the lock is released and AFTER the
// entity is fully registered, so subscribers always observe a state the
// tick loop has already accepted. A publish failure is logged but never
// propagates — the entity is still part of the zone.
func (tl *TickLoop) addEntity(ctx context.Context, e *domain.Entity) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, fmt.Errorf("zone: add entity: %w", err)
	}
	if !tl.mapData.IsWalkable(e.X, e.Y) {
		return false, fmt.Errorf("%w: (%d,%d) not walkable", ErrInvalidCoordinates, e.X, e.Y)
	}

	tl.mu.Lock()
	if _, exists := tl.entities[e.ID]; exists {
		tl.mu.Unlock()
		return false, fmt.Errorf("%w: id=%d", ErrEntityExists, e.ID)
	}
	wasEmpty := len(tl.entities) == 0
	aoiEnt := &aoi.Entity{
		ID:   aoi.EntityID(e.ID),
		Type: toAOIType(e.Type),
		X:    e.X,
		Y:    e.Y,
	}
	if err := tl.grid.AddEntity(aoiEnt); err != nil {
		tl.mu.Unlock()
		return false, fmt.Errorf("zone: AOI add: %w", err)
	}
	tl.entities[e.ID] = e
	tl.mu.Unlock()

	//nolint:gosec // uint32↔int map-coord casts are bounded by map dimensions; overflow impossible in practice.
	tl.publish(ctx, &zonev1.ZoneEvent{
		Event: &zonev1.ZoneEvent_Spawned{
			Spawned: &zonev1.EntitySpawned{
				EntityId:   uint32(e.ID),
				EntityType: uint32(e.Type),
				X:          uint32(e.X),
				Y:          uint32(e.Y),
				// Name is left empty: the domain.Entity model does not
				// carry the player/mob display name. Step 2 (gateway)
				// is expected to resolve names from a separate source
				// when it consumes these events.
				Name: "",
			},
		},
	}, "spawn")

	return wasEmpty, nil
}

// removeEntity removes an entity from both the tick loop and AOI grid.
// Triggers onEmpty when the last player leaves. The ZoneEvent_Vanished
// notification is published AFTER the entity is fully unregistered, and
// outside the lock, to keep NATS traffic off the hot path. Publish
// failures are logged but do not change the removal outcome.
func (tl *TickLoop) removeEntity(ctx context.Context, id domain.EntityID) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("zone: remove entity: %w", err)
	}

	tl.mu.Lock()
	if _, exists := tl.entities[id]; !exists {
		tl.mu.Unlock()
		return fmt.Errorf("%w: id=%d", ErrEntityMissing, id)
	}
	hadPlayers := tl.hasPlayersLocked()
	delete(tl.entities, id)
	isEmpty := len(tl.entities) == 0
	onEmpty := tl.onEmpty
	if err := tl.grid.RemoveEntity(aoi.EntityID(id)); err != nil {
		tl.logger.Warn().Err(err).
			Uint32("entity_id", uint32(id)).
			Msg("zone: AOI remove failed")
	}
	tl.mu.Unlock()

	tl.publish(ctx, &zonev1.ZoneEvent{
		Event: &zonev1.ZoneEvent_Vanished{
			Vanished: &zonev1.EntityVanished{
				EntityId: uint32(id),
				// 1 = logged out; future codes (teleport, out-of-sight)
				// will be added by Step 2 / 3 when those vanish reasons
				// are introduced.
				Type: 1,
			},
		},
	}, "vanish")

	if isEmpty && hadPlayers && onEmpty != nil {
		onEmpty()
	}
	return nil
}

// publish hands event to the zone Publisher, tagging the failure mode
// for log filtering. It must be called with a context that is still
// valid (typically the same ctx passed to addEntity/removeEntity/tick).
// Publish failures are logged and deliberately swallowed: a dropped
// NATS message must not fail a state mutation.
func (tl *TickLoop) publish(ctx context.Context, event *zonev1.ZoneEvent, kind string) {
	if err := tl.publisher.PublishEvent(ctx, tl.mapData.Name, event); err != nil {
		tl.logger.Warn().
			Err(err).
			Str("kind", kind).
			Msg("zone: publish event failed")
	}
}

// hasPlayersLocked reports whether the zone currently contains any
// player-typed entities. Caller must hold tl.mu (read or write).
func (tl *TickLoop) hasPlayersLocked() bool {
	for _, e := range tl.entities {
		if e.Type == domain.EntityPlayer {
			return true
		}
	}
	return false
}

// moveEntity validates the target, computes an A* path, and stores it
// on the entity. The A* search runs outside the lock (single-threaded
// and may be slow); the resulting path is assigned under the write lock
// so the tick loop sees a consistent entity state.
func (tl *TickLoop) moveEntity(ctx context.Context, id domain.EntityID, x, y int) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("zone: move entity: %w", err)
	}

	tl.mu.RLock()
	e, ok := tl.entities[id]
	if !ok {
		tl.mu.RUnlock()
		return fmt.Errorf("%w: id=%d", ErrEntityMissing, id)
	}
	sx, sy := e.X, e.Y
	tl.mu.RUnlock()

	path, err := computePath(sx, sy, x, y, tl.mapData, tl.pf)
	if err != nil {
		return err
	}

	tl.mu.Lock()
	defer tl.mu.Unlock()
	cur, ok := tl.entities[id]
	if !ok {
		return fmt.Errorf("%w: id=%d", ErrEntityMissing, id)
	}
	if cur.X != sx || cur.Y != sy {
		tl.logger.Debug().
			Uint32("entity_id", uint32(id)).
			Int("from_x", sx).Int("from_y", sy).
			Int("cur_x", cur.X).Int("cur_y", cur.Y).
			Msg("zone: discarding stale path; entity moved during compute")
		return nil
	}
	cur.TargetX = x
	cur.TargetY = y
	cur.Path = path
	cur.NextMoveTick = tl.tickNum + moveInterval(cur.MoveSpeed, int(tl.tickRate/time.Millisecond))
	return nil
}

// getVisible returns the entities in the AOI viewport around entity id.
func (tl *TickLoop) getVisible(ctx context.Context, id domain.EntityID) ([]*domain.Entity, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("zone: get visible: %w", err)
	}
	tl.mu.RLock()
	e, ok := tl.entities[id]
	if !ok {
		tl.mu.RUnlock()
		return nil, fmt.Errorf("%w: id=%d", ErrEntityMissing, id)
	}
	x, y := e.X, e.Y
	tl.mu.RUnlock()
	return snapshotVisible(tl.grid, x, y), nil
}

// getEntity returns a snapshot copy of the entity.
func (tl *TickLoop) getEntity(ctx context.Context, id domain.EntityID) (*domain.Entity, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("zone: get entity: %w", err)
	}
	tl.mu.RLock()
	defer tl.mu.RUnlock()
	e, ok := tl.entities[id]
	if !ok {
		return nil, fmt.Errorf("%w: id=%d", ErrEntityMissing, id)
	}
	cp := *e
	if e.Path != nil {
		cp.Path = append([]pathfinding.Point(nil), e.Path...)
	}
	return &cp, nil
}

// entityCount returns the current entity count (no lock held).
func (tl *TickLoop) entityCount() int {
	tl.mu.RLock()
	n := len(tl.entities)
	tl.mu.RUnlock()
	return n
}
