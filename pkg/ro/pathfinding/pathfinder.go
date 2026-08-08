// Package pathfinding implements A* pathfinding on a walkability grid.
package pathfinding

import (
	"container/heap"
	"fmt"
)

// Pathfinder runs A* searches against a fixed Grid. Pre-allocates per-cell
// scratch buffers (g-cost, parent, closed set, open-set heap) in New so the
// hot path of FindPath performs no allocation beyond the returned path slice.
//
// A Pathfinder is NOT safe for concurrent use; allocate one per goroutine
// (the zone tick loop runs single-threaded, so a single instance suffices).
type Pathfinder struct {
	grid   Grid
	width  int
	height int
	size   int

	gCost  []int32
	parent []int32
	closed []bool

	pq *priorityQueue
}

// New builds a Pathfinder backed by grid. The grid dimensions are captured
// at construction time; passing a grid whose Width/Height change after this
// call is a programming error.
func New(grid Grid) *Pathfinder {
	w := grid.Width()
	h := grid.Height()
	n := w * h
	return &Pathfinder{
		grid:   grid,
		width:  w,
		height: h,
		size:   n,
		gCost:  make([]int32, n),
		parent: make([]int32, n),
		closed: make([]bool, n),
		pq:     &priorityQueue{items: make([]pathNode, 0, n)},
	}
}

// FindPath returns the shortest walkable A* path from start to target.
//
// The returned slice excludes start and includes target. When the optimal path
// is longer than MaxWalkPath, the result is truncated to that length (no
// error). Returned errors are:
//
//   - start or target outside the grid
//   - start or target on a non-walkable cell
//   - ErrNoPath when no walkable route exists
//
// start == target returns (empty slice, nil).
func (p *Pathfinder) FindPath(start, target Point) ([]Point, error) {
	if err := p.validateEndpoints(start, target); err != nil {
		return nil, err
	}
	if start.X == target.X && start.Y == target.Y {
		return []Point{}, nil
	}

	w := int32(p.width)                              //nolint:gosec // width is bounded by MAX_MAP_SIZE (512).
	startIdx := int32(start.Y)*w + int32(start.X)    //nolint:gosec // see above.
	targetIdx := int32(target.Y)*w + int32(target.X) //nolint:gosec // see above.

	p.reset()
	p.gCost[startIdx] = 0
	p.parent[startIdx] = -1
	heap.Push(p.pq, &pathNode{ //nolint:errcheck // heap.Push returns any, not error.
		f: heuristic(int32(start.X), int32(start.Y), int32(target.X), int32(target.Y)), //nolint:gosec
		g: 0,
		x: int32(start.X), //nolint:gosec // coordinates are grid-bounded.
		y: int32(start.Y), //nolint:gosec
	})

	for p.pq.Len() > 0 {
		cur := heap.Pop(p.pq).(*pathNode) //nolint:errcheck,forcetypeassert // heap.Pop returns any, not error.
		curIdx := cur.y*w + cur.x
		if p.closed[curIdx] {
			continue
		}
		p.closed[curIdx] = true

		if curIdx == targetIdx {
			return p.reconstructPath(targetIdx, w), nil
		}

		p.expand(cur, w, int32(p.height), target.X, target.Y) //nolint:gosec // height is bounded by MAX_MAP_SIZE.
	}

	return nil, ErrNoPath
}

func (p *Pathfinder) validateEndpoints(start, target Point) error {
	if !p.inBounds(start.X, start.Y) {
		return fmt.Errorf("pathfinding: start out of bounds: (%d,%d)", start.X, start.Y)
	}
	if !p.inBounds(target.X, target.Y) {
		return fmt.Errorf("pathfinding: target out of bounds: (%d,%d)", target.X, target.Y)
	}
	if !p.grid.Walkable(start.X, start.Y) {
		return fmt.Errorf("pathfinding: start not walkable: (%d,%d)", start.X, start.Y)
	}
	if !p.grid.Walkable(target.X, target.Y) {
		return fmt.Errorf("pathfinding: target not walkable: (%d,%d)", target.X, target.Y)
	}
	return nil
}

// expand relaxes the eight neighbors of cur into the open set.
func (p *Pathfinder) expand(cur *pathNode, w int32, h int32, tx, ty int) {
	for _, d := range neighbors {
		nx := cur.x + d.dx
		ny := cur.y + d.dy
		if nx < 0 || ny < 0 || nx >= w || ny >= h {
			continue
		}
		nIdx := ny*w + nx
		if p.closed[nIdx] {
			continue
		}
		if !p.grid.Walkable(int(nx), int(ny)) {
			continue
		}

		cost := int32(MoveCost)
		if d.dx != 0 && d.dy != 0 {
			if !p.grid.Walkable(int(cur.x+d.dx), int(cur.y)) {
				continue
			}
			if !p.grid.Walkable(int(cur.x), int(cur.y+d.dy)) {
				continue
			}
			cost = MoveDiagonalCost
		}

		newG := cur.g + cost
		if newG >= p.gCost[nIdx] {
			continue
		}
		p.gCost[nIdx] = newG
		p.parent[nIdx] = cur.y*w + cur.x
		f := newG + heuristic(nx, ny, int32(tx), int32(ty))     //nolint:gosec // tx/ty are grid-bounded.
		heap.Push(p.pq, &pathNode{f: f, g: newG, x: nx, y: ny}) //nolint:errcheck // heap.Push returns any.
	}
}

// LineOfSight reports whether the straight line from from to to is unblocked.
// Walks the line with Bresenham's algorithm and returns false if any
// intermediate cell (excluding start and target) is non-walkable.
//
// Used for ranged-attack wall checks; not used by FindPath itself.
func (p *Pathfinder) LineOfSight(from, to Point) bool {
	x0, y0 := from.X, from.Y
	x1, y1 := to.X, to.Y
	dx := x1 - x0
	if dx < 0 {
		dx = -dx
	}
	ady := y1 - y0
	if ady < 0 {
		ady = -ady
	}
	dy := -ady
	sx := 1
	if x0 >= x1 {
		sx = -1
	}
	sy := 1
	if y0 >= y1 {
		sy = -1
	}
	err := dx + dy

	for {
		if x0 != from.X || y0 != from.Y {
			if x0 != x1 || y0 != y1 {
				if !p.grid.Walkable(x0, y0) {
					return false
				}
			}
		}
		if x0 == x1 && y0 == y1 {
			return true
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

// neighbors enumerates the 8 directions in the deterministic order used by
// A*: orthogonal moves first (N, S, E, W), then diagonals (NE, NW, SE, SW).
// Ties on f-cost are broken by this order so two pathfinders over the same
// grid always emit the same path.
var neighbors = [...]struct{ dx, dy int32 }{
	{0, 1},   // N
	{0, -1},  // S
	{1, 0},   // E
	{-1, 0},  // W
	{1, 1},   // NE
	{-1, 1},  // NW
	{1, -1},  // SE
	{-1, -1}, // SW
}

const infCost = int32(1 << 30)

// heuristic is the Manhattan distance × MoveCost. It is intentionally
// inadmissible (overestimates diagonal steps by treating them as two
// orthogonal moves) so the produced path matches rAthena's game-client
// behavior, which also uses an inadmissible Manhattan heuristic.
func heuristic(x0, y0, x1, y1 int32) int32 {
	dx := x1 - x0
	if dx < 0 {
		dx = -dx
	}
	dy := y1 - y0
	if dy < 0 {
		dy = -dy
	}
	return (dx + dy) * MoveCost
}

func (p *Pathfinder) inBounds(x, y int) bool {
	return x >= 0 && y >= 0 && x < p.width && y < p.height
}

func (p *Pathfinder) reset() {
	for i := range p.gCost {
		p.gCost[i] = infCost
		p.parent[i] = -1
		p.closed[i] = false
	}
	p.pq.items = p.pq.items[:0]
}

func (p *Pathfinder) reconstructPath(targetIdx, w int32) []Point {
	out := make([]Point, 0, MaxWalkPath+1)
	cur := targetIdx
	for cur != -1 {
		out = append(out, Point{X: int(cur % w), Y: int(cur / w)})
		cur = p.parent[cur]
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if len(out) <= 1 {
		return []Point{}
	}
	out = out[1:]
	if len(out) > MaxWalkPath {
		out = out[:MaxWalkPath]
	}
	return out
}

// pathNode is a single open-set entry.
type pathNode struct {
	f, g int32
	x, y int32
}

// priorityQueue is a min-heap of pathNode ordered by f-cost. Implements
// container/heap.Interface.
type priorityQueue struct {
	items []pathNode
}

func (pq *priorityQueue) Len() int { return len(pq.items) }

func (pq *priorityQueue) Less(i, j int) bool { return pq.items[i].f < pq.items[j].f }

func (pq *priorityQueue) Swap(i, j int) {
	pq.items[i], pq.items[j] = pq.items[j], pq.items[i]
}

func (pq *priorityQueue) Push(x any) {
	n := x.(*pathNode) //nolint:errcheck,forcetypeassert // heap.Push returns any, not error.
	pq.items = append(pq.items, *n)
}

func (pq *priorityQueue) Pop() any {
	n := len(pq.items)
	item := pq.items[n-1]
	pq.items = pq.items[:n-1]
	return &item
}
