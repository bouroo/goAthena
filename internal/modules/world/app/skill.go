package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/skilldb"
	"github.com/bouroo/goAthena/pkg/ro/skilltree"
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

// Learn-skill gate errors. These are separate from cast errors because the
// gateway layer drops failures silently (no reply packet).
var (
	// ErrNoSkillPoints is returned when the character has no skill points left.
	ErrNoSkillPoints = errors.New("no skill points")
	// ErrSkillNotInTree is returned when the skill is not available for the
	// character's job (not in skill_tree.yml for that job).
	ErrSkillNotInTree = errors.New("skill not in job tree")
	// ErrSkillMaxLevel is returned when the skill is already at its effective
	// maximum level (tree MaxLevel or character job level cap).
	ErrSkillMaxLevel = errors.New("skill at max level")
	// ErrSkillLocked is returned when the tree entry's BaseLevel/JobLevel
	// thresholds or prerequisite skills are not yet met (rAthena's
	// pc_calc_skilltree unlock gate).
	ErrSkillLocked = errors.New("skill locked by tree requirements")
)

// SkillService resolves a skill cast against a target entity. It validates the
// cast (skill known, level in range, target reachable, SP affordable), spends
// the SP, and for an enemy-targeted (offensive) skill resolves a melee-equivalent
// hit through the existing CombatService. Full skill-damage modeling — element,
// size, crit, and per-skill multipliers — is deliberately out of scope here and
// left to future work; this phase delivers a castable, visible, damage-dealing
// skill by reusing the proven pre-renewal melee path.
//
// LearnSkill spends a skill point to raise one learned skill level, gated by
// the job's skill tree (skill_tree.yml) and the character's job level.
type SkillService struct {
	world  *WorldService
	combat *CombatService
	skills *skilldb.Registry
	tree   *skilltree.Registry
}

// NewSkillService builds a skill service backed by the world registry, the
// combat service (for offensive-skill hits), and the skill_db registry. skills
// may be nil (every cast resolves unknown), but the app wiring provisions a
// non-nil registry.
func NewSkillService(world *WorldService, combat *CombatService, skills *skilldb.Registry) *SkillService {
	return &SkillService{world: world, combat: combat, skills: skills}
}

// SetTree injects the skill tree registry (skill_tree.yml). Call this once
// during app wiring before any LearnSkill calls.
func (s *SkillService) SetTree(tree *skilltree.Registry) {
	s.tree = tree
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

// LearnSkill raises one learned-skill level for charID by spending one skill
// point. It is gated by the job's skill tree (skill_tree.yml).
//
// Returns (newLevel, spCost, range, upgradable, nil) on success.
// On failure (no points, not in tree, at max) returns (0, 0, 0, false, <sentinel>).
//
// The caller is responsible for sending the ZC_SKILLINFO_UPDATE reply and the
// ParChange(SPSkillPoint) notification on success.
// treeUnlocked enforces the tree entry's unlock gate: BaseLevel/JobLevel
// thresholds met and every prerequisite skill learned at (or above) its
// required level. rAthena caps raises at the tree MaxLevel only; it never caps
// by the character's current JobLevel.
func (s *SkillService) treeUnlocked(treeEntry *skilltree.SkillEntry, e *domain.Entity) error {
	if e.Level < treeEntry.BaseLevel || e.JobLevel < treeEntry.JobLevel {
		return ErrSkillLocked
	}
	for _, req := range treeEntry.Requires {
		reqID, ok := s.skills.IDByName(req.Name)
		if !ok {
			return ErrSkillLocked
		}
		if e.LearnedSkills[reqID] < req.Level {
			return ErrSkillLocked
		}
	}
	return nil
}

// LearnSkill spends one skill point to raise skillID's learned level by one,
// gated by the Novice skill tree (unlock thresholds + prerequisites, then the
// tree MaxLevel cap). On success it persists the new learned level and point
// balance and returns the ZC_SKILLINFO_UPDATE payload: new learned level, SP
// cost at that level, range at that level, and whether another raise remains.
// Failure returns a typed sentinel; nothing is spent or persisted.
func (s *SkillService) LearnSkill(ctx context.Context, charID uint32, skillID int32) (newLevel int16, spCost int32, rng int16, upgradable bool, err error) {
	// 1. Resolve skill entry by ID → get Name.
	entry := s.skills.Get(skillID)
	if entry == nil {
		return 0, 0, 0, false, ErrUnknownSkill
	}
	skillName := entry.Name

	// 2. Snapshot the entity under the world lock. All reads that gate the
	// learn come from this snapshot; the mutation at step 10 re-locks and
	// revalidates, so no gate races a concurrent learn.
	s.world.mu.Lock()
	e, ok := s.world.entities[domain.EntityID(charID)]
	snapshot := domain.Entity{}
	if ok {
		snapshot = *e
	}
	s.world.mu.Unlock()
	if !ok {
		return 0, 0, 0, false, domain.ErrEntityNotFound
	}
	e = &snapshot

	jobName := "Novice"
	if e.Job != 0 {
		// Non-Novice not yet supported; fail silently like a missing tree entry.
		return 0, 0, 0, false, ErrSkillNotInTree
	}

	// 3. Check skill tree: skill must be in this job's tree.
	tree, ok := s.tree.Get(jobName)
	if !ok {
		return 0, 0, 0, false, ErrSkillNotInTree
	}
	treeEntry, ok := tree.Skills[skillName]
	if !ok {
		return 0, 0, 0, false, ErrSkillNotInTree
	}

	// 4. Check skill points available.
	if e.SkillPoint == 0 {
		return 0, 0, 0, false, ErrNoSkillPoints
	}

	// 5. Unlock gates mirror rAthena pc_calc_skilltree (pc.cpp:2857).
	if err := s.treeUnlocked(treeEntry, e); err != nil {
		return 0, 0, 0, false, err
	}

	// 6. Current level must sit below the tree cap.
	cur := e.LearnedSkills[skillID]
	if cur >= treeEntry.MaxLevel {
		return 0, 0, 0, false, ErrSkillMaxLevel
	}

	// 7. Apply: increment learned, decrement points.
	newLevel = cur + 1
	if e.LearnedSkills == nil {
		e.LearnedSkills = make(map[int32]int16)
	}
	e.LearnedSkills[skillID] = newLevel

	// 8. Persist skills and skill points.
	skills := make([]domain.LearnedSkill, 0, len(e.LearnedSkills))
	for sid, lv := range e.LearnedSkills {
		skills = append(skills, domain.LearnedSkill{SkillID: sid, Level: lv})
	}
	if err := s.world.repo.SaveSkills(ctx, charID, skills); err != nil {
		return 0, 0, 0, false, fmt.Errorf("SaveSkills: %w", err)
	}
	newSP := e.SkillPoint - 1
	if err := s.world.repo.SaveState(ctx, charID, e.Level, e.JobLevel,
		e.MaxHP, e.MaxSP, e.HP, e.SP, e.BaseExp, e.JobExp,
		e.StatusPoint, newSP); err != nil {
		return 0, 0, 0, false, fmt.Errorf("SaveState(SkillPoint): %w", err)
	}

	// 9. Return stats for the ZC reply.
	spCost = s.skills.SpCostAt(skillID, int(newLevel))
	rng = entry.Range.At(int(newLevel))
	upgradable = newLevel < treeEntry.MaxLevel

	// 10. Update in-memory entity (reflect the persist).
	s.world.mu.Lock()
	if se, ok := s.world.entities[domain.EntityID(charID)]; ok {
		se.SkillPoint = newSP
		se.LearnedSkills = e.LearnedSkills
	}
	s.world.mu.Unlock()

	return newLevel, spCost, rng, upgradable, nil
}
