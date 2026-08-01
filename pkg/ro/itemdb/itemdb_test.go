//go:build unit

package itemdb

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/pkg/ro/equip"
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
    Locations:
      Right_Hand: true
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

func TestItemEntry_HealParsesItemhealForms(t *testing.T) {
	t.Parallel()
	const yaml = `Header:
  Type: ITEM_DB
  Version: 3
Body:
  - Id: 503
    Name: Red_Potion
    Type: Healing
    Script: |
      itemheal rand(175,235),0;
  - Id: 504
    Name: White_Potion
    Type: Healing
    Script: |
      itemheal rand(325,405),0;
  - Id: 505
    Name: Blue_Potion
    Type: Healing
    Script: |
      itemheal 0,rand(40,60);
  - Id: 600
    Name: Fixed
    Type: Healing
    Script: itemheal 100,50;
  - Id: 601
    Name: NoScript
    Type: Etc
`
	reg, err := Load(strings.NewReader(yaml))
	require.NoError(t, err)

	for _, test := range []struct {
		id           int32
		hpMin, hpMax int32
		spMin, spMax int32
	}{
		{503, 175, 235, 0, 0},
		{504, 325, 405, 0, 0},
		{505, 0, 0, 40, 60},
		{600, 100, 100, 50, 50},
	} {
		t.Run(strconv.FormatInt(int64(test.id), 10), func(t *testing.T) {
			hpMin, hpMax, spMin, spMax, ok := reg.Get(test.id).Heal()
			assert.True(t, ok)
			assert.Equal(t, test.hpMin, hpMin)
			assert.Equal(t, test.hpMax, hpMax)
			assert.Equal(t, test.spMin, spMin)
			assert.Equal(t, test.spMax, spMax)
		})
	}
	_, _, _, _, ok := reg.Get(601).Heal()
	assert.False(t, ok)
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
	// Locations block folds into the EQP_* bitmask the equip use case reads.
	assert.Equal(t, equip.HandRight, sword.EquipLocations, "Right_Hand → EQP_HAND_R (2)")
}

func TestLoad_DefaultsTypeAndIgnoresUnknownFields(t *testing.T) {
	reg := loadFixture(t)

	jellopy := reg.Get(909)
	require.NotNil(t, jellopy)
	assert.Equal(t, "Etc", jellopy.Type)
	assert.Equal(t, int32(10), jellopy.Weight)
	// A non-equip item has no Locations block, so its bitmask is zero — the value
	// the equip use case reads to reject a non-equipment item.
	assert.Equal(t, uint32(0), jellopy.EquipLocations)
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

func TestRegistry_ByAegisName(t *testing.T) {
	t.Parallel()

	reg := loadFixture(t)

	// Hit — resolves the numeric id the wire format needs.
	jellopy := reg.ByAegisName("Jellopy")
	require.NotNil(t, jellopy)
	assert.Equal(t, int32(909), jellopy.Id)

	sword := reg.ByAegisName("Sword")
	require.NotNil(t, sword)
	assert.Equal(t, int32(1101), sword.Id)

	// Miss and empty name never panic and return nil.
	assert.Nil(t, reg.ByAegisName("DoesNotExist"))
	assert.Nil(t, reg.ByAegisName(""))

	// Nil receiver never panics.
	assert.Nil(t, (*Registry)(nil).ByAegisName("Jellopy"))
}

func TestLoad_DuplicateAegisNameLastWins(t *testing.T) {
	t.Parallel()

	reg, err := Load(strings.NewReader(`Header:
  Type: ITEM_DB
  Version: 3
Body:
  - Id: 1
    AegisName: Dup
    Name: First
  - Id: 2
    AegisName: Dup
    Name: Last
`))
	require.NoError(t, err)
	entry := reg.ByAegisName("Dup")
	require.NotNil(t, entry)
	assert.Equal(t, "Last", entry.Name)
	assert.Equal(t, int32(2), entry.Id)
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

// TestLoad_DuplicateMappingKeyLastWins covers the rathenaThailand data quirk
// where item_db_equip.yml repeats a Trade: block within one item
// (lines 169692/169703). yaml.v3 rejects duplicate mapping keys when decoding
// into a struct; the loader tolerates them, last occurrence wins, matching
// rAthena's loader. Without this the whole item_db load aborts and item drops,
// equip, and use-item healing all break at boot.
func TestLoad_DuplicateMappingKeyLastWins(t *testing.T) {
	t.Parallel()

	t.Run("unknown duplicate key tolerated", func(t *testing.T) {
		t.Parallel()

		reg, err := Load(strings.NewReader(`Header:
  Type: ITEM_DB
  Version: 3
Body:
  - Id: 1
    AegisName: Helm
    Name: Helm
    Trade:
      NoDraw: false
      NoTrade: true
    Trade:
      NoDraw: true
`))
		require.NoError(t, err)
		require.Equal(t, 1, reg.Len())
		assert.Equal(t, "Helm", reg.Get(1).Name)
	})

	t.Run("known duplicate field last wins", func(t *testing.T) {
		t.Parallel()

		reg, err := Load(strings.NewReader(`Header:
  Type: ITEM_DB
  Version: 3
Body:
  - Id: 2
    AegisName: Knife
    Defense: 5
    Defense: 42
`))
		require.NoError(t, err)
		assert.Equal(t, int32(42), reg.Get(2).Defense)
	})
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
