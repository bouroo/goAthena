package combat_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bouroo/goAthena/pkg/ro/attrfix"
	"github.com/bouroo/goAthena/pkg/ro/combat"
	"github.com/bouroo/goAthena/pkg/ro/mode"
	"github.com/bouroo/goAthena/pkg/ro/sizefix"
	"github.com/bouroo/goAthena/pkg/ro/statcalc"
)

// damageCase is one hand-computed damage vector for NormalMelee, asserted in both
// modes against the same Attacker/Defender pair. wantPre / wantRenewal are the
// exact int32 damage a connecting hit deals.
type damageCase struct {
	name        string
	attacker    statcalc.Base
	hardDEF     int32
	softDEF     int32
	wantPre     int32
	wantRenewal int32
}

// nakedNovice is a level-1 character with the six base stats all 1 — the
// character module's novice-start stats (Str/Agi/Vit/Int/Dex/Luk = 1).
func nakedNovice() statcalc.Base {
	return statcalc.Base{Level: 1, Str: 1, Agi: 1, Vit: 1, Int: 1, Dex: 1, Luk: 1}
}

// damageCases bind every formula seam to a hand-computed value, sourced from
// battle.cpp + status.cpp. The attacker's BaseATK is the sole damage source for
// a naked PC (weapon/equip/mastery atk = 0); the DEF reduction is mode-keyed.
//
//	Pre-re  batk = str + (str/10)² + dex/5 + luk/5
//	         dmg  = batk·(100−def1)/100 − vit_def, then floor 1
//	Renewal batk  = (str·10 + dex·10/5 + luk·10/3 + lvl·10/4)/10
//	         dmg  = 2·batk·(4000+def1)/(4000+10·def1) − vit_def, clamp 0
//
// Naked L1 novice ⇒ pre-re batk = 1+0+0+0 = 1; renewal batk = (10+2+3+2)/10 = 1.
var damageCases = []damageCase{
	{
		// Poring (slim mob_db: Defense 0, Vit 1). SoftDEF = 1 both modes
		// (pre-re Vit=1; renewal (1+1)/2=1). Neutral attacker ⇒ 100% element.
		// Pre-re: 1·100/100 − 1 = 0 → floor 1. Renewal: 2·4000/4000 − 1 = 1.
		name:     "naked novice vs Poring (def0/vit1)",
		attacker: nakedNovice(), hardDEF: 0, softDEF: 1, wantPre: 1, wantRenewal: 1,
	},
	{
		// Tank (Def 5, Vit 4). SoftDEF = 4 (pre-re Vit=4; renewal (5+4)/2=4).
		// Pre-re: 1·95/100 − 4 = 0 − 4 = −4 → floor 1. Renewal: 2·4005/4050 − 4
		// = 8010/4050 − 4 = 1 − 4 = −3 → clamp 0.
		name:     "naked novice vs Tank (def5/vit4)",
		attacker: nakedNovice(), hardDEF: 5, softDEF: 4, wantPre: 1, wantRenewal: 0,
	},
	{
		// Floored (Def 100, Vit 0). SoftDEF = 0.
		// Pre-re: 1·0/100 − 0 = 0 → floor 1. Renewal: 2·4100/5000 − 0 = 1.
		name:     "naked novice vs Floored (def100/vit0)",
		attacker: nakedNovice(), hardDEF: 100, softDEF: 0, wantPre: 1, wantRenewal: 1,
	},
	{
		// A stronger attacker (Str 50, Dex 30, Luk 10, L1) exercises BaseATK
		// feeding real (non-floored) damage. Pre-re batk = 50+25+6+2 = 83;
		// renewal batk = (500+60+33+2)/10 = 59.
		// Pre-re vs def0/vit1: 83·100/100 − 1 = 82.
		// Renewal vs def0/vit1: 2·59·4000/4000 − 1 = 118 − 1 = 117.
		name:     "strong attacker vs Poring (def0/vit1)",
		attacker: statcalc.Base{Level: 1, Str: 50, Agi: 1, Vit: 1, Int: 1, Dex: 30, Luk: 10},
		hardDEF:  0, softDEF: 1, wantPre: 82, wantRenewal: 117,
	},
	{
		// Same strong attacker vs Floored (def100/vit0): exercises the Renewal
		// softDEF ratio reducing the assembled damage (4100/5000 = 0.82).
		// Pre-re: 83·0/100 − 0 = 0 → floor 1.
		// Renewal: 2·59·4100/5000 − 0 = 118·4100/5000 = 483800/5000 = 96.
		name:     "strong attacker vs Floored (def100/vit0)",
		attacker: statcalc.Base{Level: 1, Str: 50, Agi: 1, Vit: 1, Int: 1, Dex: 30, Luk: 10},
		hardDEF:  100, softDEF: 0, wantPre: 1, wantRenewal: 96,
	},
}

func TestNormalMelee_PreRenewal(t *testing.T) {
	t.Parallel()
	for _, tc := range damageCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := combat.NormalMelee(
				combat.Attacker{Base: tc.attacker},
				combat.Defender{HardDEF: tc.hardDEF, SoftDEF: tc.softDEF},
				statcalc.PreRenewalSet, mode.PreRenewal,
			)
			assert.Equal(t, tc.wantPre, got.Damage, "pre-renewal damage")
		})
	}
}

func TestNormalMelee_Renewal(t *testing.T) {
	t.Parallel()
	for _, tc := range damageCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := combat.NormalMelee(
				combat.Attacker{Base: tc.attacker},
				combat.Defender{HardDEF: tc.hardDEF, SoftDEF: tc.softDEF},
				statcalc.RenewalSet, mode.Renewal,
			)
			assert.Equal(t, tc.wantRenewal, got.Damage, "renewal damage")
		})
	}
}

// TestNormalMelee_WeaponATK pins the M10 equipment→damage contribution: an
// Attacker with a non-zero Equipment.WeaponATK deals strictly more than the same
// attacker bare-handed, in both modes. Hand-computed against the "strong
// attacker vs Poring (def0/vit1)" vector: pre-re batk 83, renewal batk 59.
//
//	WeaponATK 50 folds into the base:
//	  pre-re  naked 83·100/100 − 1 = 82;  armed (83+50) − 1 = 132
//	  renewal naked 2·59 − 1 = 117;       armed (118+50) − 1 = 167
func TestNormalMelee_WeaponATK(t *testing.T) {
	t.Parallel()
	attacker := statcalc.Base{Level: 1, Str: 50, Agi: 1, Vit: 1, Int: 1, Dex: 30, Luk: 10}
	defender := combat.Defender{HardDEF: 0, SoftDEF: 1}

	nakedPre := combat.NormalMelee(combat.Attacker{Base: attacker}, defender, statcalc.PreRenewalSet, mode.PreRenewal)
	armedPre := combat.NormalMelee(
		combat.Attacker{Base: attacker, Equipment: statcalc.Equipment{WeaponATK: 50}},
		defender, statcalc.PreRenewalSet, mode.PreRenewal,
	)
	assert.Equal(t, int32(82), nakedPre.Damage, "pre-re naked baseline")
	assert.Equal(t, int32(132), armedPre.Damage, "pre-re armed")
	assert.Greater(t, armedPre.Damage, nakedPre.Damage, "weapon must raise pre-re damage")

	nakedRe := combat.NormalMelee(combat.Attacker{Base: attacker}, defender, statcalc.RenewalSet, mode.Renewal)
	armedRe := combat.NormalMelee(
		combat.Attacker{Base: attacker, Equipment: statcalc.Equipment{WeaponATK: 50}},
		defender, statcalc.RenewalSet, mode.Renewal,
	)
	assert.Equal(t, int32(117), nakedRe.Damage, "renewal naked baseline")
	assert.Equal(t, int32(167), armedRe.Damage, "renewal armed")
	assert.Greater(t, armedRe.Damage, nakedRe.Damage, "weapon must raise renewal damage")
}

func TestInMeleeRange(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name           string
		ax, ay, bx, by int
		want           bool
	}{
		{"same cell", 100, 100, 100, 100, true},
		{"adjacent E", 101, 100, 100, 100, true},
		{"adjacent diagonal", 101, 101, 100, 100, true},
		{"two cells E (out of range)", 102, 100, 100, 100, false},
		{"two cells diagonal (out of range)", 102, 102, 100, 100, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, combat.InMeleeRange(tc.ax, tc.ay, tc.bx, tc.by))
		})
	}
}

// --- Element / Size / Crit / Miss (combat depth) ---------------------------
//
// The cases below pin the post-DEF multiplicative modifiers against the real
// rathenaThailand pre-re attr_fix.yml / size_fix.yml. The strong attacker
// (Str 50, Dex 30, Luk 10, L1) has pre-re batk 83 and renewal batk 59; against a
// def0/vit0 target the only scaling axis is the modifier rate, so each
// hand-computed value is base·rate/100 (integer truncation).
//
//	pre-re  base = batk = 83        dmg = 83·rate/100
//	renewal base = 2·batk = 118     dmg = 118·rate/100

// strongAttacker is the M10 "strong attacker" vector: pre-re batk 83, renewal
// batk 59, pre-re Hit = level+dex = 31, pre-re Crit = 10+luk·10/3 = 43.
func strongAttacker() statcalc.Base {
	return statcalc.Base{Level: 1, Str: 50, Agi: 1, Vit: 1, Int: 1, Dex: 30, Luk: 10}
}

// loadPreRenewalTables loads the real rathenaThailand pre-re attr_fix.yml and
// size_fix.yml, skipping the test when the submodule is absent (mirrors the
// pkg/ro/attrfix + pkg/ro/sizefix submodule-data tests).
func loadPreRenewalTables(t *testing.T) (*attrfix.RateTable, *sizefix.SizeTable) {
	t.Helper()
	root := filepath.Join("..", "..", "..", "third_party", "rathenaThailand", "db", "pre-re")
	attrPath := filepath.Join(root, "attr_fix.yml")
	if _, err := os.Stat(attrPath); err != nil {
		t.Skipf("rathenaThailand submodule not available at %s: %v", attrPath, err)
	}
	attrTbl, err := attrfix.LoadFile(attrPath)
	if err != nil {
		t.Fatalf("load attr_fix: %v", err)
	}
	sizeTbl, err := sizefix.LoadFile(filepath.Join(root, "size_fix.yml"))
	if err != nil {
		t.Fatalf("load size_fix: %v", err)
	}
	return attrTbl, sizeTbl
}

// TestNormalMelee_Element pins the post-DEF element multiplier (attr_fix level
// 1): neutral-vs-neutral is the 100% baseline, super-effective pairs raise it,
// resisted/immune pairs lower it to 0. A nil attr_fix table (no option) degrades
// to the identity rate so the legacy baseline survives before wiring.
func TestNormalMelee_Element(t *testing.T) {
	t.Parallel()
	attrTbl, _ := loadPreRenewalTables(t)
	a := strongAttacker()

	// No attr_fix table => identity element rate (100%): legacy neutral baseline.
	noTable := combat.NormalMelee(
		combat.Attacker{Base: a, WeaponElement: attrfix.EleWater},
		combat.Defender{HardDEF: 0, SoftDEF: 0, Element: attrfix.EleFire, ElementLevel: 1},
		statcalc.PreRenewalSet, mode.PreRenewal,
	)
	assert.Equal(t, int32(100), noTable.ElementRate, "nil table => identity rate")
	assert.Equal(t, int32(83), noTable.Damage, "nil table => baseline damage")

	cases := []struct {
		name          string
		weaponElement int
		defender      combat.Defender
		wantRate      int32
		wantDamage    int32
	}{
		{"neutral vs neutral (100%)", attrfix.EleNeutral, combat.Defender{Element: attrfix.EleNeutral, ElementLevel: 1}, 100, 83},
		{"water vs fire (150%, higher)", attrfix.EleWater, combat.Defender{Element: attrfix.EleFire, ElementLevel: 1}, 150, 124},
		{"fire vs water (50%, lower)", attrfix.EleFire, combat.Defender{Element: attrfix.EleWater, ElementLevel: 1}, 50, 41},
		{"neutral vs ghost1 (25%, lower)", attrfix.EleNeutral, combat.Defender{Element: attrfix.EleGhost, ElementLevel: 1}, 25, 20},
		{"holy vs holy1 (0%, immune)", attrfix.EleHoly, combat.Defender{Element: attrfix.EleHoly, ElementLevel: 1}, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := combat.NormalMelee(
				combat.Attacker{Base: a, WeaponElement: tc.weaponElement},
				tc.defender, statcalc.PreRenewalSet, mode.PreRenewal,
				combat.WithAttributeFix(attrTbl),
			)
			assert.Equal(t, tc.wantRate, got.ElementRate, "element rate")
			assert.Equal(t, tc.wantDamage, got.Damage, "damage")
			assert.False(t, got.Miss, "a connecting hit is not a miss even when immune")
		})
	}
}

// TestNormalMelee_Size pins the weapon-type × mob-size multiplier (size_fix).
// The rathenaThailand file lists only Knuckle and Whip as deviations; every
// other weapon (e.g. Dagger) and any omitted size resolve to the 100% identity.
func TestNormalMelee_Size(t *testing.T) {
	t.Parallel()
	_, sizeTbl := loadPreRenewalTables(t)
	a := strongAttacker()

	// No size_fix table / empty weapon => identity size rate (100%).
	noTable := combat.NormalMelee(
		combat.Attacker{Base: a, WeaponSubType: "Knuckle"},
		combat.Defender{Size: "Large"},
		statcalc.PreRenewalSet, mode.PreRenewal,
	)
	assert.Equal(t, int32(100), noTable.SizeRate, "nil table => identity rate")
	assert.Equal(t, int32(83), noTable.Damage, "nil table => baseline damage")

	cases := []struct {
		name       string
		subType    string
		size       string
		wantRate   int32
		wantDamage int32
	}{
		{"knuckle vs large (50%, lower)", "Knuckle", "Large", 50, 41},
		{"knuckle vs medium (75%, lower)", "Knuckle", "Medium", 75, 62},
		{"knuckle vs small (100%)", "Knuckle", "Small", 100, 83},
		{"dagger vs large (unknown weapon => 100%)", "Dagger", "Large", 100, 83},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := combat.NormalMelee(
				combat.Attacker{Base: a, WeaponSubType: tc.subType},
				combat.Defender{Size: tc.size},
				statcalc.PreRenewalSet, mode.PreRenewal,
				combat.WithSizeFix(sizeTbl),
			)
			assert.Equal(t, tc.wantRate, got.SizeRate, "size rate")
			assert.Equal(t, tc.wantDamage, got.Damage, "damage")
		})
	}
}

// TestNormalMelee_Crit pins the pre-renewal critical multiplier (2x, applied
// after element/size) and that a critical bypasses flee (Hit=false still lands).
func TestNormalMelee_Crit(t *testing.T) {
	t.Parallel()
	a := strongAttacker()

	// Pre-re: batk 83 × 2 = 166.
	crit := combat.NormalMelee(
		combat.Attacker{Base: a},
		combat.Defender{},
		statcalc.PreRenewalSet, mode.PreRenewal,
		combat.WithRoll(combat.Roll{Hit: true, Crit: true}),
	)
	assert.Equal(t, int32(166), crit.Damage, "pre-re crit doubles damage")
	assert.True(t, crit.Crit, "Crit flag set")
	assert.False(t, crit.Miss, "crit connects")

	// A critical bypasses flee: Hit=false still lands the crit (pre-renewal).
	bypass := combat.NormalMelee(
		combat.Attacker{Base: a},
		combat.Defender{},
		statcalc.PreRenewalSet, mode.PreRenewal,
		combat.WithRoll(combat.Roll{Hit: false, Crit: true}),
	)
	assert.Equal(t, int32(166), bypass.Damage, "crit bypasses flee")
	assert.True(t, bypass.Crit)
	assert.False(t, bypass.Miss, "a crit is never a miss")
}

// TestNormalMelee_Miss pins the miss path (Damage 0) and that the rate fields
// are still computed/reported so the caller's roll and the wire/log layer stay
// consistent.
func TestNormalMelee_Miss(t *testing.T) {
	t.Parallel()
	a := strongAttacker()

	miss := combat.NormalMelee(
		combat.Attacker{Base: a},
		combat.Defender{},
		statcalc.PreRenewalSet, mode.PreRenewal,
		combat.WithRoll(combat.Roll{Hit: false, Crit: false}),
	)
	assert.Equal(t, int32(0), miss.Damage, "miss deals 0")
	assert.True(t, miss.Miss)
	assert.False(t, miss.Crit)
	assert.Equal(t, int32(31), miss.HitRate, "pre-re Hit = level+dex = 1+30")
	assert.Equal(t, int32(43), miss.CritRate, "pre-re Crit = 10+luk*10/3 = 10+33")
}

// TestNormalMelee_Element_Renewal proves the element multiplier applies on the
// Renewal path too (cross-mode): renewal base 2·batk = 118, water-vs-fire 150%
// => 118·150/100 = 177.
func TestNormalMelee_Element_Renewal(t *testing.T) {
	t.Parallel()
	attrTbl, _ := loadPreRenewalTables(t)
	a := strongAttacker()

	got := combat.NormalMelee(
		combat.Attacker{Base: a, WeaponElement: attrfix.EleWater},
		combat.Defender{Element: attrfix.EleFire, ElementLevel: 1},
		statcalc.RenewalSet, mode.Renewal,
		combat.WithAttributeFix(attrTbl),
	)
	assert.Equal(t, int32(150), got.ElementRate)
	assert.Equal(t, int32(177), got.Damage, "renewal element multiplier")
}
