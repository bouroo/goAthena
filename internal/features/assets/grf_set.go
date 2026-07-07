// Package assets provides the GRF-backed HTTP asset server for roBrowser.
// It opens GRF archives from a configured directory, serves files over
// HTTP with CORS headers, and caches decompressed payloads in an LRU.
package assets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	infraassets "github.com/bouroo/goAthena/internal/infrastructure/assets"
)

// ErrNotFound is returned when no archive in the set contains the file.
var ErrNotFound = errors.New("assets: file not found")

// GRFSet manages multiple GRF archives opened from a single directory.
// Files are searched in lexicographic order; the first match wins.
type GRFSet struct {
	archives []*infraassets.GRF
	cache    *infraassets.Cache
}

// OpenGRFSet opens all .grf files in dir and returns a GRFSet. The
// cache is bounded to maxCacheBytes; a non-positive value disables
// eviction. Returns an error if dir does not exist or contains no .grf
// files.
func OpenGRFSet(dir string, maxCacheBytes int64) (*GRFSet, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read grf dir %q: %w", dir, err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if ext := filepath.Ext(e.Name()); ext != ".grf" {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	if len(names) == 0 {
		return nil, fmt.Errorf("no .grf files found in %q", dir)
	}

	archives := make([]*infraassets.GRF, 0, len(names))
	for _, name := range names {
		g, err := infraassets.Open(filepath.Join(dir, name))
		if err != nil {
			for _, opened := range archives {
				_ = opened.Close()
			}
			return nil, fmt.Errorf("open grf %q: %w", name, err)
		}
		archives = append(archives, g)
	}

	return &GRFSet{
		archives: archives,
		cache:    infraassets.NewCache(maxCacheBytes),
	}, nil
}

// ReadFile returns the decompressed contents of name, searching each
// archive in order. Results are cached in the LRU. Returns ErrNotFound
// when no archive contains the file.
func (gs *GRFSet) ReadFile(name string) ([]byte, error) {
	if gs.cache != nil {
		if data, ok := gs.cache.Get(name); ok {
			return data, nil
		}
	}

	var lastErr error
	for _, g := range gs.archives {
		if !g.HasFile(name) {
			continue
		}
		data, err := g.ReadFile(name)
		if err != nil {
			lastErr = err
			continue
		}
		if gs.cache != nil {
			gs.cache.Put(name, data)
		}
		return data, nil
	}

	if lastErr != nil {
		return nil, fmt.Errorf("read %q: %w", name, lastErr)
	}
	return nil, fmt.Errorf("%w: %s", ErrNotFound, name)
}

// HasFile reports whether name exists in any archive.
func (gs *GRFSet) HasFile(name string) bool {
	for _, g := range gs.archives {
		if g.HasFile(name) {
			return true
		}
	}
	return false
}

// Close releases all underlying GRF file handles. Errors from individual
// archives are joined and returned.
func (gs *GRFSet) Close() error {
	var errs []error
	for _, g := range gs.archives {
		if err := g.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
