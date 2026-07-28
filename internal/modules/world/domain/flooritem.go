package domain

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/bouroo/goAthena/pkg/ro/aoi"
)

// ErrFloorItemAlreadyRegistered reports that the EntityID is already live. The
// FloorItemRegistry allocator is unique, so a collision is a programming error
// (double-register), not a normal runtime condition.
var ErrFloorItemAlreadyRegistered = errors.New("floor item already registered")

// FloorItemIDBase is the lower bound for floor-item EntityIDs. rAthena floor
// items (flooritem_data::id) draw from the shared block-id counter (START_NPC_NUM
// = 110000000); goAthena partitions them into their own registry keyed by
// EntityID, so the id only needs to be unambiguous. Allocating from 2e9 keeps the
// wire-level object id disjoint from both player account ids (< START_ACCOUNT_NUM
// = 2000000) and mob ids (from MobIDBase = 110000000), so a client/server log
// never conflates a dropped item with a mob or a player.
const FloorItemIDBase uint32 = 2000000000

// FloorItem is the world's in-memory representation of one item lying on a map
// tile after a drop (mob death, player drop, script makeitem): the AOI identity,
// the item (numeric NameID + Amount the wire format carries), the cell, and the
// loot-protection owner. The M9c-1 slice only drops floor items and broadcasts
// ZC_ITEM_FALL_ENTRY — pickup (and loot-protection enforcement) lands in M10 —
// so the entry is immutable for its whole floor life: it carries no mutex, and
// the FloorItemRegistry guards the index, not the entry.
//
// Type is the rAthena IT_* enum (clif.cpp itemtype()) the wire format carries,
// resolved once at drop time from the item_db Type string via itemdb.WireType.
// Caching it here avoids a registry lookup on every broadcast/reveal/pickup.
type FloorItem struct {
	// EntityID is the AOI key and the drop packet's ground-item object id. It is
	// allocated from FloorItemIDBase up by FloorItemRegistry so it never
	// collides with a mob or account id.
	EntityID aoi.EntityID

	// MapName is the map this item lies on, so the M10 pickup path and the
	// deferred lifetime-timer can find it without scanning every map.
	MapName string

	// NameID is the resolved item_db id (the wire's nameID / sprite). A drop
	// resolves the mob_db drop entry's AegisName through item_db to this.
	NameID uint32

	// Type is the IT_* wire enum (IT_HEALING=0 … IT_CASH=13).
	Type uint16

	// Amount is the stack count. rAthena drops stackables at their rolled
	// amount and equipment one-at-a-time; M9c-1 drops amount 1 per winning
	// entry (the per-entry amount multipliers are a balance knob for later).
	Amount uint16

	// PosX/PosY is the map cell the item occupies — the dead mob's death cell.
	PosX int16
	PosY int16

	// OwnerAccountID is the killer, used by M10's loot-protection timer (the
	// first-get / second-get windows rAthena sets in map_addflooritem). 0 means
	// free-for-all; M9c-1 sets 0 because no protection window is enforced yet.
	OwnerAccountID uint32
}

// FloorItemRegistry is the in-process index of dropped items: the primary key is
// the EntityID, with a per-map inverted index so the deferred M10 pickup and
// lifetime-timer paths can fan out per map. It owns EntityID allocation, drawing
// from FloorItemIDBase so floor-item ids stay disjoint from mob and account ids.
//
// Concurrency: the by-id and by-map maps are guarded by one mutex. onMobDeath
// (drop) is the sole writer for M9c-1; M10 pickup and the lifetime-timer will
// add/remove concurrently. NextEntityID is lock-free (atomic CAS) so allocation
// does not serialize behind map mutations — identical to MobRegistry.
type FloorItemRegistry struct {
	mu    sync.RWMutex
	next  uint32
	byID  map[aoi.EntityID]*FloorItem
	byMap map[string]map[aoi.EntityID]*FloorItem
}

// NewFloorItemRegistry returns an empty registry whose EntityID allocator starts
// at FloorItemIDBase (the first allocated id is FloorItemIDBase itself).
func NewFloorItemRegistry() *FloorItemRegistry {
	return &FloorItemRegistry{
		next:  FloorItemIDBase,
		byID:  make(map[aoi.EntityID]*FloorItem),
		byMap: make(map[string]map[aoi.EntityID]*FloorItem),
	}
}

// NextEntityID allocates a unique floor-item EntityID. It is lock-free and safe
// to call concurrently; the CAS loop guarantees each returned id is handed out
// exactly once, starting at FloorItemIDBase and ascending.
func (r *FloorItemRegistry) NextEntityID() aoi.EntityID {
	for {
		cur := atomic.LoadUint32(&r.next)
		if atomic.CompareAndSwapUint32(&r.next, cur, cur+1) {
			return aoi.EntityID(cur)
		}
	}
}

// Register indexes a floor item by EntityID and map. A second item with the same
// EntityID is a programming error (the allocator is unique) and is rejected
// rather than shadowing the first.
func (r *FloorItemRegistry) Register(fi *FloorItem) error {
	if fi == nil {
		return errors.New("register nil floor item")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byID[fi.EntityID]; exists {
		return ErrFloorItemAlreadyRegistered
	}
	r.byID[fi.EntityID] = fi
	if r.byMap[fi.MapName] == nil {
		r.byMap[fi.MapName] = make(map[aoi.EntityID]*FloorItem)
	}
	r.byMap[fi.MapName][fi.EntityID] = fi
	return nil
}

// Unregister removes a floor item from both indexes. Idempotent: an unknown id is
// a no-op (a concurrent pickup racing this leaves the registry consistent).
func (r *FloorItemRegistry) Unregister(id aoi.EntityID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fi, ok := r.byID[id]
	if !ok {
		return
	}
	delete(r.byID, id)
	if items, ok := r.byMap[fi.MapName]; ok {
		delete(items, id)
		if len(items) == 0 {
			delete(r.byMap, fi.MapName)
		}
	}
}

// ByEntity returns the floor item for an EntityID, or (nil,false) if none. The
// returned pointer is shared; callers must not mutate it.
func (r *FloorItemRegistry) ByEntity(id aoi.EntityID) (*FloorItem, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	fi, ok := r.byID[id]
	return fi, ok
}

// Len returns the number of live floor items. Used by tests/diagnostics; the
// lifetime-timer (M10) will also bound its sweep with this.
func (r *FloorItemRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byID)
}
