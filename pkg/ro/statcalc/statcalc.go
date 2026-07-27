// Package statcalc derives a player character's Pre-Renewal combat stats from
// base attributes, mirroring rAthena's status_calc_misc (non-RENEWAL branch),
// status_base_atk, status_base_matk_min/max, and status_base_amotion_pc. These
// are pure functions over integer base stats — the deterministic core that the
// ZC_STATUS enter burst (and, later, player-side combat damage) shares.
//
// PACKETVER 20250604 is Pre-Renewal: the formulas below are the #else
// (non-RENEWAL) branches of their rAthena sources, which are materially
// different from the RENEWAL branches. Do not "simplify" them toward the
// renewal forms.
//
// Equipment contributions and the job's weapon-class attack delay are
// caller-supplied inputs, so the package has no data dependency. A naked
// Novice is the no-equipment, novice-fist-delay baseline this slice ships.
package statcalc

import (
	"math"

	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// Base holds the seven integer attributes status_calc_misc consumes. Types match
// the character-domain fields (uint16); clif clamps them to uint8 on the wire,
// which the ZCStatus builder applies.
type Base struct {
	Level         uint16
	Str, Agi, Vit uint16
	Int, Dex, Luk uint16
}

// BaseATK is the left-side (base) physical attack for a non-bow PC:
//
//	str + (str/10)² + dex/5 + luk/5
//
// Source: status.cpp status_base_atk, BL_PC non-RENEWAL branch (the bow path is
// not used — the slice has no bows).
func BaseATK(b Base) int32 {
	s := int32(b.Str)
	dstr := s / 10
	return s + dstr*dstr + int32(b.Dex)/5 + int32(b.Luk)/5
}

// MatkMin / MatkMax are the soft magic-attack bounds:
//
//	min = int + (int/7)²
//	max = int + (int/5)²
//
// Source: status.cpp status_base_matk_min/max (no RENEWAL guard — same both).
func MatkMin(b Base) int32 {
	i := int32(b.Int)
	return i + (i/7)*(i/7)
}

// MatkMax is the high MATK bound. See MatkMin.
func MatkMax(b Base) int32 {
	i := int32(b.Int)
	return i + (i/5)*(i/5)
}

// Hit and Flee are the level + stat totals (non-RENEWAL adds nothing else):
//
//	Hit  = level + dex
//	Flee = level + agi
//
// Source: status.cpp status_calc_misc #else (HIT/FLEE), lines ~2704-2711.
func Hit(b Base) int32 { return int32(b.Level) + int32(b.Dex) }

// Flee is level + agi (non-RENEWAL). See Hit for the source.
func Flee(b Base) int32 { return int32(b.Level) + int32(b.Agi) }

// SoftDEF is the right-side (vit) defense; SoftMDEF is the right-side soft
// magic defense:
//
//	softDEF  = vit
//	softMDEF = int + vit/2
//
// Source: status.cpp status_calc_misc #else (Def2/Mdef2), lines ~2712-2719.
// The left-side (item) DEF/MDEF are equipment contributions (StatusInputs).
func SoftDEF(b Base) int32 { return int32(b.Vit) }

// SoftMDEF is the right-side soft magic defense, int + vit/2. See SoftDEF.
func SoftMDEF(b Base) int32 { return int32(b.Int) + int32(b.Vit)/2 }

// Critical and PerfectFlee return the *stored* values (before the /10 the wire
// applies). status_calc_misc computes (enable_critical / enable_perfect_flee
// both include BL_PC by default):
//
//	cri   = 10 + luk*10/3   (integer division; "every 1 luk = +0.3 crit")
//	flee2 = luk + 10        ("every 10 luk = +1 perfect flee")
//
// The ZC_STATUS wire fields carry these /10; see ZCStatus. Source: status.cpp
// status_calc_misc #else, lines ~2722-2740.
func Critical(b Base) int32 { return 10 + int32(b.Luk)*10/3 }

// PerfectFlee is the stored perfect-flee total, luk + 10. See Critical.
func PerfectFlee(b Base) int32 { return int32(b.Luk) + 10 }

// Amotion is the PC attack delay (ms-scaled) for a single equipped weapon:
//
//	amotion = weaponBaseASPD
//	amotion -= amotion * (4*agi + dex) / 1000
//
// (bAspd/shield adjustments are 0 for a naked character and omitted.) The wire
// ASPD field carries amotion directly; the client derives its display ASPD from
// it. weaponBaseASPD is the job's aspd_base[weapon] — Novice fist is 500 (db/
// pre-re/job_aspd.yml:95, BaseASPD.Fist). Source: status.cpp
// status_base_amotion_pc #else, lines 2404-2414.
func Amotion(b Base, weaponBaseASPD uint16) int32 {
	a := int32(weaponBaseASPD)
	return a - a*(4*int32(b.Agi)+int32(b.Dex))/1000
}

// Equipment holds the right/left-side contributions equipment adds. For a naked
// character every field is 0; the slice has no inventory, so the burst always
// builds from the zero value.
type Equipment struct {
	WeaponATK int32 // right-side ATK (weapon damage); 0 unequipped
	ItemDEF   int32 // left-side DEF (armor); 0 unequipped
	ItemMDEF  int32 // left-side MDEF (armor); 0 unequipped
}

// StatusInputs is everything clif_initialstatus needs to build ZC_STATUS: the
// base attributes, status points, and the (zero for this slice) equipment
// contributions plus the job weapon delay.
type StatusInputs struct {
	Base
	StatusPoint    uint32
	Equipment      Equipment
	WeaponBaseASPD uint16
}

// clampU8 mirrors clif's `min(v, UINT8_MAX)` for the six base-stat fields.
func clampU8(v uint16) uint8 {
	if v > math.MaxUint8 {
		return math.MaxUint8
	}
	return uint8(v) //nolint:gosec // G115: clamped to 0..255
}

// clampInt16 mirrors rAthena cap_value(..., lo, SHRT_MAX): the derived stats are
// capped to int16 before the wire write. Fresh-character values are tiny; the
// clamp only matters at the (unreachable in this slice) high end.
func clampInt16(v int32) int16 {
	if v > math.MaxInt16 {
		return math.MaxInt16
	}
	if v < 0 {
		return 0
	}
	return int16(v) //nolint:gosec // G115: clamped to 0..32767
}

// ZCStatus builds the ZC_STATUS (0x00bd) packet body from base attributes,
// applying clif_initialstatus's field mapping and wire clamping. NeedX uses the
// kernel's pc_need_status_point (packet.StatusPointCost). Source: clif.cpp
// clif_initialstatus, lines 4123-4162.
func ZCStatus(in StatusInputs) packet.StatusResponse {
	b := in.Base
	return packet.StatusResponse{
		StatusPoint: uint16(in.StatusPoint), //nolint:gosec // G115: clif min(.,INT16_MAX); slice points are tiny
		Str:         clampU8(b.Str), NeedStr: packet.StatusPointCost(clampU8(b.Str)),
		Agi: clampU8(b.Agi), NeedAgi: packet.StatusPointCost(clampU8(b.Agi)),
		Vit: clampU8(b.Vit), NeedVit: packet.StatusPointCost(clampU8(b.Vit)),
		Int: clampU8(b.Int), NeedInt: packet.StatusPointCost(clampU8(b.Int)),
		Dex: clampU8(b.Dex), NeedDex: packet.StatusPointCost(clampU8(b.Dex)),
		Luk: clampU8(b.Luk), NeedLuk: packet.StatusPointCost(clampU8(b.Luk)),
		Atk1:     clampInt16(BaseATK(b)),
		Atk2:     clampInt16(in.Equipment.WeaponATK),
		MatkMax:  clampInt16(MatkMax(b)),
		MatkMin:  clampInt16(MatkMin(b)),
		Def1:     clampInt16(in.Equipment.ItemDEF),
		Def2:     clampInt16(SoftDEF(b)),
		Mdef1:    clampInt16(in.Equipment.ItemMDEF),
		Mdef2:    clampInt16(SoftMDEF(b)),
		Hit:      clampInt16(Hit(b)),
		Flee:     clampInt16(Flee(b)),
		Flee2:    clampInt16(PerfectFlee(b) / 10),
		Critical: clampInt16(Critical(b) / 10),
		ASPD:     clampInt16(Amotion(b, in.WeaponBaseASPD)),
		PlusASPD: 0,
	}
}
