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
// its own internal synchronization. The periodic tick loop fires a
// caller-supplied update callback; respawn and combat advance on their own
// event paths, not this loop.
type WorldService struct {
	mu       sync.RWMutex
	entities map[domain.EntityID]*domain.Entity  // global by EntityID
	byMap    map[string]map[domain.EntityID]bool // map→entity set
	grids    map[string]*aoi.GridManager         // map→AOI grid
	repo     domain.WorldRepository
	log      *slog.Logger
	tickRate time.Duration
	stopCh   chan struct{}
	// hpSeconds/spSeconds accumulate elapsed time since the last natural regen.
	// When the standing interval elapses (6 s HP, 8 s SP) RegenTick advances every
	// living PC's vitals. Written only from the single tick-loop goroutine, so no
	// lock is required.
	hpSeconds float64
	spSeconds float64
	// OnStatChange, when set, is invoked after RegenTick changes a player's HP/SP
	// so the gateway can emit ZC_PAR_CHANGE to that player's client. charID is the
	// entity's GID (char_id). nil = regen is silent (server state still advances;
	// useful for tests and headless operation). Invoked off the world mutex.
	OnStatChange func(charID uint32, hp, sp int32)
}

// Pre-renewal standing natural-regen intervals (rAthena status_natural_heal).
// Sitting halves them; moving/attacking pauses regen — neither is modeled here.
const (
	hpRegenInterval = 6 * time.Second
	spRegenInterval = 8 * time.Second
)

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

// PlayersNear returns the IDs of player-character entities within the AOI view
// range (15 cells) of pos on mapName. These are the recipients a localized map
// event (a move, a drop, a mob death) must reach so OTHER players see it. Mobs
// and NPCs are excluded because they own no client connection and never receive
// packets. The query reuses the AOI grid's broadcast range so a neighbor who
// could see an event is exactly a neighbor who is told about it.
//
// The world mutex is held across the grid query and the type filter so the two
// reads form one consistent snapshot; the AOI grid's own per-tower locks are
// acquired underneath (world→tower order, matching AddEntity/MoveEntity).
func (w *WorldService) PlayersNear(mapName string, pos domain.Position) []domain.EntityID {
	w.mu.RLock()
	defer w.mu.RUnlock()
	gm := w.grids[mapName]
	if gm == nil {
		return nil
	}
	visible := gm.QueryVisible(int(pos.X), int(pos.Y))
	out := make([]domain.EntityID, 0, len(visible))
	for _, e := range visible {
		ce, ok := w.entities[domain.EntityID(e.ID)]
		if ok && ce.Type == domain.EntityTypePC {
			out = append(out, ce.ID)
		}
	}
	return out
}

// PlayersOnMap returns the IDs of every player-character entity currently on
// mapName. It is the map-wide variant of PlayersNear, used when an event has no
// single cell anchor. PlayersNear is the preferred tight query for ordinary
// localized events.
func (w *WorldService) PlayersOnMap(mapName string) []domain.EntityID {
	w.mu.RLock()
	defer w.mu.RUnlock()
	set := w.byMap[mapName]
	out := make([]domain.EntityID, 0, len(set))
	for id := range set {
		if ce, ok := w.entities[id]; ok && ce.Type == domain.EntityTypePC {
			out = append(out, id)
		}
	}
	return out
}

// StartTick runs the periodic game loop. Each tick fires update and blocks until
// ctx is cancelled or Stop is called. The composition root passes a regen
// callback (RegenTick); spawn runs on SpawnService's own timers and combat is
// event-driven. When update is nil (tests only) StartTick logs a single boot
// warning and returns instead of spinning the ticker for nothing.
func (w *WorldService) StartTick(ctx context.Context, update func(ctx context.Context, dt time.Duration)) {
	rateHz := int(time.Second / w.tickRate)
	if update == nil {
		w.log.Warn("world tick loop idle: no update callback registered; periodic entity-state advance is disabled (spawn/combat remain event-driven)", "rate_hz", rateHz)
		return
	}
	w.log.Info("world tick loop started", "rate_hz", rateHz)
	ticker := time.NewTicker(w.tickRate)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case t := <-ticker.C:
			update(ctx, t.Sub(t.Add(-w.tickRate)))
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

// RegenTick advances natural HP/SP regen by dt. It accumulates elapsed time and,
// when the standing interval elapses (6 s HP, 8 s SP), advances every living PC's
// vitals by the pre-renewal status_natural_heal amount, clamped to the max. Mobs/
// NPCs and dead PCs (HP <= 0) are skipped. When OnStatChange is set it is invoked
// per changed PC, off the world mutex. Entity mutation takes the world mutex
// (combat's applyDamage uses the same lock), so regen is race-free with damage.
func (w *WorldService) RegenTick(dt time.Duration) {
	w.hpSeconds += dt.Seconds()
	w.spSeconds += dt.Seconds()
	hpInt := hpRegenInterval.Seconds()
	spInt := spRegenInterval.Seconds()
	// Count elapsed intervals so a large dt (e.g. a stalled tick, a headless
	// test advancing time) catches up rather than firing a single regen.
	hpTicks := int(w.hpSeconds / hpInt)
	spTicks := int(w.spSeconds / spInt)
	if hpTicks > 0 {
		w.hpSeconds -= float64(hpTicks) * hpInt
	}
	if spTicks > 0 {
		w.spSeconds -= float64(spTicks) * spInt
	}
	if hpTicks == 0 && spTicks == 0 {
		return
	}

	type statNotify struct {
		charID uint32
		hp, sp int32
	}
	var pending []statNotify
	w.mu.Lock()
	for id, e := range w.entities {
		if e.Type != domain.EntityTypePC || e.HP <= 0 {
			continue // only living PCs regen
		}
		changed := false
		if hpTicks > 0 && e.HP < e.MaxHP {
			e.HP = clampRegen(e.HP, e.MaxHP, hpRegenAmount(e.MaxHP, e.Vit)*int32(hpTicks)) //nolint:gosec // G115: hpTicks is a count of 6s intervals; tiny at the 50Hz tick.
			changed = true
		}
		if spTicks > 0 && e.SP < e.MaxSP {
			e.SP = clampRegen(e.SP, e.MaxSP, spRegenAmount(e.MaxSP, e.Int)*int32(spTicks)) //nolint:gosec // G115: spTicks is a count of 8s intervals; tiny at the 50Hz tick.
			changed = true
		}
		if changed {
			pending = append(pending, statNotify{charID: uint32(id), hp: e.HP, sp: e.SP})
		}
	}
	w.mu.Unlock()

	if w.OnStatChange == nil {
		return
	}
	for _, n := range pending {
		w.OnStatChange(n.charID, n.hp, n.sp)
	}
}

// hpRegenAmount is the standing HP regen per 6 s interval: floor(MaxHP/200) +
// floor(Vit/2) + 1. The divisor and +1 follow the goAthena spec; rAthena's exact
// status_natural_heal constants are not verified against source (off-limits to
// read here) — adjust here if a domain check diverges.
func hpRegenAmount(maxHP int32, vit uint16) int32 {
	return maxHP/200 + int32(vit)/2 + 1
}

// spRegenAmount is the standing SP regen per 8 s interval: floor(MaxSP/100) +
// floor(Int/2) + 1. Same spec-source caveat as hpRegenAmount.
func spRegenAmount(maxSP int32, intStat uint16) int32 {
	return maxSP/100 + int32(intStat)/2 + 1
}

// clampRegen adds amt to cur and clamps at maxV. The int64 intermediate avoids
// overflow when cur+amt would exceed int32.
func clampRegen(cur, maxV, amt int32) int32 {
	v := int64(cur) + int64(amt)
	if v > int64(maxV) {
		return maxV
	}
	return int32(v) //nolint:gosec // G115: bounded to [cur, maxV] which fits int32.
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

// WarpPlayer persists a player's warp destination (map + tile). The caller emits
// ZC_NPCACK_MAPMOVE; the client reconnects and EnterMap reloads this position.
// It is the script `warp` builtin's world capability and satisfies the content
// domain's ScriptWorld port (structural — world stays free of content imports).
func (w *WorldService) WarpPlayer(charID uint32, mapName string, x, y int16) error {
	pos := domain.Position{X: x, Y: y}
	if err := w.repo.SetPosition(context.Background(), charID, mapName, pos); err != nil {
		return fmt.Errorf("warp player: %w", err)
	}
	return nil
}

// HealPlayer restores a player's HP and SP by hpPct/spPct percent of their
// maximums, clamped to [0, max], and returns the resulting values so the caller
// can emit ZC_PAR_CHANGE. It is the script `percentheal` builtin's world
// capability and satisfies the content domain's ScriptWorld port.
func (w *WorldService) HealPlayer(charID uint32, hpPct, spPct int) (hp, sp int32, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	e, ok := w.entities[domain.EntityID(charID)]
	if !ok {
		return 0, 0, domain.ErrEntityNotFound
	}
	e.HP = applyPctHeal(e.HP, e.MaxHP, hpPct)
	e.SP = applyPctHeal(e.SP, e.MaxSP, spPct)
	return e.HP, e.SP, nil
}

// AddVitals applies an absolute HP/SP delta to a player, clamped to [0, Max], and
// returns the resulting values. It is the usable-item (potion) use case's world
// capability: a healing item's flat restore (Red Potion +45 HP) flows through
// here, unlike HealPlayer's percentage restore. HP/SP is transient runtime state
// and is not persisted (mirroring HealPlayer). Like RegenTick it fires
// OnStatChange off the world mutex so the gateway can emit ZC_PAR_CHANGE; delta
// may be negative (the [0, Max] clamp keeps it symmetric for future drain paths).
func (w *WorldService) AddVitals(charID uint32, hp, sp int32) (hpAfter, spAfter int32, err error) {
	w.mu.Lock()
	e, ok := w.entities[domain.EntityID(charID)]
	if !ok {
		w.mu.Unlock()
		return 0, 0, domain.ErrEntityNotFound
	}
	e.HP = clampVitals(e.HP, hp, e.MaxHP)
	e.SP = clampVitals(e.SP, sp, e.MaxSP)
	hpAfter, spAfter = e.HP, e.SP
	w.mu.Unlock()
	if w.OnStatChange != nil {
		w.OnStatChange(charID, hpAfter, spAfter)
	}
	return hpAfter, spAfter, nil
}

// applyPctHeal adds rate percent of max to cur, clamped to [0, max]. A non-positive
// max yields 0 (no vitals to heal). rate is clamped to [0,100] first; the int64
// intermediate avoids overflow when rate*max would exceed int32.
func applyPctHeal(cur, maxV int32, rate int) int32 {
	if rate < 0 {
		rate = 0
	}
	if rate > 100 {
		rate = 100
	}
	if maxV <= 0 {
		return 0
	}
	v := int64(cur) + int64(rate)*int64(maxV)/100
	switch {
	case v > int64(maxV):
		return maxV
	case v < 0:
		return 0
	default:
		return int32(v) //nolint:gosec // G115: bounded to [0, max] which fits int32.
	}
}

// clampVitals adds delta to cur, clamped to [0, max]. A non-positive max yields 0
// (no vitals to change). delta may be negative (drain); the floor at 0 keeps the
// clamp symmetric. The int64 intermediate avoids overflow when cur+delta would
// exceed int32.
func clampVitals(cur, delta, maxV int32) int32 {
	if maxV <= 0 {
		return 0
	}
	v := int64(cur) + int64(delta)
	switch {
	case v > int64(maxV):
		return maxV
	case v < 0:
		return 0
	default:
		return int32(v) //nolint:gosec // G115: bounded to [0, maxV] which fits int32.
	}
}
