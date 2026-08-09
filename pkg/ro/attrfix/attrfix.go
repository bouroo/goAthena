// Package attrfix loads rAthena's pre-renewal attr_fix.yml — the elemental
// attribute damage-adjustment table — into a lookup registry. The table is
// keyed by the defender's ElementLevel (1..4): each level holds a 10×10 matrix
// of attacker-element × defender-element rates (percent; 100 = neutral, 150 =
// +50%, 0 = immune, negative = heal). The combat path multiplies a post-DEF hit
// by Rate/100; a missing or unloadable file degrades to the identity rate (100)
// so the server still boots.
package attrfix

import (
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// Ele indices mirror rAthena's Ele enum (map/attribute.cpp). They are the
// attacker/defender element axes of the rate matrix.
const (
	EleNeutral int = 0
	EleWater   int = 1
	EleEarth   int = 2
	EleFire    int = 3
	EleWind    int = 4
	ElePoison  int = 5
	EleHoly    int = 6
	EleShadow  int = 7
	EleGhost   int = 8
	EleUndead  int = 9
)

const numElements = 10

// identityRate is the rate a nil table or out-of-range lookup resolves to:
// 100% of the post-DEF damage, i.e. no elemental adjustment.
const identityRate = 100

// elementByName maps rAthena's YAML element keys to Ele indices. attr_fix.yml
// spells Shadow as "Dark"; "Shadow" is accepted as an alias for the mob_db
// Element spelling used elsewhere.
var elementByName = map[string]int{
	"Neutral": EleNeutral,
	"Water":   EleWater,
	"Earth":   EleEarth,
	"Fire":    EleFire,
	"Wind":    EleWind,
	"Poison":  ElePoison,
	"Holy":    EleHoly,
	"Dark":    EleShadow,
	"Shadow":  EleShadow,
	"Ghost":   EleGhost,
	"Undead":  EleUndead,
}

// ParseElement resolves an rAthena element name (as it appears in attr_fix.yml
// or mob_db.yml) to its Ele index. ok is false for an unknown name.
func ParseElement(name string) (int, bool) {
	e, ok := elementByName[name]
	return e, ok
}

// levelTable is one ElementLevel's full attacker × defender rate matrix.
type levelTable [numElements][numElements]int

// RateTable holds every loaded element-level rate matrix and answers the
// percent modifier for an attacker element against a defender element/level.
type RateTable struct {
	byLevel  map[int]levelTable
	maxLevel int
}

// NewRateTable returns an empty, non-nil table whose Rate always yields the
// identity (100). It is the zero value the app wiring falls back to when
// attr_fix.yml is absent or unreadable.
func NewRateTable() *RateTable {
	return &RateTable{byLevel: make(map[int]levelTable)}
}

// MaxLevel returns the highest loaded defender ElementLevel, or 0 when no level
// was loaded (a nil or empty table). It is informational for startup logging.
func (t *RateTable) MaxLevel() int {
	if t == nil {
		return 0
	}
	return t.maxLevel
}

// Rate returns the elemental damage modifier (percent) for attackerEle hitting
// a defenderEle of defenderEleLevel: 100 = neutral, 150 = +50%, 0 = immune,
// negative = heal. A nil table, an out-of-range element, or a missing level
// resolves to the identity rate (100). The defender's ElementLevel is clamped
// to the loaded range (1..maxLevel) the way rAthena clamps element levels.
func (t *RateTable) Rate(attackerEle, defenderEle, defenderEleLevel int) int {
	if t == nil {
		return identityRate
	}
	if attackerEle < 0 || attackerEle >= numElements || defenderEle < 0 || defenderEle >= numElements {
		return identityRate
	}
	return t.levelTable(defenderEleLevel)[attackerEle][defenderEle]
}

// levelTable resolves the matrix for the requested defender ElementLevel,
// clamping into the loaded range and falling back to the identity matrix when
// no level is loaded at all.
func (t *RateTable) levelTable(defenderEleLevel int) levelTable {
	if len(t.byLevel) == 0 {
		return identityLevelTable()
	}
	lvl := defenderEleLevel
	if lvl < 1 {
		lvl = 1
	}
	if t.maxLevel > 0 && lvl > t.maxLevel {
		lvl = t.maxLevel
	}
	if lt, ok := t.byLevel[lvl]; ok {
		return lt
	}
	return identityLevelTable()
}

// identityLevelTable is a fully-neutral matrix (every cell 100) used for nil
// tables and unloaded levels.
func identityLevelTable() levelTable {
	var lt levelTable
	for a := range lt {
		for d := range lt[a] {
			lt[a][d] = identityRate
		}
	}
	return lt
}

type fileFormat struct {
	Header struct {
		Type    string `yaml:"Type"`
		Version int    `yaml:"Version"`
	} `yaml:"Header"`
	Body []levelBody `yaml:"Body"`
}

// levelBody is one attr_fix Body entry: a Level scalar plus dynamic
// attacker-element keys. A custom UnmarshalYAML splits the Level scalar from
// the attacker-element mappings (yaml.v3 cannot decode the mixed
// scalar/mapping shape into a plain struct or a map[string]*yaml.Node).
type levelBody struct {
	Level int
	lt    levelTable
}

// UnmarshalYAML reads one Body entry from its node: the "Level" scalar names
// the defender ElementLevel; every other key is an attacker element whose
// mapping gives defender-element -> percent. Cells start at the identity (100)
// so omitted defender-element pairs resolve to the neutral rate.
func (lb *levelBody) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping node, got %s", node.Tag)
	}
	lb.lt = identityLevelTable()
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, val := node.Content[i], node.Content[i+1]
		if key.Value == "Level" {
			if err := val.Decode(&lb.Level); err != nil {
				return fmt.Errorf("decode Level: %w", err)
			}
			continue
		}
		attackerEle, ok := ParseElement(key.Value)
		if !ok {
			return fmt.Errorf("unknown attacker element %q", key.Value)
		}
		var defRates map[string]int
		if err := val.Decode(&defRates); err != nil {
			return fmt.Errorf("element %q: %w", key.Value, err)
		}
		for defName, pct := range defRates {
			defEle, ok := ParseElement(defName)
			if !ok {
				return fmt.Errorf("element %q: unknown defender element %q", key.Value, defName)
			}
			lb.lt[attackerEle][defEle] = pct
		}
	}
	return nil
}

// Load parses a rAthena attr_fix.yml from r and returns a RateTable. It expects
// Header.Type=="ATTRIBUTE_DB". Each Body entry's dynamic attacker-element keys
// are decoded against the Ele enum; an unknown element name is a data error.
func Load(r io.Reader) (*RateTable, error) {
	var f fileFormat
	if err := yaml.NewDecoder(r).Decode(&f); err != nil {
		return nil, fmt.Errorf("parse attr_fix yaml: %w", err)
	}
	if f.Header.Type != "ATTRIBUTE_DB" {
		return nil, fmt.Errorf("attr_fix: unexpected Header.Type %q (want %q)", f.Header.Type, "ATTRIBUTE_DB")
	}
	t := NewRateTable()
	for i, lb := range f.Body {
		if lb.Level < 1 {
			return nil, fmt.Errorf("attr_fix: body entry %d: missing or invalid Level", i)
		}
		t.byLevel[lb.Level] = lb.lt
		if lb.Level > t.maxLevel {
			t.maxLevel = lb.Level
		}
	}
	return t, nil
}

// LoadFile opens and parses attr_fix.yml at path.
func LoadFile(path string) (*RateTable, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open attr_fix %q: %w", path, err)
	}
	defer f.Close()
	return Load(f)
}
