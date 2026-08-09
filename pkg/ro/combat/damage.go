// Package combat resolves the outcome of a single physical attack hit. It is the
// goAthena port of rAthena's battle_calc_weapon_attack for the deterministic slice
// the Thai client exercises first: a player's normal auto-attack (skill_id == 0)
// against a mob, with the hit connecting (no RNG miss) and no critical multiplier.
// The damage base is status base-ATK plus the equipped weapon's WeaponATK
// (ItemDEF/ItemMDEF and element/size/crit remain future work).
//
// The damage math is faithful and mode-keyed (mode.Mode): Pre-Renewal uses
// battle_calc_base_damage + the multiplicative DEF reduction + battle_min_damage;
// Renewal uses battle_calc_damage_parts (statusAtk × 2) + the (4000+def1)/(4000+
// 10·def1) softDEF ratio. Element, size, crit, and the hit/flee miss gate are out
// of scope and land with the accuracy/crit work.
package combat

import (
	"github.com/bouroo/goAthena/pkg/ro/mode"
	"github.com/bouroo/goAthena/pkg/ro/statcalc"
)

// Attacker is the attacker's damage-relevant profile for one normal melee hit.
type Attacker struct {
	// Base is the attacker's six base stats + level; BaseATK is derived from it.
	Base statcalc.Base
	// Equipment carries the equipped-gear contributions. WeaponATK folds into the
	// damage base; ItemDEF/ItemMDEF are defender-side and unused here. A zero
	// Equipment (a naked PC) adds nothing, preserving the bare-handed baseline.
	Equipment statcalc.Equipment
}

// Defender is the mob's defensive profile for one normal melee hit.
type Defender struct {
	// HardDEF is status def1 — the mob_db Defense column (percentage / softDEF
	// ratio input).
	HardDEF int32
	// SoftDEF is status def2, reduced to the mode's form by the caller: Pre-
	// Renewal mobs use Vit; Renewal mobs use (Level+Vit)/2 (status.cpp:2654).
	// The pre-re rnd(0,(Vit/20)²) variance is 0 for every mob with Vit < 20, so it
	// is omitted for the deterministic slice.
	SoftDEF int32
}

// Result is one normal melee hit's resolved outcome.
type Result struct {
	Damage int32
}

// NormalMelee resolves the damage of one connecting normal melee hit (skill_id
// == 0) by Attacker a against Defender d in game mode m, using the mode's
// FormulaSet fs for the base-ATK contribution. The damage base is statusAtk
// (batk = fs.BaseATK(a.Base)) plus the equipped weapon's WeaponATK; equipAtk
// and masteryAtk remain 0 (no cards/over-ups this milestone). The hit is
// assumed to connect; the miss (flee) and critical RNG axes are separate and
// not modeled here.
func NormalMelee(a Attacker, d Defender, fs statcalc.FormulaSet, m mode.Mode) Result {
	batk := int64(fs.BaseATK(a.Base))
	watk := int64(a.Equipment.WeaponATK)
	switch m {
	case mode.Renewal:
		// battle_calc_damage_parts: statusAtk = batk (attr-fixed; Neutral attacker
		// ⇒ 100%), then ×2, plus weaponAtk; equipAtk/masteryAtk = 0 this milestone,
		// so the assembled wd.damage = 2·batk + watk (battle.cpp:5525). The Renewal
		// defense reduction applies the (4000+def1)/(4000+10·def1) ratio to the
		// assembled total and subtracts vit_def (battle.cpp:4867). It is NOT
		// floored — battle_min_damage is guarded by #ifndef RENEWAL
		// (battle.cpp:4912) — so a tough target can yield a non-positive raw
		// damage that is clamped to 0 here so the client never receives a
		// negative HP delta.
		damage := reduceRenewal(2*batk+watk, int64(d.HardDEF), int64(d.SoftDEF))
		return Result{Damage: clampNonNeg(damage)}
	default: // mode.PreRenewal
		// battle_calc_base_damage: batk + the weapon's watk is the damage base.
		// The multiplicative DEF reduction subtracts vit_def (battle.cpp:4871-4882),
		// then battle_min_damage(..., 1) floors a connecting hit at 1
		// (battle.cpp:4912).
		damage := reducePreRenewal(batk+watk, int64(d.HardDEF), int64(d.SoftDEF))
		damage = max(damage, 1)              // battle_min_damage(...,1): a connecting hit floors at 1
		return Result{Damage: int32(damage)} //nolint:gosec // G115: floored ≥1, bounded by game values
	}
}

// reducePreRenewal applies the classic multiplicative DEF reduction:
// damage·(100−def1)/100 − vit_def (battle.cpp:4871-4882, #ifndef RENEWAL).
func reducePreRenewal(damage, def1, vitDef int64) int64 {
	damage = damage * (100 - def1) / 100
	return damage - vitDef
}

// reduceRenewal applies the Renewal softDEF ratio:
// damage·(4000+def1)/(4000+10·def1) − vit_def (battle.cpp:4867).
func reduceRenewal(damage, def1, vitDef int64) int64 {
	return damage*(4000+def1)/(4000+10*def1) - vitDef
}

// clampNonNeg clamps a non-positive Renewal damage value to 0 for the wire
// layer. The Renewal path does not floor (battle_min_damage is #ifndef RENEWAL),
// so a tough target can yield a non-positive raw damage that must not reach the
// client as a negative HP delta.
func clampNonNeg(damage int64) int32 {
	return int32(max(damage, 0)) //nolint:gosec // G115: non-negative and bounded by game values
}
