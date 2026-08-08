// Package app implements the world bounded context use cases: entity lifecycle
// (add/remove/move), AOI visibility, and the 50 Hz tick loop. The WorldService
// owns the in-memory entity registry and the per-map AOI grids; it is the
// authority for on-map state.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
)

// WorldService owns the in-memory entity registry and per-map AOI grids. It is
// safe for concurrent use: a mutex protects the registry; the AOI grid manages
// its own internal synchronization. The 50 Hz tick loop drives periodic entity
// updates (spawn/despawn/visibility refresh).
type WorldService struct {
	mu       sync.RWMutex
	entities map[domain.EntityID]*domain.Entity  // global by EntityID
	byMap    map[string]map[domain.EntityID]bool // map→entity set
	grids    map[string]*aoi.GridManager         // map→AOI grid
	repo     domain.WorldRepository
	log      *slog.Logger
	tickRate time.Duration
	stopCh   chan struct{}
}

// NewWorldService builds a WorldService. The tick rate is derived from
// tickRateHz (50 = 20 ms). The tick loop starts on StartTick and stops on Stop.
func NewWorldService(repo domain.WorldRepository, log *slog.Logger, tickRateHz int) *WorldService {
	if tickRateHz < 1 {
		tickRateHz = 50
	}
	return &WorldService{
		entities: make(map[domain.EntityID]*domain.Entity),
		byMap:    make(map[string]map[domain.EntityID]bool),
		grids:    make(map[string]*aoi.GridManager),
		repo:     repo,
		log:      log,
		tickRate: time.Second / time.Duration(tickRateHz),
		stopCh:   make(chan struct{}),
	}
}

// ensureGrid returns the AOI grid for a map, creating it on first access. Map
// dimensions default to a conservative 512×512 cell canvas; map-specific
// bounds are applied when the map data loader lands (later milestone).
func (w *WorldService) ensureGrid(mapName string) *aoi.GridManager {
	w.mu.Lock()
	defer w.mu.Unlock()
	gm, ok := w.grids[mapName]
	if !ok {
		gm = aoi.NewGridManager(512, 512)
		w.grids[mapName] = gm
		w.byMap[mapName] = make(map[domain.EntityID]bool)
	}
	return gm
}

// AddEntity registers an entity in the registry and inserts it into its map's
// AOI grid. Returns ErrEntityAlreadyExists if the ID is already present.
func (w *WorldService) AddEntity(e domain.Entity) error {
	w.mu.Lock()
	if _, exists := w.entities[e.ID]; exists {
		w.mu.Unlock()
		return domain.ErrEntityAlreadyExists
	}
	eptr := &e
	w.entities[e.ID] = eptr
	w.mu.Unlock()

	gm := w.ensureGrid(e.Map)
	if err := gm.AddEntity(&aoi.Entity{
		ID: aoi.EntityID(e.ID),
		X:  int(e.Pos.X),
		Y:  int(e.Pos.Y),
	}); err != nil {
		return fmt.Errorf("aoi add: %w", err)
	}

	w.mu.Lock()
	w.byMap[e.Map][e.ID] = true
	w.mu.Unlock()
	return nil
}

// RemoveEntity removes an entity from the registry and AOI grid.
func (w *WorldService) RemoveEntity(id domain.EntityID) error {
	w.mu.Lock()
	e, ok := w.entities[id]
	if !ok {
		w.mu.Unlock()
		return domain.ErrEntityNotFound
	}
	delete(w.entities, id)
	mapName := e.Map
	if set, ok := w.byMap[mapName]; ok {
		delete(set, id)
	}
	gm := w.grids[mapName]
	w.mu.Unlock()

	if gm != nil {
		_ = gm.RemoveEntity(aoi.EntityID(id))
	}
	return nil
}

// Get returns a copy of the entity by ID.
func (w *WorldService) Get(id domain.EntityID) (domain.Entity, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	e, ok := w.entities[id]
	if !ok {
		return domain.Entity{}, domain.ErrEntityNotFound
	}
	return *e, nil
}

// MoveEntity updates an entity's position and moves it in the AOI grid.
func (w *WorldService) MoveEntity(id domain.EntityID, pos domain.Position) error {
	w.mu.Lock()
	e, ok := w.entities[id]
	if !ok {
		w.mu.Unlock()
		return domain.ErrEntityNotFound
	}
	e.Pos = pos
	mapName := e.Map
	gm := w.grids[mapName]
	w.mu.Unlock()

	if gm != nil {
		// AOI grid update: remove + re-add at new position (simple, correct;
		// a delta-update path can replace this if profiling shows a hotspot).
		_ = gm.RemoveEntity(aoi.EntityID(id))
		_ = gm.AddEntity(&aoi.Entity{
			ID: aoi.EntityID(id), X: int(pos.X), Y: int(pos.Y),
		})
	}
	return nil
}

// QueryVisible returns the entity IDs visible from a position on a map (within
// the AOI view range). This is the input to the spawn/visibility refresh path.
func (w *WorldService) QueryVisible(mapName string, x, y int) []domain.EntityID {
	w.mu.RLock()
	gm := w.grids[mapName]
	w.mu.RUnlock()
	if gm == nil {
		return nil
	}
	visible := gm.QueryVisible(x, y)
	out := make([]domain.EntityID, 0, len(visible))
	for _, e := range visible {
		out = append(out, domain.EntityID(e.ID))
	}
	return out
}

// StartTick runs the 50 Hz game loop. Each tick fires the update callback (the
// gateway/ingress layer registers its broadcast logic there). Blocks until Stop.
func (w *WorldService) StartTick(ctx context.Context, update func(ctx context.Context, dt time.Duration)) {
	ticker := time.NewTicker(w.tickRate)
	defer ticker.Stop()
	w.log.Info("world tick loop started", "rate_hz", int(time.Second/w.tickRate))
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case t := <-ticker.C:
			if update != nil {
				update(ctx, t.Sub(t.Add(-w.tickRate)))
			}
		}
	}
}

// Stop signals the tick loop to drain.
func (w *WorldService) Stop() {
	select {
	case <-w.stopCh:
	default:
		close(w.stopCh)
	}
}

// EnterMap loads a character's enter state from the repo and registers it as a
// PC entity. Returns the populated entity for the map-enter response path.
func (w *WorldService) EnterMap(ctx context.Context, charID uint32) (domain.Entity, error) {
	e, err := w.repo.LoadEnterState(ctx, charID)
	if err != nil {
		return domain.Entity{}, fmt.Errorf("load enter state: %w", err)
	}
	e.ID = domain.EntityID(charID)
	e.Type = domain.EntityTypePC
	if err := w.AddEntity(e); err != nil {
		return domain.Entity{}, err
	}
	_ = w.repo.SetOnline(ctx, charID, true, e.Pos)
	return e, nil
}

// LeaveMap removes a character from the world and marks it offline.
func (w *WorldService) LeaveMap(ctx context.Context, charID uint32) error {
	e, err := w.Get(domain.EntityID(charID))
	if err != nil {
		return err
	}
	if err := w.RemoveEntity(domain.EntityID(charID)); err != nil {
		return err
	}
	if err := w.repo.SetOnline(ctx, charID, false, e.Pos); err != nil {
		return fmt.Errorf("set offline: %w", err)
	}
	return nil
}

// SetPosition persists a char's destination map + position (warp/transit). The
// caller sends MapMoveResponse so the client reconnects and EnterMap loads this.
func (w *WorldService) SetPosition(ctx context.Context, charID uint32, mapName string, pos domain.Position) error {
	if err := w.repo.SetPosition(ctx, charID, mapName, pos); err != nil {
		return fmt.Errorf("set position: %w", err)
	}
	return nil
}
