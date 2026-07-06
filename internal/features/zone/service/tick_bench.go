//go:build unit

package service

import (
	"context"
	"math/rand/v2"

	"github.com/bouroo/goAthena/internal/features/zone/domain"
)

// This file exposes a thin, exported surface over the TickLoop's
// unexported drive methods so the out-of-package load-test harness
// (test/load) can populate the zone and step the simulation directly.
//
// It is guarded by the `unit` build tag, so none of these helpers exist
// in a production build — the normal gRPC path continues to go through
// ZoneService. The load harness runs with `-tags=unit`, which is where
// these become visible.

// TickForBenchmark runs a single synchronous tick step against a
// background context. The load harness times this call to measure
// per-tick compute latency without the fixed-rate ticker sleep.
func (tl *TickLoop) TickForBenchmark() {
	_ = tl.tick(context.Background())
}

// AddEntityForBenchmark registers e in the tick loop and AOI grid,
// bypassing the Agones allocation hook that ZoneService.AddEntity owns.
func (tl *TickLoop) AddEntityForBenchmark(e *domain.Entity) error {
	_, err := tl.addEntity(context.Background(), e)
	return err
}

// MoveEntityForBenchmark computes and assigns an A* path to the entity,
// exactly as a client MoveEntity command would.
func (tl *TickLoop) MoveEntityForBenchmark(id domain.EntityID, x, y int) error {
	return tl.moveEntity(context.Background(), id, x, y)
}

// MoverIDsForBenchmark returns the IDs of every entity that currently
// holds a non-empty movement path. The harness snapshots this set once
// before a measured run to identify the "moving" actors so it can
// re-issue destinations as their paths are consumed.
func (tl *TickLoop) MoverIDsForBenchmark() []domain.EntityID {
	tl.mu.RLock()
	defer tl.mu.RUnlock()
	ids := make([]domain.EntityID, 0, len(tl.entities))
	for id, e := range tl.entities {
		if len(e.Path) > 0 {
			ids = append(ids, id)
		}
	}
	return ids
}

// RepathExhaustedForBenchmark re-issues a fresh random walkable
// destination to every entity in ids whose path has been fully consumed,
// simulating the continuous stream of MoveEntity commands during WOE
// combat. It returns the number of entities re-pathed.
//
// The A* search runs here, off the measured tick window — mirroring
// production, where pathfinding happens on gRPC handler goroutines and
// never on the physics thread.
func (tl *TickLoop) RepathExhaustedForBenchmark(ids []domain.EntityID, rng *rand.Rand) int {
	tl.mu.RLock()
	exhausted := make([]domain.EntityID, 0, len(ids))
	for _, id := range ids {
		if e, ok := tl.entities[id]; ok && len(e.Path) == 0 {
			exhausted = append(exhausted, id)
		}
	}
	tl.mu.RUnlock()

	n := 0
	for _, id := range exhausted {
		x, y := tl.randomWalkableCell(rng)
		if err := tl.moveEntity(context.Background(), id, x, y); err == nil {
			n++
		}
	}
	return n
}

// randomWalkableCell returns a walkable (x, y) on the loaded map. It
// retries random draws a bounded number of times before falling back to
// the map origin (which the benchmark maps guarantee walkable).
func (tl *TickLoop) randomWalkableCell(rng *rand.Rand) (int, int) {
	md := tl.mapData
	for range 256 {
		x := rng.IntN(md.Width)
		y := rng.IntN(md.Height)
		if md.IsWalkable(x, y) {
			return x, y
		}
	}
	return 0, 0
}
