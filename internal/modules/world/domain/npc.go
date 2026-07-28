package domain

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// ErrNPCAlreadyRegistered reports that the EntityID is already live. The
// NPCRegistry allocator is unique, so a collision is a programming error
// (double-register), not a normal runtime condition.
var ErrNPCAlreadyRegistered = errors.New("npc already registered")

// NPCIDBase is the lower bound for NPC EntityIDs. rAthena draws NPC block ids
// from the shared START_NPC_NUM (110000000) counter that also feeds mobs and
// floor items; goAthena partitions each kind into its own registry keyed by
// EntityID, so an NPC id only needs to be unambiguous within the whole id space.
// Allocating from 500M keeps NPC ids disjoint from account ids (< START_ACCOUNT_NUM
// = 2000000), mob ids (from MobIDBase = 110000000, which realistic corpora never
// grow past a few thousand), and floor-item ids (from FloorItemIDBase = 2e9), so
// a CZ_CONTACT_NPC's AID never resolves to a mob or a player by accident.
const NPCIDBase uint32 = 500000000

// NPC is the world's in-memory representation of a script NPC: the AOI identity,
// the sprite the client renders, the display name, the map cell it stands on, and
// the compiled-script key the dialog handler runs when the NPC is clicked. An NPC
// is static — it never moves, takes damage, or changes state after placement — so
// unlike Mob it carries no mutex and every field is set once at spawn and read
// only afterward. The NPCRegistry guards the index, not the entry.
type NPC struct {
	// EntityID is the AOI grid key and the spawn packet's AID field. It is
	// allocated from NPCIDBase up by NPCRegistry so it never collides with a
	// player, mob, or floor-item id. CZ_CONTACT_NPC carries this id back when the
	// client clicks the NPC, and the dialog handler resolves the NPC through it.
	EntityID aoi.EntityID

	// Sprite is the NPC's view class — the sprite number the client renders
	// (rAthena npc_data view_data class, e.g. 46 = a Kafra-style sprite). It is
	// written to the spawn packet's Job/class slot, exactly as a mob writes its
	// mob_db id there.
	Sprite int16

	// Name is the NPC's display name (UTF-8, e.g. "Healer"). Written to the
	// 24-byte name slot of the spawn packet.
	Name string

	// MapName is the map the NPC stands on (lower-case rAthena name). Drives the
	// per-map inverted index so the spawn exchange fans out only NPCs on the
	// entering player's map.
	MapName string

	// PosX/PosY is the fixed cell the NPC occupies; Dir is its facing byte (0..7).
	PosX int16
	PosY int16
	Dir  uint8

	// ScriptName is the compiled-script key (the NPC header name) the content
	// module's dialog handler runs when this NPC is clicked. It links the world
	// entity to its behavior without the world domain importing the script
	// package — the id is a plain string the content module resolves.
	ScriptName string
}

// SpawnUnit builds the ZC_SET_UNIT_IDLE frame for this NPC. The field map mirrors
// rAthena clif_set_unit_idle for a BL_NPC at PACKETVER 20250604:
//
//   - ObjectType = 6 (NPC_EVT_TYPE; clif_bl_type BL_NPC, non-walking standard
//     NPC sprite, PACKETVER >= 20170726).
//   - AID = the NPC's block id; GID = 0 (the char-code slot is PC-only).
//   - Speed = 0 (a static NPC has no walk delay).
//   - Job = the NPC sprite class (view_data class).
//   - XSize = YSize = 0 (the 5/5 size hint is PC-only).
//   - CLevel = 0, MaxHP = HP = 0 (an NPC has no combat stats; no HP bar).
//   - All equipment/view fields default to 0.
func (n *NPC) SpawnUnit() packet.SetUnitIdleResponse {
	return packet.SetUnitIdleResponse{
		ObjectType: 6,                  // NPC_EVT_TYPE
		AID:        uint32(n.EntityID), //nolint:gosec // G115: aoi.EntityID(uint32) → uint32 wire slot, allocated < 2^31
		GID:        0,                  // non-PC: char-code slot is unused
		Speed:      0,                  // static NPC: no walk delay
		Job:        n.Sprite,
		PosX:       n.PosX,
		PosY:       n.PosY,
		Dir:        n.Dir,
		XSize:      0,
		YSize:      0,
		CLevel:     0,
		MaxHP:      0,
		HP:         0,
		Name:       n.Name,
	}
}

// NPCRegistry is the in-process index of live NPCs: the primary key is the
// EntityID (AOI key), with a per-map inverted index so the spawn exchange can fan
// out per map without scanning every NPC. It owns EntityID allocation, drawing
// from NPCIDBase so NPC ids stay disjoint from player, mob, and floor-item ids.
//
// Concurrency: the by-id and by-map maps are guarded by one mutex. NPC placement
// (boot) is the sole writer; the spawn exchange (read fan-out) and the dialog
// handler (ByEntity lookup) are concurrent readers. NextEntityID is lock-free
// (atomic CAS) so allocation does not serialize behind map mutations — identical
// to MobRegistry/FloorItemRegistry.
type NPCRegistry struct {
	mu    sync.RWMutex
	next  uint32
	byID  map[aoi.EntityID]*NPC
	byMap map[string]map[aoi.EntityID]*NPC
}

// NewNPCRegistry returns an empty registry whose EntityID allocator starts at
// NPCIDBase (the first allocated id is NPCIDBase itself).
func NewNPCRegistry() *NPCRegistry {
	return &NPCRegistry{
		next:  NPCIDBase,
		byID:  make(map[aoi.EntityID]*NPC),
		byMap: make(map[string]map[aoi.EntityID]*NPC),
	}
}

// NextEntityID allocates a unique NPC EntityID. It is lock-free and safe to call
// concurrently; the CAS loop guarantees each returned id is handed out exactly
// once, starting at NPCIDBase and ascending.
func (r *NPCRegistry) NextEntityID() aoi.EntityID {
	for {
		cur := atomic.LoadUint32(&r.next)
		if atomic.CompareAndSwapUint32(&r.next, cur, cur+1) {
			return aoi.EntityID(cur)
		}
	}
}

// Register indexes an NPC by EntityID and map. A second NPC with the same
// EntityID is a programming error (the allocator is unique) and is rejected
// rather than shadowing the first.
func (r *NPCRegistry) Register(n *NPC) error {
	if n == nil {
		return errors.New("register nil npc")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byID[n.EntityID]; exists {
		return ErrNPCAlreadyRegistered
	}
	r.byID[n.EntityID] = n
	if r.byMap[n.MapName] == nil {
		r.byMap[n.MapName] = make(map[aoi.EntityID]*NPC)
	}
	r.byMap[n.MapName][n.EntityID] = n
	return nil
}

// Unregister removes an NPC from both indexes. Idempotent: an unknown id is a
// no-op.
func (r *NPCRegistry) Unregister(id aoi.EntityID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.byID[id]
	if !ok {
		return
	}
	delete(r.byID, id)
	if npcs, ok := r.byMap[n.MapName]; ok {
		delete(npcs, id)
		if len(npcs) == 0 {
			delete(r.byMap, n.MapName)
		}
	}
}

// ByEntity returns the live NPC for an EntityID, or (nil,false) if none. The
// returned pointer is shared; callers must not mutate it. The dialog handler
// (M11c) resolves the clicked NPC through this.
func (r *NPCRegistry) ByEntity(id aoi.EntityID) (*NPC, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n, ok := r.byID[id]
	return n, ok
}

// OnMap returns a defensive snapshot of the NPCs currently on mapName, so the
// spawn exchange can iterate without holding the registry lock. Returns an empty
// (non-nil) slice for an unknown or empty map.
func (r *NPCRegistry) OnMap(mapName string) []*NPC {
	r.mu.RLock()
	defer r.mu.RUnlock()
	src := r.byMap[mapName]
	out := make([]*NPC, 0, len(src))
	for _, n := range src {
		out = append(out, n)
	}
	return out
}

// Len returns the number of live NPCs. Used by tests and diagnostics.
func (r *NPCRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byID)
}
