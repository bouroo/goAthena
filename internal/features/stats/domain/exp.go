package domain

import (
	"sync/atomic"

	"github.com/bouroo/goAthena/pkg/ro/jobdb"
)

// MaxBaseLevel is the pre-Renewal maximum base level for standard classes.
const MaxBaseLevel uint32 = 99

// MaxLevelExp is the EXP cap applied once a character reaches MaxBaseLevel,
// matching rAthena MAX_LEVEL_BASE_EXP (rathena/src/map/pc.cpp:8645). A
// max-level character stops accumulating beyond this so the column does not
// grow without bound.
const MaxLevelExp uint64 = 99_999_999

// baseExpTable[i] is the base EXP a character at base level i+1 must earn
// to advance to level i+2. It is the pre-Renewal table shared by all
// standard classes (Novice, Swordsman, Mage, ..., Taekwon) — group 1 of
// rathena/db/pre-re/job_exp.yml `BaseExp`.
//
// baseExpTable[97] is the threshold for level 98→99; baseExpTable[98] is
// the MAX_LEVEL_BASE_EXP cap referenced at max level.
var baseExpTable = [...]uint64{
	9, 16, 25, 36, 77, 112, 153, 200, 253, 320,
	385, 490, 585, 700, 830, 970, 1120, 1260, 1420, 1620,
	1860, 1990, 2240, 2504, 2950, 3426, 3934, 4474, 6889, 7995,
	9174, 10425, 11748, 13967, 15775, 17678, 19677, 21773, 30543, 34212,
	38065, 42102, 46323, 53026, 58419, 64041, 69892, 75973, 102468, 115254,
	128692, 142784, 157528, 178184, 196300, 215198, 234879, 255341, 330188, 365914,
	403224, 442116, 482590, 536948, 585191, 635278, 687211, 740988, 925400, 1473746,
	1594058, 1718928, 1848355, 1982340, 2230113, 2386162, 2547417, 2713878, 3206160, 3681024,
	4022472, 4377024, 4744680, 5125440, 5767272, 6204000, 6655464, 7121664, 7602600, 9738720,
	11649960, 13643520, 18339300, 23836800, 35658000, 48687000, 58135000, 99999998, 99999999,
}

// BaseExpForLevel returns the base EXP required to advance from level to
// level+1. Returns 0 for level < 1 and for level >= MaxBaseLevel (a
// max-level character has no next threshold). Mirrors rAthena
// pc_nextbaseexp / JobDatabase::get_baseExp (rathena/src/map/pc.cpp:8640).
func BaseExpForLevel(level uint32) uint64 {
	if level < 1 || level >= MaxBaseLevel {
		return 0
	}
	return baseExpTable[level-1]
}

// ExpGain is the result of applying a base-EXP gain to a character. It is
// the pure output of ApplyBaseExpGain; callers (gateway) translate it into
// packets and a persistence RPC.
type ExpGain struct {
	// NewLevel is the base level after the gain.
	NewLevel uint32
	// NewExp is the base EXP remaining within the current (NewLevel)
	// level band — i.e. progress toward NewLevel+1, in [0, threshold).
	NewExp uint64
	// GrantedStatusPoints is the total status points awarded by all
	// level-ups the gain triggered. Zero when no level-up occurred.
	GrantedStatusPoints uint32
	// LeveledUp is true when NewLevel exceeds the input level.
	LeveledUp bool
}

// ApplyBaseExpGain computes the effect of adding gain base EXP to a
// character currently at (baseLevel, baseExp), where baseExp is the EXP
// accumulated within the current level band (0-based, the same semantics
// as rAthena sd->status.base_exp).
//
// It mirrors rAthena pc_checkbaselevelup (rathena/src/map/pc.cpp:8244):
// repeatedly subtract the next-level threshold, grant status points, and
// increment the level while the accumulated EXP covers the threshold
// (multi-level-up). Excess EXP carries into the new level band.
//
// At MaxBaseLevel the EXP is capped at MaxLevelExp and no further
// level-up occurs.
func ApplyBaseExpGain(baseLevel uint32, baseExp uint64, gain uint64) ExpGain {
	newLevel, newExp := baseLevel, baseExp+gain

	// Already maxed: cap and stop.
	if newLevel >= MaxBaseLevel {
		if newExp > MaxLevelExp {
			newExp = MaxLevelExp
		}
		return ExpGain{NewLevel: newLevel, NewExp: newExp}
	}

	var granted uint32
	for newLevel < MaxBaseLevel {
		next := BaseExpForLevel(newLevel)
		if next == 0 || newExp < next {
			break
		}
		newExp -= next
		granted += StatusPointGrant(newLevel)
		newLevel++
	}

	// Cap carry-over at max level (rathena clamps base_exp to
	// MAX_LEVEL_BASE_EXP once MaxBaseLevel is reached).
	if newLevel >= MaxBaseLevel && newExp > MaxLevelExp {
		newExp = MaxLevelExp
	}

	return ExpGain{
		NewLevel:            newLevel,
		NewExp:              newExp,
		GrantedStatusPoints: granted,
		LeveledUp:           newLevel > baseLevel,
	}
}

// ExpRegistry is a per-job base-EXP lookup backed by a loaded job_exp.yml.
// jobName is the rAthena job name as it appears in `Body[].Jobs` of
// rathena/db/pre-re/job_exp.yml (e.g. "Novice", "Swordman"). Level is
// 1-based.
type ExpRegistry struct {
	db *jobdb.Registry
}

// NewExpRegistryFromJobDB adapts a loaded jobdb.Registry into the domain
// ExpRegistry. Returns nil when db is nil so DI callers can collapse the
// nil-empty and missing-input cases.
func NewExpRegistryFromJobDB(db *jobdb.Registry) *ExpRegistry {
	if db == nil {
		return nil
	}
	return &ExpRegistry{db: db}
}

// BaseExpForLevel returns the configured base EXP threshold for the given
// job and 1-based level, or 0 when the job is unknown, the level is out of
// range, or the registry has no entry for that point. The bound check uses
// MaxBaseLevel so callers cannot drift above the rAthena cap.
func (r *ExpRegistry) BaseExpForLevel(jobName string, level uint32) uint64 {
	if r == nil || r.db == nil {
		return 0
	}
	if level < 1 || level >= MaxBaseLevel {
		return 0
	}
	return r.db.BaseExpForLevel(jobName, int(level))
}

// expRegistryPtr is the active DB-backed registry set by DI at boot. When
// nil, BaseExpForJobLevel and ApplyBaseExpGainForJob fall back to
// baseExpTable via BaseExpForLevel. Mirrors skilldomain.activeRegistry
// (D-PH14).
var expRegistryPtr atomic.Pointer[ExpRegistry]

// SetExpRegistry installs the DB-backed job-exp registry. Passing nil
// resets to the hardcoded baseExpTable fallback.
func SetExpRegistry(r *ExpRegistry) {
	expRegistryPtr.Store(r)
}

// GetExpRegistry returns the currently active ExpRegistry, or nil when
// none is configured (fallback to baseExpTable).
func GetExpRegistry() *ExpRegistry {
	return expRegistryPtr.Load()
}

// BaseExpForJobLevel returns the base EXP required to advance from level
// to level+1 for the given job. When no registry has been installed via
// SetExpRegistry (e.g. tests, dev mode), it falls back to BaseExpForLevel
// and the hardcoded baseExpTable.
//
// Returns 0 for level < 1, level >= MaxBaseLevel, and for unknown jobs.
// Mirrors rAthena pc_nextbaseexp(job, level)
// (rathena/src/map/pc.cpp:8640).
func BaseExpForJobLevel(jobName string, level uint32) uint64 {
	if level < 1 || level >= MaxBaseLevel {
		return 0
	}
	if reg := expRegistryPtr.Load(); reg != nil {
		if v := reg.BaseExpForLevel(jobName, level); v != 0 {
			return v
		}
	}
	return BaseExpForLevel(level)
}

// ApplyBaseExpGainForJob computes the effect of adding gain base EXP to a
// character of the given job, using the per-job BaseExpForJobLevel
// threshold for each level boundary. When no registry is configured it
// behaves exactly like ApplyBaseExpGain (the gateway's existing call-site
// continues to compile unchanged this phase).
//
// At MaxBaseLevel the EXP is capped at MaxLevelExp and no further
// level-up occurs.
func ApplyBaseExpGainForJob(jobName string, baseLevel uint32, baseExp uint64, gain uint64) ExpGain {
	newLevel, newExp := baseLevel, baseExp+gain

	// Already maxed: cap and stop.
	if newLevel >= MaxBaseLevel {
		if newExp > MaxLevelExp {
			newExp = MaxLevelExp
		}
		return ExpGain{NewLevel: newLevel, NewExp: newExp}
	}

	var granted uint32
	for newLevel < MaxBaseLevel {
		next := BaseExpForJobLevel(jobName, newLevel)
		if next == 0 || newExp < next {
			break
		}
		newExp -= next
		granted += StatusPointGrant(newLevel)
		newLevel++
	}

	// Cap carry-over at max level (rathena clamps base_exp to
	// MAX_LEVEL_BASE_EXP once MaxBaseLevel is reached).
	if newLevel >= MaxBaseLevel && newExp > MaxLevelExp {
		newExp = MaxLevelExp
	}

	return ExpGain{
		NewLevel:            newLevel,
		NewExp:              newExp,
		GrantedStatusPoints: granted,
		LeveledUp:           newLevel > baseLevel,
	}
}
