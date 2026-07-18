// Package mapindex loads rAthena's db/map_index.txt into a bidirectional
// registry that maps map names to numeric indices (and vice versa) for
// inter-server communication.
package mapindex

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Registry maps map names to numeric indices and indices to map names.
type Registry struct {
	byName  map[string]int
	byIndex map[int]string
}

// Load parses map_index.txt from r and builds a Registry.
//
// Format rules:
//   - Lines starting with "//" are comments and are skipped.
//   - Blank or whitespace-only lines are skipped.
//   - "mapname<whitespace>index" assigns the explicit index.
//   - "mapname" auto-increments the previous index by 1.
//   - Index 0 is reserved for error status and is rejected.
func Load(r io.Reader) (*Registry, error) {
	byName := make(map[string]int)
	byIndex := make(map[int]string)

	scanner := bufio.NewScanner(r)
	nextIndex := 1

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "//") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		mapName := fields[0]
		if _, exists := byName[mapName]; exists {
			return nil, fmt.Errorf("map_index: duplicate map name %q", mapName)
		}

		var index int
		if len(fields) >= 2 {
			parsed, err := strconv.Atoi(fields[1])
			if err != nil {
				return nil, fmt.Errorf("map_index: parse index for %q: %w", mapName, err)
			}
			index = parsed
			nextIndex = index + 1
		} else {
			index = nextIndex
			nextIndex++
		}

		if index == 0 {
			return nil, fmt.Errorf("map_index: index 0 is reserved for error status, map %q", mapName)
		}

		if existing, exists := byIndex[index]; exists {
			return nil, fmt.Errorf("map_index: duplicate index %d for map %q (already used by %q)", index, mapName, existing)
		}

		byName[mapName] = index
		byIndex[index] = mapName
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("map_index: read: %w", err)
	}

	return &Registry{byName: byName, byIndex: byIndex}, nil
}

// LoadFile opens path and calls Load.
func LoadFile(path string) (*Registry, error) {
	f, err := os.Open(path) // #nosec G304 -- path is operator-configured map_index.txt, not user input
	if err != nil {
		return nil, fmt.Errorf("open map_index %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return Load(f)
}

// Get returns the numeric index for a map name. The second return value is
// false if the name is not registered.
func (reg *Registry) Get(mapName string) (int, bool) {
	if reg == nil {
		return 0, false
	}
	idx, ok := reg.byName[mapName]
	return idx, ok
}

// NameAt returns the map name for a numeric index. The second return value
// is false if the index is not registered.
func (reg *Registry) NameAt(index int) (string, bool) {
	if reg == nil {
		return "", false
	}
	name, ok := reg.byIndex[index]
	return name, ok
}

// Len returns the number of registered maps.
func (reg *Registry) Len() int {
	if reg == nil {
		return 0
	}
	return len(reg.byName)
}
