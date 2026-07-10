// Package domain holds the pre-Renewal Novice skill registry and the
// wire-shape resolver used by the identity service's skill list builder.
//
// The package is pure: no I/O, no packet types, no external dependencies.
// All data is sourced from rAthena's pre-re skill_db.yml and skill.hpp.
package domain

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

// Skill is the registry entry for a single skill. SpCost[level-1] is the
// SP cost at that level; for passives the slice is empty.
type Skill struct {
	ID       uint16
	Name     string
	MaxLevel uint8
	Inf      uint16
	Range    int16
	SpCost   []uint16
}

// SpAt returns the SP cost at the given (1-based) level, or 0 if the level
// is out of range.
func (s Skill) SpAt(level uint8) uint16 {
	if level == 0 || int(level) > len(s.SpCost) {
		return 0
	}
	return s.SpCost[level-1]
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
		s, ok := registry[ls.ID]
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
	s, ok := registry[id]
	return s, ok
}

// registry is the pre-Renewal Novice starter tree. Values sourced from
// third_party/rathena/db/pre-re/skill_db.yml.
//
// NV_BASIC — pre-re/skill_db.yml:146-149 (passive, no SP).
// NV_FIRSTAID — pre-re/skill_db.yml:5063-5075 (InfSelf, SP=3).
// NV_TRICKDEAD — pre-re/skill_db.yml:5076-5091 (InfSelf, SP=5).
var registry = map[uint16]Skill{
	1: {
		ID:       1,
		Name:     "NV_BASIC",
		MaxLevel: 9,
		Inf:      InfNone,
		Range:    0,
		SpCost:   nil,
	},
	142: {
		ID:       142,
		Name:     "NV_FIRSTAID",
		MaxLevel: 1,
		Inf:      InfSelf,
		Range:    0,
		SpCost:   []uint16{3},
	},
	143: {
		ID:       143,
		Name:     "NV_TRICKDEAD",
		MaxLevel: 1,
		Inf:      InfSelf,
		Range:    0,
		SpCost:   []uint16{5},
	},
}
