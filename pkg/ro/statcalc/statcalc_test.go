package statcalc

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// statusCase is one hand-computed ZC_STATUS expectation. The expected values are
// derived from the rAthena non-RENEWAL formulas (see statcalc.go doc), not from
// the code under test, so a formula drift surfaces as a field mismatch. noviceFist
// is the documented Novice fist aspd_base (job_aspd.yml:95).
const noviceFist uint16 = 500

func TestZCStatus(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   StatusInputs
		want packet.StatusResponse
	}{
		{
			// Low-stat novice: Str=11 makes (str/10)² = 1; the other stats are 1,
			// so their squared/quotient terms vanish and only the additive
			// constants remain. Exercises NeedX on both a >10 stat (3) and a
			// <10 stat (2).
			name: "novice 11/1/1/1/1/1 level 1",
			in: StatusInputs{
				Base:           Base{Level: 1, Str: 11, Agi: 1, Vit: 1, Int: 1, Dex: 1, Luk: 1},
				StatusPoint:    9,
				WeaponBaseASPD: noviceFist,
			},
			// BaseATK = 11 + 1 + 0 + 0 = 12; Matk 1/1; Hit/Flee = 2/2;
			// SoftDEF = 1; SoftMDEF = 1 + 0 = 1; Crit(13)/10 = 1; Flee2(11)/10 = 1;
			// Amotion = 500 - 500*5/1000 = 498.
			want: packet.StatusResponse{
				StatusPoint: 9,
				Str:         11, NeedStr: 3,
				Agi: 1, NeedAgi: 2,
				Vit: 1, NeedVit: 2,
				Int: 1, NeedInt: 2,
				Dex: 1, NeedDex: 2,
				Luk: 1, NeedLuk: 2,
				Atk1: 12, Atk2: 0, MatkMax: 1, MatkMin: 1,
				Def1: 0, Def2: 1, Mdef1: 0, Mdef2: 1,
				Hit: 2, Flee: 2, Flee2: 1, Critical: 1, ASPD: 498, PlusASPD: 0,
			},
		},
		{
			// Higher stats exercise every squared term: (str/10)², (int/7)²,
			// (int/5)², luk*10/3, and a non-trivial amotion reduction.
			name: "mid 50/20/30/40/10/15 level 50",
			in: StatusInputs{
				Base:           Base{Level: 50, Str: 50, Agi: 20, Vit: 30, Int: 40, Dex: 10, Luk: 15},
				StatusPoint:    100,
				WeaponBaseASPD: noviceFist,
			},
			// BaseATK = 50 + 25 + 2 + 3 = 80; Matk = 40+25 / 40+64 = 65/104;
			// Hit = 60; Flee = 70; SoftDEF = 30; SoftMDEF = 40 + 15 = 55;
			// Crit = 10 + 50 = 60 → /10 = 6; Flee2 = 25 → /10 = 2;
			// Amotion = 500 - 500*90/1000 = 455.
			want: packet.StatusResponse{
				StatusPoint: 100,
				Str:         50, NeedStr: 6, // 1 + (50+9)/10 = 6
				Agi: 20, NeedAgi: 3, // 1 + (20+9)/10 = 3
				Vit: 30, NeedVit: 4, // 1 + (30+9)/10 = 4
				Int: 40, NeedInt: 5, // 1 + (40+9)/10 = 5
				Dex: 10, NeedDex: 2, // 1 + (10+9)/10 = 2
				Luk: 15, NeedLuk: 3, // 1 + (15+9)/10 = 3
				Atk1: 80, Atk2: 0, MatkMax: 104, MatkMin: 65,
				Def1: 0, Def2: 30, Mdef1: 0, Mdef2: 55,
				Hit: 60, Flee: 70, Flee2: 2, Critical: 6, ASPD: 455, PlusASPD: 0,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ZCStatus(tc.in)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestDerivedFormulas covers the raw formula functions independently of the
// ZCStatus mapping, with values that would be masked by the /10 wire division.
func TestDerivedFormulas(t *testing.T) {
	t.Parallel()
	b := Base{Level: 12, Str: 23, Agi: 7, Vit: 14, Int: 21, Dex: 9, Luk: 6}
	// Str/10 = 2 → 23 + 4 + 1 + 1 = 29.
	assert.Equal(t, int32(29), BaseATK(b), "BaseATK")
	// Int=21: (21/7)²=9, (21/5)²=16 → 30 / 37.
	assert.Equal(t, int32(30), MatkMin(b), "MatkMin")
	assert.Equal(t, int32(37), MatkMax(b), "MatkMax")
	// Hit = 12 + 9 = 21; Flee = 12 + 7 = 19.
	assert.Equal(t, int32(21), Hit(b), "Hit")
	assert.Equal(t, int32(19), Flee(b), "Flee")
	// SoftDEF = 14; SoftMDEF = 21 + 7 = 28.
	assert.Equal(t, int32(14), SoftDEF(b), "SoftDEF")
	assert.Equal(t, int32(28), SoftMDEF(b), "SoftMDEF")
	// Crit(stored) = 10 + 6*10/3 = 10 + 20 = 30; Flee2(stored) = 6 + 10 = 16.
	assert.Equal(t, int32(30), Critical(b), "Critical stored")
	assert.Equal(t, int32(16), PerfectFlee(b), "PerfectFlee stored")
	// Amotion = 500 - 500*(4*7+9)/1000 = 500 - 500*37/1000 = 500 - 18 = 482.
	assert.Equal(t, int32(482), Amotion(b, noviceFist), "Amotion")
}

// TestClamps exercises the wire clamps at their boundaries so a future stat
// explosion cannot overflow the wire slot.
func TestClamps(t *testing.T) {
	t.Parallel()
	assert.Equal(t, uint8(255), clampU8(300))
	assert.Equal(t, uint8(42), clampU8(42))
	assert.Equal(t, int16(32767), clampInt16(99999))
	assert.Equal(t, int16(0), clampInt16(-5))
	assert.Equal(t, int16(123), clampInt16(123))
}
