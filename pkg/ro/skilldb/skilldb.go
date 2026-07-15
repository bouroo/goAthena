// Package skilldb loads rAthena skill_db.yml (version 4) into a lookup registry.
package skilldb

import (
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// SkillEntry holds the core scalar fields of a single rAthena skill_db entry.
// Per-level lists (Range, HitCount, Element, CastTime, Cooldown, etc.) are
// intentionally not exposed in this struct; add accessors when consumers need
// them. The Range field has a polymorphic scalar-or-list shape and is handled
// via a custom UnmarshalYAML on the Range type.
type SkillEntry struct {
	ID          int32    `yaml:"Id"`
	Name        string   `yaml:"Name"`
	Description string   `yaml:"Description"`
	MaxLevel    int32    `yaml:"MaxLevel"`
	Type        string   `yaml:"Type"`
	TargetType  string   `yaml:"TargetType"`
	Hit         string   `yaml:"Hit"`
	Element     Element  `yaml:"Element"`
	Status      string   `yaml:"Status"`
	Range       Range    `yaml:"Range"`
	Requires    Requires `yaml:"Requires"`
}

// RangeLevel pairs a (1-based) skill level with a per-level integer value.
type RangeLevel struct {
	Level int16 `yaml:"Level"`
	Size  int16 `yaml:"Size"`
}

// Range is the polymorphic Range column in skill_db.yml: a plain integer
// (e.g. "Range: 9") or a per-level list (e.g. "Range: [{Level: 1, Size: 3}, ...]").
type Range struct {
	IsScalar bool
	Value    int16
	Levels   []RangeLevel
}

// UnmarshalYAML decodes the Range node, accepting either a scalar integer or a
// per-level list of {Level, Size} entries.
func (r *Range) UnmarshalYAML(value *yaml.Node) error {
	var scalar int16
	if err := value.Decode(&scalar); err == nil {
		r.IsScalar = true
		r.Value = scalar
		r.Levels = nil
		return nil
	}
	var levels []RangeLevel
	if err := value.Decode(&levels); err != nil {
		return fmt.Errorf("skill Range: expected scalar int or per-level list: %w", err)
	}
	r.IsScalar = false
	r.Value = 0
	r.Levels = levels
	return nil
}

// At returns the Range value at the given (1-based) skill level.
// For scalar ranges the value is constant; for per-level lists it returns the
// matching entry or 0 if the level is not listed.
func (r Range) At(level int) int16 {
	if r.IsScalar {
		return r.Value
	}
	for _, l := range r.Levels {
		if int(l.Level) == level {
			return l.Size
		}
	}
	return 0
}

// LevelAmount pairs a (1-based) skill level with an amount (SP, HP, Zeny, ...).
type LevelAmount struct {
	Level  int16 `yaml:"Level"`
	Amount int32 `yaml:"Amount"`
}

// Element is the polymorphic Element column in skill_db.yml: a plain string
// (constant element) or a per-level list of {Level, Element} entries.
type Element struct {
	IsScalar bool
	Value    string
	Levels   []ElementLevel
}

// ElementLevel pairs a (1-based) skill level with an element name.
type ElementLevel struct {
	Level   int16  `yaml:"Level"`
	Element string `yaml:"Element"`
}

// UnmarshalYAML decodes Element, accepting either a scalar string or a
// per-level list of {Level, Element} entries.
func (e *Element) UnmarshalYAML(value *yaml.Node) error {
	var scalar string
	if err := value.Decode(&scalar); err == nil {
		e.IsScalar = true
		e.Value = scalar
		e.Levels = nil
		return nil
	}
	var levels []ElementLevel
	if err := value.Decode(&levels); err != nil {
		return fmt.Errorf("skill Element: expected scalar string or per-level list: %w", err)
	}
	e.IsScalar = false
	e.Value = ""
	e.Levels = levels
	return nil
}

// At returns the Element at the given (1-based) skill level.
// For scalar element the value is constant; for per-level lists it returns the
// matching entry or "" if the level is not listed.
func (e Element) At(level int) string {
	if e.IsScalar {
		return e.Value
	}
	for _, l := range e.Levels {
		if int(l.Level) == level {
			return l.Element
		}
	}
	return ""
}

// SpCost is the polymorphic SpCost column inside Requires: a plain integer
// (constant cost at every level) or a per-level list. Other cost fields in
// skill_db.yml (HpCost, ZenyCost, ...) share the same shape; introduce a
// generic Amount scalar-or-list type here when consumers need them.
type SpCost struct {
	IsScalar bool
	Value    int32
	Levels   []LevelAmount
}

// UnmarshalYAML decodes SpCost, accepting either a scalar integer or a
// per-level list of {Level, Amount} entries.
func (s *SpCost) UnmarshalYAML(value *yaml.Node) error {
	var scalar int32
	if err := value.Decode(&scalar); err == nil {
		s.IsScalar = true
		s.Value = scalar
		s.Levels = nil
		return nil
	}
	var levels []LevelAmount
	if err := value.Decode(&levels); err != nil {
		return fmt.Errorf("skill SpCost: expected scalar int or per-level list: %w", err)
	}
	s.IsScalar = false
	s.Value = 0
	s.Levels = levels
	return nil
}

// At returns the SpCost amount at the given (1-based) skill level.
// For scalar cost the value is constant; for per-level lists it returns the
// matching entry or 0 if the level is not listed.
func (s SpCost) At(level int) int32 {
	if s.IsScalar {
		return s.Value
	}
	for _, l := range s.Levels {
		if int(l.Level) == level {
			return l.Amount
		}
	}
	return 0
}

// Requires groups the cast-cost fields of skill_db.yml. Only SpCost is exposed
// here because that is what consumers need first; other requirement fields
// (HpCost, ZenyCost, Weapon, Ammo, ...) are silently ignored by yaml.v3 and can
// be added when consumers need them.
type Requires struct {
	SpCost SpCost `yaml:"SpCost"`
}

type fileFormat struct {
	Header struct {
		Type    string `yaml:"Type"`
		Version int    `yaml:"Version"`
	} `yaml:"Header"`
	Body []*SkillEntry `yaml:"Body"`
}

// Registry provides thread-safe lookup of skill entries by ID.
type Registry struct {
	entries map[int32]*SkillEntry
}

// Load parses a rAthena skill_db YAML file from an io.Reader and returns a Registry.
// It expects the rAthena YAML format with Header.Type=="SKILL_DB" and Header.Version==4.
// Unknown fields are silently ignored. The document is streamed through
// yaml.NewDecoder to avoid buffering the entire (multi-MB) skill_db.yml
// in memory before decoding.
func Load(r io.Reader) (*Registry, error) {
	var f fileFormat
	if err := yaml.NewDecoder(r).Decode(&f); err != nil {
		return nil, fmt.Errorf("parse skill_db yaml: %w", err)
	}

	if f.Header.Type != "SKILL_DB" {
		return nil, fmt.Errorf("skill_db: unexpected Header.Type %q (want %q)", f.Header.Type, "SKILL_DB")
	}
	if f.Header.Version != 4 {
		return nil, fmt.Errorf("skill_db: unsupported Header.Version %d (want 4)", f.Header.Version)
	}

	entries := make(map[int32]*SkillEntry, len(f.Body))
	for _, entry := range f.Body {
		if entry == nil {
			continue
		}
		if entry.TargetType == "" {
			entry.TargetType = "Passive"
		}
		entries[entry.ID] = entry
	}
	return &Registry{entries: entries}, nil
}

// LoadFile is a convenience wrapper that opens a file and calls Load.
func LoadFile(path string) (*Registry, error) {
	f, err := os.Open(path) // #nosec G304 -- path is operator-configured skill_db.yml, not user input
	if err != nil {
		return nil, fmt.Errorf("open skill_db %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return Load(f)
}

// Get returns the SkillEntry for the given skill ID, or nil if not found.
func (reg *Registry) Get(id int32) *SkillEntry {
	if reg == nil {
		return nil
	}
	return reg.entries[id]
}

// Len returns the number of loaded skill entries.
func (reg *Registry) Len() int {
	if reg == nil {
		return 0
	}
	return len(reg.entries)
}

// SpCostAt returns the SP cost for the given skill at the given (1-based)
// level, or 0 if the skill or level is not found.
//
// TODO: rAthena falls back to the highest level <= requested level when the
// exact level is not listed in Requires.SpCost (skill.cpp::skill_get_sp_cost).
// Phase 13 uses exact match only via SpCost.At; switch to the rAthena fallback
// when wiring the skill feature.
func (reg *Registry) SpCostAt(id int32, level int) int32 {
	if reg == nil {
		return 0
	}
	entry := reg.entries[id]
	if entry == nil {
		return 0
	}
	return entry.Requires.SpCost.At(level)
}
