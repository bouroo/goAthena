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
)

// mobFixture seeds three mobs spanning the DEF axis: a pure zero-DEF target, a
// Poring (def0/vit1), and a tough target (def50/vit20). A fixed-ATK hit must
// deal strictly less to the tougher targets, proving mob_db DEF feeds the
// pre-renewal formula through the CombatService.
const mobFixture = `Header:
  Type: MOB_DB
  Version: 5

Body:
  - Id: 9001
    Name: ZeroDef
    Defense: 0
    Vit: 0
  - Id: 1002
    AegisName: PORING
    Name: Poring
    Defense: 0
    Vit: 1
  - Id: 9003
    Name: Tough
    Defense: 50
    Vit: 20
`

// TestCombat_MobDEFReducesDamage proves the CombatService resolves a mob
// defender's DEF/stats from mob_db and feeds them to the pre-renewal formula.
//
// Attacker: level-1 PC, Str=50, Dex/Luk=0. Pre-re BaseATK = 50 + (50/10)² = 75.
// Damage = 75·(100−DEF)/100 − Vit, then floored at 1 (battle_min_damage):
//
//	ZeroDef (def0/vit0):   75·100/100 − 0  = 75
//	Poring  (def0/vit1):   75·100/100 − 1  = 74
//	Tough   (def50/vit20): 75·50/100  − 20 = 37 − 20 = 17
//
// 17 < 74 < 75 confirms HardDEF (Defense) and Vit softDEF both reduce damage.
func TestCombat_MobDEFReducesDamage(t *testing.T) {
	t.Parallel()
	mobs, err := mobdb.Load(strings.NewReader(mobFixture))
	require.NoError(t, err)
	require.Equal(t, 3, mobs.Len())

	world := app.NewWorldService(infra.NewMemoryWorldRepository(), slog.Default(), 50)
	spawn := app.NewSpawnService(world, mobs, nil)
	combat := app.NewCombatService(world, mobs)

	// Attacker PC: only Str is set, so BaseATK is exactly 75.
	const attackerID = domain.EntityID(1)
	require.NoError(t, world.AddEntity(domain.Entity{
		ID: attackerID, Type: domain.EntityTypePC, Level: 1, Str: 50, Map: "prontera",
	}))

	spawnMob := func(gid domain.EntityID, class int32, name string) {
		require.NoError(t, spawn.SpawnMob(gid, class, "prontera", domain.Position{X: 50, Y: 50}, name, 1000, 1000, 0))
	}
	spawnMob(2, 9001, "ZeroDef")
	spawnMob(3, 1002, "Poring")
	spawnMob(4, 9003, "Tough")

	cases := []struct {
		name    string
		defID   domain.EntityID
		wantDmg int32
	}{
		{"zero DEF / zero Vit", 2, 75},
		{"Poring (def0/vit1)", 3, 74},
		{"Tough (def50/vit20)", 4, 17},
	}
	for _, tc := range cases {
		dmg, _, err := combat.Attack(attackerID, tc.defID)
		require.NoError(t, err)
		assert.Equal(t, tc.wantDmg, dmg, "%s: damage with mob_db DEF", tc.name)
	}
}

// TestCombat_NilMobDB_ResolvesZeroDEF guards the graceful path: a nil mob_db
// registry (no data file) must not panic and resolves 0 DEF, so a mob hit still
// floors at 1 against a zero-ATK attacker rather than crashing.
func TestCombat_NilMobDB_ResolvesZeroDEF(t *testing.T) {
	t.Parallel()
	world := app.NewWorldService(infra.NewMemoryWorldRepository(), slog.Default(), 50)
	combat := app.NewCombatService(world, nil) // no mob_db loaded

	require.NoError(t, world.AddEntity(domain.Entity{ID: 1, Type: domain.EntityTypePC, Map: "prontera"}))
	require.NoError(t, world.AddEntity(domain.Entity{
		ID: 2, Type: domain.EntityTypeMob, Class: 1002, Map: "prontera", HP: 50, MaxHP: 50,
	}))

	dmg, died, err := combat.Attack(1, 2)
	require.NoError(t, err)
	assert.Equal(t, int32(1), dmg, "zero-ATK hit against 0-DEF mob floors at 1")
	assert.False(t, died, "a 50-HP mob hit for 1 does not die")

	// applyDamage must clamp HP at 0 (never negative).
	mob, err := world.Get(2)
	require.NoError(t, err)
	assert.Equal(t, int32(49), mob.HP, "HP subtracts the 1 damage and stays non-negative")
}
