//go:build unit

package attrfix

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fixtureYAML = `Header:
  Type: ATTRIBUTE_DB
  Version: 1
Body:
  - Level: 1
    Neutral:
      Ghost: 25
      Undead: 100
    Fire:
      Water: 50
      Earth: 150
    Water:
      Fire: 150
      Water: 25
  - Level: 3
    Neutral:
      Ghost: 0
`

func TestLoad_KnownModifiers(t *testing.T) {
	tbl, err := Load(strings.NewReader(fixtureYAML))
	require.NoError(t, err)

	// Neutral weapon vs Ghost 1 = 25% (the classic "normal weapons barely hurt
	// Ghosts") and vs Ghost 3 = 0%; the level axis must change the rate.
	assert.Equal(t, 25, tbl.Rate(EleNeutral, EleGhost, 1))
	assert.Equal(t, 0, tbl.Rate(EleNeutral, EleGhost, 3))

	// Fire attacking Water = 50%, Water attacking Fire = 150% (water douses fire
	// -> the attacker's own element is weak; the defender's element is strong).
	assert.Equal(t, 50, tbl.Rate(EleFire, EleWater, 1))
	assert.Equal(t, 150, tbl.Rate(EleWater, EleFire, 1))
	// Fire attacking Earth = 150% (fire scorches earth).
	assert.Equal(t, 150, tbl.Rate(EleFire, EleEarth, 1))
}

func TestLoad_LevelClampAndMissing(t *testing.T) {
	tbl, err := Load(strings.NewReader(fixtureYAML))
	require.NoError(t, err)

	// A defender ElementLevel below 1 clamps to level 1 (Ghost 1 = 25%).
	assert.Equal(t, 25, tbl.Rate(EleNeutral, EleGhost, 0))
	assert.Equal(t, 25, tbl.Rate(EleNeutral, EleGhost, -2))
	// Above the highest loaded level clamps to it (level 3, Ghost = 0%).
	assert.Equal(t, 0, tbl.Rate(EleNeutral, EleGhost, 99))
	// A loaded level-2 is absent here, so the missing level resolves to the
	// identity rate (100), the documented degraded fallback.
	assert.Equal(t, 100, tbl.Rate(EleNeutral, EleGhost, 2))
	assert.Equal(t, 3, tbl.MaxLevel())
}

func TestLoad_OmittedCellIsIdentity(t *testing.T) {
	tbl, err := Load(strings.NewReader(fixtureYAML))
	require.NoError(t, err)
	// Fire vs Holy is omitted from the fixture -> identity (100), not 0.
	assert.Equal(t, 100, tbl.Rate(EleFire, EleHoly, 1))
}

func TestRate_OutOfRangeAndNil(t *testing.T) {
	tbl, err := Load(strings.NewReader(fixtureYAML))
	require.NoError(t, err)
	// Out-of-range attacker/defender elements resolve to identity, never panic.
	assert.Equal(t, 100, tbl.Rate(-1, EleGhost, 1))
	assert.Equal(t, 100, tbl.Rate(EleNeutral, numElements, 1))

	var nilTable *RateTable
	assert.Equal(t, 100, nilTable.Rate(EleFire, EleWater, 1))
	assert.Equal(t, 100, NewRateTable().Rate(EleFire, EleWater, 1))
}

func TestParseElement_DarkShadowAlias(t *testing.T) {
	// attr_fix.yml spells Shadow as "Dark"; both resolve to EleShadow (7).
	dark, ok := ParseElement("Dark")
	require.True(t, ok)
	assert.Equal(t, EleShadow, dark)
	shadow, ok := ParseElement("Shadow")
	require.True(t, ok)
	assert.Equal(t, EleShadow, shadow)
	_, ok = ParseElement("Plasma")
	assert.False(t, ok)
}

func TestLoad_WrongHeaderType(t *testing.T) {
	_, err := Load(strings.NewReader(strings.Replace(fixtureYAML, "ATTRIBUTE_DB", "MOB_DB", 1)))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ATTRIBUTE_DB")
}

func TestLoad_UnknownElement(t *testing.T) {
	bad := `Header:
  Type: ATTRIBUTE_DB
Body:
  - Level: 1
    Plasma:
      Neutral: 100
`
	_, err := Load(strings.NewReader(bad))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown attacker element")
}

func TestLoad_ReadError(t *testing.T) {
	_, err := Load(errorReader{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse attr_fix yaml")
	assert.Contains(t, err.Error(), "read failure")
}

type errorReader struct{}

func (errorReader) Read(_ []byte) (int, error) { return 0, errors.New("read failure") }
