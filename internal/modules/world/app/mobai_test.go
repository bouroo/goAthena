//go:build unit

package app_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/internal/modules/world/infra"
	"github.com/bouroo/goAthena/pkg/ro/mobdb"
)

// mobAIFixture seeds an aggressive mob (Ai=4, AttackRange=2, ChaseRange=12), a
// passive mob (Ai=1), and a chaser mob (8003: Ai=4, AttackRange=1, ChaseRange=12,
// WalkSpeed=400ms/cell). 8001's AttackRange=2 lets a kill→retarget test place a
// second player one cell farther than the first while still in swing range, and
// its WalkSpeed=0 keeps the legacy hold-target behavior (no chasing). 8003's
// AttackRange=1 + WalkSpeed=400 make chase positions deterministic: one step per
// 400ms tick, halting at Chebyshev distance 1. All three carry Str/Attack/Dex/
// Level so the aggressive ones resolve a non-zero BaseATK+WeaponATK and actually
// damage a player through CombatService.Attack. nil Dice ⇒ the legacy always-hit,
// non-critical roll, so the damage is deterministic:
// BaseATK = 20+(20/10)²+20/5 = 28; WeaponATK = Attack(50)+Attack2(10) = 60;
// total 88 against a Vit-0 player = 88 damage.
const mobAIFixture = `Header:
  Type: MOB_DB
  Version: 5
Body:
  - Id: 8001
    Name: AggroMob
    Ai: 04
    Level: 10
    Attack: 50
    Attack2: 10
    Str: 20
    Dex: 20
    Defense: 0
    Vit: 0
    AttackRange: 2
    ChaseRange: 12
  - Id: 8002
    Name: PassiveMob
    Ai: 01
    Level: 10
    Attack: 50
    Attack2: 10
    Str: 20
    Dex: 20
    Defense: 0
    Vit: 0
    AttackRange: 2
    ChaseRange: 12
  - Id: 8003
    Name: ChaserMob
    Ai: 04
    Level: 10
    Attack: 50
    Attack2: 10
    Str: 20
    Dex: 20
    Defense: 0
    Vit: 0
    AttackRange: 1
    ChaseRange: 12
    WalkSpeed: 400
`

// newMobAIWorld wires a world, spawn, combat (nil Dice ⇒ deterministic hit), and
// mob AI service over the mobAIFixture registry.
func newMobAIWorld(t *testing.T) (*app.WorldService, *app.SpawnService, *app.CombatService, *app.MobAIService) {
	t.Helper()
	mobs, err := mobdb.Load(strings.NewReader(mobAIFixture))
	require.NoError(t, err)
	world := app.NewWorldService(infra.NewMemoryWorldRepository(), slog.Default(), 50)
	spawn := app.NewSpawnService(world, mobs, nil)
	combat := app.NewCombatService(world, mobs, nil)
	mobAI := app.NewMobAIService(world, mobs, combat, slog.Default())
	return world, spawn, combat, mobAI
}

// spawnPlayer places a PC with the given HP at pos on "prontera".
func spawnPlayer(t *testing.T, world *app.WorldService, id domain.EntityID, hp int32, pos domain.Position) {
	t.Helper()
	require.NoError(t, world.AddEntity(domain.Entity{
		ID: id, Type: domain.EntityTypePC, Map: "prontera", Pos: pos, HP: hp, MaxHP: hp,
	}))
}

// playerHP is a terse read+require-no-error for assertions.
func playerHP(t *testing.T, world *app.WorldService, id domain.EntityID) int32 {
	t.Helper()
	e, err := world.Get(id)
	require.NoError(t, err)
	return e.HP
}

// TestMobAI_AggressiveAttacksPlayerInRange proves an aggressive mob (Ai=4) within
// its AttackRange of a player swings at the cadence boundary and the player's HP
// drops. mobAttackInterval (2s) elapsed ⇒ one deterministic 88-damage hit.
func TestMobAI_AggressiveAttacksPlayerInRange(t *testing.T) {
	t.Parallel()
	world, spawn, _, mobAI := newMobAIWorld(t)

	require.NoError(t, spawn.SpawnMob(2, 8001, "prontera", domain.Position{X: 50, Y: 50}, "AggroMob", 1000, 1000, 0))
	spawnPlayer(t, world, 1, 100, domain.Position{X: 51, Y: 50}) // Chebyshev dist 1 ≤ AttackRange

	mobAI.MonsterTick(context.Background(), 2*time.Second)

	assert.Equal(t, int32(12), playerHP(t, world, 1), "100 − 88 deterministic damage")
}

// TestMobAI_OnMobAttackHook verifies MonsterTick surfaces every landed mob hit
// through the OnMobAttack notify sink — the hook the gateway wires to emit
// ZC_NOTIFY_ACT. A nil hook is the headless default (the HP-drop tests above run
// without one); here a capturing closure records the one deterministic swing so
// the mobID/targetID/damage/died payload is asserted at the world layer,
// independent of the gateway packet path. MonsterTick runs to completion before
// the assertions, so the capture is race-free under -race.
func TestMobAI_OnMobAttackHook(t *testing.T) {
	t.Parallel()
	world, spawn, _, mobAI := newMobAIWorld(t)

	require.NoError(t, spawn.SpawnMob(2, 8001, "prontera", domain.Position{X: 50, Y: 50}, "AggroMob", 1000, 1000, 0))
	spawnPlayer(t, world, 1, 100, domain.Position{X: 51, Y: 50}) // Chebyshev dist 1 ≤ AttackRange

	var got struct {
		mob, target domain.EntityID
		dmg         int32
		died, fired bool
	}
	mobAI.OnMobAttack = func(mobID, targetID domain.EntityID, dmg int32, died bool) {
		got.mob, got.target, got.dmg, got.died, got.fired = mobID, targetID, dmg, died, true
	}

	mobAI.MonsterTick(context.Background(), 2*time.Second)

	assert.True(t, got.fired, "OnMobAttack fired for the landed swing")
	assert.Equal(t, domain.EntityID(2), got.mob, "hook mobID is the attacker")
	assert.Equal(t, domain.EntityID(1), got.target, "hook targetID is the player")
	assert.Equal(t, int32(88), got.dmg, "deterministic 88 damage")
	assert.False(t, got.died, "player survives (100 − 88 = 12 HP)")
}

// TestMobAI_PlayerOutOfChaseRangeIdle proves a player beyond ChaseRange provokes
// no target and no damage: the mob stays idle (no attack, no accumulation).
func TestMobAI_PlayerOutOfChaseRangeIdle(t *testing.T) {
	t.Parallel()
	world, spawn, _, mobAI := newMobAIWorld(t)

	require.NoError(t, spawn.SpawnMob(2, 8001, "prontera", domain.Position{X: 50, Y: 50}, "AggroMob", 1000, 1000, 0))
	spawnPlayer(t, world, 1, 100, domain.Position{X: 70, Y: 50}) // Chebyshev dist 20 > ChaseRange 12

	// Two cadence intervals: if the mob erroneously accumulated, it would swing
	// on the second tick — assert it never does.
	mobAI.MonsterTick(context.Background(), 2*time.Second)
	mobAI.MonsterTick(context.Background(), 2*time.Second)

	assert.Equal(t, int32(100), playerHP(t, world, 1), "out-of-range player must take no damage")
}

// TestMobAI_PlayerKilledTargetDropped proves a kill drops the mob's target and
// the mob re-acquires the next nearest living player. Player1 (dist 1, HP=88) is
// the nearest target and dies on the first swing; the next cadence interval the
// mob re-targets player2 (dist 2) and damages it. This closes the kill→retarget
// loop and proves the per-mob target does not linger on a corpse.
func TestMobAI_PlayerKilledTargetDropped(t *testing.T) {
	t.Parallel()
	world, spawn, _, mobAI := newMobAIWorld(t)

	require.NoError(t, spawn.SpawnMob(2, 8001, "prontera", domain.Position{X: 50, Y: 50}, "AggroMob", 1000, 1000, 0))
	spawnPlayer(t, world, 1, 88, domain.Position{X: 51, Y: 50})  // nearest, dies to one 88 hit
	spawnPlayer(t, world, 3, 200, domain.Position{X: 52, Y: 50}) // dist 2, survives

	// First cadence: mob targets player1 (nearest) and kills it (88 HP → 0).
	mobAI.MonsterTick(context.Background(), 2*time.Second)
	assert.Equal(t, int32(0), playerHP(t, world, 1), "player1 must be killed by the first hit")

	// Second cadence: player1 is dead (HP≤0, excluded); mob re-targets player2.
	mobAI.MonsterTick(context.Background(), 2*time.Second)
	assert.Less(t, playerHP(t, world, 3), int32(200), "mob must re-target player2 after the kill")
}

// TestMobAI_PassiveMobIdle proves a passive mob (Ai=1) never acquires a target
// or swings, even with an adjacent player: passive mobs stay training dummies.
func TestMobAI_PassiveMobIdle(t *testing.T) {
	t.Parallel()
	world, spawn, _, mobAI := newMobAIWorld(t)

	require.NoError(t, spawn.SpawnMob(2, 8002, "prontera", domain.Position{X: 50, Y: 50}, "PassiveMob", 1000, 1000, 0))
	spawnPlayer(t, world, 1, 100, domain.Position{X: 51, Y: 50}) // adjacent but mob is passive

	mobAI.MonsterTick(context.Background(), 2*time.Second)
	mobAI.MonsterTick(context.Background(), 2*time.Second)

	assert.Equal(t, int32(100), playerHP(t, world, 1), "passive mob must never attack")
}

// TestMobAI_CadenceFiresMultipleTicks proves the attack accumulator resets after
// each swing so a stationary in-range player is hit on every cadence interval:
// two intervals ⇒ two hits (200 → 112 → 24), not one.
func TestMobAI_CadenceFiresMultipleTicks(t *testing.T) {
	t.Parallel()
	world, spawn, _, mobAI := newMobAIWorld(t)

	require.NoError(t, spawn.SpawnMob(2, 8001, "prontera", domain.Position{X: 50, Y: 50}, "AggroMob", 1000, 1000, 0))
	spawnPlayer(t, world, 1, 200, domain.Position{X: 51, Y: 50})

	mobAI.MonsterTick(context.Background(), 2*time.Second)
	assert.Equal(t, int32(112), playerHP(t, world, 1), "first hit: 200 − 88")

	// Accumulator reset to 0 by the first swing, so the second full interval
	// fires a second independent hit.
	mobAI.MonsterTick(context.Background(), 2*time.Second)
	assert.Equal(t, int32(24), playerHP(t, world, 1), "second hit: 112 − 88")
}

// TestMobAI_TargetHeldOutOfAttackRange proves a mob keeps (holds) a target that
// steps out of AttackRange but stays within ChaseRange: no swing that tick, and
// the target is retained so stepping back in resumes swinging on the cadence.
func TestMobAI_TargetHeldOutOfAttackRange(t *testing.T) {
	t.Parallel()
	world, spawn, _, mobAI := newMobAIWorld(t)

	require.NoError(t, spawn.SpawnMob(2, 8001, "prontera", domain.Position{X: 50, Y: 50}, "AggroMob", 1000, 1000, 0))
	spawnPlayer(t, world, 1, 200, domain.Position{X: 53, Y: 50}) // dist 3 > AttackRange 2, ≤ ChaseRange 12

	// Out of attack range: target acquired (within ChaseRange) but no swing.
	mobAI.MonsterTick(context.Background(), 2*time.Second)
	assert.Equal(t, int32(200), playerHP(t, world, 1), "out of AttackRange: no swing")

	// Step back into AttackRange: the held target is swung at on the next cadence.
	require.NoError(t, world.MoveEntity(1, domain.Position{X: 51, Y: 50}))
	mobAI.MonsterTick(context.Background(), 2*time.Second)
	assert.Equal(t, int32(112), playerHP(t, world, 1), "back in AttackRange: held target swings")
}

// mobMove captures a single OnMobMove fire for hook assertions.
type mobMove struct {
	from, to domain.Position
	speed    int16
}

// spawnChaser places the chaser mob (8003: AttackRange=1, ChaseRange=12,
// WalkSpeed=400ms/cell) at pos on "prontera" with full HP.
func spawnChaser(t *testing.T, spawn *app.SpawnService, id domain.EntityID, pos domain.Position) {
	t.Helper()
	require.NoError(t, spawn.SpawnMob(id, 8003, "prontera", pos, "ChaserMob", 1000, 1000, 0))
}

// assertMobPos reads a mob's position and asserts equality.
func assertMobPos(t *testing.T, world *app.WorldService, id domain.EntityID, want domain.Position) {
	t.Helper()
	e, err := world.Get(id)
	require.NoError(t, err)
	assert.Equal(t, want, e.Pos)
}

// chebDist returns the Chebyshev (max-of-axes) cell distance — the same metric
// the mob AI uses for range, replicated here because the app-package helper is
// unexported.
func chebDist(a, b domain.Position) int {
	dx := a.X - b.X
	if dx < 0 {
		dx = -dx
	}
	dy := a.Y - b.Y
	if dy < 0 {
		dy = -dy
	}
	if dx > dy {
		return int(dx)
	}
	return int(dy)
}

// TestMobAI_ChaseApproaches proves an aggressive mob with a target inside
// ChaseRange but outside AttackRange pursues: one greedy step per WalkSpeed
// (400ms) tick closes the gap, and the mob swings once it reaches AttackRange.
// Chaser mob: AttackRange=1, WalkSpeed=400ms/cell. Player at (53,50) is Chebyshev
// dist 3 from a mob at (50,50): two 400ms steps close 3→2→1, then a 2s attack
// cadence swing deals the deterministic 88 damage.
func TestMobAI_ChaseApproaches(t *testing.T) {
	t.Parallel()
	world, spawn, _, mobAI := newMobAIWorld(t)
	const walkStep = 400 * time.Millisecond

	spawnChaser(t, spawn, 2, domain.Position{X: 50, Y: 50})
	spawnPlayer(t, world, 1, 200, domain.Position{X: 53, Y: 50}) // dist 3 > AttackRange 1

	mobAI.MonsterTick(context.Background(), walkStep) // pursue: (50,50)→(51,50), dist 2
	assertMobPos(t, world, 2, domain.Position{X: 51, Y: 50})
	mobAI.MonsterTick(context.Background(), walkStep) // pursue: (51,50)→(52,50), dist 1
	assertMobPos(t, world, 2, domain.Position{X: 52, Y: 50})

	// Now dist 1 ≤ AttackRange 1: in range, no further move; the 400ms tick banks
	// attack time but not enough to swing.
	mobAI.MonsterTick(context.Background(), walkStep)
	assertMobPos(t, world, 2, domain.Position{X: 52, Y: 50})
	assert.Equal(t, int32(200), playerHP(t, world, 1), "not yet due: no swing")

	// Attack cadence elapses: the mob that closed to range swings.
	mobAI.MonsterTick(context.Background(), 2*time.Second)
	assert.Equal(t, int32(112), playerHP(t, world, 1), "closed to range then swung: 200 − 88")
	assertMobPos(t, world, 2, domain.Position{X: 52, Y: 50})
}

// TestMobAI_ChaseStopShort proves a pursuing mob never steps onto the player's
// cell: it halts at AttackRange (Chebyshev dist 1) and extra cadence ticks do not
// push it closer. Player at (53,50), mob at (50,50): the mob stops at (52,50) —
// dist 1 — never (53,50).
func TestMobAI_ChaseStopShort(t *testing.T) {
	t.Parallel()
	world, spawn, _, mobAI := newMobAIWorld(t)

	spawnChaser(t, spawn, 2, domain.Position{X: 50, Y: 50})
	spawnPlayer(t, world, 1, 500, domain.Position{X: 53, Y: 50})

	for i := 0; i < 10; i++ {
		mobAI.MonsterTick(context.Background(), 400*time.Millisecond)
	}

	mob, err := world.Get(2)
	require.NoError(t, err)
	assert.Equal(t, domain.Position{X: 52, Y: 50}, mob.Pos, "halts one cell short of the player")
	player, err := world.Get(1)
	require.NoError(t, err)
	assert.Equal(t, 1, chebDist(mob.Pos, player.Pos), "final gap == AttackRange, never 0")
	assert.NotEqual(t, mob.Pos, player.Pos, "mob never shares the player's cell")
}

// TestMobAI_ChaseCadence proves one chase step costs exactly WalkSpeed (400ms):
// sub-threshold ticks bank time without stepping; crossing the threshold fires
// exactly one step and resets the accumulator (no burst, no carry past the step).
func TestMobAI_ChaseCadence(t *testing.T) {
	t.Parallel()
	world, spawn, _, mobAI := newMobAIWorld(t)

	spawnChaser(t, spawn, 2, domain.Position{X: 50, Y: 50})
	spawnPlayer(t, world, 1, 500, domain.Position{X: 60, Y: 50}) // dist 10, room for several steps

	mobAI.MonsterTick(context.Background(), 399*time.Millisecond) // under WalkSpeed: no step
	assertMobPos(t, world, 2, domain.Position{X: 50, Y: 50})

	mobAI.MonsterTick(context.Background(), time.Millisecond) // +1ms crosses 400ms: one step, reset
	assertMobPos(t, world, 2, domain.Position{X: 51, Y: 50})

	mobAI.MonsterTick(context.Background(), 400*time.Millisecond) // exactly WalkSpeed from reset: one step
	assertMobPos(t, world, 2, domain.Position{X: 52, Y: 50})

	mobAI.MonsterTick(context.Background(), 399*time.Millisecond) // under again: no step
	assertMobPos(t, world, 2, domain.Position{X: 52, Y: 50})
}

// TestMobAI_ChaseDiagonal proves a mob offset on both axes steps diagonally
// (king-move) toward the target: (50,50)→(51,51) toward a player at (53,53).
func TestMobAI_ChaseDiagonal(t *testing.T) {
	t.Parallel()
	world, spawn, _, mobAI := newMobAIWorld(t)

	spawnChaser(t, spawn, 2, domain.Position{X: 50, Y: 50})
	spawnPlayer(t, world, 1, 500, domain.Position{X: 53, Y: 53}) // Chebyshev dist 3

	mobAI.MonsterTick(context.Background(), 400*time.Millisecond)
	assertMobPos(t, world, 2, domain.Position{X: 51, Y: 51})

	mobAI.MonsterTick(context.Background(), 400*time.Millisecond)
	assertMobPos(t, world, 2, domain.Position{X: 52, Y: 52}) // dist 1, stop-short next tick
}

// TestMobAI_ChaseDropOnFlee proves a chasing mob stops (and drops its target)
// when the player leaves ChaseRange mid-pursuit: no further step until a target
// re-enters range.
func TestMobAI_ChaseDropOnFlee(t *testing.T) {
	t.Parallel()
	world, spawn, _, mobAI := newMobAIWorld(t)

	spawnChaser(t, spawn, 2, domain.Position{X: 50, Y: 50})
	spawnPlayer(t, world, 1, 500, domain.Position{X: 58, Y: 50}) // dist 8 ≤ ChaseRange 12

	mobAI.MonsterTick(context.Background(), 400*time.Millisecond) // pursue: (50,50)→(51,50)
	assertMobPos(t, world, 2, domain.Position{X: 51, Y: 50})

	// Player flees far outside ChaseRange.
	require.NoError(t, world.MoveEntity(1, domain.Position{X: 90, Y: 50})) // dist 39 > ChaseRange 12

	mobAI.MonsterTick(context.Background(), 400*time.Millisecond) // target dropped: no move
	assertMobPos(t, world, 2, domain.Position{X: 51, Y: 50})
	mobAI.MonsterTick(context.Background(), 400*time.Millisecond) // still no target
	assertMobPos(t, world, 2, domain.Position{X: 51, Y: 50})
}

// TestMobAI_ChaseGridBounds proves a mob chasing a player at the grid corner
// never computes an out-of-bounds cell (the AOI grid is 512×512, valid [0,511]):
// it steps toward the corner, halts at AttackRange, and never panics.
func TestMobAI_ChaseGridBounds(t *testing.T) {
	t.Parallel()
	world, spawn, _, mobAI := newMobAIWorld(t)

	spawnChaser(t, spawn, 2, domain.Position{X: 509, Y: 509})
	spawnPlayer(t, world, 1, 500, domain.Position{X: 511, Y: 511}) // corner; Chebyshev dist 2

	for i := 0; i < 5; i++ {
		require.NotPanics(t, func() {
			mobAI.MonsterTick(context.Background(), 400*time.Millisecond)
		})
	}

	mob, err := world.Get(2)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, int(mob.Pos.X), 0)
	assert.LessOrEqual(t, int(mob.Pos.X), 511)
	assert.GreaterOrEqual(t, int(mob.Pos.Y), 0)
	assert.LessOrEqual(t, int(mob.Pos.Y), 511)
	assert.Equal(t, domain.Position{X: 510, Y: 510}, mob.Pos, "halts at AttackRange, never reaches the corner")
}

// TestMobAI_OnMobMoveHook verifies MonsterTick surfaces each chase step through
// the OnMobMove notify sink — the hook the gateway wires to emit ZC_UNIT_WALKING.
// A capturing closure records the from/to/speed payload at the world layer,
// independent of the gateway packet path. MonsterTick runs to completion before
// the assertions, so the capture is race-free under -race.
func TestMobAI_OnMobMoveHook(t *testing.T) {
	t.Parallel()
	world, spawn, _, mobAI := newMobAIWorld(t)

	spawnChaser(t, spawn, 2, domain.Position{X: 50, Y: 50})
	spawnPlayer(t, world, 1, 500, domain.Position{X: 53, Y: 50}) // dist 3

	var moves []mobMove
	mobAI.OnMobMove = func(_ domain.EntityID, from, to domain.Position, speed int16) {
		moves = append(moves, mobMove{from: from, to: to, speed: speed})
	}

	mobAI.MonsterTick(context.Background(), 400*time.Millisecond) // (50,50)→(51,50)
	mobAI.MonsterTick(context.Background(), 400*time.Millisecond) // (51,50)→(52,50)
	mobAI.MonsterTick(context.Background(), 400*time.Millisecond) // in range: no move

	require.Len(t, moves, 2, "one fire per chase step; none while in range")
	assert.Equal(t, domain.Position{X: 50, Y: 50}, moves[0].from)
	assert.Equal(t, domain.Position{X: 51, Y: 50}, moves[0].to)
	assert.Equal(t, domain.Position{X: 51, Y: 50}, moves[1].from)
	assert.Equal(t, domain.Position{X: 52, Y: 50}, moves[1].to)
	assert.Equal(t, int16(400), moves[0].speed, "speed is mob_db WalkSpeed (ms/cell)")
}
