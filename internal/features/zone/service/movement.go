package service

import (
	"fmt"

	"github.com/bouroo/goAthena/internal/features/zone/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/pathfinding"
	"github.com/bouroo/goAthena/pkg/ro/romap"
)

// moveInterval computes the number of ticks between consecutive cell steps
// for an entity given its per-cell move speed (ms) and the tick rate (ms).
// Returns 1 (move every tick) when moveSpeed is less than the tick rate —
// we don't slow the simulation below the tick cadence.
func moveInterval(moveSpeed int, tickRateMs int) uint64 {
	if tickRateMs <= 0 || moveSpeed <= tickRateMs {
		return 1
	}
	n := uint64(moveSpeed / tickRateMs) //nolint:gosec // bounded by validator (move_speed max=1000).
	if n == 0 {
		return 1
	}
	return n
}

// computePath computes a path from (sx, sy) to (tx, ty) using the supplied
// pathfinder and map data. Returns the path slice (excluding start cell).
// Validates that both endpoints are walkable before invoking the
// pathfinder so callers can return ErrDestinationNotWalkable.
func computePath(sx, sy, tx, ty int, md *romap.MapData, pf *pathfinding.Pathfinder) ([]pathfinding.Point, error) {
	if md == nil {
		return nil, fmt.Errorf("zone: map data is nil")
	}
	if !md.IsWalkable(sx, sy) {
		return nil, fmt.Errorf("zone: start (%d,%d) not walkable", sx, sy)
	}
	if !md.IsWalkable(tx, ty) {
		return nil, fmt.Errorf("%w: (%d,%d)", ErrDestinationNotWalkable, tx, ty)
	}

	start := pathfinding.Point{X: sx, Y: sy}
	target := pathfinding.Point{X: tx, Y: ty}

	path, err := pf.FindPath(start, target)
	if err != nil {
		return nil, fmt.Errorf("zone: pathfind from (%d,%d) to (%d,%d): %w",
			start.X, start.Y, target.X, target.Y, err)
	}
	return path, nil
}

// snapshotVisible returns the entities in the AOI broadcast viewport
// around (x, y) as zone-domain Entities. The result is freshly
// allocated; callers may mutate it freely.
//
// Uses adaptive squeezing so high-density areas return a smaller radius.
func snapshotVisible(g *aoi.GridManager, x, y int) []*domain.Entity {
	aoiEnts := g.QueryVisibleSqueezed(x, y)
	out := make([]*domain.Entity, 0, len(aoiEnts))
	for _, ae := range aoiEnts {
		out = append(out, &domain.Entity{
			ID:   domain.EntityID(ae.ID),
			Type: domain.EntityType(ae.Type),
			X:    ae.X,
			Y:    ae.Y,
		})
	}
	return out
}

// toAOIType converts a zone-domain EntityType to an AOI EntityType. The
// enums currently overlap 1:1; this indirection lets them diverge
// later (e.g., items, skills) without rewriting call sites.
func toAOIType(t domain.EntityType) aoi.EntityType {
	return aoi.EntityType(t)
}
