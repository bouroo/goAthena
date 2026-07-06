//go:build unit

// Package load provides a WOE-density load-test harness for the zone
// tick loop. It builds large maps, populates them with thousands of
// player/mob/NPC entities in realistic War-of-Emperium distributions,
// drives the simulation tick-by-tick, and reports per-tick latency
// statistics.
//
// The harness is the executable evidence for the Phase 5 exit gate:
// 50ms ticks sustained with 2,000 players in one zone with adaptive
// AOI squeezing active. Everything here is guarded by the `unit` build
// tag and links against the zone service's unit-only benchmark surface
// (TickForBenchmark and friends).
package load

import (
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/bouroo/goAthena/internal/features/zone/domain"
	"github.com/bouroo/goAthena/internal/features/zone/service"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/romap"

	"github.com/rs/zerolog"
)

// loadTestTickRate is the fixed physics cadence for every load scenario.
// The exit gate is defined in terms of a 50ms tick, so all measured
// latencies are compared against this budget.
const loadTestTickRate = 50 * time.Millisecond

// loadTestSeed keeps entity placement and movement reproducible so a
// failing run can be replayed deterministically.
const loadTestSeed = 0xA71E_11A5

// LoadTestConfig configures a load-test scenario.
type LoadTestConfig struct { //nolint:revive // name is fixed by the P5.7 harness spec
	MapWidth      int
	MapHeight     int
	EntityCount   int
	MovingRatio   float64 // fraction of entities with active paths
	ClusterCenter bool    // if true, cluster entities in center
	ClusterRadius int     // radius for clustering
	TickCount     int     // number of ticks to run
}

// DefaultWOEConfig returns the standard WOE-density configuration: the
// full 2,000-player exit-gate scenario on a Prontera-scale 300x300 map
// with 60% of actors in active combat movement, run for 200 ticks
// (10 seconds of wall-clock simulation at 50ms/tick).
func DefaultWOEConfig() LoadTestConfig {
	return LoadTestConfig{
		MapWidth:      300,
		MapHeight:     300,
		EntityCount:   2000,
		MovingRatio:   0.6,
		ClusterCenter: false,
		ClusterRadius: 0,
		TickCount:     200,
	}
}

// woeMap builds a walkable map of the requested size with a sparse
// pillar/wall lattice so the pathfinder does non-trivial work — mirroring
// the obstacle density of a castle interior rather than an empty field.
func woeMap(width, height int) *romap.MapData {
	md := &romap.MapData{
		Name:     "woe-load",
		Width:    width,
		Height:   height,
		Walkable: make([]bool, width*height),
		Heights:  make([]float32, width*height),
	}
	for i := range md.Walkable {
		md.Walkable[i] = true
	}
	// Vertical pillars every 20 cells, with a walkable gap every 5th row
	// so no corridor is fully sealed.
	for x := 0; x < width; x += 20 {
		for y := range height {
			if y%5 == 0 {
				continue
			}
			md.Walkable[y*width+x] = false
		}
	}
	// Horizontal pillars every 20 cells, gap every 5th column.
	for y := 0; y < height; y += 20 {
		for x := range width {
			if x%5 == 0 {
				continue
			}
			md.Walkable[y*width+x] = false
		}
	}
	return md
}

// silentLogger returns a no-op zerolog logger. Load runs generate
// thousands of tick-exceeded warnings otherwise, which would dominate
// the test output and skew timing.
func silentLogger() *zerolog.Logger {
	l := zerolog.Nop()
	return &l
}

// newLoadRand returns a reproducible PCG-backed *rand.Rand.
func newLoadRand() *rand.Rand {
	//nolint:gosec // deterministic PRNG on purpose: reproducible load runs, not security.
	return rand.New(rand.NewPCG(loadTestSeed, loadTestSeed^0x9E3779B97F4A7C15))
}

// SetupLoadTest creates a tick loop populated with entities per cfg and
// returns it ready to be measured.
func SetupLoadTest(tb testing.TB, cfg LoadTestConfig) *service.TickLoop {
	tb.Helper()
	md := woeMap(cfg.MapWidth, cfg.MapHeight)
	tl := service.NewTickLoop(md, loadTestTickRate, silentLogger())
	PopulateEntities(tl, cfg)
	return tl
}

// PopulateEntities adds cfg.EntityCount entities to the tick loop.
//
// Distribution model (WOE simulation):
//
//   - ClusterCenter=true: every entity is placed within ClusterRadius of
//     the map center — the castle-siege hotspot scenario.
//   - ClusterCenter=false: 40% of entities roam across the full map while
//     60% pack into 2-3 fixed hotspots (castle approaches), reproducing
//     the mixed density of a live WOE.
//
// A MovingRatio fraction of entities receive an initial A* destination so
// the tick loop has real per-cell movement work.
func PopulateEntities(tl *service.TickLoop, cfg LoadTestConfig) {
	rng := newLoadRand()
	md := tl.MapData()

	hotspots := woeHotspots(md.Width, md.Height)

	for i := 0; i < cfg.EntityCount; i++ {
		x, y := placeEntity(md, cfg, hotspots, rng)

		e := &domain.Entity{
			ID:        domain.EntityID(i + 1),
			Type:      entityTypeForIndex(i),
			X:         x,
			Y:         y,
			MoveSpeed: moveSpeed(rng),
		}
		if err := tl.AddEntityForBenchmark(e); err != nil {
			// Collisions on ID are impossible (monotonic i); an add error
			// here means the chosen cell was rejected — skip this actor
			// rather than aborting the whole population.
			continue
		}

		if rng.Float64() < cfg.MovingRatio {
			tx, ty := movementTarget(md, cfg, x, y, rng)
			// A failed pathfind (unreachable target) just leaves the
			// entity idle; the harness re-paths exhausted movers later.
			_ = tl.MoveEntityForBenchmark(e.ID, tx, ty)
		}
	}
}

// woeHotspots returns 3 castle-approach centers: the map center plus two
// offset flanks. Entities that are not roaming pack around these.
func woeHotspots(w, h int) [][2]int {
	return [][2]int{
		{w / 2, h / 2},
		{w / 4, h / 4},
		{3 * w / 4, 3 * h / 4},
	}
}

// placeEntity picks a walkable cell for entity i per the config's
// distribution rules.
func placeEntity(md *romap.MapData, cfg LoadTestConfig, hotspots [][2]int, rng *rand.Rand) (int, int) {
	if cfg.ClusterCenter {
		cx, cy := md.Width/2, md.Height/2
		return walkableNear(md, cx, cy, cfg.ClusterRadius, rng)
	}
	// 40% roam the full map; 60% cluster around a random hotspot within
	// a 25-cell radius (castle-approach staging).
	if rng.Float64() < 0.4 {
		return randomWalkable(md, rng)
	}
	hs := hotspots[rng.IntN(len(hotspots))]
	return walkableNear(md, hs[0], hs[1], 25, rng)
}

// movementTarget picks a destination appropriate to the scenario: within
// the cluster for hotspot runs, or anywhere on the map for roaming runs.
func movementTarget(md *romap.MapData, cfg LoadTestConfig, ox, oy int, rng *rand.Rand) (int, int) {
	if cfg.ClusterCenter {
		cx, cy := md.Width/2, md.Height/2
		return walkableNear(md, cx, cy, cfg.ClusterRadius, rng)
	}
	// Local skirmish movement: destinations stay within ~30 cells of the
	// origin, the way WOE combat movement rarely crosses the whole map.
	return walkableNear(md, ox, oy, 30, rng)
}

// walkableNear returns a walkable cell within radius of (cx, cy),
// clamped to the map. Falls back to the nearest scan if random draws
// keep hitting walls.
func walkableNear(md *romap.MapData, cx, cy, radius int, rng *rand.Rand) (int, int) {
	if radius < 1 {
		radius = 1
	}
	for range 256 {
		x := cx + rng.IntN(2*radius+1) - radius
		y := cy + rng.IntN(2*radius+1) - radius
		if md.IsWalkable(x, y) {
			return x, y
		}
	}
	return randomWalkable(md, rng)
}

// randomWalkable returns any walkable cell on the map.
func randomWalkable(md *romap.MapData, rng *rand.Rand) (int, int) {
	for range 1024 {
		x := rng.IntN(md.Width)
		y := rng.IntN(md.Height)
		if md.IsWalkable(x, y) {
			return x, y
		}
	}
	return 0, 0
}

// entityTypeForIndex spreads entities across the three AOI types. Players
// dominate a WOE scene, so we weight 2:1:1 player:mob:npc.
func entityTypeForIndex(i int) domain.EntityType {
	switch i % 4 {
	case 0, 1:
		return domain.EntityPlayer
	case 2:
		return domain.EntityMob
	default:
		return domain.EntityNPC
	}
}

// moveSpeed returns a per-cell move speed in the 100-300ms range so the
// population has a realistic spread of agi/haste.
func moveSpeed(rng *rand.Rand) int {
	return 100 + rng.IntN(201) // [100, 300]
}

// TickStats summarizes per-tick latency across a measured run.
type TickStats struct {
	Count        int
	Min          time.Duration
	Max          time.Duration
	Avg          time.Duration
	P50          time.Duration
	P99          time.Duration
	AllUnder50ms bool
}

// MeasureTicks runs the tick loop for tickCount ticks and returns latency
// statistics. It discards the first tick as warmup (pathfinder cache and
// scheduler settle), and between ticks re-issues destinations to any
// movers whose paths have been consumed so movement pressure stays
// constant for the whole run — the way a real WOE never stops moving.
//
// Only the TickForBenchmark call is timed; the re-path A* work happens
// off the clock, matching production where pathfinding runs on handler
// goroutines rather than the physics thread.
func MeasureTicks(tl *service.TickLoop, tickCount int) TickStats {
	rng := newLoadRand()
	movers := tl.MoverIDsForBenchmark()

	// Warmup tick: absorb first-run costs before measuring.
	tl.TickForBenchmark()
	tl.RepathExhaustedForBenchmark(movers, rng)

	latencies := make([]time.Duration, tickCount)
	for i := range tickCount {
		start := time.Now()
		tl.TickForBenchmark()
		latencies[i] = time.Since(start)

		// Keep the movers moving without polluting the measured window.
		tl.RepathExhaustedForBenchmark(movers, rng)
	}

	return summarize(latencies)
}

// summarize computes min/max/avg/p50/p99 over the latency samples and
// whether every sample cleared the 50ms gate.
func summarize(latencies []time.Duration) TickStats {
	stats := TickStats{Count: len(latencies), AllUnder50ms: true}
	if len(latencies) == 0 {
		return stats
	}

	sorted := append([]time.Duration(nil), latencies...)
	slices.Sort(sorted)

	var total time.Duration
	for _, d := range sorted {
		total += d
		if d >= loadTestTickRate {
			stats.AllUnder50ms = false
		}
	}

	stats.Min = sorted[0]
	stats.Max = sorted[len(sorted)-1]
	stats.Avg = total / time.Duration(len(sorted))
	stats.P50 = percentile(sorted, 0.50)
	stats.P99 = percentile(sorted, 0.99)
	return stats
}

// percentile returns the p-quantile of an already-sorted sample using
// nearest-rank. p is in [0, 1].
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)) * p)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// SqueezeDistribution counts how many currently-tracked entities fall in
// each adaptive-squeeze tier, based on each entity's local 9-grid
// density. It is used by the hotspot scenario to report how aggressively
// the AOI viewport is being squeezed under siege.
type SqueezeDistribution struct {
	Normal  int // radius 15
	Reduced int // radius 8
	Minimal int // radius 5
	Total   int
}

// MeasureSqueeze samples the squeeze tier for a set of probe points
// against the tick loop's grid. Probes should be representative entity
// positions (e.g. the hotspot center and a scatter around it).
func MeasureSqueeze(tl *service.TickLoop, probes [][2]int) SqueezeDistribution {
	grid := tl.Grid()
	var dist SqueezeDistribution
	for _, p := range probes {
		dist.Total++
		switch aoi.SqueezeTierOf(grid.Count9Grid(p[0], p[1])) {
		case aoi.SqueezeTierNormal:
			dist.Normal++
		case aoi.SqueezeTierReduced:
			dist.Reduced++
		case aoi.SqueezeTierMinimal:
			dist.Minimal++
		}
	}
	return dist
}
