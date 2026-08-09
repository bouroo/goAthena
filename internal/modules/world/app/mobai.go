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
// Only the aggro→attack loop is implemented here: an aggressive mob (mob_db
// Ai>=aggressiveMobAi) acquires the nearest player within its ChaseRange
// (Chebyshev distance, matching RO's cell/grid adjacency), keeps it while the
// player stays within ChaseRange and alive, and swings every mobAttackInterval
// when the player is within the mob's AttackRange. Passive mobs idle. Movement
// (chasing a player that stepped out of AttackRange but not ChaseRange) is
// deferred — a first deliverable that makes mobs hit adjacent players.
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

	// mu guards target and atk. MonsterTick advances on the single world loop
	// goroutine, but the public surface allows tests to drive it concurrently
	// (e.g. under -race), so the state is lock-protected.
	mu     sync.Mutex
	target map[domain.EntityID]domain.EntityID // mob→current target (0 = none)
	atk    map[domain.EntityID]time.Duration   // mob→attack accumulator
}

// pendingAttack is a mob→target pair resolved under the world read-lock, to be
// executed off-lock (CombatService.Attack acquires the world write-lock in
// applyDamage, so holding it across Attack would deadlock).
type pendingAttack struct {
	mob    domain.EntityID
	target domain.EntityID
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
	}
}

// MonsterTick advances every mob's aggro/attack state by dt. It must not hold
// world.mu across CombatService.Attack (Attack locks world.mu in applyDamage),
// so it collects mob+target snapshots under a read-lock, releases it, accumulates
// cadence, then fires attacks off-lock.
//
// The distance metric is Chebyshev: max(|dx|,|dy|). RO's map is a cell grid and
// its movement/attack adjacency is the 8-neighbor (king-move) metric, so a
// player is "in range" when their Chebyshev distance to the mob is <= the range.
func (a *MobAIService) MonsterTick(ctx context.Context, dt time.Duration) {
	attacks := a.resolve(ctx, dt)
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
}

// resolve snapshots mobs and their nearby players under world.mu, then advances
// per-mob aggro/cadence state and returns the attacks to fire off-lock. It is
// the sole lock-taking section: everything after returns plain values.
func (a *MobAIService) resolve(_ context.Context, dt time.Duration) []pendingAttack {
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
		if int64(chebyshev(e.Pos, players[targetID])) > int64(atkRange) {
			continue // target present but out of attack range: hold, do not swing
		}
		// Cadence accumulates only when a swing is due; a mob with a target but
		// out of range does not bank attack time.
		if a.advanceAndDue(id, dt) {
			attacks = append(attacks, pendingAttack{mob: id, target: targetID})
		}
	}
	return attacks
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
