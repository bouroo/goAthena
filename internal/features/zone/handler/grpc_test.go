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
