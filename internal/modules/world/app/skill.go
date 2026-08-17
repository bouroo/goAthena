package app

import (
	"errors"
	"fmt"

	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/skilldb"
)

// Skill cast validation errors. They are distinct sentinels so callers can
// branch on cause (e.g. gateway -> different deny packets) without parsing
// strings, per the no-branch-on-error-strings rule.
var (
	// ErrUnknownSkill is returned when the cast skill ID is not in the registry.
	ErrUnknownSkill = errors.New("skill not found")
	// ErrInvalidLevel is returned when the requested level is outside [1, MaxLevel].
	ErrInvalidLevel = errors.New("invalid skill level")
	// ErrSkillOutOfRange is returned when the target is beyond the skill's range.
	ErrSkillOutOfRange = errors.New("target out of skill range")
	// ErrInsufficientSP is returned when the caster lacks the SP cost.
	ErrInsufficientSP = errors.New("insufficient SP")
)

// SkillService resolves a skill cast against a target entity. It validates the
// cast (skill known, level in range, target reachable, SP affordable), spends
// the SP, and for an enemy-targeted (offensive) skill resolves a melee-equivalent
// hit through the existing CombatService. Full skill-damage modeling — element,
// size, crit, and per-skill multipliers — is deliberately out of scope here and
// left to future work; this phase delivers a castable, visible, damage-dealing
// skill by reusing the proven pre-renewal melee path.
type SkillService struct {
	world  *WorldService
	combat *CombatService
	skills *skilldb.Registry
}

// NewSkillService builds a skill service backed by the world registry, the
// combat service (for offensive-skill hits), and the skill_db registry. skills
// may be nil (every cast resolves unknown), but the app wiring provisions a
// non-nil registry.
func NewSkillService(world *WorldService, combat *CombatService, skills *skilldb.Registry) *SkillService {
	return &SkillService{world: world, combat: combat, skills: skills}
}

// UseSkillOnTarget resolves a single-target skill cast from casterID onto
// targetGID at the given level. It returns the resolved damage (>=0) and
// died=true when this cast reduced the target's HP to 0, mirroring
// CombatService.Attack's contract. SP is spent only on a cast that passes every
// gate; a failed validation or an out-of-range/insufficient-SP target spends
// nothing. Non-offensive skills (buffs/heals/passives) spend SP but apply no
// damage yet — their stat effects are TODO.
func (s *SkillService) UseSkillOnTarget(
	casterID domain.EntityID,
	skillID int32,
	level int16,
	targetGID domain.EntityID,
) (dmg int32, died bool, err error) {
	entry := s.skills.Get(skillID)
	if entry == nil {
		return 0, false, ErrUnknownSkill
	}
	if level < 1 || int32(level) > entry.MaxLevel {
		return 0, false, ErrInvalidLevel
	}

	caster, err := s.world.Get(casterID)
	if err != nil {
		return 0, false, fmt.Errorf("caster: %w", err)
	}
	// Learned-skill gate: the caster must have learned this skill at >= the
	// requested level. A skill not in LearnedSkills is unlearned.
	if caster.LearnedSkills == nil {
		return 0, false, domain.ErrSkillNotLearned
	}
	learned, ok := caster.LearnedSkills[skillID]
	if !ok || level > learned {
		if !ok {
			return 0, false, domain.ErrSkillNotLearned
		}
		return 0, false, domain.ErrSkillLevelInsufficient
	}
	target, err := s.world.Get(targetGID)
	if err != nil {
		return 0, false, fmt.Errorf("target: %w", err)
	}

	// Range gate. A server-side range (<0, conventionally -1) resolves to melee
	// adjacency; the cast must otherwise reach the target by Chebyshev distance.
	skillRange := entry.Range.At(int(level))
	if skillRange < 0 {
		skillRange = 1
	}
	if dist := chebyshev(caster.Pos, target.Pos); dist > int(skillRange) {
		return 0, false, ErrSkillOutOfRange
	}

	// SP gate: resolve the per-level cost, then atomically check + spend under
	// the world lock so concurrent casts cannot overdraw a shared caster.
	cost := entry.Requires.SpCost.At(int(level))
	if err := s.trySpendSP(casterID, cost); err != nil {
		return 0, false, err
	}

	// Damage: an enemy-targeted skill resolves a real hit through the existing
	// combat path (NormalMelee + applyDamage). Non-offensive skills apply no
	// damage — their effects (buffs/heals) are future work.
	if !isOffensiveSkill(entry) {
		return 0, false, nil
	}
	dmg, died, err = s.combat.Attack(casterID, targetGID)
	if err != nil {
		return 0, false, fmt.Errorf("apply skill damage: %w", err)
	}
	return dmg, died, nil
}

// trySpendSP deducts cost from the caster's SP under the world mutex, returning
// ErrInsufficientSP when the caster cannot afford it. The check-and-spend is a
// single critical section so concurrent casts against one caster are coherent.
func (s *SkillService) trySpendSP(id domain.EntityID, cost int32) error {
	s.world.mu.Lock()
	defer s.world.mu.Unlock()
	e, ok := s.world.entities[id]
	if !ok {
		return domain.ErrEntityNotFound
	}
	if e.SP < cost {
		return ErrInsufficientSP
	}
	e.SP -= cost
	return nil
}

// isOffensiveSkill reports whether the entry targets an enemy for damage. rAthena
// TargetType "Enemy" is the single-target attack class a player casts onto a
// monster; everything else (Self/Friend/Passive/...) is a buff/heal/passive with
// no direct hit in this phase.
func isOffensiveSkill(e *skilldb.SkillEntry) bool {
	return e != nil && e.TargetType == "Enemy"
}

// chebyshev returns the Chebyshev (max-of-axes) cell distance between two map
// positions, the metric rAthena uses for skill/attack range on a square grid.
func chebyshev(a, b domain.Position) int {
	dx := absInt(int(a.X) - int(b.X))
	dy := absInt(int(a.Y) - int(b.Y))
	if dx > dy {
		return dx
	}
	return dy
}

// absInt returns the absolute value of x.
func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
