package app

import (
	"fmt"

	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/combat"
	"github.com/bouroo/goAthena/pkg/ro/mode"
	"github.com/bouroo/goAthena/pkg/ro/statcalc"
)

// CombatService resolves melee attacks against entities. It builds the
// combat.Attacker/Defender profiles from the WorldService registry, computes
// damage via the kernel's pre-renewal formula, and applies it to the defender.
type CombatService struct {
	world *WorldService
	fs    statcalc.FormulaSet
	m     mode.Mode
}

// NewCombatService builds a combat service backed by the world registry. The
// formula set and mode are fixed at construction (pre-renewal for Thai Classic).
func NewCombatService(world *WorldService) *CombatService {
	return &CombatService{world: world, fs: statcalc.PreRenewalSet, m: mode.PreRenewal}
}

// Attack resolves a melee hit from attackerID to defenderID and applies the
// damage. Returns the resolved damage (≥0). The hit is assumed to connect
// (the flee/miss gate lands with accuracy work).
func (c *CombatService) Attack(attackerID, defenderID domain.EntityID) (int32, error) {
	attacker, err := c.world.Get(attackerID)
	if err != nil {
		return 0, fmt.Errorf("attacker: %w", err)
	}
	if _, err := c.world.Get(defenderID); err != nil {
		return 0, fmt.Errorf("defender: %w", err)
	}
	result := combat.NormalMelee(
		combat.Attacker{Base: statcalc.Base{Level: uint16(attacker.Level)}}, //nolint:gosec // G115: level bounded.
		combat.Defender{HardDEF: 0, SoftDEF: 0},                             // mob DEF stats wired when mob_db loads
		c.fs,
		c.m,
	)
	c.applyDamage(defenderID, result.Damage)
	return result.Damage, nil
}

// applyDamage subtracts damage from the entity's HP, clamping at 0.
func (c *CombatService) applyDamage(id domain.EntityID, dmg int32) {
	c.world.mu.Lock()
	defer c.world.mu.Unlock()
	e, ok := c.world.entities[id]
	if !ok {
		return
	}
	e.HP -= dmg
	if e.HP < 0 {
		e.HP = 0
	}
}
