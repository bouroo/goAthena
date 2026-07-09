//go:build unit

package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	inventorydomain "github.com/bouroo/goAthena/internal/features/inventory/domain"
)

// TestMaxWeight_PreRenewalFormula pins the pre-renewal formula
// `base + str*300` (rAthena src/map/status.cpp:3663) across the
// practical STR range. The boundary cases (str=0, str=1, max legal
// STR, and a custom non-Novice base) live together so a refactor that
// drops or reorders an operand fails loud here instead of in a
// downstream integration test.
func TestMaxWeight_PreRenewalFormula(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		base uint32
		str  uint16
		want uint32
	}{
		{
			name: "novice_no_str_yields_base_only",
			base: inventorydomain.NoviceMaxWeightBase,
			str:  0,
			want: 20000,
		},
		{
			name: "novice_str_1_adds_one_step",
			base: inventorydomain.NoviceMaxWeightBase,
			str:  1,
			want: 20300,
		},
		{
			name: "novice_str_50",
			base: inventorydomain.NoviceMaxWeightBase,
			str:  50,
			want: 20000 + 50*300,
		},
		{
			name: "novice_str_99_joblvl_cap",
			base: inventorydomain.NoviceMaxWeightBase,
			str:  99,
			want: 20000 + 99*300,
		},
		{
			name: "novice_str_130_capsule_breakpoint",
			base: inventorydomain.NoviceMaxWeightBase,
			str:  130,
			want: 20000 + 130*300,
		},
		{
			name: "non_novice_base_overrides_novice_constant",
			base: 30000, // Swordman base in pre-re job_db
			str:  60,
			want: 30000 + 60*300,
		},
		{
			name: "max_uint16_str_does_not_overflow_uint32",
			base: inventorydomain.NoviceMaxWeightBase,
			str:  65535,
			want: 20000 + 65535*300,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := inventorydomain.MaxWeight(tt.base, tt.str)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestZeroItemWeight_AlwaysZero locks the production placeholder
// behavior: every nameid returns 0. Future item_db.yml backings
// replace this struct, not the test.
func TestZeroItemWeight_AlwaysZero(t *testing.T) {
	t.Parallel()
	var l inventorydomain.ItemWeightLookup = inventorydomain.ZeroItemWeight{}
	for _, id := range []uint32{0, 1, 501, 999_999, 0xFFFF_FFFF} {
		assert.Equal(t, uint32(0), l.Weight(id), "nameid=%d must report 0 weight", id)
	}
}

// TestNoviceMaxWeightBase_Value documents the constant; it would be
// odd to "fix" 20000 silently in a future refactor.
func TestNoviceMaxWeightBase_Value(t *testing.T) {
	t.Parallel()
	assert.Equal(t, uint32(20000), inventorydomain.NoviceMaxWeightBase,
		"rAthena job_db max_weight_base for Novice is 20000 (src/map/pc.cpp:13864)")
}

// TestStrWeightStep_Value documents the constant; mirrors rAthena
// status.cpp:3663.
func TestStrWeightStep_Value(t *testing.T) {
	t.Parallel()
	assert.Equal(t, uint32(300), inventorydomain.StrWeightStep,
		"rAthena pre-re carry-weight step per STR is 300 (status.cpp:3663)")
}
