// Package app: this file adds the M5 mob use cases — populating the world with
// monsters at boot and driving their idle wander on the zone tick. It is the
// second Runnable the composition root starts (alongside "world-move"); the two
// never contend on the pathfinder because the mob tick does not pathfind (see
// Run).
package app

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/mobdb"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// stepSource is the indirection over the wander step's RNG so unit tests can
// drive a deterministic neighbor choice (and assert exact move bytes) instead
// of racing math/rand. The production default wraps the global rand source.
type stepSource interface {
	// Intn returns a non-negative pseudo-random number in [0,n); it mirrors
	// rand.Rand.Intn and is used only to pick one of the 8 neighbor offsets.
	Intn(n int) int
}

// randStep is the production stepSource over the auto-seeded global source.
type randStep struct{}

// Intn implements stepSource.
func (randStep) Intn(n int) int { return rand.Intn(n) } //nolint:gosec // G404: mob wander direction is non-secret; a predictable neighbor pick has no security impact.

// stepOffsets are the 8 moore-neighborhood cell offsets a mob may step to,
// excluding (0,0). Order matches rAthena's direction sweep loosely; the exact
// order does not matter to the wire (the client interpolates src→dest), only
// that the chosen dest is walkable.
var stepOffsets = [8][2]int{
	{0, -1}, {0, 1}, {-1, 0}, {1, 0}, // cardinals first
	{-1, -1}, {1, -1}, {-1, 1}, {1, 1}, // then diagonals
}

// spawnGroup is one entry of a map's spawns: how many of which mob to place at
// (or scattered around) a cell, and how long to wait before respawning a killed
// mob. The file format is goAthena's own lowercase `spawns:` list (NOT rAthena's
// MOB_SPAWN Header/Body), so it is parsed here rather than via mobdb.Load.
type spawnGroup struct {
	MobID     int32 `yaml:"mob_id"`
	Count     int   `yaml:"count"`
	X         int   `yaml:"x"`
	Y         int   `yaml:"y"`
	XRange    int   `yaml:"x_range"`
	YRange    int   `yaml:"y_range"`
	RespawnMS int   `yaml:"respawn_ms"`
}

// spawnFile is the on-disk shape of data/mob_spawns/<map>.yml.
type spawnFile struct {
	Spawns []spawnGroup `yaml:"spawns"`
}

// MobService owns mob population and the idle-wander tick. It holds the mob and
// player registries (to iterate mobs and resolve observing players), the map
// store (to load a map's walkability grid and AOI), the mob_db (to resolve a
// spawn's stats), the spawn-group directory, the move clock, and the tick rate.
//
// Concurrency contract — the pathfinder stays single-goroutine:
//   - The move worker ("world-move") owns every map's pathfinder; it is the sole
//     caller of FindPath (world/domain/map.go documents this).
//   - The mob tick NEVER calls FindPath. Idle wander is a single random step to
//     an adjacent walkable cell, checked via romap.MapData.IsWalkable (an
//     immutable, goroutine-safe read of the walkability slice) and committed via
//     AOI.MoveEntity (locked). That matches rAthena's mob_randomwalk cell-by-cell
//     movement without touching the shared A* scratch buffers.
type MobService struct {
	mobs       *domain.MobRegistry
	players    *domain.PlayerRegistry
	maps       domain.MapStore
	db         *mobdb.Registry
	spawnsFile string
	clock      Clock
	tickRate   time.Duration
	rand       stepSource
}

// NewMobService binds the mob collaborators. spawnsFile is the per-map spawn
// YAML (cfg.Zone.MobSpawnsPath; the repo's *_path fields are single files, not
// directories — map_dir/script_dir are the directory-typed roots); its basename
// minus ".yml" is the map it populates. spawnsFile may be empty (no spawns); db
// may be nil (no mob_db) — in either case SpawnAll is a no-op and Run ticks over
// an empty world, so a misconfigured zone still boots. tickRate is the zone tick
// cadence (cfg.Zone.TickRate); the wander step is further throttled per mob by
// its WalkSpeed. rand is the step RNG; pass nil for the production global source.
func NewMobService(mobs *domain.MobRegistry, players *domain.PlayerRegistry, maps domain.MapStore, db *mobdb.Registry, spawnsFile string, clock Clock, tickRate time.Duration, rng stepSource) *MobService {
	if rng == nil {
		rng = randStep{}
	}
	return &MobService{
		mobs:       mobs,
		players:    players,
		maps:       maps,
		db:         db,
		spawnsFile: spawnsFile,
		clock:      clock,
		tickRate:   tickRate,
		rand:       rng,
	}
}

// SpawnAll populates the configured map with mobs: it parses the spawn file,
// resolves each group's mob stats from mob_db, allocates EntityIDs, and registers
// the mobs with both the MobRegistry and the map's AOI grid. The target map is
// the spawn file's basename minus ".yml" (prontera.yml → "prontera"), mirroring
// how map_dir keys its per-map data files. A mob_id absent from mob_db is
// skipped — the zone still boots and serves the spawns that do resolve. Call
// this once at boot (the composition root), before any player can connect, so
// mobs exist when the first enter-world spawn exchange runs.
func (s *MobService) SpawnAll(ctx context.Context) error {
	if s.spawnsFile == "" || s.db == nil {
		// No spawn corpus configured: spawn nothing. The tick still runs (a
		// no-op over zero mobs) so the runnable contract holds.
		return nil
	}
	mapName := strings.TrimSuffix(filepath.Base(s.spawnsFile), ".yml")
	if err := s.spawnMap(ctx, mapName, s.spawnsFile); err != nil {
		return fmt.Errorf("mob spawn: map %q: %w", mapName, err)
	}
	return nil
}

// spawnMap loads one map's spawn file and seeds its mobs. It reads the file,
// loads (and caches) the map, then for each group instantiates `count` mobs at
// (x,y) (scattered within x_range/y_range when non-zero), placing each into the
// MobRegistry and the map's AOI grid as an EntityMob.
func (s *MobService) spawnMap(ctx context.Context, mapName, path string) error {
	raw, err := os.ReadFile(path) // #nosec G304 -- spawnsDir is operator-configured, name is dir-listed.
	if err != nil {
		return fmt.Errorf("read spawn file: %w", err)
	}
	var sf spawnFile
	if err := yaml.Unmarshal(raw, &sf); err != nil {
		return fmt.Errorf("parse spawn file: %w", err)
	}
	if len(sf.Spawns) == 0 {
		return nil
	}
	mp, err := s.maps.Load(ctx, mapName)
	if err != nil {
		return fmt.Errorf("load map: %w", err)
	}
	for _, g := range sf.Spawns {
		entry := s.db.Get(g.MobID)
		if entry == nil {
			// Unknown mob id in the spawn file: skip rather than fail the whole
			// map. The operator should fix the file, but one bad row must not
			// blank the map's other spawns.
			continue
		}
		for i := 0; i < g.Count; i++ {
			s.spawnOne(mp, mapName, entry, g)
		}
	}
	return nil
}

// spawnOne creates and registers a single mob from a spawn group + mob_db entry.
// The cell is (x,y) when the ranges are zero, else a random point within the
// rectangle — matching rAthena's spawn-area scatter. EntityID allocation,
// MobRegistry indexing, and AOI insertion are the three publication steps; a
// failure between them is non-fatal (the mob simply is not placed).
func (s *MobService) spawnOne(mp *domain.Map, mapName string, entry *mobdb.MobEntry, g spawnGroup) {
	dx, dy := 0, 0
	if g.XRange > 0 {
		dx = s.rand.Intn(g.XRange*2+1) - g.XRange
	}
	if g.YRange > 0 {
		dy = s.rand.Intn(g.YRange*2+1) - g.YRange
	}
	x, y := int16(g.X+dx), int16(g.Y+dy) //nolint:gosec // G115: int cell → int16 wire slot, map dims fit

	mob := &domain.Mob{
		EntityID:  s.mobs.NextEntityID(),
		MobID:     entry.Id,
		MapName:   mapName,
		SpawnX:    x,
		SpawnY:    y,
		PosX:      x,
		PosY:      y,
		Dir:       0,
		Level:     entry.Level,
		MaxHP:     entry.Hp,
		HP:        entry.Hp,
		Name:      entry.Name,
		WalkSpeed: int16(entry.WalkSpeed), //nolint:gosec // G115: int32 ms → int16 wire slot, speeds fit
	}
	if err := s.mobs.Register(mob); err != nil {
		// Double-register is impossible (the allocator is unique); ignore.
		return
	}
	entity := &aoi.Entity{
		ID:   mob.EntityID,
		Type: aoi.EntityMob,
		X:    int(x),
		Y:    int(y),
	}
	if err := mp.AOI.AddEntity(entity); err != nil {
		s.mobs.Unregister(mob.EntityID)
		return
	}
}

// Run is the mob wander tick. It loops on the zone tick cadence until ctx is
// cancelled (SIGTERM), then returns nil. Each tick it visits every map that has
// mobs and, for each mob whose WalkSpeed delay has elapsed since its last step,
// attempts one random adjacent step: a walkable neighbor (checked via the
// immutable MapData, not the pathfinder) moves the AOI entity and broadcasts a
// ZC_UNIT_WALKING to the players in range; a blocked neighbor is a no-op.
//
// Wander bookkeeping (last-step time) is local to this goroutine, so it needs
// no lock: Run is the sole reader/writer. Mob position is read through the
// locked Mob.Position and committed through Mob.SetPosition, so a concurrent
// enter-world spawn exchange (reading position to build a SpawnUnit) cannot race.
func (s *MobService) Run(ctx context.Context) error {
	ticker := time.NewTicker(s.tickRate)
	defer ticker.Stop()

	// lastStep is tick-local pacing state; only this goroutine touches it.
	lastStep := make(map[aoi.EntityID]time.Time)
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-ticker.C:
			for _, mapName := range s.mobs.Maps() {
				s.wanderMap(ctx, mapName, now, lastStep)
			}
		}
	}
}

// wanderMap steps every mob on one map whose walk delay has elapsed.
func (s *MobService) wanderMap(ctx context.Context, mapName string, now time.Time, lastStep map[aoi.EntityID]time.Time) {
	// ctx cancellation between maps keeps shutdown responsive on large worlds.
	select {
	case <-ctx.Done():
		return
	default:
	}
	mp, err := s.maps.Load(ctx, mapName)
	if err != nil || mp.Data == nil {
		// A map that seeded mobs should still load (it is cached); a nil Data
		// means no walkability grid to step on. Skip this tick for the map.
		return
	}
	for _, mob := range s.mobs.OnMap(mapName) {
		interval := time.Duration(mob.WalkSpeed) * time.Millisecond
		if interval <= 0 {
			interval = s.tickRate
		}
		due, ok := lastStep[mob.EntityID]
		if !ok {
			// First sighting: let the mob settle one interval before ambling.
			lastStep[mob.EntityID] = now
			continue
		}
		if now.Sub(due) < interval {
			continue
		}
		lastStep[mob.EntityID] = now
		s.wanderStep(mp, mob)
	}
}

// wanderStep attempts one random neighbor step for a mob. It picks an offset,
// checks the destination walkable on the immutable MapData, and on success
// broadcasts ZC_UNIT_WALKING to observing players and commits the move via
// AOI.MoveEntity + Mob.SetPosition. A blocked or out-of-bounds dest is a no-op.
func (s *MobService) wanderStep(mp *domain.Map, mob *domain.Mob) {
	srcX, srcY, _ := mob.Position()
	off := stepOffsets[s.rand.Intn(len(stepOffsets))]
	destX, destY := int(srcX)+off[0], int(srcY)+off[1]
	if !mp.Data.IsWalkable(destX, destY) {
		return
	}

	moveStart := s.clock.MoveStart()
	walk := mob.WalkUnit(srcX, srcY, int16(destX), int16(destY), moveStart) //nolint:gosec // G115: int cell → int16 wire slot
	s.broadcastWalk(mp, mob.EntityID, srcX, srcY, walk)

	if err := mp.AOI.MoveEntity(mob.EntityID, destX, destY); err != nil {
		// MoveEntity errors on OOB or unknown id; IsWalkable already validated
		// in-bounds, so an error here is a torn-down entity. Leave position as-is.
		return
	}
	mob.SetPosition(int16(destX), int16(destY), stepFacing(off[0], off[1])) //nolint:gosec // G115: int cell → int16 wire slot
}

// broadcastWalk sends a ZC_UNIT_WALKING frame to every player within AOI range
// of the move's source cell. Non-player entities (mobs, NPCs) are skipped — they
// have no Conn and never observe a walk broadcast. A write failure means the
// player's socket is dead; the move worker ("world-move") owns tearing dead
// players down, so here we simply skip the failed write (the player's own
// dispatch goroutine will Unregister + RemoveEntity on the dead socket).
func (s *MobService) broadcastWalk(mp *domain.Map, moverID aoi.EntityID, srcX, srcY int16, walk packet.UnitWalkingResponse) {
	for _, e := range mp.AOI.QueryVisible(int(srcX), int(srcY)) {
		if e.Type != aoi.EntityPlayer {
			continue
		}
		neighbor, ok := s.players.ByAccount(uint32(e.ID))
		if !ok {
			continue // a torn-down player the grid has not yet removed
		}
		_ = walk.Encode(connWriter{neighbor.Conn})
	}
}

// stepFacing maps a step offset to an RO direction byte (0=N,2=E,4=S,6=W with
// diagonals between), the same convention playerFacing uses. A mob that stepped
// faces the direction it moved.
func stepFacing(dx, dy int) uint8 {
	return facingDir[signIndex(dy)][signIndex(dx)]
}
