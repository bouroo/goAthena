package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog"
	"gopkg.in/yaml.v3"
)

// ShopDef is the YAML-deserialized shop NPC definition (from data/shop/*.yml).
type ShopDef struct {
	Name   string        `yaml:"name"`
	Map    string        `yaml:"map"`
	X      int           `yaml:"x"`
	Y      int           `yaml:"y"`
	Facing int           `yaml:"facing"`
	Sprite int           `yaml:"sprite"`
	Items  []ShopItemDef `yaml:"items"`
}

// ShopItemDef is one catalog entry: item ID and its buy price in zeny.
type ShopItemDef struct {
	NameID uint32 `yaml:"name_id"`
	Price  uint32 `yaml:"price"`
}

// ShopFile is the top-level YAML structure (one file may declare many shops).
type ShopFile struct {
	Shops []ShopDef `yaml:"shops"`
}

// LoadShopDefs walks root for *.yml files, deserializes each into a flat
// ShopDef slice. Per-file failures (open, read, parse) are logged as warnings
// via lg and skipped — one bad or unsupported file must not abort the whole
// walk, matching the documented graceful-degradation contract used by
// LoadScripts and world/di.go's loadMobDB/loadItemDB. Returns nil if no
// shop file did parse. The walk error itself is propagated only when the
// root is unreadable; partial results gathered up to that point are
// returned alongside so the caller can degrade rather than fail boot.
//
// When lg is nil, warnings are silently dropped (kept here so callers that
// have not yet resolved a logger, e.g. tests, can still exercise the loader).
func LoadShopDefs(root string, lg *zerolog.Logger) ([]ShopDef, error) {
	warn := func() func(string, error) {
		if lg == nil {
			return func(string, error) {}
		}
		return func(file string, err error) {
			lg.Warn().Err(err).Str("file", file).Msg("content: skipping shop file")
		}
	}()

	var defs []ShopDef
	walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".yml") {
			return nil
		}

		f, oErr := os.Open(path) //nolint:gosec // path is a config-controlled shop file
		if oErr != nil {
			warn(path, fmt.Errorf("open: %w", oErr))
			return nil
		}
		src, readErr := io.ReadAll(f)
		_ = f.Close() //nolint:errcheck // best-effort close after read
		if readErr != nil {
			warn(path, fmt.Errorf("read: %w", readErr))
			return nil
		}

		var file ShopFile
		if decErr := yaml.Unmarshal(src, &file); decErr != nil {
			warn(path, fmt.Errorf("parse: %w", decErr))
			return nil
		}
		defs = append(defs, file.Shops...)
		return nil
	})
	if walkErr != nil {
		return defs, fmt.Errorf("walk shop root %q: %w", root, walkErr)
	}
	return defs, nil
}
