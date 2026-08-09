//go:build unit

package app

import (
	"testing"

	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/stretchr/testify/assert"
)

// TestStepToward_AxesAndDiagonal verifies the greedy step reduces each axis by
// one toward the target: a cardinal offset steps one axis, a diagonal offset
// steps both (king-move).
func TestStepToward_AxesAndDiagonal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		mob, target  domain.Position
		wantDest     domain.Position
		wantProgress bool
	}{
		{"+x", domain.Position{X: 50, Y: 50}, domain.Position{X: 53, Y: 50}, domain.Position{X: 51, Y: 50}, true},
		{"-x", domain.Position{X: 50, Y: 50}, domain.Position{X: 47, Y: 50}, domain.Position{X: 49, Y: 50}, true},
		{"+y", domain.Position{X: 50, Y: 50}, domain.Position{X: 50, Y: 53}, domain.Position{X: 50, Y: 51}, true},
		{"diagonal", domain.Position{X: 50, Y: 50}, domain.Position{X: 53, Y: 53}, domain.Position{X: 51, Y: 51}, true},
		{"anti-diagonal", domain.Position{X: 50, Y: 50}, domain.Position{X: 47, Y: 47}, domain.Position{X: 49, Y: 49}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dest, ok := stepToward(tc.mob, tc.target)
			assert.Equal(t, tc.wantProgress, ok)
			assert.Equal(t, tc.wantDest, dest)
		})
	}
}

// TestStepToward_NoMoveOnTarget verifies a mob already on the target's cell makes
// no step (ok=false), so a pursuing mob can never be coaxed onto the player.
func TestStepToward_NoMoveOnTarget(t *testing.T) {
	t.Parallel()

	dest, ok := stepToward(domain.Position{X: 50, Y: 50}, domain.Position{X: 50, Y: 50})
	assert.False(t, ok)
	assert.Equal(t, domain.Position{X: 50, Y: 50}, dest)
}

// TestStepToward_ClampsToGrid verifies the destination is clamped into the AOI
// grid [0, gridMaxCell] (512×512, valid [0,511]). An out-of-bounds target must
// not yield an out-of-bounds step: jammed against both edges, no step is taken;
// jammed on one axis, the other axis still advances within bounds.
func TestStepToward_ClampsToGrid(t *testing.T) {
	t.Parallel()

	t.Run("both axes clamped to corner", func(t *testing.T) {
		t.Parallel()
		// Mob at the far corner; a step toward an OOB target clamps back to the
		// same cell, so ok=false (no out-of-bounds move produced).
		_, ok := stepToward(domain.Position{X: 511, Y: 511}, domain.Position{X: 600, Y: 600})
		assert.False(t, ok)
	})
	t.Run("origin both axes clamped", func(t *testing.T) {
		t.Parallel()
		_, ok := stepToward(domain.Position{X: 0, Y: 0}, domain.Position{X: -5, Y: -5})
		assert.False(t, ok)
	})
	t.Run("one axis clamped other advances", func(t *testing.T) {
		t.Parallel()
		// X jammed at the right edge (would step to 512), Y still advances; the
		// step stays in bounds on both axes.
		dest, ok := stepToward(domain.Position{X: 511, Y: 509}, domain.Position{X: 600, Y: 511})
		assert.True(t, ok)
		assert.Equal(t, int16(511), dest.X)
		assert.Equal(t, int16(510), dest.Y)
	})
}

// TestClampCoord pins the [0, gridMaxCell] clamp on the boundary and beyond.
func TestClampCoord(t *testing.T) {
	t.Parallel()

	assert.Equal(t, int16(0), clampCoord(-1))
	assert.Equal(t, int16(0), clampCoord(0))
	assert.Equal(t, int16(511), clampCoord(511))
	assert.Equal(t, int16(511), clampCoord(512))
	assert.Equal(t, int16(511), clampCoord(32767))
}
