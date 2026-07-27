// Package infra adapts the world bounded context to its infrastructure. Its
// first adapter is the filesystem-backed MapStore: it reads .gat/.rsw pairs from
// the configured map directory and builds the per-map AOI grid and A* pathfinder
// the world domain defines, caching each so a map loads once and serves every
// entity on it.
package infra

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/pathfinding"
	"github.com/bouroo/goAthena/pkg/ro/romap"
)

// FileMapStore loads maps from a directory of .gat/.rsw file pairs, caching the
// built spatial foundation per map name so repeated loads of the same map reuse
// one AOI grid and pathfinder. It is safe for concurrent use.
type FileMapStore struct {
	mapDir string

	mu    sync.RWMutex
	cache map[string]*domain.Map
}

// NewFileMapStore returns a FileMapStore rooted at mapDir. mapDir may be
// relative; it is resolved against the process working directory at Load time,
// matching how the server runs under `task serve` (cwd = repo root).
func NewFileMapStore(mapDir string) *FileMapStore {
	return &FileMapStore{mapDir: mapDir, cache: make(map[string]*domain.Map)}
}

// Load reads <mapDir>/<name>.gat and <mapDir>/<name>.rsw, parses them into a
// romap.MapData, and builds the per-map AOI grid and pathfinder. The .gat is
// mandatory; a missing .rsw is tolerated (romap reports no water plane). The
// result is cached by name, so the second load of the same name returns the
// same *Map without re-reading the files.
func (s *FileMapStore) Load(_ context.Context, name string) (*domain.Map, error) {
	// Fast path under the read lock once the map is cached.
	s.mu.RLock()
	if cached, ok := s.cache[name]; ok {
		s.mu.RUnlock()
		return cached, nil
	}
	s.mu.RUnlock()

	if !validMapName(name) {
		return nil, fmt.Errorf("world: invalid map name %q", name)
	}

	gatPath := filepath.Join(s.mapDir, name+".gat")
	gat, err := os.ReadFile(gatPath) // #nosec G304 -- name is sanitized by validMapName; map_dir is operator-configured.
	if err != nil {
		return nil, fmt.Errorf("world: read %s.gat: %w", name, err)
	}
	rswPath := filepath.Join(s.mapDir, name+".rsw")
	rsw, err := os.ReadFile(rswPath) // #nosec G304 -- name is sanitized by validMapName; map_dir is operator-configured.
	if err != nil {
		// A missing .rsw is non-fatal: romap.LoadMap accepts a nil rsw and
		// reports WaterLevel = WaterAbsent. Only a hard read error (e.g.
		// permission denied) fails the load.
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("world: read %s.rsw: %w", name, err)
		}
		rsw = nil
	}

	data, err := romap.LoadMap(name, gat, rsw)
	if err != nil {
		return nil, fmt.Errorf("world: parse %s: %w", name, err)
	}

	built := &domain.Map{
		Data:       data,
		AOI:        aoi.NewGridManager(data.Width, data.Height),
		Pathfinder: pathfinding.New(pathfinding.FromMapData(data)),
	}

	// Take the write lock only to publish. Double-check: another goroutine may
	// have loaded the same map while this one held only the read lock — prefer
	// the first-built instance for identity stability and discard ours.
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.cache[name]; ok {
		return existing, nil
	}
	s.cache[name] = built
	return built, nil
}

// validMapName reports whether name is a safe, single-component map basename:
// non-empty, bounded length, no path separators, and no embedded NUL. A rAthena
// map name is a bare token like "prontera"; banning separators makes
// parent-dir traversal through filepath.Join impossible, so a name can never
// reach outside map_dir.
func validMapName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	return !strings.ContainsAny(name, `/\`) && !strings.ContainsRune(name, 0)
}
