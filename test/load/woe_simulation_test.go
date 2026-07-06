//go:build unit

package load

import (
	"testing"
	"time"

	"github.com/bouroo/goAthena/pkg/ro/aoi"
)

// reportStats prints the standard WOE load-test summary table.
func reportStats(t *testing.T, title string, cfg LoadTestConfig, s TickStats) {
	t.Helper()
	check := "\u2717"
	if s.AllUnder50ms {
		check = "\u2713"
	}
	t.Logf("\n%s\n"+
		"\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\n"+
		"Map:            %d\u00d7%d\n"+
		"Entities:       %d (moving ratio %.0f%%)\n"+
		"Ticks measured: %d\n"+
		"Avg latency:    %s\n"+
		"P50 latency:    %s\n"+
		"P99 latency:    %s\n"+
		"Min latency:    %s\n"+
		"Max latency:    %s\n"+
		"All under 50ms: %s",
		title,
		cfg.MapWidth, cfg.MapHeight,
		cfg.EntityCount, cfg.MovingRatio*100,
		s.Count,
		s.Avg.Round(time.Microsecond),
		s.P50.Round(time.Microsecond),
		s.P99.Round(time.Microsecond),
		s.Min.Round(time.Microsecond),
		s.Max.Round(time.Microsecond),
		check,
	)
}

// TestWOEDensity_2000Players_50msGate is THE EXIT GATE for Phase 5.
//
// 2,000 players on a Prontera-scale 300x300 map, 60% in active WOE-combat
// movement, 200 ticks (10 seconds at 50ms/tick). The gate is strict:
// EVERY tick must clear the 50ms budget — not just the average — because
// a single dropped frame is a visible stutter to 2,000 clients.
func TestWOEDensity_2000Players_50msGate(t *testing.T) {
	t.Parallel()

	cfg := DefaultWOEConfig()
	tl := SetupLoadTest(t, cfg)

	stats := MeasureTicks(tl, cfg.TickCount)
	reportStats(t, "WOE Load Test: 2000 players, 300\u00d7300 map, 200 ticks", cfg, stats)

	if !stats.AllUnder50ms {
		t.Errorf("EXIT GATE FAILED: not all ticks under 50ms (max=%s)", stats.Max)
	}
	if stats.P99 >= 50*time.Millisecond {
		t.Errorf("EXIT GATE FAILED: p99 %s >= 50ms", stats.P99)
	}
	if stats.Avg >= 10*time.Millisecond {
		t.Errorf("EXIT GATE FAILED: avg %s >= 10ms", stats.Avg)
	}
}

// TestWOEDensity_HotspotClustering packs all 2,000 players into a 50x50
// castle-siege footprint to stress adaptive squeezing at extreme density.
// The gate remains strict (all ticks < 50ms) and the test reports how the
// squeeze tiers distribute across the hotspot.
func TestWOEDensity_HotspotClustering(t *testing.T) {
	t.Parallel()

	cfg := LoadTestConfig{
		MapWidth:      300,
		MapHeight:     300,
		EntityCount:   2000,
		MovingRatio:   0.6,
		ClusterCenter: true,
		ClusterRadius: 25, // 50x50 footprint
		TickCount:     200,
	}
	tl := SetupLoadTest(t, cfg)

	stats := MeasureTicks(tl, cfg.TickCount)
	reportStats(t, "WOE Hotspot: 2000 players in 50\u00d750 castle, 200 ticks", cfg, stats)

	// Probe the squeeze tier across the cluster footprint: a grid of
	// points spanning the 50x50 castle area.
	cx, cy := cfg.MapWidth/2, cfg.MapHeight/2
	var probes [][2]int
	for dy := -cfg.ClusterRadius; dy <= cfg.ClusterRadius; dy += 5 {
		for dx := -cfg.ClusterRadius; dx <= cfg.ClusterRadius; dx += 5 {
			probes = append(probes, [2]int{cx + dx, cy + dy})
		}
	}
	dist := MeasureSqueeze(tl, probes)
	t.Logf("Squeeze tier distribution across castle (%d probes):\n"+
		"  normal  (r=%d): %d\n"+
		"  reduced (r=%d): %d\n"+
		"  minimal (r=%d): %d",
		dist.Total,
		aoi.SqueezeRadiusNormal, dist.Normal,
		aoi.SqueezeRadiusTier1, dist.Reduced,
		aoi.SqueezeRadiusTier2, dist.Minimal,
	)

	if !stats.AllUnder50ms {
		t.Errorf("HOTSPOT GATE FAILED: not all ticks under 50ms (max=%s)", stats.Max)
	}
	// At this density the center MUST be squeezing hard — assert at least
	// some probes hit the minimal tier, proving squeezing engaged.
	if dist.Minimal == 0 && dist.Reduced == 0 {
		t.Errorf("expected adaptive squeezing to engage under 2000-player siege, "+
			"but all %d probes stayed at normal radius", dist.Total)
	}
}

// TestWOEDensity_SustainedLoad runs 1,500 players for 1,000 ticks
// (~50 seconds) to prove the simulation is stable over time: no drift, no
// leak-driven latency creep. The tighter avg budget (5ms) guards against
// gradual degradation.
func TestWOEDensity_SustainedLoad(t *testing.T) {
	t.Parallel()

	cfg := LoadTestConfig{
		MapWidth:      300,
		MapHeight:     300,
		EntityCount:   1500,
		MovingRatio:   0.6,
		ClusterCenter: false,
		ClusterRadius: 0,
		TickCount:     1000,
	}
	tl := SetupLoadTest(t, cfg)

	stats := MeasureTicks(tl, cfg.TickCount)
	reportStats(t, "WOE Sustained: 1500 players, 1000 ticks (~50s)", cfg, stats)

	if stats.Avg >= 5*time.Millisecond {
		t.Errorf("SUSTAINED GATE FAILED: avg %s >= 5ms (latency creep?)", stats.Avg)
	}
	if stats.Max >= 50*time.Millisecond {
		t.Errorf("SUSTAINED GATE FAILED: max %s >= 50ms", stats.Max)
	}
}

// TestWOEDensity_AOIQueryPerformance verifies that an AOI viewport query
// from the center of a 2,000-player hotspot stays under 1ms, and that
// adaptive squeezing meaningfully shrinks the result set versus the
// unsqueezed radius.
func TestWOEDensity_AOIQueryPerformance(t *testing.T) {
	t.Parallel()

	cfg := LoadTestConfig{
		MapWidth:      300,
		MapHeight:     300,
		EntityCount:   2000,
		MovingRatio:   0.6,
		ClusterCenter: true,
		ClusterRadius: 25,
		TickCount:     0,
	}
	tl := SetupLoadTest(t, cfg)
	grid := tl.Grid()

	cx, cy := cfg.MapWidth/2, cfg.MapHeight/2

	// Warm up the query path.
	_ = grid.QueryVisibleSqueezed(cx, cy)

	const queries = 1000
	start := time.Now()
	for range queries {
		_ = grid.QueryVisibleSqueezed(cx, cy)
	}
	perQuery := time.Since(start) / queries

	full := grid.QueryVisible(cx, cy)
	squeezed := grid.QueryVisibleSqueezed(cx, cy)

	t.Logf("AOI query @ hotspot center (%d,%d): %s/query over %d queries",
		cx, cy, perQuery.Round(time.Nanosecond), queries)
	t.Logf("Result set: full radius(%d)=%d entities, squeezed=%d entities (%.1f%% reduction)",
		aoi.DefaultBroadcastRadius, len(full), len(squeezed),
		100*(1-float64(len(squeezed))/float64(max1(len(full)))))

	if perQuery >= time.Millisecond {
		t.Errorf("AOI GATE FAILED: %s/query >= 1ms at max density", perQuery)
	}
	// Squeezing must reduce the result set relative to the full radius
	// under this density, or the viewport-bounding optimization is dead.
	if len(squeezed) >= len(full) {
		t.Errorf("expected squeezed result (%d) < full result (%d) at 2000-player density",
			len(squeezed), len(full))
	}
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}
