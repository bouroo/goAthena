//go:build unit

package handler

import (
	"context"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gomock "go.uber.org/mock/gomock"

	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	storagedomain "github.com/bouroo/goAthena/internal/features/storage/domain"
	storagedomainmock "github.com/bouroo/goAthena/internal/features/storage/domain/mock"
)

func setupTestHandler(t *testing.T) (*grpcHandler, *storagedomainmock.MockStorageService) {
	ctrl := gomock.NewController(t)
	svc := storagedomainmock.NewMockStorageService(ctrl)
	logger := zerolog.Nop()
	handler := NewGPCHandler(svc, &logger).(*grpcHandler)
	return handler, svc
}

func TestOpenStorage_Success(t *testing.T) {
	handler, svc := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.OpenStorageRequest{
		CharId: 1001,
	}

	svc.EXPECT().OpenStorage(ctx, uint32(1001)).Return(nil)

	resp, err := handler.OpenStorage(ctx, req)

	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Empty(t, resp.ErrorMessage)
	assert.NotNil(t, resp.Items)
}

func TestOpenStorage_ServiceError(t *testing.T) {
	handler, svc := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.OpenStorageRequest{
		CharId: 1001,
	}

	svc.EXPECT().OpenStorage(ctx, uint32(1001)).Return(storagedomain.ErrStorageLocked)

	resp, err := handler.OpenStorage(ctx, req)

	require.NoError(t, err)
	assert.False(t, resp.Success)
	assert.NotEmpty(t, resp.ErrorMessage)
}

func TestOpenStorage_InvalidCharID(t *testing.T) {
	handler, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.OpenStorageRequest{
		CharId: 0,
	}

	resp, err := handler.OpenStorage(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidArgument")
}

func TestOpenStorage_NilRequest(t *testing.T) {
	handler, _ := setupTestHandler(t)
	ctx := context.Background()

	resp, err := handler.OpenStorage(ctx, nil)

	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidArgument")
}

func TestDepositItem_Success(t *testing.T) {
	handler, svc := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.DepositItemRequest{
		CharId:          1001,
		InventoryItemId: 501,
		Amount:          5,
	}

	svc.EXPECT().DepositItem(ctx, uint32(1001), uint64(501), int32(5)).Return(nil)

	resp, err := handler.DepositItem(ctx, req)

	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Empty(t, resp.ErrorMessage)
}

func TestDepositItem_ServiceError(t *testing.T) {
	handler, svc := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.DepositItemRequest{
		CharId:          1001,
		InventoryItemId: 501,
		Amount:          5,
	}

	svc.EXPECT().DepositItem(ctx, uint32(1001), uint64(501), int32(5)).Return(storagedomain.ErrStorageFull)

	resp, err := handler.DepositItem(ctx, req)

	require.NoError(t, err)
	assert.False(t, resp.Success)
	assert.NotEmpty(t, resp.ErrorMessage)
	assert.Contains(t, resp.ErrorMessage, "full")
}

func TestDepositItem_InvalidCharID(t *testing.T) {
	handler, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.DepositItemRequest{
		CharId:          0,
		InventoryItemId: 501,
		Amount:          5,
	}

	resp, err := handler.DepositItem(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidArgument")
}

func TestDepositItem_ZeroInventoryItemId(t *testing.T) {
	handler, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.DepositItemRequest{
		CharId:          1001,
		InventoryItemId: 0,
		Amount:          5,
	}

	resp, err := handler.DepositItem(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidArgument")
}

func TestDepositItem_ZeroAmount(t *testing.T) {
	handler, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.DepositItemRequest{
		CharId:          1001,
		InventoryItemId: 501,
		Amount:          0,
	}

	resp, err := handler.DepositItem(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidArgument")
}

func TestDepositItem_NegativeAmount(t *testing.T) {
	handler, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.DepositItemRequest{
		CharId:          1001,
		InventoryItemId: 501,
		Amount:          -5,
	}

	resp, err := handler.DepositItem(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidArgument")
}

func TestDepositItem_NilRequest(t *testing.T) {
	handler, _ := setupTestHandler(t)
	ctx := context.Background()

	resp, err := handler.DepositItem(ctx, nil)

	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidArgument")
}

func TestWithdrawItem_Success(t *testing.T) {
	handler, svc := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.WithdrawItemRequest{
		CharId:        1001,
		StorageItemId: 1,
		Amount:        5,
	}

	svc.EXPECT().WithdrawItem(ctx, uint32(1001), uint64(1), int32(5)).Return(nil)

	resp, err := handler.WithdrawItem(ctx, req)

	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Empty(t, resp.ErrorMessage)
}

func TestWithdrawItem_ServiceError(t *testing.T) {
	handler, svc := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.WithdrawItemRequest{
		CharId:        1001,
		StorageItemId: 1,
		Amount:        5,
	}

	svc.EXPECT().WithdrawItem(ctx, uint32(1001), uint64(1), int32(5)).Return(storagedomain.ErrInsufficientStorageItem)

	resp, err := handler.WithdrawItem(ctx, req)

	require.NoError(t, err)
	assert.False(t, resp.Success)
	assert.NotEmpty(t, resp.ErrorMessage)
	assert.Contains(t, resp.ErrorMessage, "Insufficient")
}

func TestWithdrawItem_InvalidCharID(t *testing.T) {
	handler, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.WithdrawItemRequest{
		CharId:        0,
		StorageItemId: 1,
		Amount:        5,
	}

	resp, err := handler.WithdrawItem(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidArgument")
}

func TestWithdrawItem_ZeroStorageItemId(t *testing.T) {
	handler, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.WithdrawItemRequest{
		CharId:        1001,
		StorageItemId: 0,
		Amount:        5,
	}

	resp, err := handler.WithdrawItem(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidArgument")
}

func TestWithdrawItem_ZeroAmount(t *testing.T) {
	handler, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.WithdrawItemRequest{
		CharId:        1001,
		StorageItemId: 1,
		Amount:        0,
	}

	resp, err := handler.WithdrawItem(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidArgument")
}

func TestWithdrawItem_NegativeAmount(t *testing.T) {
	handler, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.WithdrawItemRequest{
		CharId:        1001,
		StorageItemId: 1,
		Amount:        -5,
	}

	resp, err := handler.WithdrawItem(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidArgument")
}

func TestWithdrawItem_NilRequest(t *testing.T) {
	handler, _ := setupTestHandler(t)
	ctx := context.Background()

	resp, err := handler.WithdrawItem(ctx, nil)

	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidArgument")
}

func TestCloseStorage_Success(t *testing.T) {
	handler, svc := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.CloseStorageRequest{
		CharId: 1001,
	}

	svc.EXPECT().CloseStorage(ctx, uint32(1001)).Return(nil)

	resp, err := handler.CloseStorage(ctx, req)

	require.NoError(t, err)
	assert.True(t, resp.Success)
	assert.Empty(t, resp.ErrorMessage)
}

func TestCloseStorage_ServiceError(t *testing.T) {
	handler, svc := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.CloseStorageRequest{
		CharId: 1001,
	}

	svc.EXPECT().CloseStorage(ctx, uint32(1001)).Return(assert.AnError)

	resp, err := handler.CloseStorage(ctx, req)

	require.NoError(t, err)
	assert.False(t, resp.Success)
	assert.NotEmpty(t, resp.ErrorMessage)
}

func TestCloseStorage_InvalidCharID(t *testing.T) {
	handler, _ := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.CloseStorageRequest{
		CharId: 0,
	}

	resp, err := handler.CloseStorage(ctx, req)

	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidArgument")
}

func TestCloseStorage_NilRequest(t *testing.T) {
	handler, _ := setupTestHandler(t)
	ctx := context.Background()

	resp, err := handler.CloseStorage(ctx, nil)

	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "InvalidArgument")
}

func TestMapError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{
			name:     "StorageNotFound",
			err:      storagedomain.ErrStorageNotFound,
			expected: "Storage not found",
		},
		{
			name:     "StorageLocked",
			err:      storagedomain.ErrStorageLocked,
			expected: "locked by another operation",
		},
		{
			name:     "StorageFull",
			err:      storagedomain.ErrStorageFull,
			expected: "full",
		},
		{
			name:     "InsufficientStorageItem",
			err:      storagedomain.ErrInsufficientStorageItem,
			expected: "Insufficient",
		},
		{
			name:     "InventoryFull",
			err:      storagedomain.ErrInventoryFull,
			expected: "Inventory is full",
		},
		{
			name:     "InvalidItemAmount",
			err:      storagedomain.ErrInvalidItemAmount,
			expected: "Invalid item amount",
		},
		{
			name:     "GenericError",
			err:      assert.AnError,
			expected: "Storage error:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapError(tt.err)
			assert.Contains(t, result, tt.expected)
		})
	}
}

func TestDepositItem_InventoryFullError(t *testing.T) {
	handler, svc := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.DepositItemRequest{
		CharId:          1001,
		InventoryItemId: 501,
		Amount:          5,
	}

	svc.EXPECT().DepositItem(ctx, uint32(1001), uint64(501), int32(5)).Return(storagedomain.ErrInventoryFull)

	resp, err := handler.DepositItem(ctx, req)

	require.NoError(t, err)
	assert.False(t, resp.Success)
	assert.NotEmpty(t, resp.ErrorMessage)
	assert.Contains(t, resp.ErrorMessage, "Inventory")
}

func TestWithdrawItem_StorageFullError(t *testing.T) {
	handler, svc := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.WithdrawItemRequest{
		CharId:        1001,
		StorageItemId: 1,
		Amount:        5,
	}

	svc.EXPECT().WithdrawItem(ctx, uint32(1001), uint64(1), int32(5)).Return(storagedomain.ErrStorageFull)

	resp, err := handler.WithdrawItem(ctx, req)

	require.NoError(t, err)
	assert.False(t, resp.Success)
	assert.NotEmpty(t, resp.ErrorMessage)
	assert.Contains(t, resp.ErrorMessage, "Storage")
}

func TestOpenStorage_StorageLockedError(t *testing.T) {
	handler, svc := setupTestHandler(t)
	ctx := context.Background()

	req := &zonev1.OpenStorageRequest{
		CharId: 1001,
	}

	svc.EXPECT().OpenStorage(ctx, uint32(1001)).Return(storagedomain.ErrStorageLocked)

	resp, err := handler.OpenStorage(ctx, req)

	require.NoError(t, err)
	assert.False(t, resp.Success)
	assert.NotEmpty(t, resp.ErrorMessage)
	assert.Contains(t, resp.ErrorMessage, "locked")
}
