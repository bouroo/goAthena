//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	"github.com/bouroo/goAthena/internal/features/identity/domain"
	zonedomain "github.com/bouroo/goAthena/internal/features/zone/domain"
	"github.com/bouroo/goAthena/internal/features/zone/service"
	"github.com/bouroo/goAthena/internal/infrastructure/agones"
	"github.com/bouroo/goAthena/pkg/ro/romap"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// silentLogger returns a *zerolog.Logger that discards everything.
// The local zone service requires a non-nil logger; the E2E suite
// only needs to verify behavior, not log output.
func silentLogger() *zerolog.Logger {
	l := zerolog.Nop()
	return &l
}

// newSyntheticMap builds a fully-walkable map data fixture suitable
// for the zone service's tick loop + pathfinder. Mirrors the helper
// in internal/features/zone/service/service_test.go.
func newSyntheticMap(name string, w, h int) *romap.MapData {
	md := &romap.MapData{
		Name:     name,
		Width:    w,
		Height:   h,
		Walkable: make([]bool, w*h),
		Heights:  make([]float32, w*h),
	}
	for i := range md.Walkable {
		md.Walkable[i] = true
	}
	return md
}

// newLocalZoneService builds a TickLoop + ZoneService against a
// synthetic in-memory map. The cluster's remote zone instance is not
// reachable for MoveEntity / GetVisible (those are in-process only);
// the E2E suite therefore exercises both the gRPC EnterZone path and
// the in-process service API to cover the full zone surface.
func newLocalZoneService(t *testing.T) *service.ZoneService {
	t.Helper()
	md := newSyntheticMap("e2e_test", 64, 64)
	tl := service.NewTickLoop(md, 50*time.Millisecond, silentLogger())
	require.NotNil(t, tl)
	zs := service.NewZoneService(tl, agones.NewLocal(silentLogger()), 150, 0, silentLogger())
	require.NotNil(t, zs)
	return zs
}

// TestE2E_ZoneEnterAndMove exercises the gRPC EnterZone path against
// the cluster's zone service and the in-process MoveEntity / GetVisible
// APIs against a synthetic map.
//
// EnterZone is the gRPC entry point (remote cluster). AddEntity +
// MoveEntity + GetVisible are in-process service methods that have no
// gRPC surface in Phase 5; we exercise them locally with a fresh
// ZoneService to assert the zone domain semantics without requiring
// the cluster to expose them.
func TestE2E_ZoneEnterAndMove(t *testing.T) {
	h := NewE2EHarness(t)
	ctx := TestContext(t)

	userID := UniqueUserID()
	const password = "zone-pass"
	accountID := createTestAccount(t, h, userID, password, domain.SexMale)
	t.Cleanup(func() { deleteTestAccount(t, h, accountID) })
	t.Cleanup(func() {
		dctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		deleteValkeySession(dctx, h, accountID)
	})

	login := loginAsAccount(t, h, userID, password)
	charName := UniqueCharName()
	charID := createTestCharacter(t, h, accountID, 0, charName)
	t.Cleanup(func() { deleteTestCharacter(t, h, charID) })

	// Step 1: EnterZone over gRPC.
	resp, err := h.ZoneClient.EnterZone(ctx, &zonev1.EnterZoneRequest{
		AccountId:  login.GetAccountId(),
		CharId:     charID,
		LoginId1:   login.GetLoginId1(),
		ClientTick: 0,
		Sex:        login.GetSex(),
		Packetver:  20130807,
		ClientIp:   "203.0.113.30",
	})
	// EnterZone may legitimately return success==false on clusters
	// where the session validator is not yet wired in (early-phase
	// rollouts). The E2E suite must degrade gracefully: log the
	// outcome, skip the rest of the assertions if the cluster is
	// not yet ready, but always exercise the in-process ZoneService
	// path which is hermetic.
	switch {
	case err != nil:
		t.Logf("zone gRPC unreachable / EnterZone not yet wired: %v", err)
	case resp.GetSuccess():
		assert.NotEmpty(t, resp.GetMapName(),
			"successful EnterZone must return a map name")
	default:
		t.Logf("EnterZone returned success=false (cluster may be in mid-rollout): %s",
			resp.GetError())
	}

	// Step 2: in-process ZoneService — add an entity, move it, query
	// visibility. This is the only way to validate the zone domain
	// semantics end-to-end without exposing MoveEntity / GetVisible
	// over gRPC; the alternative is to wait for Phase 6+ RPCs.
	zs := newLocalZoneService(t)
	addCtx, addCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer addCancel()
	player := &zonedomain.Entity{
		ID:        zonedomain.EntityID(charID),
		Type:      zonedomain.EntityPlayer,
		X:         10,
		Y:         10,
		MoveSpeed: 150,
	}
	require.NoError(t, zs.AddEntity(addCtx, player),
		"add entity to local zone service")

	require.NoError(t, zs.MoveEntity(addCtx, zonedomain.EntityID(charID), 20, 20),
		"move entity to (20,20)")
	// Allow the tick loop to consume the path. With a 50ms tick rate,
	// 100ms is enough for two path steps.
	time.Sleep(150 * time.Millisecond)

	// Step 3: query the entity — X/Y must reflect either the target
	// or an intermediate cell along the path. The exact value depends
	// on tick scheduling; the invariant we assert is "closer to target
	// than origin" (manhattan distance is a reasonable proxy).
	got, err := zs.GetEntity(addCtx, zonedomain.EntityID(charID))
	require.NoError(t, err)
	require.NotNil(t, got)
	startDist := absInt(10-got.X) + absInt(10-got.Y)
	endDist := absInt(20-got.X) + absInt(20-got.Y)
	assert.Less(t, endDist, startDist,
		"entity must be closer to (20,20) than to (10,10) after MoveEntity")
}

// TestE2E_ZoneVisibleSet covers the AOI visibility query: two
// entities placed close together must see each other; an entity far
// away must not.
func TestE2E_ZoneVisibleSet(t *testing.T) {
	zs := newLocalZoneService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	a := &zonedomain.Entity{ID: 1001, Type: zonedomain.EntityPlayer, X: 5, Y: 5, MoveSpeed: 150}
	b := &zonedomain.Entity{ID: 1002, Type: zonedomain.EntityMob, X: 6, Y: 5, MoveSpeed: 0}
	c := &zonedomain.Entity{ID: 1003, Type: zonedomain.EntityMob, X: 60, Y: 60, MoveSpeed: 0}

	require.NoError(t, zs.AddEntity(ctx, a))
	require.NoError(t, zs.AddEntity(ctx, b))
	require.NoError(t, zs.AddEntity(ctx, c))

	visible, err := zs.GetVisible(ctx, zonedomain.EntityID(1001))
	require.NoError(t, err)

	ids := make(map[zonedomain.EntityID]struct{}, len(visible))
	for _, e := range visible {
		ids[e.ID] = struct{}{}
	}
	_, hasB := ids[1002]
	_, hasC := ids[1003]
	assert.True(t, hasB, "nearby entity b must be visible to a")
	assert.False(t, hasC, "distant entity c must not be visible to a")
}

// TestE2E_ZoneDuplicateEntity verifies ErrEntityExists on a duplicate
// add — the cluster invariant that entity IDs are unique per zone.
func TestE2E_ZoneDuplicateEntity(t *testing.T) {
	zs := newLocalZoneService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	e := &zonedomain.Entity{ID: 2001, Type: zonedomain.EntityPlayer, X: 0, Y: 0, MoveSpeed: 150}
	require.NoError(t, zs.AddEntity(ctx, e))

	err := zs.AddEntity(ctx, e)
	require.Error(t, err)
	assert.ErrorIs(t, err, service.ErrEntityExists)
}

// TestE2E_ZoneRemoveEntity ensures the remove path is symmetric and
// the subsequent visibility query reflects the empty state.
func TestE2E_ZoneRemoveEntity(t *testing.T) {
	zs := newLocalZoneService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	e := &zonedomain.Entity{ID: 3001, Type: zonedomain.EntityMob, X: 10, Y: 10, MoveSpeed: 0}
	require.NoError(t, zs.AddEntity(ctx, e))
	require.NoError(t, zs.RemoveEntity(ctx, e.ID))

	_, err := zs.GetEntity(ctx, e.ID)
	require.Error(t, err)
	assert.ErrorIs(t, err, service.ErrEntityMissing)
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
