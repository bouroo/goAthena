//go:build unit

package aoi

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newEntity(id EntityID, typ EntityType, x, y int) *Entity {
	return &Entity{ID: id, Type: typ, X: x, Y: y}
}

func TestNewGridManager_BasicDimensions(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(100, 100)
	assert.Equal(t, 100, gm.Width())
	assert.Equal(t, 100, gm.Height())
	// 100 / 18 rounded up = 6
	assert.Equal(t, 6, gm.GridWidth())
	assert.Equal(t, 6, gm.GridHeight())
	assert.Equal(t, 36, len(gm.towers))
}

func TestTowerID_BoundsAndMapping(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(36, 36)
	tests := []struct {
		name string
		x, y int
		want int
	}{
		{"origin", 0, 0, 0},
		{"last in tower 0,0", 17, 17, 0},
		{"first in tower 0,1", 0, 18, 1 * gm.GridWidth()},
		{"first in tower 1,0", 18, 0, 1},
		{"deep tower", 35, 35, 1*gm.GridWidth() + 1},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, gm.TowerID(tc.x, tc.y))
		})
	}
}

func TestTowerID_OutOfBounds(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(50, 50)
	assert.Equal(t, -1, gm.TowerID(-1, 0))
	assert.Equal(t, -1, gm.TowerID(0, -1))
	assert.Equal(t, -1, gm.TowerID(50, 0))
	assert.Equal(t, -1, gm.TowerID(0, 50))
}

func TestAddEntity_HappyPath(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(100, 100)
	e := newEntity(1, EntityPlayer, 10, 20)
	require.NoError(t, gm.AddEntity(e))
	assert.Equal(t, 1, gm.EntityCount())
	assert.Equal(t, 1, gm.TowerEntityCount(gm.TowerID(10, 20)))
}

func TestAddEntity_DuplicateFails(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(100, 100)
	require.NoError(t, gm.AddEntity(newEntity(1, EntityPlayer, 10, 20)))
	err := gm.AddEntity(newEntity(1, EntityMob, 50, 50))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrEntityExists))
}

func TestAddEntity_OutOfBounds(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(100, 100)
	err := gm.AddEntity(newEntity(1, EntityPlayer, -1, 0))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrOutOfBounds))

	err = gm.AddEntity(newEntity(2, EntityMob, 0, 100))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrOutOfBounds))
}

func TestAddEntity_NilEntity(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(10, 10)
	err := gm.AddEntity(nil)
	require.Error(t, err)
}

func TestRemoveEntity_HappyPath(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(100, 100)
	e := newEntity(1, EntityPlayer, 10, 20)
	require.NoError(t, gm.AddEntity(e))
	require.NoError(t, gm.RemoveEntity(1))
	assert.Equal(t, 0, gm.EntityCount())
}

func TestRemoveEntity_UnknownID(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(100, 100)
	err := gm.RemoveEntity(42)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrEntityMissing))
}

func TestRemoveEntity_AllTypes(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(100, 100)
	require.NoError(t, gm.AddEntity(newEntity(1, EntityPlayer, 5, 5)))
	require.NoError(t, gm.AddEntity(newEntity(2, EntityNPC, 6, 6)))
	require.NoError(t, gm.AddEntity(newEntity(3, EntityMob, 7, 7)))
	assert.Equal(t, 3, gm.EntityCount())
	require.NoError(t, gm.RemoveEntity(1))
	require.NoError(t, gm.RemoveEntity(2))
	require.NoError(t, gm.RemoveEntity(3))
	assert.Equal(t, 0, gm.EntityCount())
}

func TestMoveEntity_SameTower(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(100, 100)
	require.NoError(t, gm.AddEntity(newEntity(1, EntityPlayer, 10, 10)))
	require.NoError(t, gm.MoveEntity(1, 17, 17))

	x, y, tid, ok := gm.EntityLocation(1)
	require.True(t, ok)
	assert.Equal(t, 17, x)
	assert.Equal(t, 17, y)
	// Both 10,10 and 17,17 fall in tower 0,0
	assert.Equal(t, 0, tid)
	assert.Equal(t, 1, gm.TowerEntityCount(0))
}

func TestMoveEntity_CrossTower(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(100, 100)
	require.NoError(t, gm.AddEntity(newEntity(1, EntityPlayer, 0, 0)))
	require.NoError(t, gm.MoveEntity(1, 25, 25)) // tower 0,0 → tower 1,1

	x, y, tid, ok := gm.EntityLocation(1)
	require.True(t, ok)
	assert.Equal(t, 25, x)
	assert.Equal(t, 25, y)
	assert.Equal(t, gm.TowerID(25, 25), tid)
	assert.Equal(t, 0, gm.TowerEntityCount(0))
	assert.Equal(t, 1, gm.TowerEntityCount(tid))
}

func TestMoveEntity_UnknownID(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(100, 100)
	err := gm.MoveEntity(99, 5, 5)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrEntityMissing))
}

func TestMoveEntity_OutOfBounds(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(100, 100)
	require.NoError(t, gm.AddEntity(newEntity(1, EntityPlayer, 50, 50)))
	err := gm.MoveEntity(1, -1, 50)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrOutOfBounds))
}

func TestMoveEntity_PreservesType(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(200, 200)
	require.NoError(t, gm.AddEntity(newEntity(1, EntityMob, 5, 5)))
	require.NoError(t, gm.MoveEntity(1, 25, 25))

	x, y, tid, ok := gm.EntityLocation(1)
	require.True(t, ok)
	assert.Equal(t, 25, x)
	assert.Equal(t, 25, y)
	assert.Equal(t, gm.TowerID(25, 25), tid)
	// Mob map in destination tower must contain it.
	dst := gm.Get9GridTowers(25, 25)
	var found bool
	for _, tw := range dst {
		tw.mu.RLock()
		if _, ok := tw.mobs[1]; ok {
			found = true
		}
		tw.mu.RUnlock()
	}
	assert.True(t, found, "moved mob should be in destination tower's mobs map")
}

func TestMoveEntity_AcrossManyTowers(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(200, 200)
	require.NoError(t, gm.AddEntity(newEntity(1, EntityPlayer, 0, 0)))
	for i := 1; i <= 50; i++ {
		require.NoError(t, gm.MoveEntity(1, i*3, i*3))
	}
	x, y, _, _ := gm.EntityLocation(1)
	assert.Equal(t, 150, x)
	assert.Equal(t, 150, y)
	assert.Equal(t, 1, gm.EntityCount())
}

func TestGet9GridTowers_Center(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(100, 100)
	towers := gm.Get9GridTowers(50, 50)
	assert.Len(t, towers, 9)
}

func TestGet9GridTowers_Corner(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(100, 100)
	towers := gm.Get9GridTowers(0, 0)
	assert.Len(t, towers, 4)
}

func TestGet9GridTowers_Edge(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(100, 100)
	towers := gm.Get9GridTowers(0, 50)
	assert.Len(t, towers, 6)
}

func TestGet9GridTowers_OutOfBounds(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(100, 100)
	assert.Nil(t, gm.Get9GridTowers(-1, 0))
	assert.Nil(t, gm.Get9GridTowers(0, 100))
}

func TestQueryVisible_ReturnsEntitiesInRadius(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(100, 100)
	// 7 entities within ±15 cells of (50, 50); 2 deliberately outside.
	points := []struct{ x, y int }{
		{50, 50},
		{55, 50},
		{50, 55},
		{40, 40},
		{60, 60},
		{35, 50},
		{50, 35},
		// out of range
		{20, 20},
		{70, 70},
	}
	for i, p := range points {
		require.NoError(t, gm.AddEntity(newEntity(EntityID(i+1), EntityPlayer, p.x, p.y)))
	}

	visible := gm.QueryVisible(50, 50)
	assert.Len(t, visible, len(points)-2)

	ids := make(map[EntityID]struct{}, len(visible))
	for _, e := range visible {
		ids[e.ID] = struct{}{}
	}
	for i := 0; i < len(points)-2; i++ {
		_, ok := ids[EntityID(i+1)]
		assert.True(t, ok, "entity %d should be visible", i+1)
	}
	for i := len(points) - 2; i < len(points); i++ {
		_, ok := ids[EntityID(i+1)]
		assert.False(t, ok, "entity %d should be outside the viewport", i+1)
	}
}

func TestQueryVisible_ExcludesDistantEntities(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(100, 100)
	require.NoError(t, gm.AddEntity(newEntity(1, EntityPlayer, 50, 50)))
	require.NoError(t, gm.AddEntity(newEntity(2, EntityPlayer, 50, 50))) // duplicate cell
	require.NoError(t, gm.AddEntity(newEntity(3, EntityMob, 0, 0)))      // far
	require.NoError(t, gm.AddEntity(newEntity(4, EntityMob, 99, 99)))    // far

	visible := gm.QueryVisible(50, 50)
	assert.Len(t, visible, 2)
}

func TestQueryVisible_EdgeCases(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(20, 20)
	require.NoError(t, gm.AddEntity(newEntity(1, EntityPlayer, 0, 0)))
	require.NoError(t, gm.AddEntity(newEntity(2, EntityPlayer, 19, 19)))

	// Corner query — only entities within radius of (0,0).
	visible := gm.QueryVisible(0, 0)
	assert.Len(t, visible, 1)
}

func TestQueryVisible_OutOfBoundsYieldsNil(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(10, 10)
	require.NoError(t, gm.AddEntity(newEntity(1, EntityPlayer, 5, 5)))
	assert.Nil(t, gm.QueryVisible(-1, 5))
	assert.Nil(t, gm.QueryVisible(5, 10))
}

func TestConcurrentMoves_NoRaceAndNoLoss(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(400, 400)
	const N = 100
	for i := 0; i < N; i++ {
		require.NoError(t, gm.AddEntity(newEntity(EntityID(i+1), EntityPlayer, i%400, (i*7)%400)))
	}

	var wg sync.WaitGroup
	var moves int64
	for g := 0; g < 100; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			x := (seed * 13) % 400
			y := (seed * 17) % 400
			for i := 0; i < 200; i++ {
				id := EntityID((seed + i) % N)
				nx := (x + i) % 400
				ny := (y + i*3) % 400
				if err := gm.MoveEntity(id+1, nx, ny); err == nil {
					atomic.AddInt64(&moves, 1)
				}
			}
		}(g)
	}
	wg.Wait()

	assert.Equal(t, N, gm.EntityCount(), "no entity should be lost under concurrent moves")
	assert.Greater(t, atomic.LoadInt64(&moves), int64(0))
}

func TestConcurrentAddRemove_NoRace(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(200, 200)
	var wg sync.WaitGroup
	for g := 0; g < 50; g++ {
		wg.Add(2)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				id := EntityID(seed*1000 + i + 1)
				_ = gm.AddEntity(newEntity(id, EntityMob, (seed+i)%200, (seed*3+i)%200))
			}
		}(g)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				id := EntityID(seed*1000 + i + 1)
				_ = gm.RemoveEntity(id)
			}
		}(g)
	}
	wg.Wait()
	// Final state: no panic, no race; count is whatever the last writer left.
	_ = gm.EntityCount()
}

func TestCount9Grid(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(200, 200)
	for i := 0; i < 20; i++ {
		require.NoError(t, gm.AddEntity(newEntity(EntityID(i+1), EntityPlayer, 50+i, 50+i)))
	}
	got := gm.Count9Grid(50, 50)
	assert.Equal(t, 20, got)
}

func TestEntityTypeString(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "player", EntityPlayer.String())
	assert.Equal(t, "npc", EntityNPC.String())
	assert.Equal(t, "mob", EntityMob.String())
	assert.Contains(t, EntityType(99).String(), "99")
}

func TestTowerEntityCount_OutOfRange(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(100, 100)
	assert.Equal(t, 0, gm.TowerEntityCount(-1))
	assert.Equal(t, 0, gm.TowerEntityCount(len(gm.towers)+1))
}
