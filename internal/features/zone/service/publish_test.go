//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"

	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	"github.com/bouroo/goAthena/internal/features/zone/domain"
	domainmock "github.com/bouroo/goAthena/internal/features/zone/domain/mock"
	"github.com/bouroo/goAthena/pkg/ro/pathfinding"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTickLoop_PublishesEvents uses the generated domain.Publisher mock
// to assert that addEntity/tick/removeEntity publish the right
// ZoneEvent oneof arms with the right field values. This is the
// executable evidence for Phase 1 Step 1 of the MMO broadcast
// foundation: every observable state change in the zone emits a NATS
// event by the time the mutating call returns.
func TestTickLoop_PublishesEvents(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	pub := domainmock.NewMockPublisher(ctrl)

	// The synthetic map is named "broadcast" — that's what the
	// publisher helper will pass as mapName to PublishEvent.
	md := newSyntheticMapSized(64, 64)
	md.Name = "broadcast"
	tl := NewTickLoop(md, 50*time.Millisecond, silentLogger(), pub)
	require.NotNil(t, tl)

	ctx := context.Background()
	player := &domain.Entity{ID: 42, Type: domain.EntityPlayer, X: 10, Y: 10, MoveSpeed: 150}

	// Spawned event: emitted by addEntity AFTER the entity is registered.
	pub.EXPECT().
		PublishEvent(gomock.Any(), "broadcast", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, ev proto.Message) error {
			ze, ok := ev.(*zonev1.ZoneEvent)
			require.True(t, ok, "expected *zonev1.ZoneEvent, got %T", ev)
			sp := ze.GetSpawned()
			require.NotNil(t, sp, "expected Spawned oneof arm")
			assert.Equal(t, uint32(42), sp.GetEntityId())
			assert.Equal(t, uint32(domain.EntityPlayer), sp.GetEntityType())
			assert.Equal(t, uint32(10), sp.GetX())
			assert.Equal(t, uint32(10), sp.GetY())
			assert.Equal(t, "", sp.GetName(), "name is intentionally empty at the zone layer")
			return nil
		}).
		Times(1)

	_, err := tl.addEntity(ctx, player)
	require.NoError(t, err)

	// Moved event: injected via direct Path assignment so the test does
	// not depend on moveInterval timing. Setting NextMoveTick to 0
	// forces the next tick to consume the path step immediately.
	tl.mu.Lock()
	player.Path = []pathfinding.Point{{X: 11, Y: 10}}
	player.NextMoveTick = 0
	tl.mu.Unlock()

	// Capture (entityX, entityY) BEFORE the tick so we can verify the
	// event's src_* / dest_* field mapping: src_* is where the entity
	// WAS, dest_* is where it IS AFTER the move.
	//nolint:gosec // unit-test only; map-coord casts are bounded.
	pub.EXPECT().
		PublishEvent(gomock.Any(), "broadcast", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, ev proto.Message) error {
			ze, ok := ev.(*zonev1.ZoneEvent)
			require.True(t, ok)
			mv := ze.GetMoved()
			require.NotNil(t, mv)
			assert.Equal(t, uint32(42), mv.GetEntityId())
			assert.Equal(t, uint32(11), mv.GetDestX(), "dest is the NEW cell")
			assert.Equal(t, uint32(10), mv.GetDestY())
			assert.Equal(t, uint32(10), mv.GetSrcX(), "src is the OLD cell")
			assert.Equal(t, uint32(10), mv.GetSrcY())
			assert.NotZero(t, mv.GetMoveStartTime())
			return nil
		}).
		Times(1)

	require.NoError(t, tl.tick(ctx))

	// Vanished event: emitted by removeEntity AFTER the entity is unregistered.
	pub.EXPECT().
		PublishEvent(gomock.Any(), "broadcast", gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, ev proto.Message) error {
			ze, ok := ev.(*zonev1.ZoneEvent)
			require.True(t, ok)
			vn := ze.GetVanished()
			require.NotNil(t, vn)
			assert.Equal(t, uint32(42), vn.GetEntityId())
			assert.Equal(t, uint32(1), vn.GetType(), "phase 1 only emits logged-out vanish")
			return nil
		}).
		Times(1)

	require.NoError(t, tl.removeEntity(ctx, 42))

	// gomock.Verify is implicit via Times; a missed/over-called expectation
	// surfaces at test cleanup via ctrl.Finish().
}

// TestTickLoop_PublishFailureDoesNotFailEntityOps asserts the
// non-fatal-publish contract: a failing publisher must not roll back an
// addEntity (the entity stays registered) or block a removeEntity (the
// entity is still removed). This is the operational resilience
// requirement on the broadcast path.
func TestTickLoop_PublishFailureDoesNotFailEntityOps(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	pub := domainmock.NewMockPublisher(ctrl)
	md := newSyntheticMapSized(32, 32)
	md.Name = "failmap"
	tl := NewTickLoop(md, 50*time.Millisecond, silentLogger(), pub)
	require.NotNil(t, tl)

	ctx := context.Background()

	// First add: publisher errors. The entity must still be present
	// after the call.
	pub.EXPECT().PublishEvent(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(assert.AnError).
		Times(1)

	_, err := tl.addEntity(ctx, &domain.Entity{ID: 7, Type: domain.EntityMob, X: 5, Y: 5, MoveSpeed: 150})
	require.NoError(t, err, "publish error must not propagate")
	_, err = tl.getEntity(ctx, 7)
	require.NoError(t, err, "entity must be in the zone despite publish failure")

	// Remove: publisher errors. The entity must still be gone.
	pub.EXPECT().PublishEvent(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(assert.AnError).
		Times(1)
	require.NoError(t, tl.removeEntity(ctx, 7))
	_, err = tl.getEntity(ctx, 7)
	require.Error(t, err, "entity must be removed despite publish failure")
}
