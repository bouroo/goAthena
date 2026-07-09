// Package domain holds the pure, dependency-free stat and leveling
// computation for the pre-Renewal (pre-re) ruleset. Both the gateway
// (session-level EXP accumulation + level-up detection) and the identity
// service (stat-point allocation persistence) build on these functions.
//
// All formulas are transcribed from rAthena src/map/pc.cpp with the
// non-RENEWAL (pre-re) code path. Deterministic logic lives here in tested
// code, never re-derived in the model.
package domain

// StatType identifies one of the six base attributes a player can raise
// with status points. The numeric values match the rAthena SP_STR..SP_LUK
// parameter IDs (rathena/src/map/map.hpp enum), so a CZ_STATUS_CHANGE
// status-id wire byte maps directly to this type.
type StatType uint8

// Raisable base stats. The numeric values match the rAthena SP_STR..SP_LUK
// parameter IDs (rathena/src/map/map.hpp enum), so a CZ_STATUS_CHANGE
// status-id wire byte maps directly to a StatType value.
const (
	// StatStr is SP_STR (strength).
	StatStr StatType = 13
	// StatAgi is SP_AGI (agility).
	StatAgi StatType = 14
	// StatVit is SP_VIT (vitality).
	StatVit StatType = 15
	// StatInt is SP_INT (intelligence).
	StatInt StatType = 16
	// StatDex is SP_DEX (dexterity).
	StatDex StatType = 17
	// StatLuk is SP_LUK (luck).
	StatLuk StatType = 18
)

// MaxStat is the pre-renewal cap on any single base stat
// (battle_config max_parameter; default 99 for non-trans classes).
// A character cannot raise a stat beyond this with status points.
const MaxStat uint8 = 99

// StatTypes lists every raisable stat in canonical (SP_*) order.
var StatTypes = [...]StatType{StatStr, StatAgi, StatVit, StatInt, StatDex, StatLuk}

// Valid reports whether t is one of the six raisable base stats.
func (t StatType) Valid() bool {
	switch t {
	case StatStr, StatAgi, StatVit, StatInt, StatDex, StatLuk:
		return true
	}
	return false
}

// statCostStep returns the status points needed to raise a stat from
// currentVal to currentVal+1, using the pre-Renewal formula:
//
//	PC_STATUS_POINT_COST(low) = 1 + ((low) + 9) / 10
//
// Source: rathena/src/map/pc.cpp:8803 (non-RENEWAL branch). This is the
// same formula as pkg/ro/packet.StatusPointCost; it is duplicated here so
// the domain layer stays free of transport (packet) imports — clean
// architecture keeps the domain depending only on the language runtime.
func statCostStep(currentVal uint8) uint32 {
	// currentVal is a base stat (0..MaxStat=99), so the result is at most
	// 1 + (99+9)/10 = 11 — no overflow. Promote to int to avoid uint8
	// wraparound in currentVal+9.
	return uint32(1 + (int(currentVal)+9)/10) //nolint:gosec // G115: bounded to ≤11 for valid stats
}

// StatCost returns the total status points needed to raise a stat from
// currentVal by increase steps. It sums the per-step cost over the
// half-open range [currentVal, currentVal+increase), matching rAthena's
// pc_need_status_point (rathena/src/map/pc.cpp:8809).
//
// increase <= 0 yields 0 (nothing to spend). currentVal+increase clamped
// by the caller to MaxStat; this function does not enforce the cap so the
// caller can distinguish "over the cap" (a validation error) from "cannot
// afford" (a different error).
func StatCost(currentVal uint8, increase int) uint32 {
	if increase <= 0 {
		return 0
	}
	var total uint32
	for low := int(currentVal); low < int(currentVal)+increase; low++ {
		total += statCostStep(uint8(low)) //nolint:gosec // G115: low is a base stat, bounded 0..MaxStat
	}
	return total
}
