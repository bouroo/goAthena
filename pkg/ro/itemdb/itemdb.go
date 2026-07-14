// Package itemdb loads rAthena item_db*.yml (version 3) into a lookup registry.
package itemdb

import (
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// ItemEntry holds scalar fields for a single item type.
type ItemEntry struct {
	Id            int32  `yaml:"Id"` //nolint:revive // yaml tag is "Id" to match rAthena item_db.yml
	AegisName     string `yaml:"AegisName"`
	Name          string `yaml:"Name"`
	Type          string `yaml:"Type"`
	SubType       string `yaml:"SubType"`
	Buy           int32  `yaml:"Buy"`
	Sell          int32  `yaml:"Sell"`
	Weight        int32  `yaml:"Weight"`
	Attack        int32  `yaml:"Attack"`
	Defense       int32  `yaml:"Defense"`
	Range         int32  `yaml:"Range"`
	Slots         int32  `yaml:"Slots"`
	WeaponLevel   int32  `yaml:"WeaponLevel"`
	ArmorLevel    int32  `yaml:"ArmorLevel"`
	EquipLevelMin int32  `yaml:"EquipLevelMin"`
	EquipLevelMax int32  `yaml:"EquipLevelMax"`
	Refineable    bool   `yaml:"Refineable"`
	View          int32  `yaml:"View"`
}

type fileFormat struct {
	Header struct {
		Type    string `yaml:"Type"`
		Version int    `yaml:"Version"`
	} `yaml:"Header"`
	Body []*ItemEntry `yaml:"Body"`
}

// Registry provides thread-safe lookup of item entries by ID.
type Registry struct {
	entries map[int32]*ItemEntry
}

// Load parses a rAthena item_db YAML file from an io.Reader and returns a Registry.
// It expects the rAthena YAML format with Header.Type=="ITEM_DB" and Header.Version==3.
// Unknown fields are silently ignored. The document is streamed through
// yaml.NewDecoder to avoid buffering the entire (multi-MB) item_db.yml
// in memory before decoding.
func Load(r io.Reader) (*Registry, error) {
	var f fileFormat
	if err := yaml.NewDecoder(r).Decode(&f); err != nil {
		return nil, fmt.Errorf("parse item_db yaml: %w", err)
	}

	if f.Header.Type != "ITEM_DB" {
		return nil, fmt.Errorf("item_db: unexpected Header.Type %q (want %q)", f.Header.Type, "ITEM_DB")
	}
	if f.Header.Version != 3 {
		return nil, fmt.Errorf("item_db: unsupported Header.Version %d (want 3)", f.Header.Version)
	}

	entries := make(map[int32]*ItemEntry, len(f.Body))
	for _, entry := range f.Body {
		if entry == nil {
			continue
		}
		if entry.Type == "" {
			entry.Type = "Etc"
		}
		entries[entry.Id] = entry
	}
	return &Registry{entries: entries}, nil
}

// LoadFile is a convenience wrapper that opens a file and calls Load.
func LoadFile(path string) (*Registry, error) {
	f, err := os.Open(path) // #nosec G304 -- path is operator-configured item_db.yml, not user input
	if err != nil {
		return nil, fmt.Errorf("open item_db %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return Load(f)
}

// Get returns the ItemEntry for the given item ID, or nil if not found.
func (reg *Registry) Get(id int32) *ItemEntry {
	if reg == nil {
		return nil
	}
	return reg.entries[id]
}

// Len returns the number of loaded item entries.
func (reg *Registry) Len() int {
	if reg == nil {
		return 0
	}
	return len(reg.entries)
}

// Weight returns the non-negative weight for the given item name ID.
func (reg *Registry) Weight(nameID uint32) uint32 {
	const maxItemID = uint32(1<<31 - 1)
	if nameID > maxItemID {
		return 0
	}

	entry := reg.Get(int32(nameID))
	if entry == nil || entry.Weight < 0 {
		return 0
	}
	return uint32(entry.Weight)
}
