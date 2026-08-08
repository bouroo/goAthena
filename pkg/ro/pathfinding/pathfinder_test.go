//go:build unit

package pathfinding

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/pkg/ro/romap"
)

// boolGrid is a minimal Grid implementation backed by a []bool walkability
// slice. Out-of-bounds is treated as non-walkable.
type boolGrid struct {
	w, h  int
	cells []bool
}

func newBoolGrid(w, h int) *boolGrid {
	return &boolGrid{w: w, h: h, cells: make([]bool, w*h)}
}

func (g *boolGrid) Width() int  { return g.w }
func (g *boolGrid) Height() int { return g.h }

func (g *boolGrid) Walkable(x, y int) bool {
	if x < 0 || y < 0 || x >= g.w || y >= g.h {
		return false
	}
	return g.cells[y*g.w+x]
}

func (g *boolGrid) set(x, y int, walkable bool) {
	g.cells[y*g.w+x] = walkable
}

func (g *boolGrid) fill(walkable bool) {
	for i := range g.cells {
		g.cells[i] = walkable
	}
}

func openGrid(w, h int) *boolGrid {
	g := newBoolGrid(w, h)
	g.fill(true)
	return g
}

func TestNew_AllocatesBuffers(t *testing.T) {
	t.Parallel()

	g := openGrid(10, 10)
	p := New(g)
	require.NotNil(t, p)
	assert.Equal(t, 100, len(p.gCost))
	assert.Equal(t, 100, len(p.parent))
	assert.Equal(t, 100, len(p.closed))
	assert.Equal(t, 100, cap(p.pq.items))
}

func TestFindPath_StraightLine(t *testing.T) {
	t.Parallel()

	g := openGrid(20, 20)
	p := New(g)

	path, err := p.FindPath(Point{0, 0}, Point{19, 0})
	require.NoError(t, err)
	assert.Equal(t, 19, len(path))
	for i, pt := range path {
		assert.Equal(t, i+1, pt.X, "step %d x", i)
		assert.Equal(t, 0, pt.Y, "step %d y", i)
	}
}

func TestFindPath_Diagonal(t *testing.T) {
	t.Parallel()

	g := openGrid(20, 20)
	p := New(g)

	path, err := p.FindPath(Point{0, 0}, Point{10, 10})
	require.NoError(t, err)
	require.Len(t, path, 10)
	for i, pt := range path {
		assert.Equal(t, i+1, pt.X, "step %d x", i)
		assert.Equal(t, i+1, pt.Y, "step %d y", i)
	}
}

func TestFindPath_LShapedPath(t *testing.T) {
	t.Parallel()

	g := openGrid(20, 20)
	for y := 0; y < 19; y++ {
		g.set(10, y, false)
	}
	p := New(g)

	path, err := p.FindPath(Point{0, 0}, Point{19, 10})
	require.NoError(t, err)
	require.NotEmpty(t, path)

	for _, pt := range path {
		assert.False(t, pt.X == 10 && pt.Y < 19,
			"path must not cross the wall at x=10, y in [0,18]; got %v", pt)
	}

	end := path[len(path)-1]
	assert.Equal(t, 19, end.X)
	assert.Equal(t, 10, end.Y)
}

func TestFindPath_CornerCutPrevention(t *testing.T) {
	t.Parallel()

	g := openGrid(10, 10)
	g.set(5, 4, false)
	g.set(4, 5, false)
	p := New(g)

	path, err := p.FindPath(Point{4, 4}, Point{5, 5})
	require.NoError(t, err)
	require.NotEmpty(t, path)
	for _, pt := range path {
		assert.NotEqual(t, Point{X: 4, Y: 4}, pt, "path must exclude the start cell from the returned slice")
		assert.False(t, pt.X == 4 && pt.Y == 5, "path must not pass through wall (4,5)")
		assert.False(t, pt.X == 5 && pt.Y == 4, "path must not pass through wall (5,4)")
	}

	g2 := openGrid(6, 6)
	for _, pt := range []Point{{1, 1}, {2, 1}, {3, 1}, {1, 2}, {3, 2}, {1, 3}, {2, 3}, {3, 3}} {
		g2.set(pt.X, pt.Y, false)
	}
	p2 := New(g2)
	_, err = p2.FindPath(Point{0, 0}, Point{2, 2})
	require.ErrorIs(t, err, ErrNoPath, "fully enclosed target must be unreachable")
}

func TestFindPath_NoPath(t *testing.T) {
	t.Parallel()

	g := openGrid(10, 10)
	g.set(5, 0, false)
	g.set(5, 1, false)
	g.set(5, 2, false)
	g.set(5, 3, false)
	g.set(5, 4, false)
	g.set(5, 5, false)
	g.set(5, 6, false)
	g.set(5, 7, false)
	g.set(5, 8, false)
	g.set(5, 9, false)
	p := New(g)

	path, err := p.FindPath(Point{0, 0}, Point{9, 9})
	require.ErrorIs(t, err, ErrNoPath)
	assert.Nil(t, path)
}

func TestFindPath_NoPathEnclosedTarget(t *testing.T) {
	t.Parallel()

	g := openGrid(10, 10)
	for _, pt := range []Point{{4, 4}, {5, 4}, {6, 4}, {4, 5}, {6, 5}, {4, 6}, {5, 6}, {6, 6}} {
		g.set(pt.X, pt.Y, false)
	}
	p := New(g)

	_, err := p.FindPath(Point{0, 0}, Point{5, 5})
	require.ErrorIs(t, err, ErrNoPath)
}

func TestFindPath_MaxPathLength(t *testing.T) {
	t.Parallel()

	const w = 200
	const h = 200
	g := openGrid(w, h)
	p := New(g)

	start := Point{0, 0}
	target := Point{199, 199}
	path, err := p.FindPath(start, target)
	require.NoError(t, err)
	assert.Len(t, path, MaxWalkPath)

	for i := 1; i < len(path); i++ {
		dx := path[i].X - path[i-1].X
		dy := path[i].Y - path[i-1].Y
		assert.True(t, dx >= -1 && dx <= 1 && dy >= -1 && dy <= 1,
			"consecutive cells must be adjacent: %v -> %v", path[i-1], path[i])
	}
}

func TestFindPath_StartEqualsTarget(t *testing.T) {
	t.Parallel()

	g := openGrid(10, 10)
	p := New(g)

	path, err := p.FindPath(Point{3, 3}, Point{3, 3})
	require.NoError(t, err)
	assert.Empty(t, path)
}

func TestFindPath_OutOfBounds(t *testing.T) {
	t.Parallel()

	g := openGrid(10, 10)
	p := New(g)

	_, err := p.FindPath(Point{-1, 0}, Point{5, 5})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNoPath)

	_, err = p.FindPath(Point{0, 0}, Point{10, 0})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNoPath)

	_, err = p.FindPath(Point{0, 0}, Point{0, 10})
	require.Error(t, err)

	_, err = p.FindPath(Point{0, 0}, Point{-1, -1})
	require.Error(t, err)
}

func TestFindPath_NonWalkableEndpoints(t *testing.T) {
	t.Parallel()

	g := openGrid(10, 10)
	g.set(0, 0, false)
	g.set(9, 9, false)
	p := New(g)

	_, err := p.FindPath(Point{0, 0}, Point{5, 5})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNoPath)

	_, err = p.FindPath(Point{5, 5}, Point{9, 9})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNoPath)
}

func TestFindPath_ReusePathfinder(t *testing.T) {
	t.Parallel()

	g := openGrid(20, 20)
	p := New(g)

	for i := 0; i < 5; i++ {
		path, err := p.FindPath(Point{0, 0}, Point{19, 19})
		require.NoError(t, err)
		require.NotEmpty(t, path)
	}
}

func TestLineOfSight_Clear(t *testing.T) {
	t.Parallel()

	g := openGrid(20, 20)
	p := New(g)

	assert.True(t, p.LineOfSight(Point{0, 0}, Point{10, 0}))
	assert.True(t, p.LineOfSight(Point{0, 0}, Point{10, 10}))
	assert.True(t, p.LineOfSight(Point{0, 0}, Point{0, 10}))
	assert.True(t, p.LineOfSight(Point{5, 5}, Point{5, 5}))
	assert.True(t, p.LineOfSight(Point{10, 0}, Point{0, 10}))
}

func TestLineOfSight_BlockedByWall(t *testing.T) {
	t.Parallel()

	g := openGrid(20, 20)
	g.set(5, 0, false)
	p := New(g)

	assert.False(t, p.LineOfSight(Point{0, 0}, Point{10, 0}))
}

func TestLineOfSight_BlockedDiagonal(t *testing.T) {
	t.Parallel()

	g := openGrid(20, 20)
	g.set(5, 5, false)
	p := New(g)

	assert.False(t, p.LineOfSight(Point{0, 0}, Point{10, 10}))
}

func TestLineOfSight_SkipTargetIfBlocked(t *testing.T) {
	t.Parallel()

	g := openGrid(20, 20)
	g.set(10, 10, false)
	p := New(g)

	assert.True(t, p.LineOfSight(Point{0, 0}, Point{10, 10}))
}

func TestLineOfSight_SkipStartIfBlocked(t *testing.T) {
	t.Parallel()

	g := openGrid(20, 20)
	g.set(0, 0, false)
	p := New(g)

	assert.True(t, p.LineOfSight(Point{0, 0}, Point{10, 10}))
}

func TestLineOfSight_NegativeStep(t *testing.T) {
	t.Parallel()

	g := openGrid(20, 20)
	p := New(g)

	assert.True(t, p.LineOfSight(Point{10, 10}, Point{0, 0}))
	assert.True(t, p.LineOfSight(Point{10, 0}, Point{0, 10}))
}

func TestFromMapData_Nil(t *testing.T) {
	t.Parallel()

	assert.Nil(t, FromMapData(nil))
}

func TestFromMapData_Basic(t *testing.T) {
	t.Parallel()

	const w = 8
	const h = 8
	types := make([][]int, h)
	for y := 0; y < h; y++ {
		types[y] = make([]int, w)
		for x := 0; x < w; x++ {
			types[y][x] = 0
		}
	}
	for y := 1; y < 6; y++ {
		types[y][4] = 1
	}

	gat := buildGAT(t, w, h, types)

	md, err := romap.LoadMap("test", gat, nil)
	require.NoError(t, err)

	grid := FromMapData(md)
	require.NotNil(t, grid)
	assert.Equal(t, w, grid.Width())
	assert.Equal(t, h, grid.Height())

	p := New(grid)
	path, err := p.FindPath(Point{0, 0}, Point{7, 7})
	require.NoError(t, err)
	require.NotEmpty(t, path)
	for _, pt := range path {
		assert.False(t, pt.X == 4 && pt.Y >= 1 && pt.Y <= 5,
			"path must avoid wall cells in column x=4 rows 1..5, got %v", pt)
	}

	end := path[len(path)-1]
	assert.Equal(t, 7, end.X)
	assert.Equal(t, 7, end.Y)
}

// buildGAT emits a synthetic .gat buffer with the given cell-type grid. Used
// only by the FromMapData test; production code reads real .gat bytes.
func buildGAT(t *testing.T, w, h int, types [][]int) []byte {
	t.Helper()
	if len(types) != h {
		t.Fatalf("types must have %d rows, got %d", h, len(types))
	}
	buf := make([]byte, 14+w*h*20)
	buf[6] = byte(w)
	buf[7] = byte(w >> 8)
	buf[8] = byte(w >> 16)
	buf[9] = byte(w >> 24)
	buf[10] = byte(h)
	buf[11] = byte(h >> 8)
	buf[12] = byte(h >> 16)
	buf[13] = byte(h >> 24)
	off := 14
	for y := 0; y < h; y++ {
		if len(types[y]) != w {
			t.Fatalf("types[%d] must have %d cols, got %d", y, w, len(types[y]))
		}
		for x := 0; x < w; x++ {
			off += 16
			typ := uint32(types[y][x])
			buf[off] = byte(typ)
			buf[off+1] = byte(typ >> 8)
			buf[off+2] = byte(typ >> 16)
			buf[off+3] = byte(typ >> 24)
			off += 4
		}
	}
	return buf
}

var _ = errors.New // silence unused import when test list shrinks
