package app

import (
	"fmt"

	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/combat"
	"github.com/bouroo/goAthena/pkg/ro/mobdb"
	"github.com/bouroo/goAthena/pkg/ro/mode"
	"github.com/bouroo/goAthena/pkg/ro/statcalc"
)

// CombatService resolves melee attacks against entities. It builds the
// combat.Attacker/Defender profiles from the WorldService registry — mob
// defenders resolve their DEF/stats from mob_db — computes damage via the
// kernel's pre-renewal formula, and applies it to the defender.
type CombatService struct {
	world *WorldService
	mobs  *mobdb.Registry
	fs    statcalc.FormulaSet
	m     mode.Mode
}

// NewCombatService builds a combat service backed by the world registry and the
// mob_db registry. The formula set and mode are fixed at construction
// (pre-renewal for Thai Classic). mobs may be nil (mob defenders then resolve 0
// DEF), but the app wiring provisions a non-nil registry.
func NewCombatService(world *WorldService, mobs *mobdb.Registry) *CombatService {
	return &CombatService{world: world, mobs: mobs, fs: statcalc.PreRenewalSet, m: mode.PreRenewal}
}

// Attack resolves a melee hit from attackerID to defenderID and applies the
// damage. It returns the resolved damage (≥0) and died=true when this hit
// reduced the defender's HP to 0 (the orchestrator despawns/drops on death).
// The hit is assumed to connect (the flee/miss gate lands with accuracy work).
func (c *CombatService) Attack(attackerID, defenderID domain.EntityID) (int32, bool, error) {
	attacker, err := c.world.Get(attackerID)
	if err != nil {
		return 0, false, fmt.Errorf("attacker: %w", err)
	}
	defender, err := c.world.Get(defenderID)
	if err != nil {
		return 0, false, fmt.Errorf("defender: %w", err)
	}
	result := combat.NormalMelee(
		combat.Attacker{Base: attackerBase(attacker)},
		defenderProfile(c.mobs, defender),
		c.fs,
		c.m,
	)
	died := c.applyDamage(defenderID, result.Damage)
	return result.Damage, died, nil
}

// attackerBase maps a world entity's base stats to the kernel's stat base. Mob
// base stats stay zero (mob-vs-mob attacking is not wired), so a PC's char-table
// stats are the only non-zero source feeding BaseATK.
func attackerBase(e domain.Entity) statcalc.Base {
	return statcalc.Base{
		Level: uint16(e.Level), //nolint:gosec // G115: level bounded by game values
		Str:   e.Str, Agi: e.Agi, Vit: e.Vit, Int: e.Int, Dex: e.Dex, Luk: e.Luk,
	}
}

// defenderProfile builds the defensive profile for one hit. A mob defender
// resolves HardDEF (Defense) and SoftDEF (Vit) from mob_db by the entity's
// Class; a missing entry or zero class yields 0 DEF. A PC defender has no
// equipment DEF yet (lands with M10), so only its Vit softDEF applies. The
// pre-renewal mode uses Vit directly for SoftDEF (status_calc_misc #else).
func defenderProfile(mobs *mobdb.Registry, e domain.Entity) combat.Defender {
	if e.Type != domain.EntityTypeMob || e.Class == 0 {
		return combat.Defender{SoftDEF: int32(e.Vit)}
	}
	if mob := mobs.Get(e.Class); mob != nil {
		return combat.Defender{HardDEF: mob.Defense, SoftDEF: mob.Vit}
	}
	return combat.Defender{}
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
