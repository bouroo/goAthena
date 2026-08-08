package app

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/mobdb"
)

// SpawnService owns floor items (drops on the map) and mob spawning. Mob spawn
// points + mob death→drop live here; the pickup path resolves a floor item and
// hands it to the inventory port (injected by the gateway handler).
type SpawnService struct {
	world *WorldService
	mobs  *mobdb.Registry

	mu        sync.Mutex
	floor     map[uint32]*domain.FloorItem // by GroundID
	groundSeq atomic.Uint32
}

// NewSpawnService builds a SpawnService. mobs may be nil (no drops in tests).
func NewSpawnService(world *WorldService, mobs *mobdb.Registry) *SpawnService {
	return &SpawnService{world: world, mobs: mobs, floor: make(map[uint32]*domain.FloorItem)}
}

// SpawnMob registers a mob entity in the world at the given position.
func (s *SpawnService) SpawnMob(mobID domain.EntityID, mapName string, pos domain.Position, name string, hp, maxHp int32) error {
	return s.world.AddEntity(domain.Entity{
		ID:    mobID,
		Type:  domain.EntityTypeMob,
		Map:   mapName,
		Pos:   pos,
		Name:  name,
		HP:    hp,
		MaxHP: maxHp,
	})
}

// OnMobDeath generates floor-item drops from the mob's drop table. The AegisName
// → NameID resolution requires item_db; until that wiring lands (M6b), this is
// a no-op and drops are placed explicitly via DropItem. A nil mobs registry
// also yields no drops.
func (s *SpawnService) OnMobDeath(_ int32, _ string, _ domain.Position, _ domain.EntityID) []domain.FloorItem {
	// TODO(M6b): resolve mob.Drops AegisName→NameID via item_db, apply rate% RNG.
	return nil
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

// FloorItems returns a snapshot of floor items on a map near a position (for
// the AOI broadcast of ZC_ITEM_ENTRY on map-enter / spawn).
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

// (DropEntry alias removed — mob drop-table resolution lands in M6b with item_db wiring.)
var _ = mobdb.DropEntry{} //nolint:staticcheck // keep mobdb import for future drop resolution
