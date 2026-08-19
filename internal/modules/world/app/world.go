// Package app implements the world bounded context use cases: entity lifecycle
// (add/remove/move), AOI visibility, and the 50 Hz tick loop. The WorldService
// owns the in-memory entity registry and the per-map AOI grids; it is the
// authority for on-map state.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
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
	// leveling handles the conversion of EXP to levels.
	leveling *LevelingService
	// respawnCtx cancels timers armed by ArmRespawn so in-flight respawn timers
	// drain on Stop (a dead PC that has not yet respawned does not fire after
	// shutdown). Mirrors SpawnService's ctx/cancel respawn-timer pattern.
	respawnCtx    context.Context
	respawnCancel context.CancelFunc
	// respawnTimers maps a char to its in-flight respawn timer so the gateway can
	// cancel a pending auto-respawn when the player drives a type=0 respawn
	// (CZ_RESTART) before the timer fires. A char has at most one pending timer;
	// ArmRespawn cancels any prior. Guarded by mu.
	respawnTimers map[uint32]*respawnTimer
	// regenAcc holds per-entity elapsed time since the last HP/SP regen tick.
	// Per-entity (not global) so sitters (interval halved) and standers (full
	// interval) advance on the same 50 Hz tick independently. It is read and
	// written under w.mu alongside the entity registry; RemoveEntity deletes a
	// stale entry so a PC that leaves a map and re-enters starts fresh.
	regenAcc map[domain.EntityID]regenAccum
	// OnStatChange, when set, is invoked after RegenTick changes a player's HP/SP
	// so the gateway can emit ZC_PAR_CHANGE to that player's client. charID is the
	// entity's GID (char_id). nil = regen is silent (server state still advances;
	// useful for tests and headless operation). Invoked off the world mutex.
	OnStatChange func(charID uint32, hp, sp int32)
	// OnExpChange, when set, is invoked after GrantExp changes a player's EXP
	// so the gateway can emit ZC_EXP_CHANGE to that player's client. charID is the
	// entity's GID (char_id). nil = grant is silent. Invoked off the world mutex.
	OnExpChange func(charID uint32, baseExp, jobExp uint64)
	// OnRespawn, when set, is invoked after RespawnPlayer revives a PC at its save
	// point so the gateway can broadcast the save-point appear (ZC_SPAWN_UNIT to
	// neighbors) and relocate the player's own client (ZC_ACCEPT_ENTER). charID is
	// the entity's GID (char_id). nil = respawn is silent (server state still
	// advances; useful for tests and headless operation). Invoked off the world
	// mutex, after OnStatChange, so the HP/SP bar settles before the appear burst.
	OnRespawn func(charID uint32)
	// OnLevelUp, when set, is invoked after a PC levels up.
	// It provides the new level, recalculated MaxHP/MaxSP, and remaining status points.
	// nil = leveling is silent. Invoked off the world mutex.
	OnLevelUp func(charID uint32, newLevel int16, maxHP, maxSP int32, statusPoint uint32)
}

// Pre-renewal standing natural-regen intervals (rAthena status_natural_heal).
// Sitting halves them; moving/attacking pauses regen — moving/attacking is not
// modeled here (sitting halving is, via Entity.Sitting in RegenTick).
const (
	hpRegenInterval = 6 * time.Second
	spRegenInterval = 8 * time.Second
)

// respawnRevivePct is the fraction of MaxHP/MaxSP a dead PC is revived to on
// respawn (100 = full). rAthena revives at a percentage of max vitals; goAthena
// revives to full — a documented default, NOT derived from rAthena source.
const respawnRevivePct = 100

// regenAccum tracks elapsed time since the last HP/SP regen for one entity.
// Stored per EntityID in WorldService.regenAcc.
type regenAccum struct {
	hp, sp float64
}

// NewWorldService builds a WorldService. The tick rate is derived from
// tickRateHz (50 = 20 ms). The tick loop starts on StartTick and stops on Stop.
func NewWorldService(repo domain.WorldRepository, log *slog.Logger, tickRateHz int) *WorldService {
	if tickRateHz < 1 {
		tickRateHz = 50
	}
	respawnCtx, respawnCancel := context.WithCancel(context.Background())
	return &WorldService{
		entities:      make(map[domain.EntityID]*domain.Entity),
		byMap:         make(map[string]map[domain.EntityID]bool),
		grids:         make(map[string]*aoi.GridManager),
		regenAcc:      make(map[domain.EntityID]regenAccum),
		repo:          repo,
		log:           log,
		tickRate:      time.Second / time.Duration(tickRateHz),
		stopCh:        make(chan struct{}),
		respawnCtx:    respawnCtx,
		respawnCancel: respawnCancel,
		respawnTimers: make(map[uint32]*respawnTimer),
	}
}

// ensureGrid returns the AOI grid for a map, creating it on first access. Map
// dimensions default to a conservative 512×512 cell canvas; map-specific
// bounds are applied when the map data loader lands (later milestone).
func (w *WorldService) ensureGrid(mapName string) *aoi.GridManager {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ensureGridLocked(mapName)
}

// ensureGridLocked is the lock-free core of ensureGrid. The caller MUST hold w.mu.
// It creates the grid and an empty byMap set on first access for a map, but never
// clobbers an existing set — so a caller that has already added an entity to
// byMap[mapName] under the lock can safely ensure the map exists first.
func (w *WorldService) ensureGridLocked(mapName string) *aoi.GridManager {
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
	delete(w.regenAcc, id) // drop stale regen timer so a re-entering PC starts fresh
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

// PlayerByName resolves an online player-character's entity by exact name.
// Names are unique among live PCs (the char table enforces it); the scan walks
// the registry under RLock. Whisper routing uses this — a target that is not
// online resolves false, which the caller reports as target-offline.
func (w *WorldService) PlayerByName(name string) (domain.Entity, bool) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	for _, e := range w.entities {
		if e.Type == domain.EntityTypePC && e.Name == name {
			return *e, true
		}
	}
	return domain.Entity{}, false
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

// SetLeveling attaches the EXP→level converter after construction so the DI
// root (which loads job_exp.yml/job_basepoints.yml) can wire it without
// churning NewWorldService's many call sites. nil (the zero value) keeps
// leveling disabled — EXP accrues but thresholds are never consumed.
func (w *WorldService) SetLeveling(svc *LevelingService) {
	w.leveling = svc
}

// Stop drains the world's background work: the respawn timers (so a dead PC's
// pending auto-respawn never fires into a torn-down world) and the tick/checkpoint
// loops' stop channel. Idempotent — closing stopCh twice must not panic.
func (w *WorldService) Stop() {
	w.respawnCancel()
	select {
	case <-w.stopCh:
	default:
		close(w.stopCh)
	}
}

// RegenTick advances natural HP/SP regen by dt for every living PC. Each entity
// accumulates elapsed time in regenAcc; when its interval elapses (6 s HP / 8 s
// SP standing, halved to 3 s / 4 s while Entity.Sitting) it advances vitals by
// the pre-renewal status_natural_heal amount, clamped to the max. Mobs/NPCs and
// dead PCs (HP <= 0) are skipped. When OnStatChange is set it is invoked per
// changed PC, off the world mutex. regenAcc is mutated under w.mu (same lock as
// the entity registry), so regen stays race-free with damage and with SetSitting.
// Moving/attacking pausing regen (rAthena status_natural_heal) is not modeled.
func (w *WorldService) RegenTick(dt time.Duration) {
	dtSec := dt.Seconds()
	hpStanding := hpRegenInterval.Seconds()
	spStanding := spRegenInterval.Seconds()

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
		acc := w.regenAcc[id]
		acc.hp += dtSec
		acc.sp += dtSec
		// Sitting halves the standing interval (status_natural_heal).
		hpInt := hpStanding
		spInt := spStanding
		if e.Sitting {
			hpInt /= 2
			spInt /= 2
		}
		// Count elapsed intervals so a large dt (a stalled tick, a headless test
		// advancing time) catches up rather than firing a single regen.
		hpTicks := int(acc.hp / hpInt) //nolint:gosec // G115: count of 6/3 s intervals; tiny at the 50 Hz tick.
		spTicks := int(acc.sp / spInt) //nolint:gosec // G115: count of 8/4 s intervals; tiny at the 50 Hz tick.
		changed := false
		if hpTicks > 0 {
			acc.hp -= float64(hpTicks) * hpInt
			if e.HP < e.MaxHP {
				e.HP = clampRegen(e.HP, e.MaxHP, hpRegenAmount(e.MaxHP, e.Vit)*int32(hpTicks)) //nolint:gosec // G115: hpTicks is a count of 6/3 s intervals; tiny at the 50 Hz tick.
				changed = true
			}
		}
		if spTicks > 0 {
			acc.sp -= float64(spTicks) * spInt
			if e.SP < e.MaxSP {
				e.SP = clampRegen(e.SP, e.MaxSP, spRegenAmount(e.MaxSP, e.Int)*int32(spTicks)) //nolint:gosec // G115: spTicks is a count of 8/4 s intervals; tiny at the 50 Hz tick.
				changed = true
			}
		}
		w.regenAcc[id] = acc
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

// SetSitting sets a PC entity's seated state. The gateway calls it on
// CZ_ACTION_REQUEST sit/stand (DMGSitDown/DMGStandUp); Sitting halves the
// natural-regen interval in RegenTick. Returns ErrEntityNotFound if the entity is
// absent — a sit/stand for an entity not on this map is harmless and the caller
// ignores that sentinel. Thread-safe; the action request normally runs on the
// reactor goroutine, but the lock is taken for consistency with the other
// entity mutators (AddVitals, MoveEntity).
func (w *WorldService) SetSitting(charID uint32, sitting bool) error {
	w.mu.Lock()
	e, ok := w.entities[domain.EntityID(charID)]
	if !ok {
		w.mu.Unlock()
		return domain.ErrEntityNotFound
	}
	e.Sitting = sitting
	w.mu.Unlock()
	return nil
}

// SetFacing commits a PC entity's body direction and head facing (CZ_CHANGE_DIR).
// The broadcast to neighbors reads it off the cached entity, so a client that
// walks or re-enters AOI afterwards sees the updated facing. Absent entity is a
// late packet after leave — callers treat it like SetSitting's sentinel.
func (w *WorldService) SetFacing(charID uint32, dir uint8, head uint16) error {
	w.mu.Lock()
	e, ok := w.entities[domain.EntityID(charID)]
	if !ok {
		w.mu.Unlock()
		return domain.ErrEntityNotFound
	}
	e.Dir = dir
	e.Head = head
	w.mu.Unlock()
	return nil
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
	// Load learned skills from the skill table.
	skills, err := w.repo.LoadSkills(ctx, charID)
	if err != nil {
		return domain.Entity{}, fmt.Errorf("load skills: %w", err)
	}
	if len(skills) > 0 {
		e.LearnedSkills = make(map[int32]int16, len(skills))
		for _, s := range skills {
			e.LearnedSkills[s.SkillID] = s.Level
		}
	}
	if err := w.AddEntity(e); err != nil {
		return domain.Entity{}, err
	}
	_ = w.repo.SetOnline(ctx, charID, true, e.Pos)
	return e, nil
}

// LeaveMap removes a character from the world and marks it offline. Both the
// warp path (transit.Warp) and the disconnect path (gateway OnClose) funnel
// through here so position + offline flag + vitals are persisted from a single
// primitive. e is a value snapshot taken under w.mu by Get, so e.HP/e.SP are a
// consistent point-in-time read (combat/regen mutate HP concurrently on the
// tick/attack goroutines) — the DB writes run after the snapshot, off the lock.
func (w *WorldService) LeaveMap(ctx context.Context, charID uint32) error {
	e, err := w.Get(domain.EntityID(charID))
	if err != nil {
		// Idempotent: a char-select logout (CZ_RESTART type=1) persists + despawns,
		// then closing the conn re-enters LeaveMap via OnClose. A second call on an
		// already-removed entity is a clean no-op, not an error that floods logs.
		if errors.Is(err, domain.ErrEntityNotFound) {
			return nil
		}
		return err
	}
	if err := w.RemoveEntity(domain.EntityID(charID)); err != nil {
		return err
	}
	if err := w.repo.SetOnline(ctx, charID, false, e.Pos); err != nil {
		return fmt.Errorf("set offline: %w", err)
	}
	if err := w.repo.SaveState(ctx, charID, e.Level, e.JobLevel, e.MaxHP, e.MaxSP, e.HP, e.SP, e.BaseExp, e.JobExp, e.StatusPoint, e.SkillPoint); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	if len(e.LearnedSkills) > 0 {
		skills := make([]domain.LearnedSkill, 0, len(e.LearnedSkills))
		for sid, lvl := range e.LearnedSkills {
			skills = append(skills, domain.LearnedSkill{SkillID: sid, Level: lvl})
		}
		if err := w.repo.SaveSkills(ctx, charID, skills); err != nil {
			return fmt.Errorf("save skills: %w", err)
		}
	}
	return nil
}

// SaveAll persists every online PC's vitals + EXP and marks it offline, so a
// graceful shutdown/restart does not lose in-session HP/SP and EXP-from-kill
// changes (combat/regen/heal/respawn) nor leave stale-online rows. Best-effort:
// each char's failure is logged and skipped so one bad row never aborts the
// save-all. State+position are snapshotted under w.mu (the normal shutdown path
// has already stopped the tick loop, so regen is not mutating concurrently); the
// DB writes run after the lock is released so the snapshot does not block the
// tick loop on other paths.
func (w *WorldService) SaveAll(ctx context.Context) {
	w.mu.RLock()
	type vitalSnap struct {
		charID       uint32
		pos          domain.Position
		hp, sp       int32
		maxHP, maxSP int32
		level        int16
		jobLevel     int16
		baseExp      uint64
		jobExp       uint64
		statusPoint  uint32
		skillPoint   uint32
	}
	snaps := make([]vitalSnap, 0, len(w.entities))
	for id, e := range w.entities {
		if e.Type != domain.EntityTypePC {
			continue
		}
		snaps = append(snaps, vitalSnap{
			charID:      uint32(id), //nolint:gosec // G115: EntityID wraps a char_id (uint32).
			pos:         e.Pos,
			hp:          e.HP,
			sp:          e.SP,
			level:       e.Level,
			jobLevel:    e.JobLevel,
			baseExp:     e.BaseExp,
			jobExp:      e.JobExp,
			statusPoint: e.StatusPoint,
			skillPoint:  e.SkillPoint,
		})
	}
	w.mu.RUnlock()
	for _, s := range snaps {
		if err := w.repo.SaveState(ctx, s.charID, s.level, s.jobLevel, s.maxHP, s.maxSP, s.hp, s.sp, s.baseExp, s.jobExp, s.statusPoint, s.skillPoint); err != nil {
			w.log.Warn("save-all state", "char_id", s.charID, "err", err)
			continue
		}
		if err := w.repo.SetOnline(ctx, s.charID, false, s.pos); err != nil {
			w.log.Warn("save-all offline", "char_id", s.charID, "err", err)
		}
	}
}

// Checkpoint persists every online PC's vitals + EXP + position WITHOUT marking
// it offline, so a hard crash between disconnects (SIGKILL/panic/power loss)
// loses at most one checkpoint interval of in-session HP/SP/EXP change instead
// of the whole session. It is the periodic durability snapshot driven by
// StartCheckpoint; SaveAll is the shutdown/logout flush that additionally flips
// the online flag to false — reusing SaveAll for a periodic checkpoint would
// mass-disconnect every connected char, so Checkpoint is a separate method.
// Best-effort and off-lock, mirroring SaveAll: the snapshot is taken under w.mu
// (RLock) and released before the DB writes, so a slow write does not block the
// 50 Hz tick loop. Each char's failure is logged and skipped so one bad row
// never aborts the checkpoint.
func (w *WorldService) Checkpoint(ctx context.Context) {
	w.mu.RLock()
	type vitalSnap struct {
		charID       uint32
		pos          domain.Position
		hp, sp       int32
		maxHP, maxSP int32
		level        int16
		jobLevel     int16
		baseExp      uint64
		jobExp       uint64
		statusPoint  uint32
		skillPoint   uint32
	}
	snaps := make([]vitalSnap, 0, len(w.entities))
	for id, e := range w.entities {
		if e.Type != domain.EntityTypePC {
			continue
		}
		snaps = append(snaps, vitalSnap{
			charID:      uint32(id), //nolint:gosec // G115: EntityID wraps a char_id (uint32).
			pos:         e.Pos,
			hp:          e.HP,
			sp:          e.SP,
			level:       e.Level,
			jobLevel:    e.JobLevel,
			baseExp:     e.BaseExp,
			jobExp:      e.JobExp,
			statusPoint: e.StatusPoint,
			skillPoint:  e.SkillPoint,
		})
	}
	w.mu.RUnlock()
	for _, s := range snaps {
		if err := w.repo.SaveState(ctx, s.charID, s.level, s.jobLevel, s.maxHP, s.maxSP, s.hp, s.sp, s.baseExp, s.jobExp, s.statusPoint, s.skillPoint); err != nil {
			w.log.Warn("checkpoint state", "char_id", s.charID, "err", err)
			continue
		}
		// SetOnline with online=true re-persists position without flipping the
		// online flag, so a connected PC stays online after the snapshot.
		if err := w.repo.SetOnline(ctx, s.charID, true, s.pos); err != nil {
			w.log.Warn("checkpoint position", "char_id", s.charID, "err", err)
		}
	}
}

// StartCheckpoint spawns a slow ticker that periodically calls Checkpoint so a
// hard crash (SIGKILL/panic/power loss) between disconnects loses at most one
// interval of in-session HP/SP/EXP change. It is separate from the 50 Hz tick
// loop (StartTick): checkpointing is an infrequent durability snapshot (default
// 5 m), not per-frame work. The goroutine drains on ctx cancellation (graceful
// shutdown) or Stop, mirroring StartTick's lifecycle. Each checkpoint runs under
// its own timeout derived from ctx so a slow DB on one tick does not block the
// next; on shutdown the already-cancelled ctx makes the racing checkpoint
// no-op fast, leaving the final flush to SaveAll.
func (w *WorldService) StartCheckpoint(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		w.log.Info("world checkpoint loop started", "interval", interval)
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.stopCh:
				return
			case <-ticker.C:
				cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				w.Checkpoint(cctx)
				cancel()
			}
		}
	}()
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

// RelocatePlayer moves a live PC to a new map+cell in one step: entity map+pos,
// AOI grid (off the old map's grid, onto the destination map's grid), and the
// persisted position. It is the portal path's world capability — unlike
// WarpPlayer (persist-only, for the script builtin whose client reconnects),
// the live entity is reseated immediately so destination-map neighbors and the
// re-enter back-fill resolve the player at the arrival cell.
func (w *WorldService) RelocatePlayer(charID uint32, mapName string, x, y int16) error {
	id := domain.EntityID(charID) //nolint:gosec // G115: charID is a char_id (uint32).
	pos := domain.Position{X: x, Y: y}
	w.mu.Lock()
	e, ok := w.entities[id]
	if !ok {
		w.mu.Unlock()
		return domain.ErrEntityNotFound
	}
	oldMap := e.Map
	e.Map = mapName
	e.Pos = pos
	destGrid := w.grids[mapName]
	oldGrid := w.grids[oldMap]
	w.mu.Unlock()

	if oldGrid != nil {
		_ = oldGrid.RemoveEntity(aoi.EntityID(id))
	}
	if destGrid != nil {
		_ = destGrid.AddEntity(&aoi.Entity{
			ID: aoi.EntityID(id), X: int(x), Y: int(y),
		})
	}
	if err := w.repo.SetPosition(context.Background(), charID, mapName, pos); err != nil {
		return fmt.Errorf("relocate player: %w", err)
	}
	return nil
}

// RespawnPlayer moves a dead PC to its save point and revives its vitals, so a
// mob-killed player reappears at its save point instead of sitting dead forever.
// It is the bounded goAthena death model: no ghost/tomb/respawn-button — a killed
// PC vanishes (broadcast by the caller, gateway STEP), is out of the visible set
// for the respawn delay, then reappears here with HP/SP restored. The player's
// connection stays alive across the delay.
//
// The save point is the runtime-cached Entity.SaveMap/SavePos; a char without a
// seeded save point respawns on its current map in place (bounded default — every
// production char loads one from the char table). Vitals revive to
// respawnRevivePct of max. All mutations run under w.mu and the AOI reseat happens
// off-lock (the grid synchronizes itself), mirroring MoveEntity; OnStatChange is
// collected under the lock and dispatched off it so the gateway's HP/SP-bar emit
// never runs on the world mutex. Thread-safe against the tick loop: MonsterTick
// drops dead targets on death, so moving a revived PC mid-tick is race-free.
func (w *WorldService) RespawnPlayer(charID uint32) error {
	type statNotify struct {
		charID uint32
		hp, sp int32
	}
	var (
		notify   *statNotify
		oldMap   string
		saveMap  string
		savePos  domain.Position
		oldGrid  *aoi.GridManager
		saveGrid *aoi.GridManager
	)
	w.mu.Lock()
	e, ok := w.entities[domain.EntityID(charID)]
	if !ok {
		w.mu.Unlock()
		return domain.ErrEntityNotFound
	}
	// Resolve save point, falling back to the current cell when none is seeded.
	saveMap = e.SaveMap
	savePos = e.SavePos
	if saveMap == "" {
		saveMap = e.Map
		savePos = e.Pos
	}
	// Re-seat on the save map: ensure its grid+set exist (ensureGridLocked never
	// clobbers an existing set), drop the entity from the death map's set, then
	// seat it on the save map. Calling ensureGridLocked before the byMap add mirrors
	// AddEntity's ensureGrid-then-byMap order so the entity is never orphaned.
	oldMap = e.Map
	saveGrid = w.ensureGridLocked(saveMap)
	if saveMap != oldMap {
		if set, ok := w.byMap[oldMap]; ok {
			delete(set, e.ID)
		}
		e.Map = saveMap
	}
	w.byMap[saveMap][e.ID] = true
	e.Pos = savePos
	// Revive vitals to respawnRevivePct of max (applyPctHeal from 0 = fraction of max).
	e.HP = applyPctHeal(0, e.MaxHP, respawnRevivePct)
	e.SP = applyPctHeal(0, e.MaxSP, respawnRevivePct)
	if w.OnStatChange != nil {
		notify = &statNotify{charID: charID, hp: e.HP, sp: e.SP}
	}
	oldGrid = w.grids[oldMap]
	w.mu.Unlock()

	// AOI reseat off-lock: remove from the death cell's grid, then re-add at the
	// save cell on the already-ensured save grid. The grid synchronizes itself, so
	// this is race-free with the tick loop (matching MoveEntity's reseat pattern).
	if oldGrid != nil {
		_ = oldGrid.RemoveEntity(aoi.EntityID(charID))
	}
	_ = saveGrid.RemoveEntity(aoi.EntityID(charID)) // clear a stale cell on the save grid
	if err := saveGrid.AddEntity(&aoi.Entity{
		ID: aoi.EntityID(charID), X: int(savePos.X), Y: int(savePos.Y),
	}); err != nil {
		return fmt.Errorf("respawn aoi add: %w", err)
	}

	if notify != nil {
		w.OnStatChange(notify.charID, notify.hp, notify.sp)
	}
	if w.OnRespawn != nil {
		w.OnRespawn(charID)
	}
	return nil
}

// respawnTimer is the per-char identity token for an in-flight respawn timer: the
// heap-allocated pointer lets ArmRespawn's goroutine tell its own slot from a
// later re-arm's (pointers are comparable; func values are not).
type respawnTimer struct {
	cancel context.CancelFunc
}

// ArmRespawn arms a per-char cancellable timer that fires RespawnPlayer(charID)
// after delay. Each death arms one timer (a re-arm cancels any prior pending one
// for that char); Stop cancels in-flight timers via the service context so a
// pending respawn never fires after shutdown. The gateway calls this when a mob
// kills a PC (after broadcasting the death-cell vanish); the timer callback
// performs the respawn + save-point appear (the gateway STEP wires the appear
// broadcast). CancelRespawn lets the player-driven respawn button (CZ_RESTART
// type=0) cancel a still-pending timer so the button and the timer never both
// fire RespawnPlayer.
func (w *WorldService) ArmRespawn(charID uint32, delay time.Duration) {
	ctx, cancel := context.WithCancel(w.respawnCtx)
	rt := &respawnTimer{cancel: cancel}
	w.mu.Lock()
	if prev, ok := w.respawnTimers[charID]; ok {
		prev.cancel() // cancel a prior pending timer (defensive; death arms once)
	}
	w.respawnTimers[charID] = rt
	w.mu.Unlock()

	go func() {
		t := time.NewTimer(delay)
		defer t.Stop()
		defer cancel()
		fired := false
		select {
		case <-ctx.Done():
		case <-t.C:
			fired = true
		}
		w.mu.Lock()
		// Clear our slot only if no later re-arm replaced it (pointer identity).
		if cur, ok := w.respawnTimers[charID]; ok && cur == rt {
			delete(w.respawnTimers, charID)
		}
		w.mu.Unlock()
		if !fired {
			return
		}
		if err := w.RespawnPlayer(charID); err != nil {
			w.log.Warn("respawn failed", "char_id", charID, "err", err)
		}
	}()
}

// CancelRespawn cancels a pending auto-respawn timer for charID (a no-op if none
// is armed). The gateway calls this on a player-driven respawn (CZ_RESTART
// type=0) so the death timer and the respawn button do not both fire
// RespawnPlayer; RespawnPlayer is idempotent regardless, but cancelling avoids a
// redundant revive + appear burst.
func (w *WorldService) CancelRespawn(charID uint32) {
	w.mu.Lock()
	rt, ok := w.respawnTimers[charID]
	if ok {
		delete(w.respawnTimers, charID)
	}
	w.mu.Unlock()
	if ok {
		rt.cancel()
	}
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

// GrantExp accrues a mob-kill EXP reward to a player: it adds baseExp/jobExp to
// the entity's runtime EXP totals (clamped at math.MaxUint64 so the uint64 add
// can never overflow or panic) and returns the new totals. It is the EXP-on-kill
// use case's world capability: a dead mob's mob_db BaseExp/JobExp flows through
// here. EXP is runtime state mirrored to the char table by LeaveMap/SaveAll (via
// SaveState), so disconnect/shutdown/warp persist it from the same path as
// HP/SP. Leveling is deliberately deferred — GrantExp only accrues EXP; the
// level-up track (job_stats EXP curve, HP/SP recalc, status-point accrual) is a
// separate subsystem not modeled here. Thread-safe: mutated under w.mu; the
// OnExpChange notify fires off the lock (mirrors AddVitals/OnStatChange).
func (w *WorldService) GrantExp(ctx context.Context, charID uint32, baseExp, jobExp uint64) (newBase, newJob uint64, err error) {
	w.mu.Lock()
	e, ok := w.entities[domain.EntityID(charID)]
	if !ok {
		w.mu.Unlock()
		return 0, 0, domain.ErrEntityNotFound
	}
	e.BaseExp = clampExpAdd(e.BaseExp, baseExp)
	e.JobExp = clampExpAdd(e.JobExp, jobExp)
	newBase, newJob = e.BaseExp, e.JobExp
	w.mu.Unlock()
	// Level-up check FIRST (off-lock — it manages its own locking), so the
	// thresholds consumed by leveling are reflected in the totals the client is
	// told about. Firing OnExpChange before the consumption would report the
	// pre-leveling EXP and leave the client's bar drifting ahead of the server.
	if w.leveling != nil {
		_, _ = w.leveling.CheckLevelUp(ctx, charID)
		_, _ = w.leveling.CheckJobLevelUp(ctx, charID)
	}
	w.mu.RLock()
	if e, ok := w.entities[domain.EntityID(charID)]; ok {
		newBase, newJob = e.BaseExp, e.JobExp
	}
	w.mu.RUnlock()
	if w.OnExpChange != nil {
		w.OnExpChange(charID, newBase, newJob)
	}
	return newBase, newJob, nil
}

// AllocateStat raises one base stat by one step, spending status points at the
// kernel's StatusPointCost rate (the packet package's pc_need_status_point
// table — 1 + (cur+9)/10 pre-renewal; keeping it in one place avoids drift).
// Stats cap at 99 (documented pre-re convention). Returns the new stat value,
// remaining points, and the cost consumed. Typed errors: ErrEntityNotFound,
// ErrUnknownStat, ErrStatCapped, ErrNoStatusPoints.
func (w *WorldService) AllocateStat(charID uint32, stat string) (newVal uint32, remaining uint32, cost uint32, err error) {
	const maxStat = 99

	w.mu.Lock()
	defer w.mu.Unlock()
	e, ok := w.entities[domain.EntityID(charID)]
	if !ok {
		return 0, 0, 0, domain.ErrEntityNotFound
	}

	var cur *uint16
	switch stat {
	case "Str":
		cur = &e.Str
	case "Agi":
		cur = &e.Agi
	case "Vit":
		cur = &e.Vit
	case "Int":
		cur = &e.Int
	case "Dex":
		cur = &e.Dex
	case "Luk":
		cur = &e.Luk
	default:
		return 0, 0, 0, fmt.Errorf("%w: %s", domain.ErrUnknownStat, stat)
	}

	if *cur >= maxStat {
		return 0, 0, 0, fmt.Errorf("%w: %s at %d", domain.ErrStatCapped, stat, maxStat)
	}
	cost = uint32(ropacket.StatusPointCost(uint8(*cur))) //nolint:gosec // G115: cur is capped at 98 here, fits uint8.
	if cost > e.StatusPoint {
		return 0, 0, 0, fmt.Errorf("%w: have %d, need %d", domain.ErrNoStatusPoints, e.StatusPoint, cost)
	}

	e.StatusPoint -= cost
	*cur++
	newVal = uint32(*cur)
	remaining = e.StatusPoint
	return newVal, remaining, cost, nil
}

// LearnSkill records skillID as learned at level for the given char. It raises the
// learned level in-place if level exceeds the current record. A zero or negative
// level is a no-op. This is the test seam and the future CZ_SKILLUP handler's
// underlying primitive.
func (w *WorldService) LearnSkill(charID uint32, skillID int32, level int16) error {
	if level <= 0 {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	e, ok := w.entities[domain.EntityID(charID)]
	if !ok {
		return domain.ErrEntityNotFound
	}
	if e.LearnedSkills == nil {
		e.LearnedSkills = make(map[int32]int16)
	}
	if cur, exists := e.LearnedSkills[skillID]; !exists || level > cur {
		e.LearnedSkills[skillID] = level
	}
	return nil
}

// clampExpAdd returns cur+delta saturated at math.MaxUint64. EXP is an
// accumulating uint64; without the cap a grant near the ceiling would overflow
// (wrapping toward 0). math.MaxUint64 is a documented sane cap — no realistic
// EXP total approaches it, so saturation is a defensive bound, not gameplay.
func clampExpAdd(cur, delta uint64) uint64 {
	if delta > math.MaxUint64-cur {
		return math.MaxUint64
	}
	return cur + delta
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
