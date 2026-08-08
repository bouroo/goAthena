package statcalc

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// statusCase is one hand-computed ZC_STATUS expectation. The expected values are
// derived from the rAthena formulas (see statcalc.go doc for which branch), not
// from the code under test, so a formula drift surfaces as a field mismatch.
// noviceFist is the documented Novice fist aspd_base (job_aspd.yml:95).
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
			name: "pre-re novice 11/1/1/1/1/1 level 1",
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
			name: "pre-re mid 50/20/30/40/10/15 level 50",
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
			got := ZCStatus(tc.in, PreRenewalSet)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestZCStatusRenewal binds the Renewal formula set to hand-computed vectors
// from the #ifdef RENEWAL branches (see baseatk_re.go / derived_re.go sources).
// The same inputs as the pre-re cases isolate the mode difference (e.g. the
// +175 Hit / +100 Flee Renewal offsets). ASPD is shared with pre-re here because
// the Renewal ASPD pipeline is deferred (see amotion_re.go).
func TestZCStatusRenewal(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   StatusInputs
		want packet.StatusResponse
	}{
		{
			name: "renewal novice 11/1/1/1/1/1 level 1",
			in: StatusInputs{
				Base:           Base{Level: 1, Str: 11, Agi: 1, Vit: 1, Int: 1, Dex: 1, Luk: 1},
				StatusPoint:    9,
				WeaponBaseASPD: noviceFist,
			},
			// BaseATK = (110 + 2 + 3 + 2)/10 = 11; Matk = 1+0+0+0+0 = 1/1;
			// Hit = 1+1+0+175 = 177; Flee = 1+1+0+100 = 102;
			// SoftDEF = (1+1)/2+0 = 1; SoftMDEF = 1+0+0 = 1;
			// Crit = 0+10+3 = 13 → /10 = 1; Flee2 = 11 → /10 = 1;
			// Amotion (deferred) = 498.
			want: packet.StatusResponse{
				StatusPoint: 9,
				Str:         11, NeedStr: 3,
				Agi: 1, NeedAgi: 2,
				Vit: 1, NeedVit: 2,
				Int: 1, NeedInt: 2,
				Dex: 1, NeedDex: 2,
				Luk: 1, NeedLuk: 2,
				Atk1: 11, Atk2: 0, MatkMax: 1, MatkMin: 1,
				Def1: 0, Def2: 1, Mdef1: 0, Mdef2: 1,
				Hit: 177, Flee: 102, Flee2: 1, Critical: 1, ASPD: 498, PlusASPD: 0,
			},
		},
		{
			name: "renewal mid 50/20/30/40/10/15 level 50",
			in: StatusInputs{
				Base:           Base{Level: 50, Str: 50, Agi: 20, Vit: 30, Int: 40, Dex: 10, Luk: 15},
				StatusPoint:    100,
				WeaponBaseASPD: noviceFist,
			},
			// BaseATK = (500 + 20 + 50 + 125)/10 = 695/10 = 69; Matk = 40+20+2+5+12 = 79/79;
			// Hit = 50+10+5+175 = 240; Flee = 50+20+3+100 = 173;
			// SoftDEF = 80/2 + 4 = 44; SoftMDEF = 40+12+8 = 60;
			// Crit = 5+10+45 = 60 → /10 = 6; Flee2 = 25 → /10 = 2;
			// Amotion (deferred) = 455.
			want: packet.StatusResponse{
				StatusPoint: 100,
				Str:         50, NeedStr: 6,
				Agi: 20, NeedAgi: 3,
				Vit: 30, NeedVit: 4,
				Int: 40, NeedInt: 5,
				Dex: 10, NeedDex: 2,
				Luk: 15, NeedLuk: 3,
				Atk1: 69, Atk2: 0, MatkMax: 79, MatkMin: 79,
				Def1: 0, Def2: 44, Mdef1: 0, Mdef2: 60,
				Hit: 240, Flee: 173, Flee2: 2, Critical: 6, ASPD: 455, PlusASPD: 0,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := ZCStatus(tc.in, RenewalSet)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestRegistry resolves each mode and asserts it routes to the right set, so a
// future sets-array reshuffle cannot silently swap the two modes.
func TestRegistry(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	assert.Equal(t, PreRenewalSet, r.Get(0)) // 0 == PreRenewal
	assert.Equal(t, RenewalSet, r.Get(1))    // 1 == Renewal
}

// TestDerivedFormulas covers the raw Pre-Renewal formula functions independently
// of the ZCStatus mapping, with values that would be masked by the /10 wire
// division.
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

// TestDerivedFormulasRenewal covers the Renewal formula set with the same Base
// the pre-re test uses, so the contrast between modes is explicit in the diff.
func TestDerivedFormulasRenewal(t *testing.T) {
	t.Parallel()
	b := Base{Level: 12, Str: 23, Agi: 7, Vit: 14, Int: 21, Dex: 9, Luk: 6}
	re := RenewalSet
	// BaseATK = (230 + 18 + 20 + 30)/10 = 298/10 = 29.
	assert.Equal(t, int32(29), re.BaseATK(b), "BaseATK")
	// Matk min=max = 21 + 10 + 1 + 2 + 3 = 37.
	assert.Equal(t, int32(37), re.MatkMin(b), "MatkMin")
	assert.Equal(t, int32(37), re.MatkMax(b), "MatkMax")
	// Hit = 12 + 9 + 2 + 175 = 198; Flee = 12 + 7 + 1 + 100 = 120.
	assert.Equal(t, int32(198), re.Hit(b), "Hit")
	assert.Equal(t, int32(120), re.Flee(b), "Flee")
	// SoftDEF = (12+14)/2 + 1 = 14; SoftMDEF = 21 + 3 + 4 = 28.
	assert.Equal(t, int32(14), re.SoftDEF(b), "SoftDEF")
	assert.Equal(t, int32(28), re.SoftMDEF(b), "SoftMDEF")
	// Crit(stored) = 1 + 10 + 18 = 29; Flee2(stored) = 16.
	assert.Equal(t, int32(29), re.Critical(b), "Critical stored")
	assert.Equal(t, int32(16), re.PerfectFlee(b), "PerfectFlee stored")
	// Amotion deferred to the pre-re body: 500 - 500*37/1000 = 482.
	assert.Equal(t, int32(482), re.Amotion(b, noviceFist), "Amotion")
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
