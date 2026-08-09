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
	"github.com/bouroo/goAthena/pkg/ro/attrfix"
	"github.com/bouroo/goAthena/pkg/ro/mobdb"
	"github.com/bouroo/goAthena/pkg/ro/sizefix"
	"github.com/bouroo/goAthena/pkg/ro/statcalc"
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
	combat := app.NewCombatService(world, mobs, nil)

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
	combat := app.NewCombatService(world, nil, nil) // no mob_db loaded

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

// modMobFixture seeds a Water-typed mob (for the element axis) and a Medium mob
// (for the size axis). Both are def0/vit0 so the only damage variance is the
// modifier under test.
const modMobFixture = `Header:
  Type: MOB_DB
  Version: 5

Body:
  - Id: 9101
    Name: WaterMob
    Defense: 0
    Vit: 0
    Element: Water
    ElementLevel: 1
  - Id: 9102
    Name: MediumMob
    Defense: 0
    Vit: 0
    Size: Medium
`

// attrFixture carries only the Water-defender rates the element test asserts
// against: Wind attacks Water for 175% (advantage), Fire for 50% (disadvantage).
// Every other cell stays the identity (100) — attr_fix initializes each level's
// matrix to all-100, so omitted pairs resolve to neutral.
const attrFixture = `Header:
  Type: ATTRIBUTE_DB
  Version: 1

Body:
  - Level: 1
    Wind:
      Water: 175
    Fire:
      Water: 50
`

// sizeFixture mirrors rAthena's real size_fix: a Knuckle deals 75% to Medium.
const sizeFixture = `Header:
  Type: SIZE_FIX_DB
  Version: 1

Body:
  - Weapon: Knuckle
    Medium: 75
    Large: 50
`

// fakeProfile injects a PC's equipped-weapon contributions (WeaponATK plus the
// weapon's element index and subtype) without the inventory+itemdb machinery. It
// satisfies app's unexported equipmentProfiler via structural conformance (its
// single method is exported).
type fakeProfile struct {
	weaponATK int32
	weaponEle int
	weaponSub string
}

func (f fakeProfile) EquipmentProfile(_ context.Context, _, _ uint32) (statcalc.Equipment, error) {
	return statcalc.Equipment{
		WeaponATK:     f.weaponATK,
		WeaponElement: f.weaponEle,
		WeaponSubType: f.weaponSub,
	}, nil
}

// alwaysMinDice draws 0 on every axis: 0 < hitPct (≥5) connects and 0 < critRate
// (≥10) crits. It forces a critical hit deterministically.
type alwaysMinDice struct{}

func (alwaysMinDice) Intn(int) int { return 0 }

// alwaysMaxDice draws n-1 on every axis: 99 ≥ hitPct (≤95) misses and 999 ≥
// critRate never crits. It forces a miss deterministically.
type alwaysMaxDice struct{}

func (alwaysMaxDice) Intn(n int) int { return n - 1 }

// strAttacker is a level-1 PC with Str=50: pre-re BaseATK = 50 + (50/10)² = 75,
// the fixed damage base every modifier test reasons from.
func strAttacker(id domain.EntityID) domain.Entity {
	return domain.Entity{ID: id, Type: domain.EntityTypePC, Level: 1, Str: 50, Map: "prontera"}
}

// svcCfg holds the building blocks armedAttack assembles a CombatService from,
// so each weapon variant gets a fresh service while sharing world/registries/dice.
type svcCfg struct {
	world *app.WorldService
	mobs  *mobdb.Registry
	attrs *attrfix.RateTable
	sizes *sizefix.SizeTable
	dice  app.Dice
}

// armedAttack builds a one-shot service from cfg armed with wpn, resolves one
// hit from PC 1 onto mob 2, and returns the damage. Damage is HP-independent
// (computed before applyDamage), so successive calls compare cleanly.
func armedAttack(t *testing.T, cfg svcCfg, wpn fakeProfile) int32 {
	t.Helper()
	opts := []app.Option{app.WithAttributeFix(cfg.attrs), app.WithSizeFix(cfg.sizes)}
	if cfg.dice != nil {
		opts = append(opts, app.WithDice(cfg.dice))
	}
	dmg, _, err := app.NewCombatService(cfg.world, cfg.mobs, wpn, opts...).Attack(1, 2)
	require.NoError(t, err)
	return dmg
}

// TestCombat_ElementModifier proves a mob's mob_db Element/ElementLevel feed the
// attr_fix table through the CombatService. Same attacker, same def0/vit0 Water
// mob: a Wind weapon deals 175% (131) and a Fire weapon 50% (37) of the neutral
// 75 baseline. (The pre-re wheel is Wind>Water, Fire<Water — the data file, not
// the element names, decides direction.)
func TestCombat_ElementModifier(t *testing.T) {
	t.Parallel()
	mobs, err := mobdb.Load(strings.NewReader(modMobFixture))
	require.NoError(t, err)
	attrs, err := attrfix.Load(strings.NewReader(attrFixture))
	require.NoError(t, err)

	world := app.NewWorldService(infra.NewMemoryWorldRepository(), slog.Default(), 50)
	require.NoError(t, world.AddEntity(strAttacker(1)))
	require.NoError(t, world.AddEntity(domain.Entity{
		ID: 2, Type: domain.EntityTypeMob, Class: 9101, Map: "prontera", HP: 10000, MaxHP: 10000,
	}))
	cfg := svcCfg{world: world, mobs: mobs, attrs: attrs}

	neutral := armedAttack(t, cfg, fakeProfile{weaponEle: attrfix.EleNeutral})
	wind := armedAttack(t, cfg, fakeProfile{weaponEle: attrfix.EleWind})
	fire := armedAttack(t, cfg, fakeProfile{weaponEle: attrfix.EleFire})

	assert.Equal(t, int32(75), neutral, "Neutral weapon vs Water = baseline 75")
	assert.Equal(t, int32(131), wind, "Wind weapon vs Water = 75·175/100 = 131")
	assert.Equal(t, int32(37), fire, "Fire weapon vs Water = 75·50/100 = 37")
	assert.Greater(t, wind, neutral, "advantageous element beats neutral")
	assert.Less(t, fire, neutral, "disadvantageous element loses to neutral")
}

// TestCombat_SizeModifier proves a mob's mob_db Size feeds the size_fix table:
// a Knuckle against a Medium mob deals 75% (56) of the 75 baseline.
func TestCombat_SizeModifier(t *testing.T) {
	t.Parallel()
	mobs, err := mobdb.Load(strings.NewReader(modMobFixture))
	require.NoError(t, err)
	sizes, err := sizefix.Load(strings.NewReader(sizeFixture))
	require.NoError(t, err)

	world := app.NewWorldService(infra.NewMemoryWorldRepository(), slog.Default(), 50)
	require.NoError(t, world.AddEntity(strAttacker(1)))
	require.NoError(t, world.AddEntity(domain.Entity{
		ID: 2, Type: domain.EntityTypeMob, Class: 9102, Map: "prontera", HP: 10000, MaxHP: 10000,
	}))
	cfg := svcCfg{world: world, mobs: mobs, sizes: sizes}

	identity := armedAttack(t, cfg, fakeProfile{}) // empty subtype = 100%
	knuckle := armedAttack(t, cfg, fakeProfile{weaponSub: "Knuckle"})

	assert.Equal(t, int32(75), identity, "unknown weapon vs Medium = baseline 75")
	assert.Equal(t, int32(56), knuckle, "Knuckle vs Medium = 75·75/100 = 56")
}

// TestCombat_CriticalDoubles proves a forced critical roll doubles the post-DEF,
// post-modifier damage. A high-Luk attacker (Luk=90 -> CritRate = 10 + 90*10/3 =
// 310 per-mille) lands the critical via the injected Dice; the legacy (nil Dice)
// baseline hits without crit. The assertion is the 2x ratio so it holds for the
// exact pre-re BaseATK (which itself folds Luk/5 into the base).
func TestCombat_CriticalDoubles(t *testing.T) {
	t.Parallel()
	mobs, err := mobdb.Load(strings.NewReader(modMobFixture))
	require.NoError(t, err)

	world := app.NewWorldService(infra.NewMemoryWorldRepository(), slog.Default(), 50)
	require.NoError(t, world.AddEntity(domain.Entity{
		ID: 1, Type: domain.EntityTypePC, Level: 1, Str: 50, Luk: 90, Map: "prontera",
	}))
	require.NoError(t, world.AddEntity(domain.Entity{
		ID: 2, Type: domain.EntityTypeMob, Class: 9102, Map: "prontera", HP: 10000, MaxHP: 10000,
	}))
	cfg := svcCfg{world: world, mobs: mobs}

	normal := armedAttack(t, cfg, fakeProfile{}) // nil Dice: no crit
	crit := armedAttack(t, svcCfg{world: world, mobs: mobs, dice: alwaysMinDice{}}, fakeProfile{})

	assert.Greater(t, normal, int32(0), "non-crit baseline deals damage")
	assert.Equal(t, 2*normal, crit, "critical doubles the non-crit damage (pre-re 2x)")
}

// TestCombat_MissDealsZero proves a missed roll deals no damage and applies
// nothing: the mob's HP is untouched. The attacker has full 75-damage potential,
// but alwaysMaxDice forces hit=false/crit=false → miss.
func TestCombat_MissDealsZero(t *testing.T) {
	t.Parallel()
	mobs, err := mobdb.Load(strings.NewReader(modMobFixture))
	require.NoError(t, err)

	world := app.NewWorldService(infra.NewMemoryWorldRepository(), slog.Default(), 50)
	require.NoError(t, world.AddEntity(strAttacker(1)))
	require.NoError(t, world.AddEntity(domain.Entity{
		ID: 2, Type: domain.EntityTypeMob, Class: 9102, Map: "prontera", HP: 1000, MaxHP: 1000,
	}))

	dmg, died, err := app.NewCombatService(
		world, mobs, fakeProfile{}, app.WithDice(alwaysMaxDice{}),
	).Attack(1, 2)
	require.NoError(t, err)
	assert.Equal(t, int32(0), dmg, "a miss deals 0 damage")
	assert.False(t, died, "a miss never kills")

	mob, err := world.Get(2)
	require.NoError(t, err)
	assert.Equal(t, int32(1000), mob.HP, "a miss applies no damage")
}
