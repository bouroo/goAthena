//go:build unit

package itemdb

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fixtureYAML = `Header:
  Type: ITEM_DB
  Version: 3

Body:
  - Id: 1101
    AegisName: Sword
    Name: Sword
    Type: Weapon
    SubType: 1hSword
    Buy: 100
    Sell: 50
    Weight: 500
    Attack: 25
    Defense: 2
    Range: 1
    Slots: 3
    WeaponLevel: 1
    ArmorLevel: 2
    EquipLevelMin: 10
    EquipLevelMax: 99
    Refineable: true
    View: 2
    UnknownScalar: ignored
    Jobs:
      All: true
    Script: |
      bonus bStr,1;
  - Id: 909
    AegisName: Jellopy
    Name: Jellopy
    Weight: 10
`

func loadFixture(t *testing.T) *Registry {
	t.Helper()
	reg, err := Load(strings.NewReader(fixtureYAML))
	require.NoError(t, err)
	require.NotNil(t, reg)
	return reg
}

func TestLoad_ParsesAllScalarFields(t *testing.T) {
	reg := loadFixture(t)

	sword := reg.Get(1101)
	require.NotNil(t, sword)
	assert.Equal(t, int32(1101), sword.Id)
	assert.Equal(t, "Sword", sword.AegisName)
	assert.Equal(t, "Sword", sword.Name)
	assert.Equal(t, "Weapon", sword.Type)
	assert.Equal(t, "1hSword", sword.SubType)
	assert.Equal(t, int32(100), sword.Buy)
	assert.Equal(t, int32(50), sword.Sell)
	assert.Equal(t, int32(500), sword.Weight)
	assert.Equal(t, int32(25), sword.Attack)
	assert.Equal(t, int32(2), sword.Defense)
	assert.Equal(t, int32(1), sword.Range)
	assert.Equal(t, int32(3), sword.Slots)
	assert.Equal(t, int32(1), sword.WeaponLevel)
	assert.Equal(t, int32(2), sword.ArmorLevel)
	assert.Equal(t, int32(10), sword.EquipLevelMin)
	assert.Equal(t, int32(99), sword.EquipLevelMax)
	assert.True(t, sword.Refineable)
	assert.Equal(t, int32(2), sword.View)
}

func TestLoad_DefaultsTypeAndIgnoresUnknownFields(t *testing.T) {
	reg := loadFixture(t)

	jellopy := reg.Get(909)
	require.NotNil(t, jellopy)
	assert.Equal(t, "Etc", jellopy.Type)
	assert.Equal(t, int32(10), jellopy.Weight)
}

func TestRegistry_GetLenAndWeight(t *testing.T) {
	reg := loadFixture(t)

	assert.Equal(t, 2, reg.Len())
	assert.Equal(t, uint32(500), reg.Weight(1101))
	assert.Equal(t, uint32(10), reg.Weight(909))
	assert.Nil(t, reg.Get(9999))
	assert.Equal(t, uint32(0), reg.Weight(9999))
	assert.Equal(t, uint32(0), reg.Weight(^uint32(0)))
	assert.Nil(t, (*Registry)(nil).Get(1101))
	assert.Equal(t, 0, (*Registry)(nil).Len())
	assert.Equal(t, uint32(0), (*Registry)(nil).Weight(1101))
}

func TestRegistry_WeightClampsNegativeValues(t *testing.T) {
	reg, err := Load(strings.NewReader(`Header:
  Type: ITEM_DB
  Version: 3
Body:
  - Id: 1
    Weight: -10
`))
	require.NoError(t, err)
	assert.Equal(t, uint32(0), reg.Weight(1))
}

func TestLoad_DuplicateIDLastWins(t *testing.T) {
	reg, err := Load(strings.NewReader(`Header:
  Type: ITEM_DB
  Version: 3
Body:
  - Id: 1
    Name: First
  - Id: 1
    Name: Last
`))
	require.NoError(t, err)
	assert.Equal(t, 1, reg.Len())
	assert.Equal(t, "Last", reg.Get(1).Name)
}

func TestLoad_SkipsNullBodyEntries(t *testing.T) {
	reg, err := Load(strings.NewReader(`Header:
  Type: ITEM_DB
  Version: 3
Body:
  - null
`))
	require.NoError(t, err)
	assert.Equal(t, 0, reg.Len())
}

func TestLoad_RejectsInvalidHeader(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		message string
	}{
		{name: "wrong type", header: "Type: MOB_DB\n  Version: 3", message: "MOB_DB"},
		{name: "wrong version", header: "Type: ITEM_DB\n  Version: 2", message: "Version 2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(strings.NewReader("Header:\n  " + tt.header + "\nBody: []\n"))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.message)
		})
	}
}

func TestLoad_MalformedYAML(t *testing.T) {
	_, err := Load(strings.NewReader("not: [valid: yaml: at: all"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse item_db yaml")
}

func TestLoad_ReadError(t *testing.T) {
	_, err := Load(errorReader{})
	require.Error(t, err)
	// The streaming decoder surfaces the underlying read failure while
	// decoding, so it is wrapped under the parse context.
	assert.Contains(t, err.Error(), "parse item_db yaml")
	assert.Contains(t, err.Error(), "read failure")
}

func TestLoadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "item_db.yml")
	require.NoError(t, os.WriteFile(path, []byte(fixtureYAML), 0o600))

	reg, err := LoadFile(path)
	require.NoError(t, err)
	assert.Equal(t, 2, reg.Len())
}

func TestLoadFile_Missing(t *testing.T) {
	_, err := LoadFile(t.TempDir() + "/missing.yml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "open item_db")
}

type errorReader struct{}

func (errorReader) Read(_ []byte) (int, error) {
	return 0, errors.New("read failure")
}
