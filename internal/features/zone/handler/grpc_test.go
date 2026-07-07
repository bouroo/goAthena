//go:build unit

package handler_test

import (
	"context"
	"errors"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	"github.com/bouroo/goAthena/internal/features/zone/domain"
	domainmock "github.com/bouroo/goAthena/internal/features/zone/domain/mock"
	"github.com/bouroo/goAthena/internal/features/zone/handler"
)

// newTestLogger returns a no-op logger for handler tests.
func newTestLogger() *zerolog.Logger {
	l := zerolog.Nop()
	return &l
}

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
