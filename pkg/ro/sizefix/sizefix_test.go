//go:build unit

package sizefix

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fixtureYAML = `Header:
  Type: SIZE_FIX_DB
  Version: 1
Body:
  - Weapon: Knuckle
    Medium: 75
    Large: 50
  - Weapon: Whip
    Large: 50
`

func TestLoad_KnownModifiersAndDefaults(t *testing.T) {
	tbl, err := Load(strings.NewReader(fixtureYAML))
	require.NoError(t, err)

	// Knuckle: Small (omitted -> default 100), Medium 75, Large 50.
	assert.Equal(t, 100, tbl.Rate("Knuckle", "Small"))
	assert.Equal(t, 75, tbl.Rate("Knuckle", "Medium"))
	assert.Equal(t, 50, tbl.Rate("Knuckle", "Large"))
	// Whip: only Large deviates (50); Small and Medium default to 100.
	assert.Equal(t, 100, tbl.Rate("Whip", "Small"))
	assert.Equal(t, 100, tbl.Rate("Whip", "Medium"))
	assert.Equal(t, 50, tbl.Rate("Whip", "Large"))
	// An unlisted weapon (Dagger is absent from the YAML) defaults to 100
	// across all sizes — rAthena lists only deviations, not every weapon.
	assert.Equal(t, 100, tbl.Rate("Dagger", "Small"))
	assert.Equal(t, 100, tbl.Rate("Dagger", "Medium"))
	assert.Equal(t, 100, tbl.Rate("Dagger", "Large"))
	assert.Equal(t, 2, tbl.Len())
}

func TestRate_UnknownSizeAndNil(t *testing.T) {
	tbl, err := Load(strings.NewReader(fixtureYAML))
	require.NoError(t, err)
	// Unknown size name resolves to identity, never panic.
	assert.Equal(t, 100, tbl.Rate("Knuckle", "Tiny"))
	// Nil/empty table resolves every lookup to identity.
	var nilTable *SizeTable
	assert.Equal(t, 100, nilTable.Rate("Knuckle", "Medium"))
	assert.Equal(t, 100, NewSizeTable().Rate("Knuckle", "Medium"))
}

func TestLoad_WrongHeaderType(t *testing.T) {
	_, err := Load(strings.NewReader(strings.Replace(fixtureYAML, "SIZE_FIX_DB", "MOB_DB", 1)))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SIZE_FIX_DB")
}

func TestLoad_MissingWeapon(t *testing.T) {
	bad := `Header:
  Type: SIZE_FIX_DB
Body:
  - Small: 100
`
	_, err := Load(strings.NewReader(bad))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing Weapon")
}

func TestLoad_ReadError(t *testing.T) {
	_, err := Load(errorReader{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse size_fix yaml")
	assert.Contains(t, err.Error(), "read failure")
}

type errorReader struct{}

func (errorReader) Read(_ []byte) (int, error) { return 0, errors.New("read failure") }
