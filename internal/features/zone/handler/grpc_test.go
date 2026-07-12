//go:build unit

package handler_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	"github.com/bouroo/goAthena/internal/features/zone/domain"
	domainmock "github.com/bouroo/goAthena/internal/features/zone/domain/mock"
	"github.com/bouroo/goAthena/internal/features/zone/handler"
	"github.com/bouroo/goAthena/internal/features/zone/service"
	"github.com/bouroo/goAthena/pkg/ro/romap"
)

// newTestLogger returns a no-op logger for handler tests.
func newTestLogger() *zerolog.Logger {
	l := zerolog.Nop()
	return &l
}

func silentLogger() *zerolog.Logger {
	l := zerolog.Nop()
	return &l
}

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

func newZoneService(t *testing.T, tickRate time.Duration) (*service.ZoneService, *service.TickLoop) {
	t.Helper()
	md := newSyntheticMap("test", 100, 100)
	tl := service.NewTickLoop(md, tickRate, silentLogger(), nopPublisher{})
	require.NotNil(t, tl)
	ag := newCountingLifecycle()
	zs := service.NewZoneService(tl, ag, 150, 0, silentLogger())
	require.NotNil(t, zs)
	return zs, tl
}

type nopPublisher struct{}

func (nopPublisher) PublishEvent(_ context.Context, _ string, _ proto.Message) error {
	return nil
}

func newCountingLifecycle() *countingLifecycle { return &countingLifecycle{} }

type countingLifecycle struct {
	mu         sync.Mutex
	ready      int32
	alloc      int32
	shutdown   int32
	health     int32
	allocFn    func(ctx context.Context) error
	shutdownFn func(ctx context.Context) error
}

func (c *countingLifecycle) Ready(_ context.Context) error {
	atomic.AddInt32(&c.ready, 1)
	return nil
}

func (c *countingLifecycle) Allocate(ctx context.Context) error {
	atomic.AddInt32(&c.alloc, 1)
	if c.allocFn != nil {
		return c.allocFn(ctx)
	}
	return nil
}

func (c *countingLifecycle) Shutdown(ctx context.Context) error {
	atomic.AddInt32(&c.shutdown, 1)
	if c.shutdownFn != nil {
		return c.shutdownFn(ctx)
	}
	return nil
}

func (c *countingLifecycle) Health(_ context.Context) error {
	atomic.AddInt32(&c.health, 1)
	return nil
}

func (c *countingLifecycle) Close() error { return nil }

func TestEnterZone_NilRequest(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockZoneService(ctrl)
	h := handler.NewGRPCHandler(svc, "prontera", 150, 200, newTestLogger())

	resp, err := h.EnterZone(context.Background(), nil)
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestEnterZone_InvalidAccountID(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockZoneService(ctrl)
	h := handler.NewGRPCHandler(svc, "prontera", 150, 200, newTestLogger())

	resp, err := h.EnterZone(context.Background(), &zonev1.EnterZoneRequest{
		AccountId: 0,
		CharId:    42,
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "account_id")
}

func TestEnterZone_InvalidCharID(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockZoneService(ctrl)
	h := handler.NewGRPCHandler(svc, "prontera", 150, 200, newTestLogger())

	resp, err := h.EnterZone(context.Background(), &zonev1.EnterZoneRequest{
		AccountId: 42,
		CharId:    0,
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "char_id")
}

func TestEnterZone_AddEntityError(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockZoneService(ctrl)
	h := handler.NewGRPCHandler(svc, "prontera", 150, 200, newTestLogger())

	svc.EXPECT().
		AddEntity(gomock.Any(), gomock.Any()).
		Return(errors.New("aoi grid full"))

	resp, err := h.EnterZone(context.Background(), &zonev1.EnterZoneRequest{
		AccountId: 42,
		CharId:    100,
	})
	require.NoError(t, err, "wire failures are not gRPC errors")
	require.NotNil(t, resp)
	assert.False(t, resp.GetSuccess())
	assert.Empty(t, resp.GetMapName())
	assert.Contains(t, resp.GetError(), "zone entry failed")
	assert.Contains(t, resp.GetError(), "aoi grid full")
}

func TestEnterZone_Success(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockZoneService(ctrl)
	const (
		wantMap   = "prontera"
		wantSpawn = 150
		wantY     = 200
	)
	h := handler.NewGRPCHandler(svc, wantMap, wantSpawn, wantY, newTestLogger())

	svc.EXPECT().
		AddEntity(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, e *domain.Entity) error {
			require.NotNil(t, e)
			assert.Equal(t, domain.EntityID(42), e.ID)
			assert.Equal(t, domain.EntityPlayer, e.Type)
			assert.Equal(t, wantSpawn, e.X)
			assert.Equal(t, wantY, e.Y)
			return nil
		})

	resp, err := h.EnterZone(context.Background(), &zonev1.EnterZoneRequest{
		AccountId: 42,
		CharId:    100,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, resp.GetSuccess())
	assert.Equal(t, wantMap, resp.GetMapName())
	assert.Equal(t, uint32(wantSpawn), resp.GetMapX())
	assert.Equal(t, uint32(wantY), resp.GetMapY())
	assert.Empty(t, resp.GetError())
}

func TestMoveEntity_NilRequest(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockZoneService(ctrl)
	h := handler.NewGRPCHandler(svc, "prontera", 150, 200, newTestLogger())

	resp, err := h.MoveEntity(context.Background(), nil)
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestMoveEntity_InvalidAccountID(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockZoneService(ctrl)
	h := handler.NewGRPCHandler(svc, "prontera", 150, 200, newTestLogger())

	resp, err := h.MoveEntity(context.Background(), &zonev1.MoveEntityRequest{
		AccountId: 0,
		DestX:     160,
		DestY:     210,
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "account_id")
}

func TestMoveEntity_EntityNotFound(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockZoneService(ctrl)
	h := handler.NewGRPCHandler(svc, "prontera", 150, 200, newTestLogger())

	svc.EXPECT().
		GetEntity(gomock.Any(), domain.EntityID(42)).
		Return(nil, errors.New("entity not registered"))

	resp, err := h.MoveEntity(context.Background(), &zonev1.MoveEntityRequest{
		AccountId: 42,
		DestX:     160,
		DestY:     210,
	})
	require.NoError(t, err, "wire failures are not gRPC errors")
	require.NotNil(t, resp)
	assert.False(t, resp.GetSuccess())
	assert.Contains(t, resp.GetError(), "entity not found")
	assert.Contains(t, resp.GetError(), "entity not registered")
}

func TestMoveEntity_MoveFailed(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockZoneService(ctrl)
	h := handler.NewGRPCHandler(svc, "prontera", 150, 200, newTestLogger())

	svc.EXPECT().
		GetEntity(gomock.Any(), domain.EntityID(42)).
		Return(&domain.Entity{ID: domain.EntityID(42), X: 150, Y: 200}, nil)
	svc.EXPECT().
		MoveEntity(gomock.Any(), domain.EntityID(42), 160, 210).
		Return(errors.New("no walkable path"))

	resp, err := h.MoveEntity(context.Background(), &zonev1.MoveEntityRequest{
		AccountId: 42,
		DestX:     160,
		DestY:     210,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.False(t, resp.GetSuccess())
	// Source comes from GetEntity snapshot, destination is echoed back
	// even on failure so the gateway can log the rejected path.
	assert.Equal(t, uint32(150), resp.GetSrcX())
	assert.Equal(t, uint32(200), resp.GetSrcY())
	assert.Equal(t, uint32(160), resp.GetDestX())
	assert.Equal(t, uint32(210), resp.GetDestY())
	assert.Contains(t, resp.GetError(), "no walkable path")
}

func TestMoveEntity_Success(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	svc := domainmock.NewMockZoneService(ctrl)
	h := handler.NewGRPCHandler(svc, "prontera", 150, 200, newTestLogger())

	svc.EXPECT().
		GetEntity(gomock.Any(), domain.EntityID(42)).
		Return(&domain.Entity{ID: domain.EntityID(42), X: 150, Y: 200}, nil)
	svc.EXPECT().
		MoveEntity(gomock.Any(), domain.EntityID(42), 160, 210).
		DoAndReturn(func(_ context.Context, id domain.EntityID, x, y int) error {
			assert.Equal(t, domain.EntityID(42), id)
			assert.Equal(t, 160, x)
			assert.Equal(t, 210, y)
			return nil
		})

	resp, err := h.MoveEntity(context.Background(), &zonev1.MoveEntityRequest{
		AccountId: 42,
		DestX:     160,
		DestY:     210,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, resp.GetSuccess())
	assert.Equal(t, uint32(150), resp.GetSrcX())
	assert.Equal(t, uint32(200), resp.GetSrcY())
	assert.Equal(t, uint32(160), resp.GetDestX())
	assert.Equal(t, uint32(210), resp.GetDestY())
	assert.Empty(t, resp.GetError())
}

func TestAttackEntity(t *testing.T) {
	t.Parallel()
	zs, _ := newZoneService(t, 50*time.Millisecond)
	ctx := context.Background()

	mob := &domain.Entity{ID: 1, Type: domain.EntityMob, X: 10, Y: 10, HP: 50, MaxHP: 50, MobID: 1002}
	require.NoError(t, zs.AddEntity(ctx, mob))

	player := &domain.Entity{ID: 2, Type: domain.EntityPlayer, X: 10, Y: 10}
	require.NoError(t, zs.AddEntity(ctx, player))

	req := &zonev1.AttackEntityRequest{
		EntityId:   1,
		Damage:     25,
		AttackerId: 2,
		SkillId:    0,
		SkillLevel: 0,
	}

	resp, err := handler.NewGRPCHandler(zs, "test", 0, 0, newTestLogger()).AttackEntity(ctx, req)
	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.False(t, resp.TargetDied)
	assert.Equal(t, int32(25), resp.DamageApplied)
	assert.Equal(t, int32(25), resp.CurrentHp)
}

func TestAttackEntity_EntityNotFound(t *testing.T) {
	t.Parallel()
	zs, _ := newZoneService(t, 50*time.Millisecond)
	ctx := context.Background()

	req := &zonev1.AttackEntityRequest{
		EntityId:   999,
		AttackerId: 1,
	}
	_, err := handler.NewGRPCHandler(zs, "test", 0, 0, newTestLogger()).AttackEntity(ctx, req)
	if err != nil {
		t.Logf("Error: %v", err)
	}
	assert.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestPickupItem(t *testing.T) {
	t.Parallel()
	zs, _ := newZoneService(t, 50*time.Millisecond)
	ctx := context.Background()

	item := &domain.Entity{ID: 100, Type: domain.EntityMob, X: 10, Y: 10, ItemID: 502, ItemAmount: 1}
	require.NoError(t, zs.AddEntity(ctx, item))

	player := &domain.Entity{ID: 1, Type: domain.EntityPlayer, X: 10, Y: 10}
	require.NoError(t, zs.AddEntity(ctx, player))

	resp, err := handler.NewGRPCHandler(zs, "test", 0, 0, newTestLogger()).PickupItem(ctx, &zonev1.PickupItemRequest{
		GroundItemId: 100,
		PlayerId:     1,
	})
	assert.NoError(t, err, "Should succeed because player is at the same location as the item")
	assert.True(t, resp.Success)
}

func TestPickupItem_EntityTooFar(t *testing.T) {
	t.Parallel()
	zs, _ := newZoneService(t, 50*time.Millisecond)
	ctx := context.Background()

	item := &domain.Entity{ID: 100, Type: domain.EntityMob, X: 10, Y: 10, ItemID: 502, ItemAmount: 1}
	require.NoError(t, zs.AddEntity(ctx, item))

	player := &domain.Entity{ID: 1, Type: domain.EntityPlayer, X: 20, Y: 20}
	require.NoError(t, zs.AddEntity(ctx, player))

	_, err := handler.NewGRPCHandler(zs, "test", 0, 0, newTestLogger()).PickupItem(ctx, &zonev1.PickupItemRequest{
		GroundItemId: 100,
		PlayerId:     1,
	})
	assert.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestPickupItem_ItemNotFound(t *testing.T) {
	t.Parallel()
	zs, _ := newZoneService(t, 50*time.Millisecond)
	ctx := context.Background()

	req := &zonev1.PickupItemRequest{GroundItemId: 999, PlayerId: 1}
	_, err := handler.NewGRPCHandler(zs, "test", 0, 0, newTestLogger()).PickupItem(ctx, req)
	assert.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}
