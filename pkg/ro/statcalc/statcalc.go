// Package statcalc derives a player character's combat stats from base
// attributes, mirroring rAthena's status_calc_misc, status_base_atk,
// status_base_matk_min/max, and status_base_amotion_pc. These are pure
// functions over integer base stats — the deterministic core that the
// ZC_STATUS enter burst (and, later, player-side combat damage) shares.
//
// Two game-balance modes coexist behind a FormulaSet, one per pkg/ro/mode.Mode:
//
//   - PreRenewal is the rAthena #else (non-RENEWAL) branch — the classic
//     formulas (see baseatk_pre.go, derived_pre.go, amotion_pre.go).
//   - Renewal is the rAthena #ifdef RENEWAL branch — ported from the Thai fork
//     rathenaThailand/src/map/status.cpp (see baseatk_re.go, derived_re.go,
//     amotion_re.go).
//
// Renewal mode and PACKETVER are INDEPENDENT axes in rAthena. The mode comes
// from the operator's zone.renewal flag (pkg/ro/mode.Mode), never from the
// packet version; do not "simplify" the two branches toward each other — they
// are materially different on purpose.
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

// Equipment holds the right/left-side contributions equipment adds. For a naked
// character every field is 0; the slice has no inventory, so the burst always
// builds from the zero value.
type Equipment struct {
	WeaponATK int32 // right-side ATK (weapon damage); 0 unequipped
	// WeaponSubType is the equipped weapon's type name (e.g. "Knuckle",
	// "Dagger") the combat kernel keys the size_fix lookup on. Empty when no
	// weapon is equipped; the kernel then resolves the identity (100%) size rate.
	WeaponSubType string
	// WeaponElement is the equipped weapon's attack element, an attrfix.Ele*
	// index (0 = Neutral). item_db carries no per-item element column yet, so
	// every production weapon is Neutral here; the field is the wiring point for
	// forged/elemental weapons and lets combat tests inject a known element.
	WeaponElement int
	ItemDEF       int32 // left-side DEF (armor); 0 unequipped
	ItemMDEF      int32 // left-side MDEF (armor); 0 unequipped
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
// applying clif_initialstatus's field mapping and wire clamping. The mode's
// formula set fs supplies every derived value, so the caller resolves the set
// once from a Registry and passes it in; no mode branching lives here. NeedX
// uses the kernel's pc_need_status_point (packet.StatusPointCost). Source:
// clif.cpp clif_initialstatus, lines 4123-4162.
func ZCStatus(in StatusInputs, fs FormulaSet) packet.StatusResponse {
	b := in.Base
	return packet.StatusResponse{
		StatusPoint: uint16(in.StatusPoint), //nolint:gosec // G115: clif min(.,INT16_MAX); slice points are tiny
		Str:         clampU8(b.Str), NeedStr: packet.StatusPointCost(clampU8(b.Str)),
		Agi: clampU8(b.Agi), NeedAgi: packet.StatusPointCost(clampU8(b.Agi)),
		Vit: clampU8(b.Vit), NeedVit: packet.StatusPointCost(clampU8(b.Vit)),
		Int: clampU8(b.Int), NeedInt: packet.StatusPointCost(clampU8(b.Int)),
		Dex: clampU8(b.Dex), NeedDex: packet.StatusPointCost(clampU8(b.Dex)),
		Luk: clampU8(b.Luk), NeedLuk: packet.StatusPointCost(clampU8(b.Luk)),
		Atk1:     clampInt16(fs.BaseATK(b)),
		Atk2:     clampInt16(in.Equipment.WeaponATK),
		MatkMax:  clampInt16(fs.MatkMax(b)),
		MatkMin:  clampInt16(fs.MatkMin(b)),
		Def1:     clampInt16(in.Equipment.ItemDEF),
		Def2:     clampInt16(fs.SoftDEF(b)),
		Mdef1:    clampInt16(in.Equipment.ItemMDEF),
		Mdef2:    clampInt16(fs.SoftMDEF(b)),
		Hit:      clampInt16(fs.Hit(b)),
		Flee:     clampInt16(fs.Flee(b)),
		Flee2:    clampInt16(fs.PerfectFlee(b) / 10),
		Critical: clampInt16(fs.Critical(b) / 10),
		ASPD:     clampInt16(fs.Amotion(b, in.WeaponBaseASPD)),
		PlusASPD: 0,
	}
}
