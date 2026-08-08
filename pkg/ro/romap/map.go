// Package romap: loaders for RO map files (.gat, .rsw) used by zone-service
// to build the walkability + height grids consumed by the AOI tower-grid
// (pkg/ro/aoi) and the A* pathfinder (pkg/ro/pathfinding).
package romap

import (
	"fmt"
)

// WaterAbsent is returned by LoadMap when the .rsw has no water plane.
const WaterAbsent = rswNoWater

// MapData is the in-memory result of .gat + .rsw loading. Heights and
// Walkable are row-major with index y*Width + x.
//
// Walkable encodes the post-translation walkability bit from .gat type codes
// (cells of type 0 "walkable ground" and type 3 "walkable water" are walkable;
// all other types are walls — see rAthena map_gat2cell).
type MapData struct {
	Name       string
	Width      int
	Height     int
	Walkable   []bool
	Heights    []float32
	WaterLevel float32
}

// LoadMap parses a .gat buffer and (optionally) a .rsw buffer into a MapData.
// The .gat buffer is mandatory; the .rsw may be nil (WaterLevel = WaterAbsent).
//
// Both byte slices are pure binary contents of the file. Decoding from a GRF
// archive is the caller's responsibility (it lives outside this package).
func LoadMap(name string, gat, rsw []byte) (*MapData, error) {
	width, height, walkable, heights, err := parseGAT(gat)
	if err != nil {
		return nil, fmt.Errorf("romap: parse %s.gat: %w", name, err)
	}

	md := &MapData{
		Name:       name,
		Width:      width,
		Height:     height,
		Walkable:   walkable,
		Heights:    heights,
		WaterLevel: WaterAbsent,
	}

	if rsw != nil {
		level, err := parseRSW(rsw)
		if err != nil {
			// Soft-fail: a malformed .rsw shouldn't block map load. Mirror
			// rAthena's behavior of returning RSW_NO_WATER on any rsw error.
			md.WaterLevel = WaterAbsent
		} else {
			md.WaterLevel = float32(level)
		}
	}

	// Note: rAthena's src/map/map.cpp:3884-3885 reclassifies walkable-ground
	// (type 0) cells as walkable-water (type 3) when their height exceeds the
	// .rsw water level. Both are already counted as walkable in our MapData,
	// so the override is a no-op for walkability — we expose WaterLevel
	// verbatim and let AOI/pathfinding consume it directly.

	return md, nil
}

// IsWalkable returns whether (x, y) is a walkable cell. Out-of-bounds
// coordinates are treated as walls (consistent with rAthena's
// map_getcellp bounds check — see src/map/map.cpp:3330-3331).
func (m *MapData) IsWalkable(x, y int) bool {
	if x < 0 || y < 0 || x >= m.Width || y >= m.Height {
		return false
	}
	return m.Walkable[y*m.Width+x]
}

// HeightAt returns the average cell height at (x, y). Returns 0 out of bounds.
func (m *MapData) HeightAt(x, y int) float32 {
	if x < 0 || y < 0 || x >= m.Width || y >= m.Height {
		return 0
	}
	return m.Heights[y*m.Width+x]
}
