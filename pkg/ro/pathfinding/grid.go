package pathfinding

import (
	"errors"

	"github.com/bouroo/goAthena/pkg/ro/romap"
)

// MoveCost is the per-cell cost of an orthogonal step in A*.
const MoveCost = 10

// MoveDiagonalCost is the per-cell cost of a diagonal step in A*. The ratio
// 14/10 approximates sqrt(2) so diagonal and straight-line distances stay
// consistent on a uniform-cost grid.
const MoveDiagonalCost = 14

// MaxWalkPath is the maximum number of cells A* will return in a single path.
// Longer optimal paths are truncated; the caller re-paths on subsequent ticks.
const MaxWalkPath = 32

// ErrNoPath is returned by FindPath when no walkable route exists between
// start and target.
var ErrNoPath = errors.New("pathfinding: no path exists")

// Point is an (x, y) cell coordinate on a walkability grid.
type Point struct {
	X, Y int
}

// Grid is the walkability source consumed by a Pathfinder. Implementations
// must be safe to query concurrently with themselves but the Pathfinder itself
// is single-threaded; one Pathfinder per goroutine is sufficient.
//
// Out-of-bounds coordinates should report Walkable == false so the A* neighbor
// expansion treats map edges as walls.
type Grid interface {
	Width() int
	Height() int
	Walkable(x, y int) bool
}

// romapGrid adapts a romap.MapData to the Grid interface. It exists so callers
// can hand a Pathfinder the result of romap.LoadMap directly without
// duplicating walkability logic.
type romapGrid struct {
	md *romap.MapData
}

// FromMapData wraps a romap.MapData as a Grid for use with New. Returns nil
// when md is nil.
func FromMapData(md *romap.MapData) Grid {
	if md == nil {
		return nil
	}
	return &romapGrid{md: md}
}

func (g *romapGrid) Width() int  { return g.md.Width }
func (g *romapGrid) Height() int { return g.md.Height }

func (g *romapGrid) Walkable(x, y int) bool {
	return g.md.IsWalkable(x, y)
}
