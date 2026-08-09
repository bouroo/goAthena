package attrfix

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSubmodulePreRenewalAttrFix proves the loader parses the real
// rathenaThailand pre-re attr_fix.yml (4 element levels, 10×10 each) and that
// known rAthena pre-renewal modifiers resolve by attacker element, defender
// element, and defender ElementLevel. Skipped when the submodule is absent so
// it never breaks CI without the submodule.
func TestSubmodulePreRenewalAttrFix(t *testing.T) {
	path := filepath.Join("..", "..", "..", "third_party", "rathenaThailand", "db", "pre-re", "attr_fix.yml")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("rathenaThailand submodule not available at %s: %v", path, err)
	}

	tbl, err := LoadFile(path)
	require.NoError(t, err)
	require.Equal(t, 4, tbl.MaxLevel(), "pre-re attr_fix must carry 4 element levels")

	// Fire attacking Water = 50%; Water attacking Fire = 150% (level 1).
	assert.Equal(t, 50, tbl.Rate(EleFire, EleWater, 1))
	assert.Equal(t, 150, tbl.Rate(EleWater, EleFire, 1))
	// Wind attacking Water = 175% (level 1).
	assert.Equal(t, 175, tbl.Rate(EleWind, EleWater, 1))

	// The defender's ElementLevel changes the rate: a Neutral weapon hits a
	// Ghost 1/2 for 25% but a Ghost 3/4 for 0%.
	assert.Equal(t, 25, tbl.Rate(EleNeutral, EleGhost, 1))
	assert.Equal(t, 25, tbl.Rate(EleNeutral, EleGhost, 2))
	assert.Equal(t, 0, tbl.Rate(EleNeutral, EleGhost, 3))
	assert.Equal(t, 0, tbl.Rate(EleNeutral, EleGhost, 4))

	// ElementLevel clamping mirrors rAthena: below 1 -> level 1, above 4 -> 4.
	assert.Equal(t, 25, tbl.Rate(EleNeutral, EleGhost, 0))
	assert.Equal(t, 0, tbl.Rate(EleNeutral, EleGhost, 5))

	// Holy attacking Undead escalates with the defender's ElementLevel: Ghost
	// 1 = 150%, level 2 = 175%, level 3/4 = 200% (the undead-smiting bonus).
	assert.Equal(t, 150, tbl.Rate(EleHoly, EleUndead, 1))
	assert.Equal(t, 175, tbl.Rate(EleHoly, EleUndead, 2))
	assert.Equal(t, 200, tbl.Rate(EleHoly, EleUndead, 3))

	// Negative rate = heal: Undead attacking a level-4 Poison target heals it
	// by 25% of the hit — verifies negative modifiers survive parsing.
	assert.Equal(t, -25, tbl.Rate(EleUndead, ElePoison, 4))
}
