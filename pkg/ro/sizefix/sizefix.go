// Package sizefix loads rAthena's pre-renewal size_fix.yml — the weapon-type ×
// mob-size physical damage table — into a lookup registry. The YAML lists only
// weapon types that deviate from the 100% default; every other weapon/size
// resolves to 100. The combat path multiplies a post-DEF hit by Rate/100; a
// missing or unloadable file degrades to the identity rate (100).
package sizefix

import (
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// identityRate is the rate a nil table or unknown weapon/size resolves to: 100%
// of the post-DEF damage, i.e. no size adjustment.
const identityRate = 100

// sizeRates holds the Small/Medium/Large modifiers for one weapon type.
type sizeRates struct {
	Small, Medium, Large int
}

// SizeTable answers the weapon-type × mob-size damage modifier (percent).
// Weapons absent from the file (the rAthena YAML only lists deviations) and any
// unknown size resolve to the 100% default.
type SizeTable struct {
	rates map[string]sizeRates
}

// NewSizeTable returns an empty, non-nil table whose Rate always yields the
// identity (100). It is the zero value the app wiring falls back to when
// size_fix.yml is absent or unreadable.
func NewSizeTable() *SizeTable {
	return &SizeTable{rates: make(map[string]sizeRates)}
}

// Len returns the number of loaded weapon types, or 0 for a nil/empty table.
// It is informational for startup logging.
func (t *SizeTable) Len() int {
	if t == nil {
		return 0
	}
	return len(t.rates)
}

// Rate returns the physical damage modifier (percent) for a weapon of
// weaponSubType against a mob of mobSize: 100 = neutral. A nil table, an
// unknown weapon, or an unknown size resolves to 100.
func (t *SizeTable) Rate(weaponSubType, mobSize string) int {
	if t == nil {
		return identityRate
	}
	r, ok := t.rates[weaponSubType]
	if !ok {
		return identityRate
	}
	switch sizeIndex(mobSize) {
	case 0:
		return r.Small
	case 1:
		return r.Medium
	case 2:
		return r.Large
	default:
		return identityRate
	}
}

// sizeIndex maps rAthena mob Size names (as emitted by mob_db.yml) to matrix
// columns. An unrecognized name returns -1 so Rate falls back to the identity.
func sizeIndex(name string) int {
	switch name {
	case "Small":
		return 0
	case "Medium":
		return 1
	case "Large":
		return 2
	default:
		return -1
	}
}

// rawEntry decodes one size_fix Body row. The size fields are pointers so an
// omitted column (the YAML lists only deviations from the 100 default) decodes
// to nil and is filled with the default rather than 0.
type rawEntry struct {
	Weapon string `yaml:"Weapon"`
	Small  *int   `yaml:"Small"`
	Medium *int   `yaml:"Medium"`
	Large  *int   `yaml:"Large"`
}

func (e rawEntry) rates() sizeRates {
	return sizeRates{Small: defaultIfNil(e.Small), Medium: defaultIfNil(e.Medium), Large: defaultIfNil(e.Large)}
}

func defaultIfNil(p *int) int {
	if p == nil {
		return identityRate
	}
	return *p
}

type fileFormat struct {
	Header struct {
		Type    string `yaml:"Type"`
		Version int    `yaml:"Version"`
	} `yaml:"Header"`
	Body []rawEntry `yaml:"Body"`
}

// Load parses a rAthena size_fix.yml from r and returns a SizeTable. It expects
// Header.Type=="SIZE_FIX_DB". A weapon listed more than once is a data error in
// rAthena; last write wins to stay deterministic (symmetric with the mob_db/
// item_db dup-tolerant loaders).
func Load(r io.Reader) (*SizeTable, error) {
	var f fileFormat
	if err := yaml.NewDecoder(r).Decode(&f); err != nil {
		return nil, fmt.Errorf("parse size_fix yaml: %w", err)
	}
	if f.Header.Type != "SIZE_FIX_DB" {
		return nil, fmt.Errorf("size_fix: unexpected Header.Type %q (want %q)", f.Header.Type, "SIZE_FIX_DB")
	}
	t := NewSizeTable()
	for _, e := range f.Body {
		if e.Weapon == "" {
			return nil, fmt.Errorf("size_fix: body entry missing Weapon")
		}
		t.rates[e.Weapon] = e.rates()
	}
	return t, nil
}

// LoadFile opens and parses size_fix.yml at path.
func LoadFile(path string) (*SizeTable, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open size_fix %q: %w", path, err)
	}
	defer f.Close()
	return Load(f)
}
