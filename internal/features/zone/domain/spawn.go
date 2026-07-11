package domain

import (
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// MobSpawnEntry defines a single mob spawn group for a map.
type MobSpawnEntry struct {
	MobID     int `yaml:"mob_id"`
	Count     int `yaml:"count"`
	X         int `yaml:"x"`
	Y         int `yaml:"y"`
	XRange    int `yaml:"x_range"`    // random offset range from X
	YRange    int `yaml:"y_range"`    // random offset range from Y
	RespawnMs int `yaml:"respawn_ms"` // respawn delay in ms (default 5000)
}

// MobSpawnConfig is a per-map list of mob spawn groups.
type MobSpawnConfig struct {
	Spawns []MobSpawnEntry `yaml:"spawns"`
}

// LoadMobSpawns reads a YAML spawn config from r.
func LoadMobSpawns(r io.Reader) (*MobSpawnConfig, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read mob spawn config: %w", err)
	}
	var cfg MobSpawnConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse mob spawn config: %w", err)
	}
	return &cfg, nil
}

// LoadMobSpawnsFile opens path and calls LoadMobSpawns.
func LoadMobSpawnsFile(path string) (*MobSpawnConfig, error) {
	f, err := os.Open(path) // #nosec G304 -- path is operator-configured map spawn file, not user input
	if err != nil {
		return nil, fmt.Errorf("open spawn config %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return LoadMobSpawns(f)
}
