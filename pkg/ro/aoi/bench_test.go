//go:build unit

package aoi

import (
	"math/rand"
	"testing"
)

// BenchmarkMoveEntity_SameTower measures the hot path where an entity
// changes position but stays in the same tower (typical for slow walks
// that don't cross a tower boundary).
func BenchmarkMoveEntity_SameTower(b *testing.B) {
	const N = 1000
	gm := NewGridManager(2000, 2000)
	orig := make([]struct{ x, y int }, N)
	for i := 0; i < N; i++ {
		orig[i] = struct{ x, y int }{(i * 7) % 2000, (i * 11) % 2000}
		_ = gm.AddEntity(newEntity(EntityID(i+1), EntityPlayer, orig[i].x, orig[i].y))
	}
	rng := rand.New(rand.NewSource(1)) //nolint:gosec // deterministic bench seed
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx := i % N
		id := EntityID(idx + 1)
		ox, oy := orig[idx].x, orig[idx].y
		dx := rng.Intn(11) - 5
		dy := rng.Intn(11) - 5
		nx := ox + dx
		ny := oy + dy
		if nx < 0 {
			nx = 0
		}
		if ny < 0 {
			ny = 0
		}
		if nx >= 2000 {
			nx = 1999
		}
		if ny >= 2000 {
			ny = 1999
		}
		orig[idx].x, orig[idx].y = nx, ny
		if err := gm.MoveEntity(id, nx, ny); err != nil {
			b.Fatalf("move: %v", err)
		}
	}
}

// BenchmarkMoveEntity_CrossTower measures the hot path where an entity
// crosses a tower boundary. Target: < 1000 ns/op.
func BenchmarkMoveEntity_CrossTower(b *testing.B) {
	const N = 1000
	gm := NewGridManager(2000, 2000)
	for i := 0; i < N; i++ {
		_ = gm.AddEntity(newEntity(EntityID(i+1), EntityPlayer, (i*18)%2000, (i*18)%2000))
	}
	rng := rand.New(rand.NewSource(1)) //nolint:gosec // deterministic bench seed
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := EntityID((i % N) + 1)
		// Jump to a random far cell — almost always crosses a tower.
		nx := rng.Intn(2000)
		ny := rng.Intn(2000)
		if err := gm.MoveEntity(id, nx, ny); err != nil {
			b.Fatalf("move: %v", err)
		}
	}
}

// BenchmarkQueryVisible populates the grid with 1000 entities clustered
// around the query center and queries the visible set. Target: < 100 µs.
func BenchmarkQueryVisible(b *testing.B) {
	const N = 1000
	gm := NewGridManager(2000, 2000)
	rng := rand.New(rand.NewSource(42)) //nolint:gosec // deterministic bench seed
	for i := 0; i < N; i++ {
		// Cluster around (1000, 1000) so the query viewport sees most of them.
		x := 1000 + rng.Intn(60) - 30
		y := 1000 + rng.Intn(60) - 30
		_ = gm.AddEntity(newEntity(EntityID(i+1), EntityPlayer, x, y))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		visible := gm.QueryVisible(1000, 1000)
		if len(visible) == 0 {
			b.Fatal("expected at least one entity in viewport")
		}
	}
}

// BenchmarkQueryVisibleSqueezed exercises the adaptive squeezing path at
// high density to confirm tier-1/tier-2 lookups stay in budget.
func BenchmarkQueryVisibleSqueezed(b *testing.B) {
	const N = 1500
	gm := NewGridManager(2000, 2000)
	// Cluster a crowd at (1000,1000) to trigger tier-1 squeeze, then
	// scatter the rest of the entities across the map.
	for i := 0; i < 600; i++ {
		x := 1000 + (i % 24)
		y := 1000 + (i / 24)
		_ = gm.AddEntity(newEntity(EntityID(i+1), EntityPlayer, x, y))
	}
	rng := rand.New(rand.NewSource(7)) //nolint:gosec // deterministic bench seed
	for i := 600; i < N; i++ {
		_ = gm.AddEntity(newEntity(EntityID(i+1), EntityPlayer, rng.Intn(2000), rng.Intn(2000)))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		visible := gm.QueryVisibleSqueezed(1000, 1000)
		if len(visible) == 0 {
			b.Fatal("expected at least one entity in squeezed viewport")
		}
	}
}
