package app

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
	"github.com/bouroo/goAthena/pkg/ro/mobdb"
	"github.com/bouroo/goAthena/pkg/ro/script"
)

// SpawnService owns floor items (drops on the map) and mob spawning. Mob spawn
// points + mob death→drop live here; the pickup path resolves a floor item and
// hands it to the inventory port (injected by the gateway handler).
type SpawnService struct {
	world *WorldService
	mobs  *mobdb.Registry
	items *itemdb.Registry

	mu        sync.Mutex
	floor     map[uint32]*domain.FloorItem // by GroundID
	groundSeq atomic.Uint32

	// portals indexes the corpus warp portals by source tile. Crossing the
	// trigger tile teleports the player; portals are invisible (no entity).
	portals map[script.WarpKey]script.WarpDef

	// spawns holds respawn templates keyed by mob EntityID, registered when a mob
	// spawns with a non-zero respawnDelay. OnMobDeath arms a timer from the
	// template so the mob re-spawns at its origin.
	spawnMu sync.Mutex
	spawns  map[domain.EntityID]spawnPoint

	// OnMobSpawn, when set, is invoked after a mob (re)enters the world — the
	// initial SpawnMob placement and every respawn-timer AddEntity — so the
	// gateway can broadcast the appear frame to the mob's AOI neighbors.
	OnMobSpawn func(mobID domain.EntityID)

	ctx    context.Context
	cancel context.CancelFunc
}

// spawnPoint captures everything needed to re-spawn a mob after death.
type spawnPoint struct {
	class   int32
	mapName string
	pos     domain.Position
	name    string
	hp      int32
	maxHp   int32
	delay   time.Duration
}

// NewSpawnService builds a SpawnService. mobs/items may be nil (no drops in
// tests); a nil item_db means mob drops resolve no items. The service owns a
// cancellable context for respawn timers; Stop drains them.
func NewSpawnService(world *WorldService, mobs *mobdb.Registry, items *itemdb.Registry) *SpawnService {
	ctx, cancel := context.WithCancel(context.Background())
	return &SpawnService{
		world:   world,
		mobs:    mobs,
		items:   items,
		floor:   make(map[uint32]*domain.FloorItem),
		spawns:  make(map[domain.EntityID]spawnPoint),
		portals: make(map[script.WarpKey]script.WarpDef),
		ctx:     ctx,
		cancel:  cancel,
	}
}

// Stop cancels pending respawn timers. Call on world shutdown so respawn
// goroutines do not outlive the world.
func (s *SpawnService) Stop() {
	s.cancel()
}

// SpawnMob registers a mob entity in the world at the given position. mobClass
// is the mob_db id the combat service resolves the mob's DEF/stats by. A
// non-zero respawnDelay registers a respawn template so the mob re-spawns at
// this position after death.
func (s *SpawnService) SpawnMob(mobID domain.EntityID, mobClass int32, mapName string, pos domain.Position, name string, hp, maxHp int32, respawnDelay time.Duration) error {
	if respawnDelay > 0 {
		s.spawnMu.Lock()
		s.spawns[mobID] = spawnPoint{
			class: mobClass, mapName: mapName, pos: pos, name: name,
			hp: hp, maxHp: maxHp, delay: respawnDelay,
		}
		s.spawnMu.Unlock()
	}
	if err := s.world.AddEntity(domain.Entity{
		ID:    mobID,
		Type:  domain.EntityTypeMob,
		Class: mobClass,
		Map:   mapName,
		Pos:   pos,
		Name:  name,
		HP:    hp,
		MaxHP: maxHp,
	}); err != nil {
		return err
	}
	if s.OnMobSpawn != nil {
		s.OnMobSpawn(mobID)
	}
	return nil
}

// OnMobDeath generates floor-item drops from the mob's drop table, despawns the
// mob (removes it from the world registry + AOI grid), and arms a respawn timer
// if the mob has a registered spawn point. It returns the floor items it placed
// so the orchestrator (gateway) can broadcast ZC_ITEM_ENTRY / ZC_NOTIFY_VANISH.
// A nil mob_db or item_db yields no drops; an AegisName with no item_db match is
// skipped (the drop does not land).
func (s *SpawnService) OnMobDeath(mobClass int32, mapName string, pos domain.Position, mobID domain.EntityID) []domain.FloorItem {
	drops := s.rollDrops(mobClass, mapName, pos, mobID)
	_ = s.world.RemoveEntity(mobID) // despawn: registry + AOI grid
	s.scheduleRespawn(mobID)
	return drops
}

// MobExp returns the mob_db BaseExp/JobExp awarded for killing a mob of the
// given class — the EXP source WorldService.GrantExp accrues to the killer on
// death. A nil mob_db or an unknown class yields (0, 0): best-effort, the killer
// simply earns no EXP (no error). mob_db stores EXP as int32 reward counts,
// clamped to ≥0 before widening to uint64. Party EXP split is deferred — the
// full reward goes to the single killer.
func (s *SpawnService) MobExp(mobClass int32) (base, job uint64) {
	if s.mobs == nil {
		return 0, 0
	}
	mob := s.mobs.Get(mobClass)
	if mob == nil {
		return 0, 0
	}
	if mob.BaseExp > 0 {
		base = uint64(mob.BaseExp) //nolint:gosec // G115: non-negative int32 reward count.
	}
	if mob.JobExp > 0 {
		job = uint64(mob.JobExp) //nolint:gosec // G115: non-negative int32 reward count.
	}
	return base, job
}

// rollDrops resolves the mob's drop table to NameIDs via item_db and applies the
// per-drop rate (rAthena 1/10000) as an RNG gate, placing each successful drop
// as a floor item at the mob's death position.
func (s *SpawnService) rollDrops(mobClass int32, mapName string, pos domain.Position, mobID domain.EntityID) []domain.FloorItem {
	if s.mobs == nil || s.items == nil {
		return nil
	}
	mob := s.mobs.Get(mobClass)
	if mob == nil {
		return nil
	}
	var drops []domain.FloorItem
	for _, d := range mob.Drops {
		if !rollDrop(d.Rate) {
			continue
		}
		item := s.items.ByAegisName(d.Item)
		if item == nil {
			continue
		}
		drops = append(drops, s.DropItem(uint32(item.Id), 1, mapName, pos, mobID)) //nolint:gosec // G115: item.Id is a positive DB id.
	}
	return drops
}

// rollDrop gates a single drop by its rate. rAthena rates are 1/10000, so 10000
// is a guaranteed drop and 7000 is 70%.
func rollDrop(rate int) bool {
	switch {
	case rate <= 0:
		return false
	case rate >= 10000:
		return true
	default:
		return rand.IntN(10000) < rate //nolint:gosec // G404: drop-rate is a game-mechanic RNG (1/10000), not a secret.
	}
}

// scheduleRespawn arms a respawn timer for the mob if it has a registered spawn
// point. Each death arms one timer; the timer re-spawns the mob at its origin
// position. Cancellable via the service context (Stop).
func (s *SpawnService) scheduleRespawn(mobID domain.EntityID) {
	s.spawnMu.Lock()
	sp, ok := s.spawns[mobID]
	s.spawnMu.Unlock()
	if !ok || sp.delay <= 0 {
		return
	}
	go func() {
		t := time.NewTimer(sp.delay)
		defer t.Stop()
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			_ = s.world.AddEntity(domain.Entity{
				ID: mobID, Type: domain.EntityTypeMob, Class: sp.class, Map: sp.mapName,
				Pos: sp.pos, Name: sp.name, HP: sp.hp, MaxHP: sp.maxHp,
			})
			if s.OnMobSpawn != nil {
				s.OnMobSpawn(mobID)
			}
		}
	}()
}

// DropItem places one floor item and returns it. The GroundID is unique.
func (s *SpawnService) DropItem(nameID, amount uint32, mapName string, pos domain.Position, dropper domain.EntityID) domain.FloorItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.groundSeq.Add(1)
	gid := s.groundSeq.Load()
	fi := &domain.FloorItem{
		GroundID: gid, NameID: nameID, Amount: amount,
		PosX: pos.X, PosY: pos.Y, Map: mapName, Dropper: dropper,
	}
	s.floor[gid] = fi
	return *fi
}

// PickupFloorItem removes a floor item by GroundID and returns it, or
// domain.ErrEntityNotFound if absent/already taken.
func (s *SpawnService) PickupFloorItem(groundID uint32) (domain.FloorItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fi, ok := s.floor[groundID]
	if !ok {
		return domain.FloorItem{}, fmt.Errorf("pickup %d: %w", groundID, domain.ErrEntityNotFound)
	}
	delete(s.floor, groundID)
	return *fi, nil
}

// RegisterPortals loads warp portals from the compiled corpus (idempotent:
// re-registering the same tile replaces its destination).
func (s *SpawnService) RegisterPortals(defs []script.WarpDef) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, def := range defs {
		s.portals[def.Key()] = def
	}
}

// PortalAt returns the warp portal anchored on (mapName, x, y), if any. The
// gateway consults it after a successful move: landing on a trigger tile
// teleports the player to the portal's destination.
func (s *SpawnService) PortalAt(mapName string, x, y int) (script.WarpDef, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	def, ok := s.portals[script.WarpKey{MapName: mapName, TriggerX: x, TriggerY: y}]
	return def, ok
}

// FloorItems returns a snapshot of floor items on a map (for the AOI broadcast
// of ZC_ITEM_ENTRY on map-enter / spawn).
func (s *SpawnService) FloorItems(mapName string) []domain.FloorItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.FloorItem, 0)
	for _, fi := range s.floor {
		if fi.Map == mapName {
			out = append(out, *fi)
		}
	}
	return out
}
