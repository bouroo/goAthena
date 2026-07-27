// Package mobdb loads rAthena mob_db.yml (version 5) into a lookup registry.
package mobdb

import (
	"fmt"
	"io"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// DropEntry represents a single item drop from a mob's drop table.
type DropEntry struct {
	Item string
	Rate int
}

// MobEntry holds combat-relevant fields for a single mob type.
type MobEntry struct {
	Id           int32 //nolint:revive // yaml tag is "Id" to match rAthena mob_db.yml
	AegisName    string
	Name         string
	Level        int32
	Hp           int32
	BaseExp      int32
	JobExp       int32
	Attack       int32
	Attack2      int32
	Defense      int32
	MagicDefense int32
	Str          int32
	Agi          int32
	Vit          int32
	Int          int32
	Dex          int32
	Luk          int32
	WalkSpeed    int32
	AttackRange  int32
	ChaseRange   int32
	Size         string
	Race         string
	Element      string
	ElementLevel int32
	Ai           int32 //nolint:revive // yaml tag is "Ai" to match rAthena mob_db.yml
	Drops        []DropEntry
}

type fileFormat struct {
	Header struct {
		Type    string `yaml:"Type"`
		Version int    `yaml:"Version"`
	} `yaml:"Header"`
	Body []*MobEntry `yaml:"Body"`
}

// Registry provides thread-safe lookup of mob entries by ID.
type Registry struct {
	entries map[int32]*MobEntry
}

// Load parses a rAthena mob_db.yml from an io.Reader and returns a Registry.
// It expects the rAthena YAML format with Header.Type=="MOB_DB" and Header.Version==5.
// Unknown fields are silently ignored.
func Load(r io.Reader) (*Registry, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read mob_db: %w", err)
	}

	var f fileFormat
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse mob_db yaml: %w", err)
	}

	if f.Header.Type != "MOB_DB" {
		return nil, fmt.Errorf("mob_db: unexpected Header.Type %q (want %q)", f.Header.Type, "MOB_DB")
	}
	if f.Header.Version != 5 {
		return nil, fmt.Errorf("mob_db: unsupported Header.Version %d (want 5)", f.Header.Version)
	}

	entries := make(map[int32]*MobEntry, len(f.Body))
	for _, e := range f.Body {
		if e == nil {
			continue
		}
		entries[e.Id] = e
	}
	return &Registry{entries: entries}, nil
}

// LoadFile is a convenience wrapper that opens a file and calls Load.
func LoadFile(path string) (*Registry, error) {
	f, err := os.Open(path) // #nosec G304 -- path is operator-configured mob_db.yml, not user input
	if err != nil {
		return nil, fmt.Errorf("open mob_db %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return Load(f)
}

// Get returns the MobEntry for the given mob ID, or nil if not found.
func (reg *Registry) Get(id int32) *MobEntry {
	if reg == nil {
		return nil
	}
	return reg.entries[id]
}

// Len returns the number of loaded mob entries.
func (reg *Registry) Len() int {
	if reg == nil {
		return 0
	}
	return len(reg.entries)
}

// UnmarshalYAML decodes the Ai field as a zero-padded string ("02", "04", ...)
// into int32. The rAthena YAML emits Ai as a string; an int value is also accepted.
func (m *MobEntry) UnmarshalYAML(node *yaml.Node) error {
	type rawMob struct {
		Id           int32  `yaml:"Id"` //nolint:revive // yaml tag must be "Id" to match rAthena mob_db.yml
		AegisName    string `yaml:"AegisName"`
		Name         string `yaml:"Name"`
		Level        int32  `yaml:"Level"`
		Hp           int32  `yaml:"Hp"`
		BaseExp      int32  `yaml:"BaseExp"`
		JobExp       int32  `yaml:"JobExp"`
		Attack       int32  `yaml:"Attack"`
		Attack2      int32  `yaml:"Attack2"`
		Defense      int32  `yaml:"Defense"`
		MagicDefense int32  `yaml:"MagicDefense"`
		Str          int32  `yaml:"Str"`
		Agi          int32  `yaml:"Agi"`
		Vit          int32  `yaml:"Vit"`
		Int          int32  `yaml:"Int"`
		Dex          int32  `yaml:"Dex"`
		Luk          int32  `yaml:"Luk"`
		WalkSpeed    int32  `yaml:"WalkSpeed"`
		AttackRange  int32  `yaml:"AttackRange"`
		ChaseRange   int32  `yaml:"ChaseRange"`
		Size         string `yaml:"Size"`
		Race         string `yaml:"Race"`
		Element      string `yaml:"Element"`
		ElementLevel int32  `yaml:"ElementLevel"`
		Ai           any    `yaml:"Ai"`
		Drops        []struct {
			Item string `yaml:"Item"`
			Rate int    `yaml:"Rate"`
		} `yaml:"Drops"`
	}

	var raw rawMob
	if err := node.Decode(&raw); err != nil {
		return fmt.Errorf("decode mob node: %w", err)
	}

	switch v := raw.Ai.(type) {
	case nil:
		m.Ai = 0
	case int:
		m.Ai = int32(v) //nolint:gosec // G115: Ai fits in int32 per rAthena mob_db spec
	case string:
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("mob %d: parse Ai %q: %w", raw.Id, v, err)
		}
		m.Ai = int32(n) //nolint:gosec // G109: Ai fits in int32 per rAthena mob_db spec
	default:
		return fmt.Errorf("mob %d: unsupported Ai type %T", raw.Id, raw.Ai)
	}

	m.Id = raw.Id
	m.AegisName = raw.AegisName
	m.Name = raw.Name
	m.Level = raw.Level
	m.Hp = raw.Hp
	m.BaseExp = raw.BaseExp
	m.JobExp = raw.JobExp
	m.Attack = raw.Attack
	m.Attack2 = raw.Attack2
	m.Defense = raw.Defense
	m.MagicDefense = raw.MagicDefense
	m.Str = raw.Str
	m.Agi = raw.Agi
	m.Vit = raw.Vit
	m.Int = raw.Int
	m.Dex = raw.Dex
	m.Luk = raw.Luk
	m.WalkSpeed = raw.WalkSpeed
	m.AttackRange = raw.AttackRange
	m.ChaseRange = raw.ChaseRange
	m.Size = raw.Size
	m.Race = raw.Race
	m.Element = raw.Element
	m.ElementLevel = raw.ElementLevel
	for _, d := range raw.Drops {
		m.Drops = append(m.Drops, DropEntry{Item: d.Item, Rate: d.Rate})
	}
	return nil
}
