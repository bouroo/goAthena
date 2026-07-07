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
	"strings"

	"github.com/rs/zerolog"

	infraassets "github.com/bouroo/goAthena/internal/infrastructure/assets"
)

// ErrNotFound is returned when no archive in the set contains the file.
var ErrNotFound = errors.New("assets: file not found")

// GRFSet manages multiple GRF archives opened from a single directory.
// Files are searched in lexicographic order; the first match wins.
type GRFSet struct {
	archives      []*infraassets.GRF
	cache         *infraassets.Cache
	maxCacheBytes int64
	logger        *zerolog.Logger
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
		if ext := strings.ToLower(filepath.Ext(e.Name())); ext != ".grf" {
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
		archives:      archives,
		cache:         infraassets.NewCache(maxCacheBytes),
		maxCacheBytes: maxCacheBytes,
	}, nil
}

// AttachLogger wires a logger so ReadFile can emit a debug line when a
// payload is too large to cache. Optional; nil disables logging.
func (gs *GRFSet) AttachLogger(logger zerolog.Logger) {
	l := logger.With().Str("component", "assets").Logger()
	gs.logger = &l
}

// ReadFile returns the decompressed contents of name, searching each
// archive in order. Results are cached in the LRU. Returns ErrNotFound
// when no archive contains the file.
//
// Payloads larger than the cache budget are intentionally not cached:
// the LRU eviction loop would otherwise drop every other entry to make
// room for one oversized file, wasting the budget.
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
		if gs.cache != nil && (gs.maxCacheBytes <= 0 || int64(len(data)) <= gs.maxCacheBytes) {
			gs.cache.Put(name, data)
		} else if gs.cache != nil && gs.maxCacheBytes > 0 {
			if gs.logger != nil {
				gs.logger.Debug().
					Str("path", name).
					Int("size", len(data)).
					Int64("budget", gs.maxCacheBytes).
					Msg("skip cache: payload exceeds budget")
			}
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

// Shutdown releases all GRF file handles. It satisfies
// do.ShutdownerWithError so samber/do/v2 calls it during
// injector.Shutdown().
func (gs *GRFSet) Shutdown() error {
	return gs.Close()
}
