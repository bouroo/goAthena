package domain

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// ErrMobAlreadyRegistered reports that the EntityID is already live. The
// MobRegistry allocator is unique, so a collision is a programming error
// (double-register), not a normal runtime condition.
var ErrMobAlreadyRegistered = errors.New("mob already registered")

// MobIDBase is the lower bound for monster EntityIDs. rAthena partitions block
// ids so monster/npc ids never overlap account ids: START_ACCOUNT_NUM = 2000000,
// END_ACCOUNT_NUM = 100000000, START_NPC_NUM = 110000000 (npc.hpp:245,
// common/mmo.hpp:176-177). Because the AOI GridManager keys entities by id alone
// (it does not fold the type into the key), a mob whose id collided with a live
// account_id would fail AddEntity with ErrEntityExists. Allocating from 110M up
// keeps the two spaces disjoint — exactly rAthena's contract.
const MobIDBase uint32 = 110000000

// Mob is the world's in-memory representation of a monster: the mob_db slice
// (sprite + stats), the AOI identity, and the world-owned mutable state
// (position, facing, HP). It is built once at spawn from a mobdb.MobEntry and
// held by MobRegistry for the mob's life.
//
// Mob HP and stats are world-owned entity state, NOT per-conn state — this is
// the legacy smell the plan calls out (a mob is not a connection; many players
// observe one mob). The mob tick (M5) mutates position; combat (M6) mutates HP.
type Mob struct {
	// mu guards the mutable post-spawn fields PosX, PosY, Dir, HP. Every other
	// field is set once at spawn and read-only afterward. The mob tick writes
	// position while a concurrent EnterWorld (on another connection) reads it to
	// build the mob's SpawnUnit for the newcomer — a cross-goroutine read/write
	// the Go race detector would flag without this lock. The tick is the sole
	// writer; spawn/walk builders are readers.
	mu sync.RWMutex

	// EntityID is the AOI grid key and the spawn packet's AID field. It is
	// allocated from MobIDBase up by MobRegistry so it never collides with a
	// player's account_id.
	EntityID aoi.EntityID

	// MobID is the mob_db id (e.g. 1002 Poring). rAthena writes it as the spawn
	// packet's class/job field — the sprite id the client renders — because a
	// mob's view_data LOOK_BASE equals its mob_db id absent a Sprite override.
	MobID int32

	// MapName is the map the mob is on (lower-case rAthena name, e.g.
	// "prontera"). Drives the per-map inverted index for tick fan-out.
	MapName string

	// SpawnX, SpawnY is the home cell the mob returns toward / respawns at.
	SpawnX int16
	SpawnY int16

	// Position at last spawn or wander step. Dir is the facing byte (0..7 for
	// mobs; the spawn/walk packets carry a single facing byte, not 0..15).
	PosX int16
	PosY int16
	Dir  uint8

	// Level, MaxHP, HP are the world-owned combat stats. The spawn packet writes
	// MaxHP/HP as -1 while the mob is at full HP (rAthena clif_set_unit_idle:
	// the HP bar only appears once hp < max_hp), so these are not directly
	// serialized on a fresh spawn — but they are the authoritative state combat
	// (M6) reads and mutates.
	Level int32
	MaxHP int32
	HP    int32

	// Name is the mob's display name (UTF-8, e.g. "Poring"). Written to the
	// 24-byte name slot of the spawn/walk packets.
	Name string

	// WalkSpeed is the mob's walk delay in ms-per-cell (mob_db WalkSpeed; lower
	// is faster). rAthena writes status.speed directly into the packet's speed
	// field; Poring = 400 (slow), Lunatic = 200. It also throttles the wander
	// tick: a mob steps no faster than its WalkSpeed.
	WalkSpeed int16
}

// Position returns the mob's current cell and facing under the read lock, so a
// concurrent SetPosition on the tick cannot race a SpawnUnit/WalkUnit build.
func (m *Mob) Position() (posX, posY int16, dir uint8) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.PosX, m.PosY, m.Dir
}

// SetPosition updates the mob's cell and facing under the write lock. The mob
// tick is the sole caller (M5); it commits a wander step atomically so a
// concurrent SpawnUnit reads either the old or the new cell, never a torn move.
func (m *Mob) SetPosition(posX, posY int16, dir uint8) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.PosX, m.PosY, m.Dir = posX, posY, dir
}

// SetHP updates the mob's current HP under the write lock. Combat (M6) is the
// sole caller; a concurrent spawn/status read sees a consistent HP.
func (m *Mob) SetHP(hp int32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.HP = hp
}

// ApplyDamage subtracts amount from the mob's HP under the write lock and
// reports whether THIS call killed it. Two players landing the killing blow in
// the same tick race this method: the lock serializes the decrements, and the
// caller that drives HP to <= 0 is the sole one to receive died=true — the
// authoritative kill credit (EXP, M6) is awarded exactly once. A call against
// an already-dead mob (HP <= 0) is a no-op returning died=false, so a late
// in-flight attack on a mob another goroutine just killed cannot double-award
// the death. amount is assumed >= 1 (the damage calc floors at 1); a non-positive
// amount leaves HP unchanged.
func (m *Mob) ApplyDamage(amount int32) (newHP int32, died bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.HP <= 0 {
		return 0, false
	}
	m.HP -= amount
	if m.HP <= 0 {
		m.HP = 0
		return 0, true
	}
	return m.HP, false
}

// SpawnUnit builds the ZC_SPAWN_UNIT frame for this mob. The field map mirrors
// rAthena clif_set_unit_idle / clif_spawn_unit for a BL_MOB at PACKETVER
// 20250604 (>= every version cutoff the struct encodes):
//
//   - ObjectType = 5 (NPC_MOB_TYPE; clif_bl_type BL_MOB with normal AI).
//   - AID = the mob's block id; GID = 0 (the char-code slot is PC-only —
//     rAthena writes GID = sd ? char_id : 0, and a mob has no map_session_data).
//   - Speed = the mob's walk delay (status_get_speed = mob_db WalkSpeed).
//   - Job = the mob_db id — the sprite class (view_data LOOK_BASE = mob id).
//   - XSize = YSize = 0 (the 5/5 size hint is PC-only; mobs/NPCs get 0/0).
//   - CLevel = the mob's level.
//   - MaxHP = HP = -1 on a full-HP spawn (the HP bar appears only once damaged).
//   - All equipment/view fields default to 0 (a mob has no hair/weapon/headgear).
func (m *Mob) SpawnUnit() packet.SpawnUnitResponse {
	posX, posY, dir := m.Position()
	return packet.SpawnUnitResponse{
		ObjectType: 5,                  // NPC_MOB_TYPE
		AID:        uint32(m.EntityID), //nolint:gosec // G115: aoi.EntityID(uint32) → uint32 wire slot, allocated < 2^31
		GID:        0,                  // non-PC: char-code slot is unused
		Speed:      m.WalkSpeed,
		Job:        int16(m.MobID), //nolint:gosec // G115: int32 mob id → int16 wire slot, mob ids fit
		PosX:       posX,
		PosY:       posY,
		Dir:        dir,
		XSize:      0, // mob/NPC size hint; 5/5 is PC-only
		YSize:      0,
		CLevel:     int16(m.Level), //nolint:gosec // G115: int32 level → int16 wire slot
		MaxHP:      -1,             // full-HP spawn: no HP bar until damaged (M6)
		HP:         -1,
		Name:       m.Name,
	}
}

// WalkUnit builds the ZC_UNIT_WALKING frame this mob emits to AOI observers when
// it takes a wander step. The appearance slice is identical to SpawnUnit (a walk
// broadcast is a spawn-with-motion, not a different view); only the move fields
// differ: SrcX/SrcY is the cell left, DestX/DestY the cell entered, and
// MoveStartTime is the server's monotone tick so observers interpolate in sync.
func (m *Mob) WalkUnit(srcX, srcY, destX, destY int16, moveStartTime uint32) packet.UnitWalkingResponse {
	return packet.UnitWalkingResponse{
		ObjectType:    5,                  // NPC_MOB_TYPE
		AID:           uint32(m.EntityID), //nolint:gosec // G115: aoi.EntityID → uint32 wire slot
		GID:           0,
		Speed:         m.WalkSpeed,
		Job:           int16(m.MobID), //nolint:gosec // G115: int32 mob id → int16 wire slot
		MoveStartTime: moveStartTime,
		SrcX:          srcX,
		SrcY:          srcY,
		DestX:         destX,
		DestY:         destY,
		XSize:         0,
		YSize:         0,
		CLevel:        int16(m.Level), //nolint:gosec // G115: int32 level → int16 wire slot
		MaxHP:         -1,
		HP:            -1,
		Name:          m.Name,
	}
}

// MobRegistry is the in-process index of live mobs: the primary key is the
// EntityID (AOI key), with a per-map inverted index so the wander tick can
// fan out per map without scanning every mob. It also owns EntityID allocation,
// drawing from MobIDBase so mob ids stay disjoint from player account_ids.
//
// Concurrency: the by-id and by-map maps are guarded by one mutex. SpawnAll
// (boot) and the wander tick (read fan-out) are the callers; M6 respawn will
// add/remove mid-tick. NextEntityID is lock-free (atomic CAS) so allocation does
// not serialize behind map mutations.
type MobRegistry struct {
	mu    sync.RWMutex
	next  uint32
	byID  map[aoi.EntityID]*Mob
	byMap map[string]map[aoi.EntityID]*Mob
}

// NewMobRegistry returns an empty registry whose EntityID allocator starts at
// MobIDBase (the first allocated id is MobIDBase itself).
func NewMobRegistry() *MobRegistry {
	return &MobRegistry{
		next:  MobIDBase,
		byID:  make(map[aoi.EntityID]*Mob),
		byMap: make(map[string]map[aoi.EntityID]*Mob),
	}
}

// NextEntityID allocates a unique mob EntityID. It is lock-free and safe to call
// concurrently; the CAS loop guarantees each returned id is handed out exactly
// once, starting at MobIDBase and ascending — matching rAthena's START_NPC_NUM.
func (r *MobRegistry) NextEntityID() aoi.EntityID {
	for {
		cur := atomic.LoadUint32(&r.next)
		if atomic.CompareAndSwapUint32(&r.next, cur, cur+1) {
			return aoi.EntityID(cur)
		}
	}
}

// Register indexes a mob by EntityID and map. A second mob with the same
// EntityID is a programming error (the allocator is unique) and is rejected
// rather than shadowing the first.
func (r *MobRegistry) Register(m *Mob) error {
	if m == nil {
		return errors.New("register nil mob")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byID[m.EntityID]; exists {
		return ErrMobAlreadyRegistered
	}
	r.byID[m.EntityID] = m
	if r.byMap[m.MapName] == nil {
		r.byMap[m.MapName] = make(map[aoi.EntityID]*Mob)
	}
	r.byMap[m.MapName][m.EntityID] = m
	return nil
}

// Unregister removes a mob from both indexes. Idempotent: an unknown id is a
// no-op (a concurrent respawn teardown racing this leaves the registry
// consistent).
func (r *MobRegistry) Unregister(id aoi.EntityID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.byID[id]
	if !ok {
		return
	}
	delete(r.byID, id)
	if mobs, ok := r.byMap[m.MapName]; ok {
		delete(mobs, id)
		if len(mobs) == 0 {
			delete(r.byMap, m.MapName)
		}
	}
}

// ByEntity returns the live mob for an EntityID, or (nil,false) if none. The
// returned pointer is shared; callers must not mutate it outside Mob methods.
func (r *MobRegistry) ByEntity(id aoi.EntityID) (*Mob, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.byID[id]
	return m, ok
}

// OnMap returns a defensive snapshot of the mobs currently on mapName, so the
// wander tick can iterate without holding the registry lock while it reads
// positions and writes to player Conns. Returns an empty (non-nil) slice for an
// unknown or empty map.
func (r *MobRegistry) OnMap(mapName string) []*Mob {
	r.mu.RLock()
	defer r.mu.RUnlock()
	src := r.byMap[mapName]
	out := make([]*Mob, 0, len(src))
	for _, m := range src {
		out = append(out, m)
	}
	return out
}

// Maps returns the distinct map names that currently hold at least one mob, so
// the wander tick iterates only maps with mobs. The order is not stable.
func (r *MobRegistry) Maps() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.byMap))
	for name := range r.byMap {
		out = append(out, name)
	}
	return out
}
