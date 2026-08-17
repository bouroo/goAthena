// Package skilldb loads rAthena skill_db.yml (version 4) into a lookup registry.
package skilldb

import (
	"fmt"
	"io"
	"maps"
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
// For scalar cost the value is constant. For per-level lists the exact entry
// wins when present; otherwise it falls back to the amount of the highest
// listed level not exceeding the requested level, matching rAthena's
// skill_get_sp_cost. A level below the lowest listed entry (or an empty list)
// returns 0 — the skill is unlearned there.
func (s SpCost) At(level int) int32 {
	if s.IsScalar {
		return s.Value
	}
	// bestLevel and amount stay 0 when no listed level is <= the requested
	// level, i.e. the skill is not yet learned at that level.
	var amount int32
	var bestLevel int
	for _, l := range s.Levels {
		if lv := int(l.Level); lv <= level && lv > bestLevel {
			bestLevel = lv
			amount = l.Amount
		}
	}
	return amount
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

// Registry provides thread-safe lookup of skill entries by ID and by Name.
// The byName index supports joining with skill_tree which keys on skill Name.
type Registry struct {
	entries map[int32]*SkillEntry
	byName  map[string]int32 // name → ID
}

// NewRegistry returns an empty, non-nil Registry. It is the zero value the app
// wiring falls back to when skill_db.yml is absent or unreadable: lookups then
// return nil (every skill casts as unknown) instead of failing boot.
func NewRegistry() *Registry {
	return &Registry{
		entries: make(map[int32]*SkillEntry),
		byName:  make(map[string]int32),
	}
}

// Register adds or replaces a skill entry by its ID. It seeds the registry
// programmatically (tests, fixtures) without parsing a skill_db YAML file;
// production loading still goes through Load/LoadFile. Like Get/Len it assumes
// the registry is populated before concurrent reads begin.
func (reg *Registry) Register(e *SkillEntry) {
	if reg == nil || e == nil {
		return
	}
	reg.entries[e.ID] = e
	reg.byName[e.Name] = e.ID
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

// IDByName returns the skill ID for skill name, or (0, false) if not found.
// Thread-safe. Supports joining with skill_tree which keys on skill Name.
func (reg *Registry) IDByName(name string) (int32, bool) {
	if reg == nil {
		return 0, false
	}
	id, ok := reg.byName[name]
	return id, ok
}

// All returns all loaded skill entries keyed by their rAthena ID.
func (reg *Registry) All() map[int32]*SkillEntry {
	if reg == nil {
		return nil
	}
	entries := make(map[int32]*SkillEntry, len(reg.entries))
	maps.Copy(entries, reg.entries)
	return entries
}

// SpCostAt returns the SP cost for the given skill at the given (1-based)
// level, delegating per-level resolution to SpCost.At. It returns 0 when the
// skill or its SpCost is absent.
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
