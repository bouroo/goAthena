//go:build unit

package app_test

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/internal/modules/world/infra"
	"github.com/bouroo/goAthena/pkg/ro/mobdb"
	"github.com/bouroo/goAthena/pkg/ro/skilldb"
)

// skillFixture is a tiny in-memory skill_db: an enemy-targeted strike (per-level
// SpCost list) and a self-targeted buff (scalar SpCost). It mirrors the rAthena
// skill_db.yml v4 shape the loader parses.
const skillFixture = `
Header:
  Type: SKILL_DB
  Version: 4
Body:
  - Id: 1000
    Name: TestStrike
    MaxLevel: 5
    Type: Weapon
    TargetType: Enemy
    Hit: Single
    Element: Neutral
    Range: 2
    Requires:
      SpCost:
        - Level: 1
          Amount: 5
        - Level: 2
          Amount: 10
  - Id: 1001
    Name: TestBuff
    MaxLevel: 1
    Type: Misc
    TargetType: Self
    Element: Neutral
    Range: 9
    Requires:
      SpCost: 3
`

const (
	skillStrikeID int32 = 1000
	skillBuffID   int32 = 1001
)

// newSkillTestWorld loads the skill fixture and wires a WorldService +
// CombatService + SkillService the same way the DI container does.
func newSkillTestWorld(t *testing.T) (*app.WorldService, *app.SkillService) {
	t.Helper()
	skills, err := skilldb.Load(strings.NewReader(skillFixture))
	require.NoError(t, err)
	require.Equal(t, 2, skills.Len())
	world := app.NewWorldService(infra.NewMemoryWorldRepository(), slog.Default(), 50)
	combat := app.NewCombatService(world, mobdb.NewRegistry(), nil)
	return world, app.NewSkillService(world, combat, skills)
}

// addCaster places a level-1 PC (Str=50 -> 75 BaseATK against 0 DEF) with the
// given SP at (50,50). Mob ID 1 is reserved for the caster.
func addCaster(t *testing.T, world *app.WorldService, sp int32) {
	t.Helper()
	require.NoError(t, world.AddEntity(domain.Entity{
		ID: 1, Type: domain.EntityTypePC, Map: "prontera",
		Pos:   domain.Position{X: 50, Y: 50},
		Level: 1, Str: 50, SP: sp, MaxSP: 100,
	}))
}

// addTarget places a 0-DEF mob with the given HP at (x,y).
func addTarget(t *testing.T, world *app.WorldService, id domain.EntityID, hp int32, x, y int16) {
	t.Helper()
	require.NoError(t, world.AddEntity(domain.Entity{
		ID: id, Type: domain.EntityTypeMob, Class: 0, Map: "prontera",
		Pos: domain.Position{X: x, Y: y}, HP: hp, MaxHP: hp,
	}))
}

// TestSkill_ValidCast_DealsDamageAndCostsSP proves an enemy-targeted skill is
// castable, visible-by-damage, and SP-affordable: it deducts the level-1 SP cost
// (5) and reduces the target by exactly the combat hit it delegated to.
func TestSkill_ValidCast_DealsDamageAndCostsSP(t *testing.T) {
	t.Parallel()
	world, svc := newSkillTestWorld(t)
	addCaster(t, world, 100)
	addTarget(t, world, 2, 1000, 51, 50) // adjacent: Chebyshev 1 <= range 2

	dmg, died, err := svc.UseSkillOnTarget(1, skillStrikeID, 1, 2)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, dmg, int32(1), "skill resolves a real melee hit")
	assert.False(t, died, "a 1000-HP target does not die from one hit")

	caster, _ := world.Get(1)
	assert.Equal(t, int32(95), caster.SP, "level-1 cast costs 5 SP")
	target, _ := world.Get(2)
	assert.Equal(t, int32(1000)-dmg, target.HP, "HP drops by exactly the dealt damage")
}

// TestSkill_InsufficientSP proves a cast below the SP cost is denied and spends
// nothing: neither caster SP nor target HP changes.
func TestSkill_InsufficientSP(t *testing.T) {
	t.Parallel()
	world, svc := newSkillTestWorld(t)
	addCaster(t, world, 3)
	addTarget(t, world, 2, 1000, 51, 50)

	dmg, died, err := svc.UseSkillOnTarget(1, skillStrikeID, 1, 2)
	assert.ErrorIs(t, err, app.ErrInsufficientSP)
	assert.Equal(t, int32(0), dmg)
	assert.False(t, died)

	caster, _ := world.Get(1)
	assert.Equal(t, int32(3), caster.SP, "denied cast spends no SP")
	target, _ := world.Get(2)
	assert.Equal(t, int32(1000), target.HP, "denied cast deals no damage")
}

// TestSkill_OutOfRange proves a target beyond the skill range is denied before
// any SP is spent.
func TestSkill_OutOfRange(t *testing.T) {
	t.Parallel()
	world, svc := newSkillTestWorld(t)
	addCaster(t, world, 100)
	addTarget(t, world, 2, 1000, 55, 50) // Chebyshev 5 > range 2

	dmg, died, err := svc.UseSkillOnTarget(1, skillStrikeID, 1, 2)
	assert.ErrorIs(t, err, app.ErrSkillOutOfRange)
	assert.Equal(t, int32(0), dmg)
	assert.False(t, died)

	caster, _ := world.Get(1)
	assert.Equal(t, int32(100), caster.SP, "out-of-range cast spends no SP")
}

// TestSkill_InvalidLevel proves levels outside [1, MaxLevel] are rejected
// without spending SP.
func TestSkill_InvalidLevel(t *testing.T) {
	t.Parallel()
	world, svc := newSkillTestWorld(t)
	addCaster(t, world, 100)
	addTarget(t, world, 2, 1000, 51, 50)

	_, _, err := svc.UseSkillOnTarget(1, skillStrikeID, 0, 2)
	assert.ErrorIs(t, err, app.ErrInvalidLevel)

	_, _, err = svc.UseSkillOnTarget(1, skillStrikeID, 6, 2) // MaxLevel is 5
	assert.ErrorIs(t, err, app.ErrInvalidLevel)

	caster, _ := world.Get(1)
	assert.Equal(t, int32(100), caster.SP, "invalid-level cast spends no SP")
}

// TestSkill_UnknownSkill proves a skill ID absent from the registry is rejected.
func TestSkill_UnknownSkill(t *testing.T) {
	t.Parallel()
	world, svc := newSkillTestWorld(t)
	addCaster(t, world, 100)
	addTarget(t, world, 2, 1000, 51, 50)

	_, _, err := svc.UseSkillOnTarget(1, 9999, 1, 2)
	assert.ErrorIs(t, err, app.ErrUnknownSkill)
}

// TestSkill_SpCostHighestLevelFallback covers SpCost.At's fallback path: casting
// at a valid level (3 <= MaxLevel 5) above the highest listed SpCost level (2)
// charges the level-2 amount (10), not 0.
func TestSkill_SpCostHighestLevelFallback(t *testing.T) {
	t.Parallel()
	world, svc := newSkillTestWorld(t)
	addCaster(t, world, 100)
	addTarget(t, world, 2, 1000, 51, 50)

	dmg, died, err := svc.UseSkillOnTarget(1, skillStrikeID, 3, 2)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, dmg, int32(1))
	assert.False(t, died)

	caster, _ := world.Get(1)
	assert.Equal(t, int32(90), caster.SP, "level 3 falls back to the level-2 cost (10)")
}

// TestSkill_KillTarget proves the death contract mirrors CombatService.Attack:
// a hit that brings HP to 0 reports died=true and clamps HP at 0.
func TestSkill_KillTarget(t *testing.T) {
	t.Parallel()
	world, svc := newSkillTestWorld(t)
	addCaster(t, world, 100)
	addTarget(t, world, 2, 1, 51, 50)

	dmg, died, err := svc.UseSkillOnTarget(1, skillStrikeID, 1, 2)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, dmg, int32(1))
	assert.True(t, died, "a 1-HP target dies from a real hit")

	target, _ := world.Get(2)
	assert.Equal(t, int32(0), target.HP, "HP clamps at 0 on death")
}

// TestSkill_NonOffensiveNoDamage documents the buff/heal stub: a self-targeted
// (non-Enemy) skill spends SP but applies no damage until its stat effect lands.
func TestSkill_NonOffensiveNoDamage(t *testing.T) {
	t.Parallel()
	world, svc := newSkillTestWorld(t)
	addCaster(t, world, 100)
	// Cast the self-buff onto the caster itself (range 9 covers distance 0).
	dmg, died, err := svc.UseSkillOnTarget(1, skillBuffID, 1, 1)
	require.NoError(t, err)
	assert.Equal(t, int32(0), dmg, "buff deals no damage")
	assert.False(t, died)

	caster, _ := world.Get(1)
	assert.Equal(t, int32(97), caster.SP, "buff still costs its SP")
}
