//go:build unit

package handler

import (
	"context"
	"testing"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	"github.com/bouroo/goAthena/internal/features/trade/domain"
	domainmock "github.com/bouroo/goAthena/internal/features/trade/domain/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestNewTradeHandler(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockSvc := domainmock.NewMockTradeService(ctrl)
	logger := zerolog.Nop()

	handler := NewTradeHandler(mockSvc, &logger)
	require.NotNil(t, handler)
}

func TestRequestTrade_Validation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockSvc := domainmock.NewMockTradeService(ctrl)
	logger := zerolog.Nop()
	handler := NewTradeHandler(mockSvc, &logger)

	tests := []struct {
		name         string
		req          *zonev1.RequestTradeRequest
		expectedCode codes.Code
		expectedMsg  string
	}{
		{
			name:         "nil request",
			req:          nil,
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "request is nil",
		},
		{
			name:         "zero requester_char_id",
			req:          &zonev1.RequestTradeRequest{RequesterCharId: 0, TargetCharId: 123},
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "requester_char_id must be > 0",
		},
		{
			name:         "zero target_char_id",
			req:          &zonev1.RequestTradeRequest{RequesterCharId: 123, TargetCharId: 0},
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "target_char_id must be > 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := handler.RequestTrade(context.Background(), tt.req)

			require.Nil(t, resp)
			require.Error(t, err)
			st, ok := status.FromError(err)
			require.True(t, ok, "error should be a gRPC status error")
			assert.Equal(t, tt.expectedCode, st.Code())
			assert.Contains(t, st.Message(), tt.expectedMsg)
		})
	}
}

func TestRequestTrade_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockSvc := domainmock.NewMockTradeService(ctrl)
	logger := zerolog.Nop()
	handler := NewTradeHandler(mockSvc, &logger)

	req := &zonev1.RequestTradeRequest{
		RequesterCharId: 100,
		TargetCharId:    200,
	}

	expectedTrade := domain.Trade{
		ID:            "trade-123",
		Player1CharID: 100,
		Player2CharID: 200,
		State:         domain.TradeStateRequested,
	}

	mockSvc.EXPECT().
		RequestTrade(gomock.Any(), uint32(100), uint32(200)).
		Return(expectedTrade, nil)

	resp, err := handler.RequestTrade(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, resp.Success)
	assert.Equal(t, "trade-123", resp.TradeId)
	assert.Empty(t, resp.Error)
}

func TestRequestTrade_TargetUnavailable(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockSvc := domainmock.NewMockTradeService(ctrl)
	logger := zerolog.Nop()
	handler := NewTradeHandler(mockSvc, &logger)

	req := &zonev1.RequestTradeRequest{
		RequesterCharId: 100,
		TargetCharId:    200,
	}

	mockSvc.EXPECT().
		RequestTrade(gomock.Any(), uint32(100), uint32(200)).
		Return(domain.Trade{}, domain.ErrTradeTargetUnavailable)

	resp, err := handler.RequestTrade(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.False(t, resp.Success)
	assert.Equal(t, "target unavailable", resp.Error)
}

func TestAddTradeItem_Validation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockSvc := domainmock.NewMockTradeService(ctrl)
	logger := zerolog.Nop()
	handler := NewTradeHandler(mockSvc, &logger)

	tests := []struct {
		name         string
		req          *zonev1.AddTradeItemRequest
		expectedCode codes.Code
		expectedMsg  string
	}{
		{
			name:         "nil request",
			req:          nil,
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "request is nil",
		},
		{
			name:         "empty trade_id",
			req:          &zonev1.AddTradeItemRequest{TradeId: "", CharId: 100, InventoryId: 1, Amount: 5},
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "trade_id is required",
		},
		{
			name:         "zero char_id",
			req:          &zonev1.AddTradeItemRequest{TradeId: "trade-123", CharId: 0, InventoryId: 1, Amount: 5},
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "char_id must be > 0",
		},
		{
			name:         "zero inventory_id",
			req:          &zonev1.AddTradeItemRequest{TradeId: "trade-123", CharId: 100, InventoryId: 0, Amount: 5},
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "inventory_id must be > 0",
		},
		{
			name:         "zero amount",
			req:          &zonev1.AddTradeItemRequest{TradeId: "trade-123", CharId: 100, InventoryId: 1, Amount: 0},
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "amount must be > 0",
		},
		{
			name:         "negative amount",
			req:          &zonev1.AddTradeItemRequest{TradeId: "trade-123", CharId: 100, InventoryId: 1, Amount: -5},
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "amount must be > 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := handler.AddTradeItem(context.Background(), tt.req)

			require.Nil(t, resp)
			require.Error(t, err)
			st, ok := status.FromError(err)
			require.True(t, ok, "error should be a gRPC status error")
			assert.Equal(t, tt.expectedCode, st.Code())
			assert.Contains(t, st.Message(), tt.expectedMsg)
		})
	}
}

func TestAddTradeItem_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockSvc := domainmock.NewMockTradeService(ctrl)
	logger := zerolog.Nop()
	handler := NewTradeHandler(mockSvc, &logger)

	req := &zonev1.AddTradeItemRequest{
		TradeId:     "trade-123",
		CharId:      100,
		InventoryId: 500,
		Amount:      10,
	}

	mockSvc.EXPECT().
		AddTradeItem(gomock.Any(), "trade-123", uint32(100), uint32(500), int32(10)).
		Return(nil)

	resp, err := handler.AddTradeItem(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, resp.Success)
	assert.Empty(t, resp.Error)
}

func TestAddTradeItem_TradeNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockSvc := domainmock.NewMockTradeService(ctrl)
	logger := zerolog.Nop()
	handler := NewTradeHandler(mockSvc, &logger)

	req := &zonev1.AddTradeItemRequest{
		TradeId:     "trade-123",
		CharId:      100,
		InventoryId: 500,
		Amount:      10,
	}

	mockSvc.EXPECT().
		AddTradeItem(gomock.Any(), "trade-123", uint32(100), uint32(500), int32(10)).
		Return(domain.ErrTradeNotFound)

	resp, err := handler.AddTradeItem(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.False(t, resp.Success)
	assert.Equal(t, "trade not found", resp.Error)
}

func TestAddTradeZeny_Validation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockSvc := domainmock.NewMockTradeService(ctrl)
	logger := zerolog.Nop()
	handler := NewTradeHandler(mockSvc, &logger)

	tests := []struct {
		name         string
		req          *zonev1.AddTradeZenyRequest
		expectedCode codes.Code
		expectedMsg  string
	}{
		{
			name:         "nil request",
			req:          nil,
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "request is nil",
		},
		{
			name:         "empty trade_id",
			req:          &zonev1.AddTradeZenyRequest{TradeId: "", CharId: 100, Zeny: 1000},
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "trade_id is required",
		},
		{
			name:         "zero char_id",
			req:          &zonev1.AddTradeZenyRequest{TradeId: "trade-123", CharId: 0, Zeny: 1000},
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "char_id must be > 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := handler.AddTradeZeny(context.Background(), tt.req)

			require.Nil(t, resp)
			require.Error(t, err)
			st, ok := status.FromError(err)
			require.True(t, ok, "error should be a gRPC status error")
			assert.Equal(t, tt.expectedCode, st.Code())
			assert.Contains(t, st.Message(), tt.expectedMsg)
		})
	}
}

func TestAddTradeZeny_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockSvc := domainmock.NewMockTradeService(ctrl)
	logger := zerolog.Nop()
	handler := NewTradeHandler(mockSvc, &logger)

	req := &zonev1.AddTradeZenyRequest{
		TradeId: "trade-123",
		CharId:  100,
		Zeny:    5000,
	}

	mockSvc.EXPECT().
		AddTradeZeny(gomock.Any(), "trade-123", uint32(100), uint32(5000)).
		Return(nil)

	resp, err := handler.AddTradeZeny(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, resp.Success)
	assert.Empty(t, resp.Error)
}

func TestConfirmTrade_Validation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockSvc := domainmock.NewMockTradeService(ctrl)
	logger := zerolog.Nop()
	handler := NewTradeHandler(mockSvc, &logger)

	tests := []struct {
		name         string
		req          *zonev1.ConfirmTradeRequest
		expectedCode codes.Code
		expectedMsg  string
	}{
		{
			name:         "nil request",
			req:          nil,
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "request is nil",
		},
		{
			name:         "empty trade_id",
			req:          &zonev1.ConfirmTradeRequest{TradeId: "", CharId: 100},
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "trade_id is required",
		},
		{
			name:         "zero char_id",
			req:          &zonev1.ConfirmTradeRequest{TradeId: "trade-123", CharId: 0},
			expectedCode: codes.InvalidArgument,
			expectedMsg:  "char_id must be > 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := handler.ConfirmTrade(context.Background(), tt.req)

			require.Nil(t, resp)
			require.Error(t, err)
			st, ok := status.FromError(err)
			require.True(t, ok, "error should be a gRPC status error")
			assert.Equal(t, tt.expectedCode, st.Code())
			assert.Contains(t, st.Message(), tt.expectedMsg)
		})
	}
}

func TestConfirmTrade_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockSvc := domainmock.NewMockTradeService(ctrl)
	logger := zerolog.Nop()
	handler := NewTradeHandler(mockSvc, &logger)

	req := &zonev1.ConfirmTradeRequest{
		TradeId: "trade-123",
		CharId:  100,
	}

	mockSvc.EXPECT().
		ConfirmTrade(gomock.Any(), "trade-123", uint32(100)).
		Return(nil)

	resp, err := handler.ConfirmTrade(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, resp.Success)
	assert.Empty(t, resp.Error)
}

func TestCompleteTrade_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockSvc := domainmock.NewMockTradeService(ctrl)
	logger := zerolog.Nop()
	handler := NewTradeHandler(mockSvc, &logger)

	req := &zonev1.CompleteTradeRequest{
		TradeId: "trade-123",
		CharId:  100,
	}

	mockSvc.EXPECT().
		CompleteTrade(gomock.Any(), "trade-123", uint32(100)).
		Return(nil)

	resp, err := handler.CompleteTrade(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, resp.Success)
	assert.Empty(t, resp.Error)
}

func TestCancelTrade_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockSvc := domainmock.NewMockTradeService(ctrl)
	logger := zerolog.Nop()
	handler := NewTradeHandler(mockSvc, &logger)

	req := &zonev1.CancelTradeRequest{
		TradeId: "trade-123",
		CharId:  100,
	}

	mockSvc.EXPECT().
		CancelTrade(gomock.Any(), "trade-123", uint32(100)).
		Return(nil)

	resp, err := handler.CancelTrade(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.True(t, resp.Success)
	assert.Empty(t, resp.Error)
}

func TestMapError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: "",
		},
		{
			name:     "trade not found",
			err:      domain.ErrTradeNotFound,
			expected: "trade not found",
		},
		{
			name:     "invalid trade state",
			err:      domain.ErrInvalidTradeState,
			expected: "invalid trade state",
		},
		{
			name:     "insufficient inventory",
			err:      domain.ErrInsufficientInventory,
			expected: "insufficient inventory",
		},
		{
			name:     "lock busy",
			err:      domain.ErrLockBusy,
			expected: "operation in progress",
		},
		{
			name:     "target unavailable",
			err:      domain.ErrTradeTargetUnavailable,
			expected: "target unavailable",
		},
		{
			name:     "unknown error",
			err:      assert.AnError,
			expected: "trade error: assert.AnError general error for testing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}
