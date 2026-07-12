package service

import (
	"context"
	"math"
	"math/rand/v2"

	"github.com/bouroo/goAthena/pkg/ro/aoi"

	"github.com/bouroo/goAthena/internal/features/zone/domain"
	"github.com/bouroo/goAthena/pkg/ro/pathfinding"
	"github.com/bouroo/goAthena/pkg/ro/romap"
)

const (
	wanderRadius   = 5
	wanderMinTicks = 60
	wanderMaxTicks = 160
	wanderRetries  = 3

	chaseRange  = 24 // cells (matches rAthena mob chase range)
	attackRange = 3  // cells

	// wanderPerTickBudget caps the number of mobs that may begin a new
	// wander path in a single tick. Without it, a fresh server (or a
	// frame where all idle mobs have WanderTimer=0) would batch-issue
	// hundreds of A* calls in one tick and breach the 10ms p99 latency
	// gate. The cap spreads pathing load across ticks; remaining mobs
	// will retry on the next tick once their WanderTimer expires.
	wanderPerTickBudget = 1
)

func wanderInterval() uint64 {
	return uint64(wanderMinTicks + rand.IntN(wanderMaxTicks-wanderMinTicks+1)) //nolint:gosec // math/rand/v2 is sufficient for gameplay RNG
}

func pickWanderTarget(md *romap.MapData, originX, originY int) (int, int, bool) {
	for range wanderRetries {
		dx := rand.IntN(2*wanderRadius+1) - wanderRadius //nolint:gosec // math/rand/v2 is sufficient for gameplay RNG
		dy := rand.IntN(2*wanderRadius+1) - wanderRadius //nolint:gosec // math/rand/v2 is sufficient for gameplay RNG
		tx, ty := originX+dx, originY+dy
		if tx >= 0 && tx < md.Width && ty >= 0 && ty < md.Height && md.IsWalkable(tx, ty) {
			return tx, ty, true
		}
	}
	return 0, 0, false
}

func tickAggressiveAI(
	ctx context.Context,
	entities map[domain.EntityID]*domain.Entity,
	currentTick uint64,
	tickRateMs int,
	grid *aoi.GridManager,
	pf *pathfinding.Pathfinder,
	damageFn func(context.Context, domain.EntityID, int32, domain.EntityID) (*domain.DamageResponse, error),
) {
	for _, e := range entities {
		if e.Type != domain.EntityMob {
			continue
		}
		if e.AI != 4 {
			continue
		}
		if len(e.Path) > 0 {
			continue
		}
		nearbyEntities := grid.QueryWithin(e.X, e.Y, chaseRange)
		var nearestPlayerID domain.EntityID
		var nearestPlayerX, nearestPlayerY int
		var nearestDist uint32 = math.MaxUint32
		foundPlayer := false
		for _, ent := range nearbyEntities {
			if ent.Type == aoi.EntityType(domain.EntityPlayer) {
				dx := e.X - ent.X
				dy := e.Y - ent.Y
				dist := uint32(intMax(abs(dx), abs(dy))) //nolint:gosec // G115: distance is always non-negative and safe to convert
				if dist < nearestDist {
					nearestDist = dist
					nearestPlayerID = domain.EntityID(ent.ID)
					nearestPlayerX = ent.X
					nearestPlayerY = ent.Y
					foundPlayer = true
				}
			}
		}
		if foundPlayer {
			if shouldAttack(e, nearestDist, currentTick) {
				attemptAttack(ctx, e, nearestPlayerID, currentTick, damageFn)
				continue
			}

			if nearestDist <= uint32(chaseRange) {
				startChase(e, nearestPlayerX, nearestPlayerY, currentTick, tickRateMs, pf)
				continue
			}
		}
		e.WanderTimer = currentTick + wanderInterval()
	}
}

func shouldAttack(e *domain.Entity, nearestDist uint32, currentTick uint64) bool {
	if nearestDist > uint32(attackRange) {
		return false
	}

	return e.WanderTimer == 0 || currentTick >= e.WanderTimer
}

func attemptAttack(ctx context.Context, e *domain.Entity, targetID domain.EntityID, currentTick uint64, damageFn func(context.Context, domain.EntityID, int32, domain.EntityID) (*domain.DamageResponse, error)) {
	const attackCooldownTicks = 4
	const damage = int32(10)

	_, err := damageFn(ctx, targetID, damage, e.ID)
	if err == nil {
		e.WanderTimer = currentTick + attackCooldownTicks
	}
}

func startChase(e *domain.Entity, targetX, targetY int, currentTick uint64, tickRateMs int, pf *pathfinding.Pathfinder) {
	start := pathfinding.Point{X: e.X, Y: e.Y}
	target := pathfinding.Point{X: targetX, Y: targetY}
	path, err := pf.FindPath(start, target)
	if err == nil && len(path) > 0 {
		e.Path = path
		e.TargetX = targetX
		e.TargetY = targetY
		e.NextMoveTick = currentTick + moveInterval(e.MoveSpeed, tickRateMs)
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func intMax(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func tickMobAI(
	entities map[domain.EntityID]*domain.Entity,
	currentTick uint64,
	tickRateMs int,
	md *romap.MapData,
	pf *pathfinding.Pathfinder,
) {
	budget := wanderPerTickBudget
	for _, e := range entities {
		if budget == 0 {
			break
		}
		if e.Type != domain.EntityMob {
			continue
		}
		if len(e.Path) > 0 {
			continue
		}
		if currentTick < e.WanderTimer {
			continue
		}

		// Skip mobs that have been displaced far from their spawn origin.
		// A* over a long path costs tens of ms; bounding the wander to
		// "near home" caps per-tick latency and matches rAthena's behavior
		// (mob_warp or chasing code is responsible for re-anchoring a
		// displaced mob before it resumes wandering).
		dx := e.X - e.SpawnOriginX
		dy := e.Y - e.SpawnOriginY
		if dx*dx+dy*dy > 2*wanderRadius*2*wanderRadius {
			e.WanderTimer = currentTick + wanderInterval()
			continue
		}

		tx, ty, ok := pickWanderTarget(md, e.SpawnOriginX, e.SpawnOriginY)
		if !ok {
			e.WanderTimer = currentTick + wanderInterval()
			continue
		}

		if tx == e.X && ty == e.Y {
			e.WanderTimer = currentTick + wanderInterval()
			continue
		}

		start := pathfinding.Point{X: e.X, Y: e.Y}
		target := pathfinding.Point{X: tx, Y: ty}
		path, err := pf.FindPath(start, target)
		if err != nil || len(path) == 0 {
			e.WanderTimer = currentTick + wanderInterval()
			continue
		}

		e.Path = path
		e.TargetX = tx
		e.TargetY = ty
		e.NextMoveTick = currentTick + moveInterval(e.MoveSpeed, tickRateMs)
		e.WanderTimer = currentTick + uint64(len(path))*moveInterval(e.MoveSpeed, tickRateMs) + wanderInterval()
		budget--
	}
}
