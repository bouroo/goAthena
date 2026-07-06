//go:build unit

package pathfinding

import (
	"testing"
)

// BenchmarkFindPath measures a ~30-cell A* search on a 200×200 walkable
// grid. The target (≤ 100µs) keeps pathfinding cheap relative to a 5 ms
// tick budget on the zone service.
func BenchmarkFindPath(b *testing.B) {
	const w = 200
	const h = 200
	g := openGrid(w, h)
	p := New(g)
	start := Point{0, 0}
	target := Point{15, 15}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path, err := p.FindPath(start, target)
		if err != nil {
			b.Fatalf("FindPath: %v", err)
		}
		if len(path) == 0 {
			b.Fatalf("FindPath: empty path")
		}
	}
}

// BenchmarkLineOfSight measures Bresenham LOS for an 18-cell diagonal.
func BenchmarkLineOfSight(b *testing.B) {
	const w = 200
	const h = 200
	g := openGrid(w, h)
	p := New(g)
	from := Point{0, 0}
	to := Point{18, 18}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !p.LineOfSight(from, to) {
			b.Fatalf("LineOfSight blocked on open grid")
		}
	}
}
