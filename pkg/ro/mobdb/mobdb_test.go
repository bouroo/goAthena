//go:build unit

package mobdb

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fixtureYAML = `Header:
  Type: MOB_DB
  Version: 5

Body:
  - Id: 1002
    AegisName: PORING
    Name: Poring
    Level: 1
    Hp: 50
    BaseExp: 2
    JobExp: 1
    Attack: 7
    Attack2: 10
    Defense: 0
    MagicDefense: 5
    Str: 1
    Agi: 1
    Vit: 1
    Int: 0
    Dex: 6
    Luk: 30
    WalkSpeed: 400
    AttackRange: 1
    ChaseRange: 12
    Size: Medium
    Race: Plant
    Element: Water
    ElementLevel: 1
    Ai: 02
    Drops:
      - Item: Jellopy
        Rate: 7000
      - Item: Knife_
        Rate: 100
  - Id: 1003
    AegisName: LUNATIC
    Name: Lunatic
    Level: 1
    Hp: 60
    BaseExp: 3
    JobExp: 2
    Attack: 8
    Attack2: 11
    Defense: 1
    MagicDefense: 5
    Str: 1
    Agi: 4
    Vit: 1
    Int: 0
    Dex: 8
    Luk: 35
    WalkSpeed: 300
    AttackRange: 1
    ChaseRange: 12
    Size: Small
    Race: Brute
    Element: Wind
    ElementLevel: 1
    Ai: 04
    Drops:
      - Item: Carrot
        Rate: 5000
`

func loadFixture(t *testing.T) *Registry {
	t.Helper()
	reg, err := Load(strings.NewReader(fixtureYAML))
	require.NoError(t, err)
	require.NotNil(t, reg)
	return reg
}

func TestLoad_ParsesAllFields(t *testing.T) {
	reg := loadFixture(t)

	poring := reg.Get(1002)
	require.NotNil(t, poring, "PORING (1002) should be loaded")

	assert.Equal(t, int32(1002), poring.Id)
	assert.Equal(t, "PORING", poring.AegisName)
	assert.Equal(t, "Poring", poring.Name)
	assert.Equal(t, int32(1), poring.Level)
	assert.Equal(t, int32(50), poring.Hp)
	assert.Equal(t, int32(2), poring.BaseExp)
	assert.Equal(t, int32(1), poring.JobExp)
	assert.Equal(t, int32(7), poring.Attack)
	assert.Equal(t, int32(10), poring.Attack2)
	assert.Equal(t, int32(0), poring.Defense)
	assert.Equal(t, int32(5), poring.MagicDefense)
	assert.Equal(t, int32(1), poring.Str)
	assert.Equal(t, int32(1), poring.Agi)
	assert.Equal(t, int32(1), poring.Vit)
	assert.Equal(t, int32(0), poring.Int)
	assert.Equal(t, int32(6), poring.Dex)
	assert.Equal(t, int32(30), poring.Luk)
	assert.Equal(t, int32(400), poring.WalkSpeed)
	assert.Equal(t, int32(1), poring.AttackRange)
	assert.Equal(t, int32(12), poring.ChaseRange)
	assert.Equal(t, "Medium", poring.Size)
	assert.Equal(t, "Plant", poring.Race)
	assert.Equal(t, "Water", poring.Element)
	assert.Equal(t, int32(1), poring.ElementLevel)
	assert.Equal(t, int32(2), poring.Ai, "Ai \"02\" must parse to 2")
}

func TestGet_Existing(t *testing.T) {
	reg := loadFixture(t)

	got := reg.Get(1003)
	require.NotNil(t, got)
	assert.Equal(t, "LUNATIC", got.AegisName)
	assert.Equal(t, int32(4), got.Agi)
	assert.Equal(t, int32(4), got.Ai, "Ai \"04\" must parse to 4")
}

func TestGet_Missing(t *testing.T) {
	reg := loadFixture(t)
	assert.Nil(t, reg.Get(9999))
	assert.Nil(t, reg.Get(0))
	assert.Nil(t, (*Registry)(nil).Get(1), "nil registry Get returns nil")
}

func TestDrops_Parsed(t *testing.T) {
	reg := loadFixture(t)

	poring := reg.Get(1002)
	require.NotNil(t, poring)
	require.Len(t, poring.Drops, 2)
	assert.Equal(t, DropEntry{Item: "Jellopy", Rate: 7000}, poring.Drops[0])
	assert.Equal(t, DropEntry{Item: "Knife_", Rate: 100}, poring.Drops[1])

	lunatic := reg.Get(1003)
	require.NotNil(t, lunatic)
	require.Len(t, lunatic.Drops, 1)
	assert.Equal(t, DropEntry{Item: "Carrot", Rate: 5000}, lunatic.Drops[0])
}

func TestLen(t *testing.T) {
	reg := loadFixture(t)
	assert.Equal(t, 2, reg.Len())
	assert.Equal(t, 0, (*Registry)(nil).Len())
}

func TestLoad_InvalidYAML(t *testing.T) {
	_, err := Load(strings.NewReader("not: [valid: yaml: at: all"))
	require.Error(t, err)
}

func TestLoad_WrongHeaderType(t *testing.T) {
	bad := `Header:
  Type: ITEM_DB
  Version: 5
Body: []
`
	_, err := Load(strings.NewReader(bad))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ITEM_DB")
}
