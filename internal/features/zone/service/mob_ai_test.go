//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/features/zone/domain"
	"github.com/bouroo/goAthena/pkg/ro/pathfinding"
	"github.com/bouroo/goAthena/pkg/ro/romap"
)

func testMapData() *romap.MapData {
	const w, h = 20, 20
	md := &romap.MapData{
		Name:     "test",
		Width:    w,
		Height:   h,
		Walkable: make([]bool, w*h),
		Heights:  make([]float32, w*h),
	}
	for i := range md.Walkable {
		md.Walkable[i] = true
	}
	return md
}

func TestTickMobAI_WanderWhenTimerExpires(t *testing.T) {
	md := testMapData()
	pf := pathfinding.New(pathfinding.FromMapData(md))

	mob := &domain.Entity{
		ID:           1,
		Type:         domain.EntityMob,
		X:            10,
		Y:            10,
		MobID:        1002,
		HP:           50,
		MaxHP:        50,
		AI:           2,
		SpawnOriginX: 10,
		SpawnOriginY: 10,
		MoveSpeed:    400,
		WanderTimer:  0,
	}

	entities := map[domain.EntityID]*domain.Entity{1: mob}

	tickMobAI(entities, 1, 50, md, pf)

	// Either a path was generated OR the wander timer was reset (AI tick occurred)
	pathGenerated := len(mob.Path) > 0
	timerReset := mob.WanderTimer > 1
	assert.True(t, pathGenerated || timerReset, "mob should have wandered (path or timer reset)")
	if pathGenerated {
		assert.Greater(t, mob.WanderTimer, uint64(1), "wander timer should be reset to future")
	}
}

func TestTickMobAI_SkipWhenTimerNotExpired(t *testing.T) {
	md := testMapData()
	pf := pathfinding.New(pathfinding.FromMapData(md))

	mob := &domain.Entity{
		ID:           1,
		Type:         domain.EntityMob,
		X:            10,
		Y:            10,
		MobID:        1002,
		AI:           2,
		SpawnOriginX: 10,
		SpawnOriginY: 10,
		MoveSpeed:    400,
		WanderTimer:  100,
	}

	entities := map[domain.EntityID]*domain.Entity{1: mob}

	tickMobAI(entities, 50, 50, md, pf)

	assert.Empty(t, mob.Path, "mob should not wander when timer not expired")
}

func TestTickMobAI_SkipPlayersAndNPCs(t *testing.T) {
	md := testMapData()
	pf := pathfinding.New(pathfinding.FromMapData(md))

	player := &domain.Entity{
		ID: 1, Type: domain.EntityPlayer, X: 10, Y: 10,
		WanderTimer: 0,
	}
	npc := &domain.Entity{
		ID: 2, Type: domain.EntityNPC, X: 10, Y: 10,
		WanderTimer: 0,
	}

	entities := map[domain.EntityID]*domain.Entity{1: player, 2: npc}

	tickMobAI(entities, 100, 50, md, pf)

	assert.Empty(t, player.Path)
	assert.Empty(t, npc.Path)
}

func TestTickMobAI_SkipWalkingMobs(t *testing.T) {
	md := testMapData()
	pf := pathfinding.New(pathfinding.FromMapData(md))

	mob := &domain.Entity{
		ID:           1,
		Type:         domain.EntityMob,
		X:            10,
		Y:            10,
		MobID:        1002,
		AI:           2,
		SpawnOriginX: 10,
		SpawnOriginY: 10,
		MoveSpeed:    400,
		WanderTimer:  0,
		Path:         []pathfinding.Point{{X: 11, Y: 10}},
	}

	originalPath := mob.Path
	entities := map[domain.EntityID]*domain.Entity{1: mob}

	tickMobAI(entities, 100, 50, md, pf)

	assert.Equal(t, originalPath, mob.Path, "walking mob's path should not change")
}

func TestPickWanderTarget_FindsWalkable(t *testing.T) {
	md := testMapData()
	x, y, ok := pickWanderTarget(md, 10, 10)
	require.True(t, ok)
	assert.True(t, md.IsWalkable(x, y))
	dx := x - 10
	dy := y - 10
	assert.LessOrEqual(t, absInt(dx), wanderRadius)
	assert.LessOrEqual(t, absInt(dy), wanderRadius)
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func TestWanderInterval_InRange(t *testing.T) {
	for i := 0; i < 100; i++ {
		v := wanderInterval()
		assert.GreaterOrEqual(t, v, uint64(wanderMinTicks))
		assert.LessOrEqual(t, v, uint64(wanderMaxTicks))
	}
}
