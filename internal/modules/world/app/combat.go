package app

import (
	"context"
	"fmt"
	"math/rand"

	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/attrfix"
	"github.com/bouroo/goAthena/pkg/ro/combat"
	"github.com/bouroo/goAthena/pkg/ro/mobdb"
	"github.com/bouroo/goAthena/pkg/ro/mode"
	"github.com/bouroo/goAthena/pkg/ro/sizefix"
	"github.com/bouroo/goAthena/pkg/ro/statcalc"
)

// equipmentProfiler resolves a PC attacker's equipped-gear contributions
// (WeaponATK/ItemDEF/ItemMDEF) for the kernel's statcalc. The production
// *EquipService satisfies it; a nil profiler (the naked baseline) resolves a
// zero profile, so tests that do not exercise equipment pass nil.
type equipmentProfiler interface {
	EquipmentProfile(ctx context.Context, accountID, charID uint32) (statcalc.Equipment, error)
}

// Dice is the RNG surface the miss/critical rolls draw from: a half-open
// [0,n) integer. *math/rand.Rand satisfies it. A nil Dice (the legacy default)
// skips the rolls and resolves a connecting, non-critical hit — preserving the
// deterministic pre-accuracy baseline for tests that do not inject randomness.
type Dice interface {
	Intn(n int) int
}

// globalDice draws from the process-wide math/rand source, which is concurrency-
// safe (locked) and auto-seeded (Go 1.20+). It is the production combat RNG;
// tests inject a deterministic *rand.Rand instead.
type globalDice struct{}

func (globalDice) Intn(n int) int {
	return rand.Intn(n) //nolint:gosec // G404: game combat RNG, not security-sensitive
}

// NewGlobalDice returns the production combat RNG. Combat resolves on the single
// 50Hz world tick; the global source is concurrency-safe regardless.
func NewGlobalDice() Dice { return globalDice{} }

// CombatService resolves melee attacks against entities. It builds the
// combat.Attacker/Defender profiles from the WorldService registry — mob
// defenders resolve their DEF/stats/element/size from mob_db, PC attackers fold
// their equipped WeaponATK and weapon element/subtype into the damage base —
// computes damage via the kernel's pre-renewal formula, rolls the hit/crit axes,
// and applies it to the defender.
type CombatService struct {
	world *WorldService
	mobs  *mobdb.Registry
	equip equipmentProfiler
	// attrs/sizes are the caller-injected element/size modifier tables handed to
	// the kernel's NormalMelee. nil is the identity (100%): melee resolves no
	// elemental/size adjustment, the pre-attr_fix baseline.
	attrs *attrfix.RateTable
	sizes *sizefix.SizeTable
	fs    statcalc.FormulaSet
	m     mode.Mode
	dice  Dice
}

// Option configures a CombatService's modifier tables and RNG source.
type Option func(*CombatService)

// WithAttributeFix injects the attr_fix element-rate table the kernel multiplies
// post-DEF damage by. Omit for the identity (100%) baseline.
func WithAttributeFix(t *attrfix.RateTable) Option {
	return func(c *CombatService) { c.attrs = t }
}

// WithSizeFix injects the size_fix weapon-type × mob-size table. Omit for the
// identity (100%) baseline.
func WithSizeFix(t *sizefix.SizeTable) Option {
	return func(c *CombatService) { c.sizes = t }
}

// WithDice injects the RNG source for the miss/crit rolls. Omit (nil) for the
// deterministic legacy default: every hit connects and none crits.
func WithDice(d Dice) Option {
	return func(c *CombatService) { c.dice = d }
}

// NewCombatService builds a combat service backed by the world registry, the
// mob_db registry, and (optionally) an equipment profiler. The formula set and
// mode are fixed at construction (pre-renewal for Thai Classic). mobs may be nil
// (mob defenders then resolve 0 DEF); equip may be nil (PC attackers then fight
// with the naked, zero-equipment baseline). The element/size modifier tables and
// RNG are optional: omit them for the identity-modifier, always-hit baseline
// (the pre-accuracy contract); the app wiring provisions all four.
func NewCombatService(world *WorldService, mobs *mobdb.Registry, equip equipmentProfiler, opts ...Option) *CombatService {
	c := &CombatService{world: world, mobs: mobs, equip: equip, fs: statcalc.PreRenewalSet, m: mode.PreRenewal}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Attack resolves a melee hit from attackerID to defenderID and applies the
// damage. It returns the resolved damage (≥0) and died=true when this hit
// reduced the defender's HP to 0 (the orchestrator despawns/drops on death). A
// miss returns 0 damage and applies nothing.
//
// The accuracy/critical axes are RNG-driven but kept OUT of the deterministic
// kernel (combat.NormalMelee): this service computes the hit/crit totals, rolls
// them against the injected Dice, and passes the outcome in as a combat.Roll.
// With no Dice injected (nil) the roll resolves a connecting, non-critical hit —
// the legacy pre-accuracy baseline — so callers that do not inject randomness
// stay fully deterministic.
//
// Roll formulas (pre-renewal, mirroring rAthena battle.cpp's hit/crit calc):
//   - hitPct = clamp(80 + attackerHit − defenderFlee, 5, 95), where attackerHit
//     is fs.Hit (Level+Dex) and defenderFlee is the mob's flee (Level+Agi). The
//     hit connects when Dice.Intn(100) < hitPct.
//   - critRate = fs.Critical, the stored per-mille critical total (10 + Luk·10/3;
//     the client divides by 10 for display). A critical lands when
//     Dice.Intn(1000) < critRate. A critical bypasses flee (pre-renewal), so it
//     always connects regardless of the hit roll (the kernel's isMiss is
//     !Hit && !Crit).
func (c *CombatService) Attack(attackerID, defenderID domain.EntityID) (int32, bool, error) {
	attacker, err := c.world.Get(attackerID)
	if err != nil {
		return 0, false, fmt.Errorf("attacker: %w", err)
	}
	defender, err := c.world.Get(defenderID)
	if err != nil {
		return 0, false, fmt.Errorf("defender: %w", err)
	}
	equip, err := c.attackerEquipment(attacker)
	if err != nil {
		return 0, false, fmt.Errorf("attacker: %w", err)
	}
	base := c.attackerBase(attacker)
	def := defenderProfile(c.mobs, defender)
	roll := c.roll(base, def)
	result := combat.NormalMelee(
		combat.Attacker{
			Base:          base,
			Equipment:     equip,
			WeaponElement: equip.WeaponElement,
			WeaponSubType: equip.WeaponSubType,
		},
		def,
		c.fs,
		c.m,
		combat.WithAttributeFix(c.attrs),
		combat.WithSizeFix(c.sizes),
		combat.WithRoll(roll),
	)
	if result.Miss {
		return 0, false, nil
	}
	died := c.applyDamage(defenderID, result.Damage)
	return result.Damage, died, nil
}

// minHitRate/maxHitRate clamp the pre-renewal accuracy percentage, mirroring
// rAthena's cap_value(hit_rate, battle_config min/max_hitrate) with the default
// battle_config bounds: a swing always has at least a 5% chance to connect.
const (
	minHitRate = 5
	maxHitRate = 95
)

// roll resolves the hit/flee and critical RNG outcome for one melee hit. With no
// Dice injected it returns the legacy connecting, non-critical hit; otherwise it
// draws against the pre-renewal accuracy and critical formulas (see Attack). Hit
// is drawn before crit so the two Intn calls are order-stable for a given Dice.
func (c *CombatService) roll(base statcalc.Base, def combat.Defender) combat.Roll {
	if c.dice == nil {
		return combat.Roll{Hit: true} // legacy default: connects, non-critical
	}
	hitPct := int(c.fs.Hit(base)) - int(def.Flee) + 80
	hitPct = max(minHitRate, min(hitPct, maxHitRate))
	critRate := int(c.fs.Critical(base))
	hit := c.dice.Intn(100) < hitPct
	crit := c.dice.Intn(1000) < critRate
	return combat.Roll{Hit: hit, Crit: crit}
}

// attackerEquipment resolves an attacker's right-side ATK contributions. A PC
// attacker folds its equipped WeaponATK via the injected profiler; a mob
// attacker has no inventory and instead resolves its mob_db Attack (the monster's
// melee damage range, used as weapon ATK). A nil profiler (the naked baseline)
// resolves zero for a PC. A load failure is propagated so the caller can surface
// it rather than silently degrading the hit to a naked swing.
func (c *CombatService) attackerEquipment(e domain.Entity) (statcalc.Equipment, error) {
	if e.Type == domain.EntityTypeMob {
		return mobAttackerEquipment(c.mobs, e), nil
	}
	if c.equip == nil {
		return statcalc.Equipment{}, nil
	}
	eq, err := c.equip.EquipmentProfile(context.Background(), e.Account, uint32(e.ID))
	if err != nil {
		return statcalc.Equipment{}, fmt.Errorf("equipment profile: %w", err)
	}
	return eq, nil
}

// attackerBase maps a world entity's base stats to the kernel's stat base. A PC
// feeds its char-table stats directly; a mob resolves Str/Agi/Dex/Luk/Level from
// mob_db by Class (its BaseATK and Hit/Flee come from there, not the entity, so
// a mob attacker can actually hit and damage a player). A missing mob_db entry
// or zero class yields the zero base — no damage.
func (c *CombatService) attackerBase(e domain.Entity) statcalc.Base {
	if e.Type == domain.EntityTypeMob && e.Class != 0 {
		if mob := c.mobs.Get(e.Class); mob != nil {
			return statcalc.Base{
				Level: uint16(mob.Level), //nolint:gosec // G115: level bounded by game values
				Str:   uint16(mob.Str),   //nolint:gosec // G115: mob stats bounded by game values
				Agi:   uint16(mob.Agi),   //nolint:gosec // G115: mob stats bounded by game values
				Vit:   uint16(mob.Vit),   //nolint:gosec // G115: mob stats bounded by game values
				Int:   uint16(mob.Int),   //nolint:gosec // G115: mob stats bounded by game values
				Dex:   uint16(mob.Dex),   //nolint:gosec // G115: mob stats bounded by game values
				Luk:   uint16(mob.Luk),   //nolint:gosec // G115: mob stats bounded by game values
			}
		}
	}
	return statcalc.Base{
		Level: uint16(e.Level), //nolint:gosec // G115: level bounded by game values
		Str:   e.Str, Agi: e.Agi, Vit: e.Vit, Int: e.Int, Dex: e.Dex, Luk: e.Luk,
	}
}

// mobAttackerEquipment resolves a mob attacker's weapon ATK from mob_db. The
// monster's Attack column is its raw damage range, used directly as the
// right-side (weapon) ATK the kernel adds to BaseATK — there is no inventory.
// Attack2 is the secondary range and folds into WeaponATK as well so the mob's
// full damage spread applies. A nil registry or missing entry resolves zero.
func mobAttackerEquipment(mobs *mobdb.Registry, e domain.Entity) statcalc.Equipment {
	if mobs == nil || e.Class == 0 {
		return statcalc.Equipment{}
	}
	mob := mobs.Get(e.Class)
	if mob == nil {
		return statcalc.Equipment{}
	}
	return statcalc.Equipment{WeaponATK: mob.Attack + mob.Attack2}
}

// defenderProfile builds the defensive profile for one hit. A mob defender
// resolves HardDEF (Defense), SoftDEF (Vit), Element/ElementLevel, Size, and
// Flee (Level+Agi) from mob_db by the entity's Class; a missing entry or zero
// class yields the zero profile. A PC defender has no equipment DEF yet, so only
// its Vit softDEF applies. The pre-renewal mode uses Vit directly for SoftDEF
// (status_calc_misc #else).
func defenderProfile(mobs *mobdb.Registry, e domain.Entity) combat.Defender {
	if e.Type != domain.EntityTypeMob || e.Class == 0 {
		return combat.Defender{SoftDEF: int32(e.Vit)}
	}
	if mob := mobs.Get(e.Class); mob != nil {
		return combat.Defender{
			HardDEF:      mob.Defense,
			SoftDEF:      mob.Vit,
			Element:      mobElement(mob.Element),
			ElementLevel: mob.ElementLevel,
			Size:         mob.Size,
			Flee:         mob.Level + mob.Agi,
		}
	}
	return combat.Defender{}
}

// mobElement resolves a mob's defense element name (the mob_db Element column,
// e.g. "Water") to its attr_fix matrix index. An empty or unknown name resolves
// to Neutral (0), which the attr_fix table yields the 100% baseline against.
func mobElement(name string) int {
	ele, ok := attrfix.ParseElement(name)
	if !ok {
		return attrfix.EleNeutral
	}
	return ele
}

// applyDamage subtracts damage from the entity's HP, clamping at 0. It returns
// died=true when the entity's HP reached 0 on this hit (was alive, now dead).
func (c *CombatService) applyDamage(id domain.EntityID, dmg int32) bool {
	c.world.mu.Lock()
	defer c.world.mu.Unlock()
	e, ok := c.world.entities[id]
	if !ok {
		return false
	}
	wasAlive := e.HP > 0
	e.HP -= dmg
	if e.HP <= 0 {
		e.HP = 0
		return wasAlive
	}
	return false
}
