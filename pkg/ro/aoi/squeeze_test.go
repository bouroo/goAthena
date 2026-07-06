//go:build unit

package aoi

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSqueezeRadius_AllTiers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		count int
		want  int
	}{
		{"zero", 0, SqueezeRadiusNormal},
		{"low", 50, SqueezeRadiusNormal},
		{"boundary normal", SqueezeNormalMax, SqueezeRadiusNormal},
		{"just over normal", SqueezeNormalMax + 1, SqueezeRadiusTier1},
		{"mid tier1", 300, SqueezeRadiusTier1},
		{"boundary tier1", SqueezeTier1Max, SqueezeRadiusTier1},
		{"just over tier1", SqueezeTier1Max + 1, SqueezeRadiusTier2},
		{"extreme", 5000, SqueezeRadiusTier2},
		{"negative treated as zero", -1, SqueezeRadiusNormal},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, SqueezeRadius(tc.count))
		})
	}
}

func TestSqueezeRadius_MonotonicNonIncreasing(t *testing.T) {
	t.Parallel()

	prev := SqueezeRadius(0)
	for n := 0; n <= 1000; n += 50 {
		r := SqueezeRadius(n)
		assert.LessOrEqual(t, r, prev, "radius must not increase as density grows (n=%d)", n)
		prev = r
	}
}

func TestSqueezeTierOf_AllTiers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		count int
		want  SqueezeTier
	}{
		{0, SqueezeTierNormal},
		{100, SqueezeTierNormal},
		{101, SqueezeTierReduced},
		{500, SqueezeTierReduced},
		{501, SqueezeTierMinimal},
		{10000, SqueezeTierMinimal},
		{-1, SqueezeTierNormal},
	}
	for _, tc := range tests {
		tc := tc
		t.Run("", func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, SqueezeTierOf(tc.count))
		})
	}
}

func TestSqueezeTier_String(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "normal", SqueezeTierNormal.String())
	assert.Equal(t, "reduced", SqueezeTierReduced.String())
	assert.Equal(t, "minimal", SqueezeTierMinimal.String())
	assert.Equal(t, "unknown", SqueezeTier(42).String())
}

func TestQueryVisibleSqueezed_AdaptsToDensity(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(400, 400)
	// Build a clustered crowd at (200,200).
	for i := 0; i < 100; i++ {
		x := 200 + (i % 10)
		y := 200 + (i / 10)
		require.NoError(t, gm.AddEntity(newEntity(EntityID(i+1), EntityPlayer, x, y)))
	}
	// Force tier 1 squeeze with one extra entity just at the boundary.
	require.NoError(t, gm.AddEntity(newEntity(101, EntityPlayer, 220, 220)))

	normal := gm.QueryVisible(200, 200)
	squeezed := gm.QueryVisibleSqueezed(200, 200)
	assert.Less(t, len(squeezed), len(normal), "squeezed query should return fewer entities under density")
	assert.GreaterOrEqual(t, len(normal), 100)
}

func TestQueryVisibleSqueezed_NormalAtLowDensity(t *testing.T) {
	t.Parallel()

	gm := NewGridManager(400, 400)
	for i := 0; i < 10; i++ {
		require.NoError(t, gm.AddEntity(newEntity(EntityID(i+1), EntityPlayer, 200+i, 200)))
	}
	normal := gm.QueryVisible(200, 200)
	squeezed := gm.QueryVisibleSqueezed(200, 200)
	assert.Equal(t, len(normal), len(squeezed), "at low density the two queries should agree")
}
