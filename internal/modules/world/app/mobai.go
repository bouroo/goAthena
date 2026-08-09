package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/mobdb"
)

// mobAttackInterval is the cadence at which a mob swings once it has a target in
// range. mob_db carries no amotion/attack-delay field (only a walk speed), so a
// single documented constant stands in: 2s is a reasonable pre-renewal melee
// cadence (a player with a fast weapon swings sooner; this is the mob side).
// Chasing-movement toward an out-of-range target is deferred; this first cut
// delivers mobs that hit adjacent players, not mobs that pathfind.
const mobAttackInterval = 2 * time.Second

// aggressiveMobAi is the lowest mob_db Ai value that is aggressive (acquires a
// target on its own). rAthena mob_db.yml assigns Ai=01/02 to passive mobs
// (Poring, Fabre, Poporing, Spore) and Ai>=03 to aggressive ones (Wolf=03,
// Scorpion=09, Zombie=04, MVPs=21). Verified against
// third_party/rathenaThailand/db/pre-re/mob_db.yml: of the 16 distinct Ai
// values, 01 (44 mobs) and 02 (31 mobs) are passive; every higher value has at
// least one aggressive example.
const aggressiveMobAi int32 = 3

// MobAIService drives mob behavior on the world tick. It mirrors the
// CombatService/SpawnService pattern: constructed against the world registry,
// the mob_db registry, and the combat service, with MonsterTick invoked from the
// 50 Hz loop alongside RegenTick.
//
// An aggressive mob (mob_db Ai>=aggressiveMobAi) acquires the nearest player
// within its ChaseRange (Chebyshev distance, matching RO's cell/grid adjacency),
// keeps it while the player stays within ChaseRange and alive, and swings every
// mobAttackInterval when the player is within the mob's AttackRange. When the
// target is within ChaseRange but outside AttackRange, the mob pursues: one
// greedy step toward the target every WalkSpeed ms (mob_db, ms per cell) until it
// closes to AttackRange, then swings. The chase step is a bounded greedy
// step-toward, not A* pathfinding: rAthena's real pathing is far richer
// (walkable-cell maps, obstacle avoidance), but the current world model is an
// open field, so a greedy step is correct and obstacle-aware pathing is deferred.
// Passive mobs idle.
type MobAIService struct {
	world  *WorldService
	mobs   *mobdb.Registry
	combat *CombatService
	log    *slog.Logger

	// OnMobAttack, when set, is invoked after a mob swings at a player (the attack
	// resolved without error), carrying the dealt damage and whether the hit
	// killed the target. It is the notify sink that mirrors world.OnStatChange:
	// the gateway sets it to emit ZC_NOTIFY_ACT so the player (and AOI neighbors)
	// see the mob land a hit and the player's HP bar drops. nil = silent/headless
	// (tests that only assert HP deltas leave it unset). Called off the world lock,
	// after Attack has applied its damage, on the same goroutine as MonsterTick.
	OnMobAttack func(mobID, targetID domain.EntityID, dmg int32, died bool)

	// OnMobMove, when set, is invoked after a mob takes one chase step toward a
	// target (MoveEntity reseated it in the AOI grid), carrying the from/to cells
	// and the mob_db WalkSpeed (ms per cell; the gateway converts to kRO units
	// when it builds the UnitWalkingResponse broadcast). It mirrors OnMobAttack:
	// the gateway sets it to emit ZC_UNIT_WALKING to the mob's AOI neighbors so
	// players see the mob walk. nil = silent/headless (tests that only assert
	// cell deltas leave it unset). Called off the world lock, after MoveEntity, on
	// the same goroutine as MonsterTick.
	OnMobMove func(mobID domain.EntityID, from, to domain.Position, speed int16)

	// mu guards target, atk and move. MonsterTick advances on the single world
	// loop goroutine, but the public surface allows tests to drive it concurrently
	// (e.g. under -race), so the state is lock-protected.
	mu     sync.Mutex
	target map[domain.EntityID]domain.EntityID // mob→current target (0 = none)
	atk    map[domain.EntityID]time.Duration   // mob→attack accumulator
	move   map[domain.EntityID]time.Duration   // mob→chase-step accumulator
}

// pendingAttack is a mob→target pair resolved under the world read-lock, to be
// executed off-lock (CombatService.Attack acquires the world write-lock in
// applyDamage, so holding it across Attack would deadlock).
type pendingAttack struct {
	mob    domain.EntityID
	target domain.EntityID
}

// pendingMove is a chase step resolved under the world read-lock (the from/to
// cells plus the mob_db WalkSpeed), executed off-lock because MoveEntity takes
// the world write-lock — holding the read-lock across it would deadlock the tick.
type pendingMove struct {
	mob   domain.EntityID
	from  domain.Position
	to    domain.Position
	speed int16
}

// NewMobAIService builds a MobAIService. mobs/combat mirror the CombatService
// construction (mobs may be nil in tests; mobs then resolve no stats and deal no
// damage).
func NewMobAIService(world *WorldService, mobs *mobdb.Registry, combat *CombatService, log *slog.Logger) *MobAIService {
	return &MobAIService{
		world:  world,
		mobs:   mobs,
		combat: combat,
		log:    log,
		target: make(map[domain.EntityID]domain.EntityID),
		atk:    make(map[domain.EntityID]time.Duration),
		move:   make(map[domain.EntityID]time.Duration),
	}
}

// MonsterTick advances every mob's aggro/attack/chase state by dt. It must not
// hold world.mu across CombatService.Attack (Attack locks world.mu in
// applyDamage) or MoveEntity (which locks world.mu), so it collects mob+target
// snapshots under a read-lock, releases it, accumulates cadence, then fires
// attacks and chase steps off-lock.
//
// The distance metric is Chebyshev: max(|dx|,|dy|). RO's map is a cell grid and
// its movement/attack adjacency is the 8-neighbor (king-move) metric, so a
// player is "in range" when their Chebyshev distance to the mob is <= the range.
func (a *MobAIService) MonsterTick(ctx context.Context, dt time.Duration) {
	attacks, moves := a.resolve(ctx, dt)
	for _, atk := range attacks {
		// Attack locks world.mu (applyDamage); calling it here, off the read-lock
		// taken in resolve, is the deadlock-avoidance contract. died=true lets us
		// drop the target so the mob does not keep swinging a corpse.
		dmg, died, err := a.combat.Attack(atk.mob, atk.target)
		if err != nil {
			a.log.Warn("mob attack failed", "mob", atk.mob, "target", atk.target, "err", err)
			continue
		}
		if died {
			a.mu.Lock()
			delete(a.target, atk.mob)
			a.mu.Unlock()
		}
		// Surface the hit to the gateway so the player (and AOI neighbors) see the
		// mob swing; dmg may be 0 on a miss/block and is still notified so the
		// client renders the swing. Fires off the world lock, after damage applied.
		if a.OnMobAttack != nil {
			a.OnMobAttack(atk.mob, atk.target, dmg, died)
		}
	}
	for _, mv := range moves {
		// MoveEntity locks world.mu; calling it here, off the read-lock taken in
		// resolve, is the deadlock-avoidance contract. It reseats the mob in the
		// AOI grid so neighbor queries resolve at the new cell.
		if err := a.world.MoveEntity(mv.mob, mv.to); err != nil {
			a.log.Warn("mob move failed", "mob", mv.mob, "from", mv.from, "to", mv.to, "err", err)
			continue
		}
		// Surface the step to the gateway so AOI neighbors see the mob walk; fires
		// off the world lock, after MoveEntity applied the new position.
		if a.OnMobMove != nil {
			a.OnMobMove(mv.mob, mv.from, mv.to, mv.speed)
		}
	}
}

// resolve snapshots mobs and their nearby players under world.mu, then advances
// per-mob aggro/cadence state and returns the attacks and chase steps to fire
// off-lock. It is the sole lock-taking section: everything after returns plain
// values (MoveEntity and CombatService.Attack each take the world write-lock, so
// neither may run while the read-lock is held).
func (a *MobAIService) resolve(_ context.Context, dt time.Duration) ([]pendingAttack, []pendingMove) {
	a.world.mu.RLock()
	defer a.world.mu.RUnlock()

	// Collect the per-map PC populations so each mob can find its nearest target
	// without a second lock pass. PCs with HP<=0 are already dead — excluded.
	playersByMap := make(map[string]map[domain.EntityID]domain.Position)
	for id, e := range a.world.entities {
		if e.Type == domain.EntityTypePC && e.HP > 0 {
			m, ok := playersByMap[e.Map]
			if !ok {
				m = make(map[domain.EntityID]domain.Position)
				playersByMap[e.Map] = m
			}
			m[id] = e.Pos
		}
	}

	var attacks []pendingAttack
	var moves []pendingMove
	for id, e := range a.world.entities {
		if e.Type != domain.EntityTypeMob || e.HP <= 0 {
			continue
		}
		mob := a.mobs.Get(e.Class)
		if mob == nil || mob.Ai < aggressiveMobAi {
			continue // passive or unknown: idle, no accumulation
		}
		chase, atkRange := mob.ChaseRange, mob.AttackRange
		players := playersByMap[e.Map]
		targetID := a.acquireOrKeep(id, e.Pos, chase, players)
		if targetID == 0 {
			continue // no target in range: idle this tick
		}
		targetPos := players[targetID]
		if int64(chebyshev(e.Pos, targetPos)) > int64(atkRange) {
			// Target within ChaseRange but outside AttackRange: pursue one greedy
			// step toward it when the WalkSpeed cadence elapses. resolve only calls
			// pursue while dist>atkRange, and each step cuts the Chebyshev distance
			// by one, so the mob halts at AttackRange (stop-short) rather than
			// stepping onto the player. The step is snapshotted off-lock: MoveEntity
			// takes the world write-lock.
			if mv, ok := a.pursue(id, e.Pos, targetPos, mob.WalkSpeed, dt); ok {
				moves = append(moves, mv)
			}
			continue // out of attack range: hold target, do not swing this tick
		}
		// In range: bank attack cadence; swing when due. Cadence accumulates only
		// while in range, so a mob that just closed to range swings on its next
		// due tick (movement does not bank attack time).
		if a.advanceAndDue(id, dt) {
			attacks = append(attacks, pendingAttack{mob: id, target: targetID})
		}
	}
	return attacks, moves
}

// pursue decides a single chase step for a mob whose target is within
// ChaseRange but outside AttackRange. It returns the pending step (ok=true) when
// the mob's WalkSpeed (ms per cell) cadence has elapsed and a step toward the
// target is possible. stop-short holds because resolve only calls pursue while
// dist>atkRange and each step reduces the Chebyshev distance by one (mob_db
// AttackRange is >=1, so a step never lands on the target's cell). A mob with
// WalkSpeed<=0 cannot chase. The accumulator is guarded by a.mu.
func (a *MobAIService) pursue(mobID domain.EntityID, mobPos, targetPos domain.Position, walkSpeed int32, dt time.Duration) (pendingMove, bool) {
	if walkSpeed <= 0 || !a.moveDue(mobID, dt, walkSpeed) {
		return pendingMove{}, false
	}
	dest, ok := stepToward(mobPos, targetPos)
	if !ok {
		return pendingMove{}, false
	}
	return pendingMove{
		mob:   mobID,
		from:  mobPos,
		to:    dest,
		speed: int16(walkSpeed), //nolint:gosec // G115: mob_db WalkSpeed (ms/cell) is small, well within int16.
	}, true
}

// acquireOrKeep returns the mob's current target if it is still valid (alive,
// in ChaseRange), else acquires the nearest in-range player. A zero return means
// no player is within ChaseRange. The target map is mutated under a.mu.
func (a *MobAIService) acquireOrKeep(mobID domain.EntityID, mobPos domain.Position, chase int32, players map[domain.EntityID]domain.Position) domain.EntityID {
	a.mu.Lock()
	defer a.mu.Unlock()

	if cur, ok := a.target[mobID]; ok {
		if pos, found := players[cur]; found && int64(chebyshev(mobPos, pos)) <= int64(chase) {
			return cur
		}
		delete(a.target, mobID) // fled, logged off, or left range: drop
	}

	var nearest domain.EntityID
	nearestDist := -1
	for pid, pos := range players {
		d := chebyshev(mobPos, pos)
		if int64(d) > int64(chase) {
			continue
		}
		if nearestDist < 0 || d < nearestDist {
			nearest, nearestDist = pid, d
		}
	}
	if nearest != 0 {
		a.target[mobID] = nearest
	}
	return nearest
}

// advanceAndDue adds dt to the mob's attack accumulator and returns true when an
// attack is due (>=mobAttackInterval), resetting the accumulator. The map is
// guarded by a.mu.
func (a *MobAIService) advanceAndDue(mobID domain.EntityID, dt time.Duration) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.atk[mobID] += dt
	if a.atk[mobID] < mobAttackInterval {
		return false
	}
	a.atk[mobID] = 0
	return true
}

// moveDue adds dt to the mob's chase-step accumulator and returns true when one
// one-cell step is due (>= the mob's WalkSpeed, in ms per cell), resetting the
// accumulator. It mirrors advanceAndDue: cadence banked only while actively
// pursuing, reset (not carried over) on each step. Guarded by a.mu.
func (a *MobAIService) moveDue(mobID domain.EntityID, dt time.Duration, walkSpeedMs int32) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.move[mobID] += dt
	if a.move[mobID] < time.Duration(walkSpeedMs)*time.Millisecond {
		return false
	}
	a.move[mobID] = 0
	return true
}

// gridMaxCell is the inclusive upper bound on a mob cell coordinate. WorldService
// builds every map's AOI grid as a 512×512 cell space (world.go ensureGridLocked
// → aoi.NewGridManager(512, 512)), so chase-step destinations clamp into
// [0, gridMaxCell] and can never feed the grid an out-of-bounds cell.
const gridMaxCell int16 = 511

// stepToward computes one greedy chase step from mob toward target, reducing the
// Chebyshev distance by one cell. It advances each axis by the sign of the delta
// (a diagonal move when both axes differ), then clamps the destination into the
// AOI grid [0, gridMaxCell]. ok=false means no step is possible — the mob is
// already on the target's cell, or every advancing axis clamped back to the mob
// (a mob jammed against the grid edge toward an out-of-bounds target).
//
// This is a bounded greedy approximation, not A*: it makes no attempt to route
// around obstacles (the world model has no walkable-cell map yet — cells are
// open), which is correct on an open field. Stop-short is enforced by resolve:
// stepToward is reached only when dist>atkRange, and each call reduces distance
// by exactly one, so the mob halts at AttackRange and never steps onto the
// target. The clamp is defensive — an in-bounds target always yields an
// in-bounds step (the destination lies between mob and target) — but it guards
// against corrupt/OOB target state and future obstacle-aware detours.
func stepToward(mob, target domain.Position) (domain.Position, bool) {
	nx := mob.X
	if target.X > mob.X {
		nx++
	} else if target.X < mob.X {
		nx--
	}
	ny := mob.Y
	if target.Y > mob.Y {
		ny++
	} else if target.Y < mob.Y {
		ny--
	}
	dest := domain.Position{X: clampCoord(nx), Y: clampCoord(ny)}
	if dest == mob {
		return mob, false
	}
	return dest, true
}

// clampCoord clamps a cell coordinate into the AOI grid [0, gridMaxCell].
func clampCoord(c int16) int16 {
	if c < 0 {
		return 0
	}
	if c > gridMaxCell {
		return gridMaxCell
	}
	return c
}
