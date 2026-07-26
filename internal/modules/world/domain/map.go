// Package domain defines the world bounded context's pure model and ports. It
// imports only the pkg/ro kernel (romap/aoi/pathfinding) — the shared RO
// vocabulary — so both the filesystem adapter (infra) and the connection-facing
// use cases (app) program against these types without depending on each other.
package domain

import (
	"context"

	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/pathfinding"
	"github.com/bouroo/goAthena/pkg/ro/romap"
)

// Map is a loaded map instance: the static spatial data plus the per-map
// mutable engines the world drives on its tick — the AOI tower-grid and the A*
// pathfinder. One Map per map name; the engines are reused across every entity
// on that map.
//
// Engine ownership (a threading contract, not a hint):
//   - AOI (aoi.GridManager) is concurrency-safe. The entity registry mutates it
//     (Add/Move/Remove) as players and mobs enter, walk, and leave.
//   - Pathfinder is single-goroutine by kernel design (see pathfinding.New).
//     The zone tick loop is its sole owner; connection handlers must not call
//     FindPath directly but enqueue movement the tick resolves.
type Map struct {
	Data       *romap.MapData
	AOI        *aoi.GridManager
	Pathfinder *pathfinding.Pathfinder
}

// MapStore loads a map's spatial foundation from a backing source (the
// filesystem in production, fakes in tests) and caches it by name so the second
// and later loads of the same map reuse one AOI grid and pathfinder.
// Implementations must be safe for concurrent use.
type MapStore interface {
	Load(ctx context.Context, name string) (*Map, error)
}
