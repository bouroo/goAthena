//go:build unit

package app_test

import (
	"context"
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
	"github.com/bouroo/goAthena/pkg/ro/skilltree"
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
// given SP at (50,50). Mob ID 1 is reserved for the caster. The caster starts
// with both test skills (1000, 1001) pre-learned at max level so existing tests
// continue to pass; the learned-skill-gate tests manage their own LearnSkill calls.
func addCaster(t *testing.T, world *app.WorldService, sp int32) {
	t.Helper()
	require.NoError(t, world.AddEntity(domain.Entity{
		ID: 1, Type: domain.EntityTypePC, Map: "prontera",
		Pos:   domain.Position{X: 50, Y: 50},
		Level: 1, Str: 50, SP: sp, MaxSP: 100,
		LearnedSkills: map[int32]int16{skillStrikeID: 5, skillBuffID: 1},
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

// TestSkill_LearnedSkill_CastsSuccessfully proves that once a skill is learned
// (via LearnSkill) it casts successfully and costs SP.
func TestSkill_LearnedSkill_CastsSuccessfully(t *testing.T) {
	t.Parallel()
	world, svc := newSkillTestWorld(t)
	addCaster(t, world, 100)

	// Learn skill 1000 at level 1.
	require.NoError(t, world.LearnSkill(1, skillStrikeID, 1))

	addTarget(t, world, 2, 1000, 51, 50)
	dmg, died, err := svc.UseSkillOnTarget(1, skillStrikeID, 1, 2)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, dmg, int32(1))
	assert.False(t, died)

	caster, _ := world.Get(1)
	assert.Equal(t, int32(95), caster.SP, "learned skill cast costs SP")
}

// TestSkill_NotLearned_ErrSkillNotLearned proves that a caster without the skill
// in LearnedSkills gets ErrSkillNotLearned and spends nothing. Uses a bare caster
// (no LearnedSkills) so it is isolated from addCaster's pre-learned skills.
func TestSkill_NotLearned_ErrSkillNotLearned(t *testing.T) {
	t.Parallel()
	world, svc := newSkillTestWorld(t)
	// Bare caster without LearnedSkills (no addCaster pre-learn).
	require.NoError(t, world.AddEntity(domain.Entity{
		ID: 1, Type: domain.EntityTypePC, Map: "prontera",
		Pos: domain.Position{X: 50, Y: 50}, Level: 1, Str: 50, SP: 100, MaxSP: 100,
	}))
	addTarget(t, world, 2, 1000, 51, 50)

	_, _, err := svc.UseSkillOnTarget(1, skillStrikeID, 1, 2)
	assert.ErrorIs(t, err, domain.ErrSkillNotLearned)

	caster, _ := world.Get(1)
	assert.Equal(t, int32(100), caster.SP, "unlearned cast spends no SP")
}

// TestSkill_LearnedLevelTooLow_ErrSkillLevelInsufficient proves that a caster who
// learned a skill at level 1 cannot cast it at level 3 (above their learned level).
// Uses a bare caster so it is isolated from addCaster's pre-learned level 5.
func TestSkill_LearnedLevelTooLow_ErrSkillLevelInsufficient(t *testing.T) {
	t.Parallel()
	world, svc := newSkillTestWorld(t)
	require.NoError(t, world.AddEntity(domain.Entity{
		ID: 1, Type: domain.EntityTypePC, Map: "prontera",
		Pos: domain.Position{X: 50, Y: 50}, Level: 1, Str: 50, SP: 100, MaxSP: 100,
	}))
	require.NoError(t, world.LearnSkill(1, skillStrikeID, 1)) // learn level 1

	addTarget(t, world, 2, 1000, 51, 50)
	_, _, err := svc.UseSkillOnTarget(1, skillStrikeID, 3, 2) // try level 3
	assert.ErrorIs(t, err, domain.ErrSkillLevelInsufficient)

	caster, _ := world.Get(1)
	assert.Equal(t, int32(100), caster.SP, "insufficient-level cast spends no SP")
}

// TestSkill_LearnSkill_RaisesInPlace proves that calling LearnSkill with a higher
// level raises the learned level in-place; casting at the new level then succeeds.
// Uses a bare caster so it is isolated from addCaster's pre-learned level 5.
func TestSkill_LearnSkill_RaisesInPlace(t *testing.T) {
	t.Parallel()
	world, svc := newSkillTestWorld(t)
	require.NoError(t, world.AddEntity(domain.Entity{
		ID: 1, Type: domain.EntityTypePC, Map: "prontera",
		Pos: domain.Position{X: 50, Y: 50}, Level: 1, Str: 50, SP: 100, MaxSP: 100,
	}))
	addTarget(t, world, 2, 1000, 51, 50)

	// Learn level 1, then fail to cast at level 2.
	require.NoError(t, world.LearnSkill(1, skillStrikeID, 1))
	_, _, err := svc.UseSkillOnTarget(1, skillStrikeID, 2, 2)
	assert.ErrorIs(t, err, domain.ErrSkillLevelInsufficient)

	// Raise the learned level to 2.
	require.NoError(t, world.LearnSkill(1, skillStrikeID, 2))
	dmg, died, err := svc.UseSkillOnTarget(1, skillStrikeID, 2, 2)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, dmg, int32(1))
	assert.False(t, died)
}

// TestLearnSkill_HappyPath verifies that spending a skill point levels up
// NV_FIRSTAID from 0 → 1 and decrements SkillPoint.
func TestLearnSkill_HappyPath(t *testing.T) {
	t.Parallel()
	// Seed repo with entity that has SkillPoint=3; EnterMap loads it into world.
	repo := infra.NewMemoryWorldRepository(domain.Entity{
		ID: 300001, Account: 1, Type: domain.EntityTypePC, Job: 0, Map: "test_map",
		Pos: domain.Position{X: 1, Y: 1}, Name: "test", Level: 1, JobLevel: 1,
		Speed: 150, HP: 100, MaxHP: 100, SP: 100, MaxSP: 100, SkillPoint: 3,
	})
	w := app.NewWorldService(repo, slog.Default(), 50)
	_, _ = w.EnterMap(context.Background(), 300001)

	combat := app.NewCombatService(w, nil, nil)
	skills := skilldb.NewRegistry()
	const skillID = int32(3)
	skills.Register(&skilldb.SkillEntry{
		ID:       skillID,
		Name:     "NV_FIRSTAID",
		Requires: skilldb.Requires{SpCost: skilldb.SpCost{IsScalar: true, Value: 6}},
		Range:    skilldb.Range{IsScalar: true, Value: 3},
	})

	treeYAML := `Header:
  Type: SKILL_TREE_DB
  Version: 1
Body:
  - Job: Novice
    Tree:
      - Name: NV_FIRSTAID
        MaxLevel: 1
`
	tree, err := skilltree.Load(strings.NewReader(treeYAML))
	require.NoError(t, err)
	svc := app.NewSkillService(w, combat, skills)
	svc.SetTree(tree)

	newLevel, spCost, rng, upgradable, err := svc.LearnSkill(context.Background(), 300001, skillID)
	require.NoError(t, err, "LearnSkill should succeed")
	assert.Equal(t, int16(1), newLevel)
	assert.Equal(t, int32(6), spCost)
	assert.Equal(t, int16(3), rng)
	assert.False(t, upgradable, "NV_FIRSTAID MaxLevel=1, should not be upgradable")

	// Second call at max level returns ErrSkillMaxLevel.
	_, _, _, _, err = svc.LearnSkill(context.Background(), 300001, skillID)
	assert.ErrorIs(t, err, app.ErrSkillMaxLevel)
}

// TestLearnSkill_NoPoints verifies that with 0 skill points LearnSkill returns
// ErrNoSkillPoints and the entity is untouched.
func TestLearnSkill_NoPoints(t *testing.T) {
	t.Parallel()
	repo := infra.NewMemoryWorldRepository(domain.Entity{
		ID: 300002, Account: 1, Type: domain.EntityTypePC, Job: 0, Map: "test_map",
		Pos: domain.Position{X: 1, Y: 1}, Name: "test", Level: 1, JobLevel: 1,
		Speed: 150, HP: 100, MaxHP: 100, SP: 100, MaxSP: 100, SkillPoint: 0,
	})
	w := app.NewWorldService(repo, slog.Default(), 50)
	_, _ = w.EnterMap(context.Background(), 300002)

	combat := app.NewCombatService(w, nil, nil)
	skills := skilldb.NewRegistry()
	const skillID = int32(3)
	skills.Register(&skilldb.SkillEntry{ID: skillID, Name: "NV_FIRSTAID"})

	treeYAML := `Header:
  Type: SKILL_TREE_DB
  Version: 1
Body:
  - Job: Novice
    Tree:
      - Name: NV_FIRSTAID
        MaxLevel: 1
`
	tree, err := skilltree.Load(strings.NewReader(treeYAML))
	require.NoError(t, err)
	svc := app.NewSkillService(w, combat, skills)
	svc.SetTree(tree)

	_, _, _, _, err = svc.LearnSkill(context.Background(), 300002, skillID)
	assert.ErrorIs(t, err, app.ErrNoSkillPoints)
}

// TestLearnSkill_NotInTree verifies that a skill not in the character's job tree
// returns ErrSkillNotInTree.
func TestLearnSkill_NotInTree(t *testing.T) {
	t.Parallel()
	repo := infra.NewMemoryWorldRepository(domain.Entity{
		ID: 300003, Account: 1, Type: domain.EntityTypePC, Job: 0, Map: "test_map",
		Pos: domain.Position{X: 1, Y: 1}, Name: "test", Level: 1, JobLevel: 1,
		Speed: 150, HP: 100, MaxHP: 100, SP: 100, MaxSP: 100, SkillPoint: 10,
	})
	w := app.NewWorldService(repo, slog.Default(), 50)
	_, _ = w.EnterMap(context.Background(), 300003)

	combat := app.NewCombatService(w, nil, nil)
	skills := skilldb.NewRegistry()
	const skillID = int32(100)
	skills.Register(&skilldb.SkillEntry{ID: skillID, Name: "SM_BASH"})

	treeYAML := `Header:
  Type: SKILL_TREE_DB
  Version: 1
Body:
  - Job: Novice
    Tree: []
`
	tree, err := skilltree.Load(strings.NewReader(treeYAML))
	require.NoError(t, err)
	svc := app.NewSkillService(w, combat, skills)
	svc.SetTree(tree)

	_, _, _, _, err = svc.LearnSkill(context.Background(), 300003, skillID)
	assert.ErrorIs(t, err, app.ErrSkillNotInTree)
}

// TestLearnSkill_UnknownSkill verifies that an unknown skill ID returns
// ErrUnknownSkill.
func TestLearnSkill_UnknownSkill(t *testing.T) {
	t.Parallel()
	repo := infra.NewMemoryWorldRepository(domain.Entity{
		ID: 300004, Account: 1, Type: domain.EntityTypePC, Job: 0, Map: "test_map",
		Pos: domain.Position{X: 1, Y: 1}, Name: "test", Level: 1, JobLevel: 1,
		Speed: 150, HP: 100, MaxHP: 100, SP: 100, MaxSP: 100, SkillPoint: 10,
	})
	w := app.NewWorldService(repo, slog.Default(), 50)
	_, _ = w.EnterMap(context.Background(), 300004)

	combat := app.NewCombatService(w, nil, nil)
	skills := skilldb.NewRegistry()
	treeYAML := `Header:
  Type: SKILL_TREE_DB
  Version: 1
Body:
  - Job: Novice
    Tree: []
`
	tree, err := skilltree.Load(strings.NewReader(treeYAML))
	require.NoError(t, err)
	svc := app.NewSkillService(w, combat, skills)
	svc.SetTree(tree)

	_, _, _, _, err = svc.LearnSkill(context.Background(), 300004, 99999)
	assert.ErrorIs(t, err, app.ErrUnknownSkill)
}

// TestLearnSkill_LevelUp verifies multi-level progression: level 5→6 for a
// skill with MaxLevel=10, capped by JobLevel=10, returns spCost/range at level 6.
// Uses Novice (Job=0) with a custom skill — non-Novice job name lookup is out of scope.
func TestLearnSkill_LevelUp(t *testing.T) {
	t.Parallel()
	// Seed repo with Novice (Job=0) that has skill ID=5 at level 5.
	repo := infra.NewMemoryWorldRepository(domain.Entity{
		ID: 300005, Account: 1, Type: domain.EntityTypePC, Job: 0, Map: "test_map",
		Pos: domain.Position{X: 1, Y: 1}, Name: "test", Level: 1, JobLevel: 10,
		Speed: 150, HP: 100, MaxHP: 1000, SP: 100, MaxSP: 200,
		SkillPoint: 5, LearnedSkills: map[int32]int16{5: 5},
	})
	w := app.NewWorldService(repo, slog.Default(), 50)
	_, _ = w.EnterMap(context.Background(), 300005)

	combat := app.NewCombatService(w, nil, nil)
	skills := skilldb.NewRegistry()
	const skillID = int32(5)
	skills.Register(&skilldb.SkillEntry{
		ID:       skillID,
		Name:     "NV_BASIC",
		Requires: skilldb.Requires{SpCost: skilldb.SpCost{IsScalar: true, Value: 1}},
		Range:    skilldb.Range{IsScalar: true, Value: 10},
	})

	treeYAML := `Header:
  Type: SKILL_TREE_DB
  Version: 1
Body:
  - Job: Novice
    Tree:
      - Name: NV_BASIC
        MaxLevel: 9
      - Name: NV_FIRSTAID
        MaxLevel: 1
`
	tree, err := skilltree.Load(strings.NewReader(treeYAML))
	require.NoError(t, err)
	svc := app.NewSkillService(w, combat, skills)
	svc.SetTree(tree)

	newLevel, spCost, rng, upgradable, err := svc.LearnSkill(context.Background(), 300005, skillID)
	require.NoError(t, err)
	assert.Equal(t, int16(6), newLevel)
	assert.Equal(t, int32(1), spCost)
	assert.Equal(t, int16(10), rng)
	assert.True(t, upgradable, "level 6 < max 9, should be upgradable")
}

// TestLearnSkill_LockedByRequirements proves the rAthena pc_calc_skilltree
// unlock gate: a tree entry whose JobLevel threshold exceeds the character's
// job level is not learnable (ErrSkillLocked), and neither is a skill whose
// prerequisite is unlearned.
func TestLearnSkill_LockedByRequirements(t *testing.T) {
	t.Parallel()
	repo := infra.NewMemoryWorldRepository(domain.Entity{
		ID: 300006, Account: 1, Type: domain.EntityTypePC, Job: 0, Map: "test_map",
		Pos: domain.Position{X: 1, Y: 1}, Name: "test", Level: 1, JobLevel: 1,
		Speed: 150, HP: 100, MaxHP: 100, SP: 100, MaxSP: 100, SkillPoint: 5,
	})
	w := app.NewWorldService(repo, slog.Default(), 50)
	_, _ = w.EnterMap(context.Background(), 300006)

	combat := app.NewCombatService(w, nil, nil)
	skills := skilldb.NewRegistry()
	skills.Register(&skilldb.SkillEntry{ID: 5, Name: "NV_BASIC"})
	skills.Register(&skilldb.SkillEntry{ID: 8, Name: "NV_FIRSTAID"})
	treeYAML := `Header:
  Type: SKILL_TREE_DB
  Version: 1
Body:
  - Job: Novice
    Tree:
      - Name: NV_BASIC
        MaxLevel: 9
      - Name: NV_FIRSTAID
        MaxLevel: 1
        JobLevel: 4
        Requires:
          - Name: NV_BASIC
            Level: 5
`
	tree, err := skilltree.Load(strings.NewReader(treeYAML))
	require.NoError(t, err)
	svc := app.NewSkillService(w, combat, skills)
	svc.SetTree(tree)

	// NV_FIRSTAID: JobLevel 1 < 4 and NV_BASIC (req level 5) unlearned.
	_, _, _, _, err = svc.LearnSkill(context.Background(), 300006, 8)
	assert.ErrorIs(t, err, app.ErrSkillLocked)

	// NV_BASIC itself is unlocked (no thresholds, no prerequisites).
	_, _, _, _, err = svc.LearnSkill(context.Background(), 300006, 5)
	require.NoError(t, err)
}
