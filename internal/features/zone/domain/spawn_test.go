//go:build unit

package domain_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/features/zone/domain"
)

const validSpawnYAML = `spawns:
  - mob_id: 1002
    count: 1
    x: 155
    y: 165
    x_range: 0
    y_range: 0
    respawn_ms: 5000
  - mob_id: 1063
    count: 2
    x: 165
    y: 175
    x_range: 5
    y_range: 3
    respawn_ms: 8000
`

func TestLoadMobSpawns_Valid(t *testing.T) {
	cfg, err := domain.LoadMobSpawns(strings.NewReader(validSpawnYAML))
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Len(t, cfg.Spawns, 2)

	first := cfg.Spawns[0]
	assert.Equal(t, 1002, first.MobID)
	assert.Equal(t, 1, first.Count)
	assert.Equal(t, 155, first.X)
	assert.Equal(t, 165, first.Y)
	assert.Equal(t, 0, first.XRange)
	assert.Equal(t, 0, first.YRange)
	assert.Equal(t, 5000, first.RespawnMs)

	second := cfg.Spawns[1]
	assert.Equal(t, 1063, second.MobID)
	assert.Equal(t, 2, second.Count)
	assert.Equal(t, 165, second.X)
	assert.Equal(t, 175, second.Y)
	assert.Equal(t, 5, second.XRange)
	assert.Equal(t, 3, second.YRange)
	assert.Equal(t, 8000, second.RespawnMs)
}

func TestLoadMobSpawns_Empty(t *testing.T) {
	cfg, err := domain.LoadMobSpawns(strings.NewReader(""))
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Empty(t, cfg.Spawns)
}

func TestLoadMobSpawns_Invalid(t *testing.T) {
	// Unbalanced brackets — guaranteed parse failure.
	const bad = "spawns: [unterminated"
	cfg, err := domain.LoadMobSpawns(strings.NewReader(bad))
	require.Error(t, err)
	assert.Nil(t, cfg)
}
