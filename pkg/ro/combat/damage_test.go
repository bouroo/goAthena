package combat_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bouroo/goAthena/pkg/ro/combat"
	"github.com/bouroo/goAthena/pkg/ro/mode"
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
