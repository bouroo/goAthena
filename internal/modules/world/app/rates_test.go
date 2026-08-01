//go:build unit

package app

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRates_baseExpAward covers the server base-EXP multiplier (battle.conf
// base_exp_rate parity): 100=1x, 200=2x, 0 disables, fractions truncate.
func TestRates_baseExpAward(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		rate   int
		mobExp int32
		want   uint64
	}{
		{"1x default", 100, 1000, 1000},
		{"2x", 200, 1000, 2000},
		{"half rounds down", 50, 99, 49},
		{"0 disables", 0, 1000, 0},
		{"zero mob exp stays zero", 100, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, Rates{BaseExp: tt.rate}.baseExpAward(tt.mobExp))
		})
	}
}

// TestRates_dropThreshold covers the server drop multiplier: 100=raw rate, a high
// multiplier clamps at the per-myriad denominator (guaranteed drop, rAthena
// cap_value), and 0 disables (never drops). A raw 0 always yields 0.
func TestRates_dropThreshold(t *testing.T) {
	t.Parallel()
	const denom = 10000
	tests := []struct {
		name    string
		drop    int
		rawRate int
		want    int
	}{
		{"1x raw rate", 100, 500, 500},
		{"2x doubles", 200, 500, 1000},
		{"clamped at 100% guarantee", 100, denom, denom},
		{"multiplier guarantees (1000x on 1%)", 100000, 100, denom},
		{"0 drop disables (raw>0)", 0, 500, 0},
		{"raw 0 always 0", 100, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Rates{Drop: tt.drop}.dropThreshold(tt.rawRate)
			assert.Equal(t, tt.want, got)
			assert.LessOrEqual(t, got, denom, "threshold must never exceed the denominator")
		})
	}
}
