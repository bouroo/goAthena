package handler

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	"github.com/bouroo/goAthena/internal/features/trade/domain"
)

// grpcHandler implements zonev1.ZoneServiceServer for trade RPCs.
// It is a thin adapter: proto <-> domain mapping, request validation,
// and error translation. The gateway dispatches kRO trade packets
// to these zone RPCs, and this handler calls the trade service.
type grpcHandler struct {
	zonev1.UnimplementedZoneServiceServer
	svc    domain.TradeService
	logger *zerolog.Logger
}

// NewTradeHandler creates a new trade gRPC handler.
func NewTradeHandler(svc domain.TradeService, logger *zerolog.Logger) zonev1.ZoneServiceServer {
	return &grpcHandler{
		svc:    svc,
		logger: logger,
	}
}

// mapError converts domain errors to human-readable error messages.
func mapError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, domain.ErrTradeNotFound):
		return "trade not found"
	case errors.Is(err, domain.ErrInvalidTradeState):
		return "invalid trade state"
	case errors.Is(err, domain.ErrInsufficientInventory):
		return "insufficient inventory"
	case errors.Is(err, domain.ErrLockBusy):
		return "operation in progress"
	case errors.Is(err, domain.ErrTradeTargetUnavailable):
		return "target unavailable"
	default:
		return fmt.Sprintf("trade error: %v", err)
	}
}

// RequestTrade initiates a trade session between two players.
func (h *grpcHandler) RequestTrade(
	ctx context.Context,
	req *zonev1.RequestTradeRequest,
) (*zonev1.RequestTradeResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetRequesterCharId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "requester_char_id must be > 0")
	}
	if req.GetTargetCharId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "target_char_id must be > 0")
	}

	h.logger.Debug().
		Uint32("requester_char_id", req.GetRequesterCharId()).
		Uint32("target_char_id", req.GetTargetCharId()).
		Msg("trade: RequestTrade called")

	trade, err := h.svc.RequestTrade(ctx, req.GetRequesterCharId(), req.GetTargetCharId())
	if err != nil {
		h.logger.Error().Stack().Err(err).
			Uint32("requester_char_id", req.GetRequesterCharId()).
			Uint32("target_char_id", req.GetTargetCharId()).
			Msg("trade: RequestTrade failed")

		return &zonev1.RequestTradeResponse{
			Success: false,
			Error:   mapError(err),
		}, nil
	}

	h.logger.Debug().
		Str("trade_id", trade.ID).
		Uint32("requester_char_id", req.GetRequesterCharId()).
		Uint32("target_char_id", req.GetTargetCharId()).
		Msg("trade: RequestTrade created")

	return &zonev1.RequestTradeResponse{
		Success: true,
		TradeId: trade.ID,
	}, nil
}

// AddTradeItem adds an item to the trade window.
func (h *grpcHandler) AddTradeItem(
	ctx context.Context,
	req *zonev1.AddTradeItemRequest,
) (*zonev1.AddTradeItemResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetTradeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "trade_id is required")
	}
	if req.GetCharId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "char_id must be > 0")
	}
	if req.GetInventoryId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "inventory_id must be > 0")
	}
	if req.GetAmount() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount must be > 0")
	}

	h.logger.Debug().
		Str("trade_id", req.GetTradeId()).
		Uint32("char_id", req.GetCharId()).
		Uint32("inventory_id", req.GetInventoryId()).
		Int32("amount", req.GetAmount()).
		Msg("trade: AddTradeItem called")

	err := h.svc.AddTradeItem(ctx, req.GetTradeId(), req.GetCharId(), req.GetInventoryId(), req.GetAmount())
	if err != nil {
		h.logger.Error().Stack().Err(err).
			Str("trade_id", req.GetTradeId()).
			Uint32("char_id", req.GetCharId()).
			Uint32("inventory_id", req.GetInventoryId()).
			Int32("amount", req.GetAmount()).
			Msg("trade: AddTradeItem failed")

		return &zonev1.AddTradeItemResponse{
			Success: false,
			Error:   mapError(err),
		}, nil
	}

	h.logger.Debug().
		Str("trade_id", req.GetTradeId()).
		Uint32("char_id", req.GetCharId()).
		Uint32("inventory_id", req.GetInventoryId()).
		Int32("amount", req.GetAmount()).
		Msg("trade: AddTradeItem processed")

	return &zonev1.AddTradeItemResponse{
		Success: true,
	}, nil
}

// AddTradeZeny adds zeny to the trade window.
func (h *grpcHandler) AddTradeZeny(
	ctx context.Context,
	req *zonev1.AddTradeZenyRequest,
) (*zonev1.AddTradeZenyResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetTradeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "trade_id is required")
	}
	if req.GetCharId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "char_id must be > 0")
	}

	h.logger.Debug().
		Str("trade_id", req.GetTradeId()).
		Uint32("char_id", req.GetCharId()).
		Uint32("zeny", req.GetZeny()).
		Msg("trade: AddTradeZeny called")

	err := h.svc.AddTradeZeny(ctx, req.GetTradeId(), req.GetCharId(), req.GetZeny())
	if err != nil {
		h.logger.Error().Stack().Err(err).
			Str("trade_id", req.GetTradeId()).
			Uint32("char_id", req.GetCharId()).
			Uint32("zeny", req.GetZeny()).
			Msg("trade: AddTradeZeny failed")

		return &zonev1.AddTradeZenyResponse{
			Success: false,
			Error:   mapError(err),
		}, nil
	}

	h.logger.Debug().
		Str("trade_id", req.GetTradeId()).
		Uint32("char_id", req.GetCharId()).
		Uint32("zeny", req.GetZeny()).
		Msg("trade: AddTradeZeny processed")

	return &zonev1.AddTradeZenyResponse{
		Success: true,
	}, nil
}

// ConfirmTrade locks the character's offer, preventing further modifications.
func (h *grpcHandler) ConfirmTrade(
	ctx context.Context,
	req *zonev1.ConfirmTradeRequest,
) (*zonev1.ConfirmTradeResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetTradeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "trade_id is required")
	}
	if req.GetCharId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "char_id must be > 0")
	}

	h.logger.Debug().
		Str("trade_id", req.GetTradeId()).
		Uint32("char_id", req.GetCharId()).
		Msg("trade: ConfirmTrade called")

	err := h.svc.ConfirmTrade(ctx, req.GetTradeId(), req.GetCharId())
	if err != nil {
		h.logger.Error().Stack().Err(err).
			Str("trade_id", req.GetTradeId()).
			Uint32("char_id", req.GetCharId()).
			Msg("trade: ConfirmTrade failed")

		return &zonev1.ConfirmTradeResponse{
			Success: false,
			Error:   mapError(err),
		}, nil
	}

	h.logger.Debug().
		Str("trade_id", req.GetTradeId()).
		Uint32("char_id", req.GetCharId()).
		Msg("trade: ConfirmTrade processed")

	return &zonev1.ConfirmTradeResponse{
		Success: true,
	}, nil
}

// CompleteTrade executes the atomic item/zeny transfer when both parties confirmed.
func (h *grpcHandler) CompleteTrade(
	ctx context.Context,
	req *zonev1.CompleteTradeRequest,
) (*zonev1.CompleteTradeResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetTradeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "trade_id is required")
	}
	if req.GetCharId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "char_id must be > 0")
	}

	h.logger.Debug().
		Str("trade_id", req.GetTradeId()).
		Uint32("char_id", req.GetCharId()).
		Msg("trade: CompleteTrade called")

	err := h.svc.CompleteTrade(ctx, req.GetTradeId(), req.GetCharId())
	if err != nil {
		h.logger.Error().Stack().Err(err).
			Str("trade_id", req.GetTradeId()).
			Uint32("char_id", req.GetCharId()).
			Msg("trade: CompleteTrade failed")

		return &zonev1.CompleteTradeResponse{
			Success: false,
			Error:   mapError(err),
		}, nil
	}

	h.logger.Debug().
		Str("trade_id", req.GetTradeId()).
		Uint32("char_id", req.GetCharId()).
		Msg("trade: CompleteTrade processed")

	return &zonev1.CompleteTradeResponse{
		Success: true,
	}, nil
}

// CancelTrade aborts the trade session.
func (h *grpcHandler) CancelTrade(
	ctx context.Context,
	req *zonev1.CancelTradeRequest,
) (*zonev1.CancelTradeResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetTradeId() == "" {
		return nil, status.Error(codes.InvalidArgument, "trade_id is required")
	}
	if req.GetCharId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "char_id must be > 0")
	}

	h.logger.Debug().
		Str("trade_id", req.GetTradeId()).
		Uint32("char_id", req.GetCharId()).
		Msg("trade: CancelTrade called")

	err := h.svc.CancelTrade(ctx, req.GetTradeId(), req.GetCharId())
	if err != nil {
		h.logger.Error().Stack().Err(err).
			Str("trade_id", req.GetTradeId()).
			Uint32("char_id", req.GetCharId()).
			Msg("trade: CancelTrade failed")

		return &zonev1.CancelTradeResponse{
			Success: false,
			Error:   mapError(err),
		}, nil
	}

	h.logger.Debug().
		Str("trade_id", req.GetTradeId()).
		Uint32("char_id", req.GetCharId()).
		Msg("trade: CancelTrade processed")

	return &zonev1.CancelTradeResponse{
		Success: true,
	}, nil
}
