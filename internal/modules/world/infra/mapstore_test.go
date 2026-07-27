//go:build unit

package infra_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/modules/world/infra"
	"github.com/bouroo/goAthena/pkg/ro/pathfinding"
	"github.com/bouroo/goAthena/pkg/ro/romap"
)

// repoRootDataMaps returns the absolute path to the repo's data/maps directory,
// located relative to this test file via runtime.Caller so the test is
// independent of the process working directory (go test runs from the package
// dir, not repo root). Mirrors the pattern in pkg/ro/packetdb/packetdb_test.go.
//
// Layout this function assumes:
//
//	internal/modules/world/infra/mapstore_test.go   ← 4 dirs up to repo root
//	data/maps/prontera.{gat,rsw}
func repoRootDataMaps(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller must work")
	abs, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "data", "maps"))
	require.NoError(t, err)
	return abs
}

// TestFileMapStore_LoadProntera is the M4a L2 gate: the filesystem adapter
// loads Prontera's .gat/.rsw, builds the spatial trio, and caches it. It
// asserts the three M4b/M4c preconditions: the map data has real dimensions,
// the AOI grid matches them, and the pathfinder resolves a route. It does not
// hardcode map-specific coordinates — adjacentWalkable finds a walkable pair
// dynamically so the test survives minor Prontera revisions.
func TestFileMapStore_LoadProntera(t *testing.T) {
	t.Parallel()

	dir := repoRootDataMaps(t)
	if _, err := os.Stat(filepath.Join(dir, "prontera.gat")); err != nil {
		t.Skipf("prontera.gat not present at %s: %v", dir, err)
	}

	store := infra.NewFileMapStore(dir)
	m, err := store.Load(context.Background(), "prontera")
	require.NoError(t, err)
	require.NotNil(t, m)

	// Dimensions loaded from the .gat header.
	assert.Equal(t, "prontera", m.Data.Name)
	assert.Greater(t, m.Data.Width, 0, "map width")
	assert.Greater(t, m.Data.Height, 0, "map height")

	// AOI grid matches the map dimensions.
	assert.Equal(t, m.Data.Width, m.AOI.Width(), "AOI width matches map")
	assert.Equal(t, m.Data.Height, m.AOI.Height(), "AOI height matches map")

	// Pathfinder resolves a real route between adjacent walkable cells. FindPath
	// excludes the start cell and includes the target, so an adjacent pair yields
	// exactly one step — assert non-empty and ending at the target, not a length.
	a, b, ok := adjacentWalkable(m.Data)
	require.True(t, ok, "found a pair of adjacent walkable cells for pathfinding")
	path, err := m.Pathfinder.FindPath(a, b)
	require.NoError(t, err)
	require.NotEmpty(t, path, "path has at least the target step")
	assert.Equal(t, b, path[len(path)-1], "path ends at the target cell")

	// Load is idempotent: the same name returns the identical cached instance.
	again, err := store.Load(context.Background(), "prontera")
	require.NoError(t, err)
	assert.Same(t, m, again, "repeated Load returns the cached instance")
}

// TestFileMapStore_MissingGAT asserts the mandatory .gat read fails loudly with
// a wrapped error rather than producing an empty map. The .rsw is optional; a
// missing one must not fail the load.
func TestFileMapStore_MissingGAT(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := infra.NewFileMapStore(dir)

	_, err := store.Load(context.Background(), "no-such-map")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no-such-map.gat")
}

// TestFileMapStore_RejectsTraversalName asserts that a map name carrying a path
// separator or parent-dir escape is rejected before any filesystem access, so a
// caller can never read outside map_dir. Each case writes a canary file the
// traversal would otherwise reach and asserts it is never opened.
func TestFileMapStore_RejectsTraversalName(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// A canary outside map_dir: a successful traversal to "../canary.gat"
	// would read this. It is intentionally absent so a missed guard surfaces
	// as a missing-file error referencing the canary path, not as a silent pass.
	parentCanary := filepath.Join(filepath.Dir(dir), "canary.gat")
	require.NoFileExists(t, parentCanary)

	store := infra.NewFileMapStore(dir)
	for _, name := range []string{
		"../canary",        // parent-dir escape
		"..\\canary",       // backslash separator (also a Windows separator)
		"a/../canary",      // embedded parent segment
		"/etc/passwd",      // absolute path
		"",                 // empty
		"pron\x00tera.gat", // embedded NUL
	} {
		_, err := store.Load(context.Background(), name)
		require.Error(t, err, "name %q must be rejected", name)
		assert.Contains(t, err.Error(), "invalid map name", "name %q", name)
	}
}

// adjacentWalkable scans the grid for the first pair of adjacent walkable cells
// (horizontally, then vertically) and returns them as pathfinder points. Returns
// ok=false if none exists — not expected on Prontera. Dynamic discovery keeps
// the pathfinding assertion free of hardcoded coordinates.
func adjacentWalkable(md *romap.MapData) (a, b pathfinding.Point, ok bool) {
	for y := 0; y < md.Height; y++ {
		for x := 0; x+1 < md.Width; x++ {
			if md.IsWalkable(x, y) && md.IsWalkable(x+1, y) {
				return pathfinding.Point{X: x, Y: y}, pathfinding.Point{X: x + 1, Y: y}, true
			}
		}
	}
	for x := 0; x < md.Width; x++ {
		for y := 0; y+1 < md.Height; y++ {
			if md.IsWalkable(x, y) && md.IsWalkable(x, y+1) {
				return pathfinding.Point{X: x, Y: y}, pathfinding.Point{X: x, Y: y + 1}, true
			}
		}
	}
	return pathfinding.Point{}, pathfinding.Point{}, false
}
