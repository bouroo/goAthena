//go:build unit

package skilldb

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
  Type: SKILL_DB
  Version: 4
Body:
  - Id: 1
    Name: NV_BASIC
    Description: Basic Skill
    MaxLevel: 9
  - Id: 5
    Name: SM_BASH
    Description: Bash
    MaxLevel: 10
    Type: Weapon
    TargetType: Attack
    Range: -1
    Hit: Single
    Element: Weapon
    Status: Stun
    Requires:
      SpCost:
        - {Level: 1, Amount: 8}
        - {Level: 6, Amount: 15}
        - {Level: 10, Amount: 18}
  - Id: 42
    Name: WZ_FIREPILLAR
    Description: Fire Pillar
    MaxLevel: 10
    Type: Magic
    TargetType: Ground
    Range:
      - {Level: 1, Size: 3}
      - {Level: 5, Size: 5}
      - {Level: 10, Size: 9}
`

func loadFixture(t *testing.T) *Registry {
	t.Helper()
	reg, err := Load(strings.NewReader(fixtureYAML))
	require.NoError(t, err)
	require.NotNil(t, reg)
	return reg
}

func TestLoad_ParsesCoreFields(t *testing.T) {
	reg := loadFixture(t)

	nv := reg.Get(1)
	require.NotNil(t, nv)
	assert.Equal(t, int32(1), nv.ID)
	assert.Equal(t, "NV_BASIC", nv.Name)
	assert.Equal(t, "Basic Skill", nv.Description)
	assert.Equal(t, int32(9), nv.MaxLevel)
	assert.Equal(t, "", nv.Type)
	assert.Equal(t, "Passive", nv.TargetType)
	assert.Equal(t, "", nv.Hit)
	assert.Equal(t, "", nv.Element.Value)

	bash := reg.Get(5)
	require.NotNil(t, bash)
	assert.Equal(t, int32(5), bash.ID)
	assert.Equal(t, "SM_BASH", bash.Name)
	assert.Equal(t, "Bash", bash.Description)
	assert.Equal(t, int32(10), bash.MaxLevel)
	assert.Equal(t, "Weapon", bash.Type)
	assert.Equal(t, "Attack", bash.TargetType)
	assert.True(t, bash.Range.IsScalar)
	assert.Equal(t, int16(-1), bash.Range.Value)
	assert.Equal(t, "Single", bash.Hit)
	assert.True(t, bash.Element.IsScalar)
	assert.Equal(t, "Weapon", bash.Element.Value)
	assert.Equal(t, "Stun", bash.Status)

	firepillar := reg.Get(42)
	require.NotNil(t, firepillar)
	assert.False(t, firepillar.Range.IsScalar)
	assert.Empty(t, firepillar.Range.Value)
	require.Len(t, firepillar.Range.Levels, 3)
	assert.Equal(t, int16(3), firepillar.Range.Levels[0].Size)
	assert.Equal(t, int16(5), firepillar.Range.Levels[1].Size)
	assert.Equal(t, int16(9), firepillar.Range.Levels[2].Size)
}

func TestLoad_DefaultsTargetTypeToPassive(t *testing.T) {
	yamlStr := `Header:
  Type: SKILL_DB
  Version: 4
Body:
  - Id: 1
    Name: PASSIVE_ONLY
    MaxLevel: 1
  - Id: 2
    Name: EXPLICIT_TARGET
    MaxLevel: 1
    TargetType: Self
`
	reg, err := Load(strings.NewReader(yamlStr))
	require.NoError(t, err)
	assert.Equal(t, "Passive", reg.Get(1).TargetType)
	assert.Equal(t, "Self", reg.Get(2).TargetType)
}

func TestRange_At_Scalar(t *testing.T) {
	yamlStr := `Header:
  Type: SKILL_DB
  Version: 4
Body:
  - Id: 1
    Name: S
    Range: 9
`
	reg, err := Load(strings.NewReader(yamlStr))
	require.NoError(t, err)
	r := reg.Get(1).Range
	require.True(t, r.IsScalar)
	assert.Equal(t, int16(9), r.At(1))
	assert.Equal(t, int16(9), r.At(5))
	assert.Equal(t, int16(9), r.At(99))
}

func TestRange_At_PerLevel(t *testing.T) {
	yamlStr := `Header:
  Type: SKILL_DB
  Version: 4
Body:
  - Id: 1
    Name: P
    Range:
      - {Level: 1, Size: 3}
      - {Level: 5, Size: 5}
      - {Level: 10, Size: 9}
`
	reg, err := Load(strings.NewReader(yamlStr))
	require.NoError(t, err)
	r := reg.Get(1).Range
	require.False(t, r.IsScalar)
	assert.Equal(t, int16(3), r.At(1))
	assert.Equal(t, int16(5), r.At(5))
	assert.Equal(t, int16(9), r.At(10))
	assert.Equal(t, int16(0), r.At(2))
	assert.Equal(t, int16(0), r.At(11))
	assert.Equal(t, int16(0), r.At(0))
}

func TestRegistry_GetLenSpCost(t *testing.T) {
	reg := loadFixture(t)

	assert.Equal(t, 3, reg.Len())

	require.NotNil(t, reg.Get(5))
	assert.Equal(t, "SM_BASH", reg.Get(5).Name)

	assert.Nil(t, reg.Get(9999))

	assert.Equal(t, int32(8), reg.SpCostAt(5, 1))
	assert.Equal(t, int32(15), reg.SpCostAt(5, 6))
	assert.Equal(t, int32(18), reg.SpCostAt(5, 10))
	assert.Equal(t, int32(0), reg.SpCostAt(5, 7))
	assert.Equal(t, int32(0), reg.SpCostAt(9999, 1))
	assert.Equal(t, int32(0), reg.SpCostAt(1, 1))

	assert.Nil(t, (*Registry)(nil).Get(5))
	assert.Equal(t, 0, (*Registry)(nil).Len())
	assert.Equal(t, int32(0), (*Registry)(nil).SpCostAt(5, 1))
}

func TestLoad_DuplicateIDLastWins(t *testing.T) {
	reg, err := Load(strings.NewReader(`Header:
  Type: SKILL_DB
  Version: 4
Body:
  - Id: 1
    Name: First
    MaxLevel: 1
  - Id: 1
    Name: Last
    MaxLevel: 99
`))
	require.NoError(t, err)
	assert.Equal(t, 1, reg.Len())
	entry := reg.Get(1)
	require.NotNil(t, entry)
	assert.Equal(t, "Last", entry.Name)
	assert.Equal(t, int32(99), entry.MaxLevel)
}

func TestRegistry_All(t *testing.T) {
	reg := loadFixture(t)
	all := reg.All()
	require.Len(t, all, 3)
	assert.Equal(t, "SM_BASH", all[5].Name)
	assert.Equal(t, "NV_BASIC", all[1].Name)
}

func TestLoad_SkipsNullBodyEntries(t *testing.T) {
	reg, err := Load(strings.NewReader(`Header:
  Type: SKILL_DB
  Version: 4
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
		{name: "wrong type", header: "Type: ITEM_DB\n  Version: 4", message: "ITEM_DB"},
		{name: "wrong version", header: "Type: SKILL_DB\n  Version: 3", message: "Version 3"},
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
	assert.Contains(t, err.Error(), "parse skill_db yaml")
}

func TestLoad_ReadError(t *testing.T) {
	_, err := Load(errorReader{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse skill_db yaml")
	assert.Contains(t, err.Error(), "read failure")
}

func TestLoadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skill_db.yml")
	require.NoError(t, os.WriteFile(path, []byte(fixtureYAML), 0o600))

	reg, err := LoadFile(path)
	require.NoError(t, err)
	assert.Equal(t, 3, reg.Len())
}

func TestLoadFile_Missing(t *testing.T) {
	_, err := LoadFile(filepath.Join(t.TempDir(), "missing.yml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "open skill_db")
}

func TestLoad_FullFile(t *testing.T) {
	path := filepath.Join("..", "..", "..", "third_party", "rathena", "db", "pre-re", "skill_db.yml")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("rAthena submodule not available at %s: %v", path, err)
	}

	reg, err := LoadFile(path)
	require.NoError(t, err)
	require.NotNil(t, reg)
	assert.Greater(t, reg.Len(), 500, "expected >500 skills loaded from full skill_db.yml")

	bash := reg.Get(5)
	require.NotNil(t, bash, "SM_BASH (Id=5) should be present")
	assert.Equal(t, "SM_BASH", bash.Name)
	assert.Equal(t, "Bash", bash.Description)
	assert.Equal(t, int32(10), bash.MaxLevel)
	assert.Equal(t, "Weapon", bash.Type)
	assert.Equal(t, "Attack", bash.TargetType)
	assert.True(t, bash.Range.IsScalar)
	assert.Equal(t, int16(-1), bash.Range.Value)
	assert.Equal(t, int32(8), reg.SpCostAt(5, 1))
	assert.Equal(t, int32(15), reg.SpCostAt(5, 10))
}

type errorReader struct{}

func (errorReader) Read(_ []byte) (int, error) {
	return 0, errors.New("read failure")
}
