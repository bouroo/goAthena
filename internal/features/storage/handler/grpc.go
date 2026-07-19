package handler

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	"github.com/bouroo/goAthena/internal/features/storage/domain"
)

type grpcHandler struct {
	zonev1.UnimplementedZoneServiceServer
	svc    domain.StorageService
	logger *zerolog.Logger
}

// NewGPCHandler creates a new gRPC handler for the storage service.
func NewGPCHandler(svc domain.StorageService, logger *zerolog.Logger) zonev1.ZoneServiceServer {
	return &grpcHandler{
		svc:    svc,
		logger: logger,
	}
}

func (h *grpcHandler) OpenStorage(ctx context.Context, req *zonev1.OpenStorageRequest) (*zonev1.OpenStorageResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetCharId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "char_id must be > 0")
	}

	accountID := req.GetCharId()

	h.logger.Debug().
		Uint32("account_id", accountID).
		Msg("storage: OpenStorage called")

	if err := h.svc.OpenStorage(ctx, accountID); err != nil {
		h.logger.Error().Stack().Err(err).
			Uint32("account_id", accountID).
			Msg("storage: OpenStorage failed")

		return &zonev1.OpenStorageResponse{
			Success:      false,
			ErrorMessage: mapError(err),
		}, nil
	}

	h.logger.Debug().
		Uint32("account_id", accountID).
		Msg("storage: OpenStorage processed")

	return &zonev1.OpenStorageResponse{
		Success:      true,
		ErrorMessage: "",
		Items:        []*zonev1.StorageItem{},
	}, nil
}

func (h *grpcHandler) DepositItem(ctx context.Context, req *zonev1.DepositItemRequest) (*zonev1.DepositItemResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetCharId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "char_id must be > 0")
	}
	if req.GetInventoryItemId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "inventory_item_id must be > 0")
	}
	if req.GetAmount() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount must be > 0")
	}

	accountID := req.GetCharId()

	h.logger.Debug().
		Uint32("account_id", accountID).
		Uint64("inventory_item_id", req.GetInventoryItemId()).
		Int32("amount", req.GetAmount()).
		Msg("storage: DepositItem called")

	if err := h.svc.DepositItem(ctx, accountID, req.GetInventoryItemId(), req.GetAmount()); err != nil {
		h.logger.Error().Stack().Err(err).
			Uint32("account_id", accountID).
			Uint64("inventory_item_id", req.GetInventoryItemId()).
			Int32("amount", req.GetAmount()).
			Msg("storage: DepositItem failed")

		return &zonev1.DepositItemResponse{
			Success:      false,
			ErrorMessage: mapError(err),
		}, nil
	}

	h.logger.Debug().
		Uint32("account_id", accountID).
		Uint64("inventory_item_id", req.GetInventoryItemId()).
		Int32("amount", req.GetAmount()).
		Msg("storage: DepositItem processed")

	return &zonev1.DepositItemResponse{
		Success:       true,
		ErrorMessage:  "",
		StorageItemId: 0,
	}, nil
}

func (h *grpcHandler) WithdrawItem(ctx context.Context, req *zonev1.WithdrawItemRequest) (*zonev1.WithdrawItemResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetCharId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "char_id must be > 0")
	}
	if req.GetStorageItemId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "storage_item_id must be > 0")
	}
	if req.GetAmount() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount must be > 0")
	}

	accountID := req.GetCharId()

	h.logger.Debug().
		Uint32("account_id", accountID).
		Uint64("storage_item_id", req.GetStorageItemId()).
		Int32("amount", req.GetAmount()).
		Msg("storage: WithdrawItem called")

	if err := h.svc.WithdrawItem(ctx, accountID, req.GetStorageItemId(), req.GetAmount()); err != nil {
		h.logger.Error().Stack().Err(err).
			Uint32("account_id", accountID).
			Uint64("storage_item_id", req.GetStorageItemId()).
			Int32("amount", req.GetAmount()).
			Msg("storage: WithdrawItem failed")

		return &zonev1.WithdrawItemResponse{
			Success:      false,
			ErrorMessage: mapError(err),
		}, nil
	}

	h.logger.Debug().
		Uint32("account_id", accountID).
		Uint64("storage_item_id", req.GetStorageItemId()).
		Int32("amount", req.GetAmount()).
		Msg("storage: WithdrawItem processed")

	return &zonev1.WithdrawItemResponse{
		Success:         true,
		ErrorMessage:    "",
		InventoryItemId: 0,
	}, nil
}

func (h *grpcHandler) CloseStorage(ctx context.Context, req *zonev1.CloseStorageRequest) (*zonev1.CloseStorageResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetCharId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "char_id must be > 0")
	}

	accountID := req.GetCharId()

	h.logger.Debug().
		Uint32("account_id", accountID).
		Msg("storage: CloseStorage called")

	if err := h.svc.CloseStorage(ctx, accountID); err != nil {
		h.logger.Error().Stack().Err(err).
			Uint32("account_id", accountID).
			Msg("storage: CloseStorage failed")

		return &zonev1.CloseStorageResponse{
			Success:      false,
			ErrorMessage: mapError(err),
		}, nil
	}

	h.logger.Debug().
		Uint32("account_id", accountID).
		Msg("storage: CloseStorage processed")

	return &zonev1.CloseStorageResponse{
		Success:      true,
		ErrorMessage: "",
	}, nil
}

func mapError(err error) string {
	switch {
	case errors.Is(err, domain.ErrStorageNotFound):
		return "Storage not found"
	case errors.Is(err, domain.ErrStorageLocked):
		return "Storage is locked by another operation"
	case errors.Is(err, domain.ErrStorageFull):
		return "Storage is full"
	case errors.Is(err, domain.ErrInsufficientStorageItem):
		return "Insufficient items in storage"
	case errors.Is(err, domain.ErrInventoryFull):
		return "Inventory is full"
	case errors.Is(err, domain.ErrInvalidItemAmount):
		return "Invalid item amount"
	default:
		return fmt.Sprintf("Storage error: %v", err)
	}
}
