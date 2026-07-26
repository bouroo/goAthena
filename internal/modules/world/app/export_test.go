//go:build unit

// Package app: this export_test.go file is the standard Go test-seam pattern.
// It lives in the INTERNAL package app (not app_test) under the unit build
// tag, so it can name the unexported moveReq type and the unexported resolve
// method that the movement worker calls. It re-exports a synchronous resolve
// entry point as an exported method so the external app_test package can drive
// a single move deterministically — without spinning the Run worker goroutine
// and racing the drain — while keeping moveReq and resolve unexported in the
// production API.
//
// golangci-lint's `unused` check never sees this file: `task lint` runs without
// the unit build tag, so this file is excluded from the lint build. When
// `task test-unit` compiles it (tag present), the wrapper is called by
// move_test.go, so it is not unused there either.
package app

import (
	"context"

	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// ResolveForTest synchronously resolves one move for the given account, as if
// the Run worker had dequeued it. It constructs the unexported moveReq from
// the exported packet request and calls resolve directly. The ctx is threaded
// through to the map-store Load; a cancelled ctx aborts the load (and the
// move) the same way Run would. Tests use this instead of RequestMove+Run so
// the assertions run after the move resolves, with no goroutine drain race.
func (s *MoveService) ResolveForTest(ctx context.Context, accountID uint32, req packet.CZRequestMoveRequest) {
	s.resolve(ctx, moveReq{accountID: accountID, destX: req.DestX, destY: req.DestY})
}

// fixedStep is a stepSource whose Intn always returns the same neighbor index
// (clamped into range), so a unit test can assert the exact step the wander tick
// would make. It lives here under the unit tag alongside the seam that uses it;
// the production tick uses randStep (the global rand source).
type fixedStep struct{ idx int }

// Intn implements stepSource, clamping the fixed index into [0,n).
func (f fixedStep) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	if f.idx >= n {
		return n - 1
	}
	if f.idx < 0 {
		return 0
	}
	return f.idx
}

// WanderStepForTest runs one synchronous wander step for mob on mp, injecting a
// fixed neighbor index so the test asserts an exact deterministic move. The
// production Run loop picks the index via the stepSource RNG; this seam bypasses
// both the ticker and the RNG so the assertions run after the step resolves.
func (s *MobService) WanderStepForTest(mp *domain.Map, mob *domain.Mob, stepIdx int) {
	s.rand = fixedStep{idx: stepIdx}
	s.wanderStep(mp, mob)
}

// StepOffsetForTest returns the wander step offset at the given index so a test
// can compute the expected destination without re-declaring the 8-entry table.
func StepOffsetForTest(i int) [2]int { return stepOffsets[i%len(stepOffsets)] }
