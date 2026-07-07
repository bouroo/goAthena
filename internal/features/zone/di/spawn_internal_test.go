//go:build unit

package di

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bouroo/goAthena/pkg/ro/romap"
)

func TestFindWalkableSpawn_CenterIsWalkable(t *testing.T) {
	t.Parallel()

	md := allWalkableMap(10, 10)

	x, y := findWalkableSpawn(md)
	assert.Equal(t, 5, x)
	assert.Equal(t, 5, y)
}

func TestFindWalkableSpawn_ScansOutward(t *testing.T) {
	t.Parallel()

	md := allWalkableMap(11, 11)
	md.Walkable[5*md.Width+5] = false // block center

	x, y := findWalkableSpawn(md)
	assert.True(t, md.IsWalkable(x, y), "result must be walkable")
	// Result must be on ring r=1 (the nearest walkable ring around center).
	assert.Equal(t, 1, max(abs(x-5), abs(y-5)))
}

func TestFindWalkableSpawn_FallbackOnDegenerate(t *testing.T) {
	t.Parallel()

	md := &romap.MapData{
		Name:     "void",
		Width:    4,
		Height:   4,
		Walkable: make([]bool, 16), // all walls
	}

	x, y := findWalkableSpawn(md)
	assert.Equal(t, 0, x)
	assert.Equal(t, 0, y)
}

func TestFindWalkableSpawn_NonSquare(t *testing.T) {
	t.Parallel()

	md := &romap.MapData{
		Name:     "wide",
		Width:    20,
		Height:   4,
		Walkable: make([]bool, 80),
	}
	for i := range md.Walkable {
		md.Walkable[i] = true
	}
	// Block a strip in the middle to force an outward scan.
	for x := 0; x < 20; x++ {
		md.Walkable[2*md.Width+x] = false
	}

	x, y := findWalkableSpawn(md)
	assert.True(t, md.IsWalkable(x, y), "result must be walkable")
}

func allWalkableMap(w, h int) *romap.MapData {
	md := &romap.MapData{
		Name:     "test",
		Width:    w,
		Height:   h,
		Walkable: make([]bool, w*h),
	}
	for i := range md.Walkable {
		md.Walkable[i] = true
	}
	return md
}
