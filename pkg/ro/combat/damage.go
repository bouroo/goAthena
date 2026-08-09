// Package combat resolves the outcome of a single physical attack hit. It is the
// goAthena port of rAthena's battle_calc_weapon_attack for the deterministic slice
// the Thai client exercises first: a player's normal auto-attack (skill_id == 0)
// against a mob. The damage base is status base-ATK plus the equipped weapon's
// WeaponATK; ItemDEF/ItemMDEF fold in as the defender's Hard/Soft DEF.
//
// The damage math is faithful and mode-keyed (mode.Mode): Pre-Renewal uses
// battle_calc_base_damage + the multiplicative DEF reduction + battle_min_damage;
// Renewal uses battle_calc_damage_parts (statusAtk × 2) + the (4000+def1)/(4000+
// 10·def1) softDEF ratio.
//
// Post-DEF the multiplicative modifiers apply in rAthena order — element rate
// (attr_fix), then weapon-type × mob-size rate (size_fix), then the critical
// multiplier (pre-renewal 2x). The hit/flee miss gate and the critical roll are
// RNG axes kept OUT of this pure kernel: the caller computes the hit/crit rates,
// rolls, and passes the outcome (Roll) in, so NormalMelee stays deterministic and
// table-testable (no math/rand in the hot path). A miss deals 0 damage; a
// critical bypasses flee (pre-renewal) and doubles the post-modifier damage.
package combat

import (
	"github.com/bouroo/goAthena/pkg/ro/attrfix"
	"github.com/bouroo/goAthena/pkg/ro/mode"
	"github.com/bouroo/goAthena/pkg/ro/sizefix"
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
	// WeaponElement is the equipped weapon's attack element (an attrfix.Ele*
	// index). The zero value is Neutral (attrfix.EleNeutral), which the
	// attr_fix table resolves to the 100% baseline against a Neutral defender.
	WeaponElement int
	// WeaponSubType is the weapon's type name (e.g. "Dagger", "Knuckle") used to
	// look up the size_fix modifier. Empty resolves to the identity size rate.
	WeaponSubType string
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
	// Element is the mob's defense element (an attrfix.Ele* index); ElementLevel
	// selects the attr_fix level matrix. A zero Element (Neutral) against a
	// Neutral weapon is the 100% baseline.
	Element int
	// ElementLevel is the mob's element level (1..4), selecting the attr_fix
	// level matrix. Zero degrades to the identity element rate (100%).
	ElementLevel int32
	// Size is the mob's size name ("Small"/"Medium"/"Large") for the size_fix
	// lookup. Empty resolves to the identity size rate.
	Size string
	// Flee is the defender's flee total (level + agi); it is the caller's miss-
	// roll input and not consumed by NormalMelee directly.
	Flee int32
}

// Result is one normal melee hit's resolved outcome.
type Result struct {
	// Damage is the final applied damage (post element/size/crit, 0 on a miss).
	Damage int32
	// ElementRate / SizeRate are the applied percentage modifiers (100 = neutral).
	ElementRate int32
	SizeRate    int32
	// HitRate / CritRate are the attacker's accuracy and critical totals, mirrored
	// from the FormulaSet so the caller (for its RNG roll) and the wire/log layer
	// share one source rather than recomputing.
	HitRate  int32
	CritRate int32
	// Crit is true when the caller rolled a critical (2x pre-renewal). Miss is
	// true when the hit did not connect. Both reflect the caller's Roll; a
	// critical (which bypasses flee) is never a miss.
	Crit bool
	Miss bool
}

// Roll is the caller-decided outcome of the hit/flee and crit RNG axes. It keeps
// math/rand out of NormalMelee so the damage kernel stays pure and table-testable:
// the caller computes the hit/crit rates, rolls, and passes the outcome in. The
// zero Roll is a connecting, non-critical hit — the legacy default before this
// milestone wired accuracy and crit.
type Roll struct {
	// Hit is true when the attack connects (the flee roll failed for the
	// defender). A critical always connects, so Hit is only consulted when Crit
	// is false.
	Hit bool
	// Crit is true when the attacker rolled a critical hit (pre-renewal 2x).
	Crit bool
}

// Option configures NormalMelee's optional registries and roll outcome.
type Option func(*resolveConfig)

type resolveConfig struct {
	attrs *attrfix.RateTable
	sizes *sizefix.SizeTable
	roll  Roll
}

// WithAttributeFix injects the attr_fix rate table. Without it NormalMelee uses
// the identity element rate (100%), preserving the legacy neutral baseline when
// the caller has not loaded attr_fix yet.
func WithAttributeFix(t *attrfix.RateTable) Option {
	return func(c *resolveConfig) { c.attrs = t }
}

// WithSizeFix injects the size_fix table. Without it NormalMelee uses the
// identity size rate (100%).
func WithSizeFix(t *sizefix.SizeTable) Option {
	return func(c *resolveConfig) { c.sizes = t }
}

// WithRoll sets the pre-rolled hit/crit outcome. The zero Roll is a connecting,
// non-critical hit.
func WithRoll(r Roll) Option {
	return func(c *resolveConfig) { c.roll = r }
}

// NormalMelee resolves the damage of one normal melee hit (skill_id == 0) by
// Attacker a against Defender d in game mode m, using the mode's FormulaSet fs
// for the base-ATK contribution. The damage base is statusAtk (batk =
// fs.BaseATK(a.Base)) plus the equipped weapon's WeaponATK; equipAtk and
// masteryAtk remain 0 (no cards/over-ups this milestone).
//
// Post-DEF the modifiers apply in rAthena order — element rate, then size rate,
// then the critical multiplier (pre-renewal 2x). The element/size rates come from
// the caller-injected attr_fix / size_fix tables (see WithAttributeFix,
// WithSizeFix); a nil table is the identity (100%). The miss gate and critical
// roll are the caller's RNG axes, passed in via WithRoll; the zero Roll is a
// connecting non-critical hit.
func NormalMelee(a Attacker, d Defender, fs statcalc.FormulaSet, m mode.Mode, opts ...Option) Result {
	cfg := resolveConfig{roll: Roll{Hit: true}} // legacy default: connecting, non-critical
	for _, o := range opts {
		o(&cfg)
	}

	// Accuracy/crit totals: pure statcalc math the caller mirrors to roll; reported
	// so the wire/log layer need not recompute them.
	hitRate := fs.Hit(a.Base)
	critRate := fs.Critical(a.Base)

	elementRate := elementModifier(cfg.attrs, a.WeaponElement, d.Element, int(d.ElementLevel))
	sizeRate := sizeModifier(cfg.sizes, a.WeaponSubType, d.Size)

	batk := int64(fs.BaseATK(a.Base))
	watk := int64(a.Equipment.WeaponATK)
	crit := cfg.roll.Crit
	miss := isMiss(cfg.roll)

	switch m {
	case mode.Renewal:
		// battle_calc_damage_parts: statusAtk = batk (attr-fixed), then ×2, plus
		// weaponAtk; equipAtk/masteryAtk = 0 this milestone, so the assembled
		// wd.damage = 2·batk + watk (battle.cpp:5525). The Renewal defense
		// reduction applies the (4000+def1)/(4000+10·def1) ratio and subtracts
		// vit_def (battle.cpp:4867); it is NOT floored (battle_min_damage is
		// #ifndef RENEWAL), so a tough target can yield non-positive raw damage
		// that clampNonNeg clamps to 0 so the client never sees a negative delta.
		damage := reduceRenewal(2*batk+watk, int64(d.HardDEF), int64(d.SoftDEF))
		damage = applyPostDEF(damage, int64(elementRate), int64(sizeRate), crit)
		if miss {
			damage = 0
		}
		return Result{
			Damage:      clampNonNeg(damage),
			ElementRate: elementRate, SizeRate: sizeRate,
			HitRate: hitRate, CritRate: critRate,
			Crit: crit, Miss: miss,
		}
	default: // mode.PreRenewal
		// battle_calc_base_damage: batk + the weapon's watk is the damage base.
		// The multiplicative DEF reduction subtracts vit_def (battle.cpp:4871-
		// 4882), then battle_min_damage(..., 1) floors a connecting hit at 1
		// (battle.cpp:4912). Element/size/crit then apply on top, so an immune
		// element (rate 0) still yields 0 on a connecting hit.
		damage := reducePreRenewal(batk+watk, int64(d.HardDEF), int64(d.SoftDEF))
		damage = max(damage, 1) // battle_min_damage(...,1): post-DEF floor for a connecting hit
		damage = applyPostDEF(damage, int64(elementRate), int64(sizeRate), crit)
		if miss {
			damage = 0
		}
		return Result{
			Damage:      int32(damage), //nolint:gosec // G115: bounded by game values, ≥0
			ElementRate: elementRate, SizeRate: sizeRate,
			HitRate: hitRate, CritRate: critRate,
			Crit: crit, Miss: miss,
		}
	}
}

// isMiss reports whether the roll outcome is a miss: the hit failed to connect
// AND it was not a critical. A critical bypasses flee (pre-renewal), so Crit
// alone is enough for the hit to connect.
func isMiss(r Roll) bool { return !r.Hit && !r.Crit }

// applyPostDEF applies the multiplicative post-DEF modifiers in rAthena order:
// element rate (×rate/100), then size rate (×rate/100), then the critical
// multiplier (pre-renewal 2x). Renewal uses a different critical multiplier; it
// shares this path for now and is verified when the Renewal combat path lands —
// the pre-renewal Thai client is the only exercised path.
func applyPostDEF(damage, elementRate, sizeRate int64, crit bool) int64 {
	damage = damage * elementRate / 100
	damage = damage * sizeRate / 100
	if crit {
		damage *= 2
	}
	return damage
}

// elementModifier resolves the elemental percentage from the attr_fix table. A
// nil table or a zero defender element level is the identity rate (100%), which
// keeps the legacy neutral baseline before attr_fix is wired.
func elementModifier(t *attrfix.RateTable, attackerEle, defenderEle, defenderLevel int) int32 {
	if t == nil || defenderLevel <= 0 {
		return 100
	}
	return int32(t.Rate(attackerEle, defenderEle, defenderLevel)) //nolint:gosec // G115: percent, bounded 0..200
}

// sizeModifier resolves the weapon-type × mob-size percentage from the size_fix
// table. A nil table, empty weapon type, or empty size is the identity rate
// (100%).
func sizeModifier(t *sizefix.SizeTable, weaponSubType, mobSize string) int32 {
	if t == nil || weaponSubType == "" || mobSize == "" {
		return 100
	}
	return int32(t.Rate(weaponSubType, mobSize)) //nolint:gosec // G115: percent, bounded 0..200
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
