// Package domain holds the pre-Renewal Novice skill registry and the
// wire-shape resolver used by the identity service's skill list builder.
//
// The package is pure: no I/O, no packet types, no external dependencies.
// All data is sourced from rAthena's pre-re skill_db.yml and skill.hpp.
package domain

import (
	"sync"

	"github.com/bouroo/goAthena/pkg/ro/skilldb"
)

// Skill Inf flag bitmask. Values verified against
// third_party/rathena/src/map/skill.hpp lines 79-85.
const (
	InfNone    uint16 = 0x00
	InfAttack  uint16 = 0x01
	InfGround  uint16 = 0x02
	InfSelf    uint16 = 0x04
	InfSupport uint16 = 0x10
	InfTrap    uint16 = 0x20
)

// InfFromTargetType maps a rAthena skill_db.yml TargetType string to the
// Inf bitmask, mirroring rAthena's skill.cpp:14865-14879 mapping
// (INF_<TargetType>_SKILL constant lookup). An empty or unknown
// TargetType yields InfNone (passive).
func InfFromTargetType(targetType string) uint16 {
	switch targetType {
	case "Attack":
		return InfAttack
	case "Ground":
		return InfGround
	case "Self":
		return InfSelf
	case "Support":
		return InfSupport
	case "Trap":
		return InfTrap
	default:
		return InfNone
	}
}

// Exported skill-ID handles. Callers should use these (not bare literals)
// when looking skills up via Lookup and reading SP via Skill.SpAt. Values
// mirror rAthena's SM_BASH enum in src/map/skill.hpp:729 and the entry id
// in db/pre-re/skill_db.yml.
const (
	SM_BASH uint16 = 5 //nolint:revive // matches rAthena SM_BASH enum used by handlers
)

// Skill is the registry entry for a single skill. spCost[level-1] is the
// SP cost at that level; for passives the slice is empty.
type Skill struct {
	ID       uint16
	Name     string
	MaxLevel uint8
	Inf      uint16
	Range    int16
	spCost   []uint16
}

// SpAt returns the SP cost at the given (1-based) level, or 0 if the level
// is out of range.
func (s Skill) SpAt(level uint8) uint16 {
	if level == 0 || int(level) > len(s.spCost) {
		return 0
	}
	return s.spCost[level-1]
}

// Registry is the active skill lookup, optionally backed by a loaded
// skill_db.yml. When nil/unset, the hardcoded defaultRegistry is used.
type Registry struct {
	entries map[uint16]Skill
}

// NewRegistry builds a domain Registry from a loaded skilldb.Registry,
// converting each skilldb.SkillEntry into a domain Skill with the Inf
// bitmask derived from TargetType. Returns nil if db is nil or empty.
func NewRegistry(db *skilldb.Registry) *Registry {
	if db == nil || db.Len() == 0 {
		return nil
	}

	r := &Registry{entries: make(map[uint16]Skill, db.Len())}
	for _, entry := range db.All() {
		skill, ok := convertSkillEntry(entry)
		if ok {
			r.entries[skill.ID] = skill
		}
	}
	if len(r.entries) == 0 {
		return nil
	}
	return r
}

func convertSkillEntry(entry *skilldb.SkillEntry) (Skill, bool) {
	if entry == nil || entry.ID < 0 || entry.ID > int32(^uint16(0)) || entry.MaxLevel < 0 || entry.MaxLevel > int32(^uint8(0)) {
		return Skill{}, false
	}
	id := uint16(entry.ID)            //nolint:gosec // rAthena skill IDs are bounded below 2^16
	maxLevel := uint8(entry.MaxLevel) //nolint:gosec // skill_db MaxLevel fits in uint8
	return Skill{
		ID:       id,
		Name:     entry.Name,
		MaxLevel: maxLevel,
		Inf:      InfFromTargetType(entry.TargetType),
		Range:    entry.Range.At(1),
		spCost:   convertSpCost(entry.Requires.SpCost, maxLevel),
	}, true
}

func convertSpCost(cost skilldb.SpCost, maxLevel uint8) []uint16 {
	if maxLevel == 0 {
		return nil
	}
	spCost := make([]uint16, 0, int(maxLevel))
	allZero := true
	for level := 1; level <= int(maxLevel); level++ {
		amount := cost.At(level)
		if amount < 0 || amount > int32(^uint16(0)) {
			spCost = append(spCost, 0)
			continue
		}
		value := uint16(amount) //nolint:gosec // validated against uint16 bounds
		spCost = append(spCost, value)
		if value != 0 {
			allZero = false
		}
	}
	if allZero {
		return nil
	}
	return spCost
}

// activeRegistry is the DB-backed registry set by DI at boot. When nil,
// Lookup and BuildSkillList fall back to defaultRegistry.
var (
	activeRegistryMu sync.RWMutex
	activeRegistry   *Registry
)

// SetRegistry installs the DB-backed skill registry. Passing nil resets to
// the hardcoded default.
func SetRegistry(r *Registry) {
	activeRegistryMu.Lock()
	activeRegistry = r
	activeRegistryMu.Unlock()
}

// LearnedSkill is a player's currently-learned skill reference.
type LearnedSkill struct {
	ID    uint16
	Level uint8
}

// SkillEntry is the wire-ready resolved skill row. Field order mirrors the
// rAthena SKILLDATA layout referenced in clif.cpp (pre-2019, header 0x010f).
type SkillEntry struct {
	ID     uint16
	Inf    uint16
	Level  uint16
	SP     uint16
	Range2 uint16
	Name   string
	UpFlag uint8
}

// BuildSkillList resolves a slice of LearnedSkill into wire-ready SkillEntry
// rows. Unknown skill IDs are skipped (no panic). Levels are clamped to
// [1, MaxLevel]. Negative Range values clamp to 0 on the wire.
//
// Entries are returned in input order. Nil or empty input yields an empty
// non-nil slice.
func BuildSkillList(learned []LearnedSkill) []SkillEntry {
	out := make([]SkillEntry, 0, len(learned))
	for _, ls := range learned {
		s, ok := Lookup(ls.ID)
		if !ok {
			continue
		}
		level := ls.Level
		if level == 0 {
			level = 1
		}
		if level > s.MaxLevel {
			level = s.MaxLevel
		}
		range2 := uint16(0)
		if s.Range > 0 {
			range2 = uint16(s.Range) //nolint:gosec // Range guarded non-negative
		}
		out = append(out, SkillEntry{
			ID:     s.ID,
			Inf:    s.Inf,
			Level:  uint16(level),
			SP:     s.SpAt(level),
			Range2: range2,
			Name:   s.Name,
			// UpFlag: upgradeable-flag computation deferred (see
			// rAthena clif.cpp:5694). Hardcoded 0 until we wire that
			// policy.
			UpFlag: 0,
		})
	}
	return out
}

// Lookup returns the registry entry for the given skill ID.
func Lookup(id uint16) (Skill, bool) {
	activeRegistryMu.RLock()
	registry := activeRegistry
	activeRegistryMu.RUnlock()
	if registry != nil {
		s, ok := registry.entries[id]
		return s, ok
	}
	s, ok := defaultRegistry[id]
	return s, ok
}

// defaultRegistry is the pre-Renewal Novice starter tree. Values sourced from
// third_party/rathena/db/pre-re/skill_db.yml.
//
// NV_BASIC — pre-re/skill_db.yml:146-149 (passive, no SP).
// NV_FIRSTAID — pre-re/skill_db.yml:5063-5075 (InfSelf, SP=3).
// NV_TRICKDEAD — pre-re/skill_db.yml:5076-5091 (InfSelf, SP=5).
// SM_BASH — pre-re/skill_db.yml:164-200 (InfAttack, MaxLevel=10, SP=8/15).
var defaultRegistry = map[uint16]Skill{
	1: {
		ID:       1,
		Name:     "NV_BASIC",
		MaxLevel: 9,
		Inf:      InfNone,
		Range:    0,
		spCost:   nil,
	},
	142: {
		ID:       142,
		Name:     "NV_FIRSTAID",
		MaxLevel: 1,
		Inf:      InfSelf,
		Range:    0,
		spCost:   []uint16{3},
	},
	143: {
		ID:       143,
		Name:     "NV_TRICKDEAD",
		MaxLevel: 1,
		Inf:      InfSelf,
		Range:    0,
		spCost:   []uint16{5},
	},
	SM_BASH: {
		ID:       SM_BASH,
		Name:     "SM_BASH",
		MaxLevel: 10,
		Inf:      InfAttack,
		Range:    0,
		spCost:   []uint16{8, 8, 8, 8, 8, 15, 15, 15, 15, 15},
	},
}
