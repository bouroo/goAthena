package handler

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	"github.com/bouroo/goAthena/internal/features/vending/domain"
)

// grpcHandler implements zonev1.ZoneServiceServer for vending RPCs.
// It is a thin adapter: proto ↔ domain mapping, request validation,
// and error translation. The gateway dispatches kRO vending packets
// to these zone RPCs, and this handler calls the vending service.
type grpcHandler struct {
	zonev1.UnimplementedZoneServiceServer
	svc    domain.VendingService
	logger *zerolog.Logger
}

// NewVendingHandler creates a new vending gRPC handler.
func NewVendingHandler(svc domain.VendingService, logger *zerolog.Logger) zonev1.ZoneServiceServer {
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
	case errors.Is(err, domain.ErrShopNotFound):
		return "shop not found"
	case errors.Is(err, domain.ErrShopAlreadyOpen):
		return "shop already open"
	case errors.Is(err, domain.ErrShopClosed):
		return "shop is closed"
	case errors.Is(err, domain.ErrInsufficientItems):
		return "insufficient items in shop"
	case errors.Is(err, domain.ErrInsufficientFunds):
		return "insufficient zeny"
	case errors.Is(err, domain.ErrInvalidItem):
		return "invalid item"
	case errors.Is(err, domain.ErrLockBusy):
		return "operation in progress"
	default:
		return fmt.Sprintf("vending error: %v", err)
	}
}

// toProtoShop converts a domain shop to its proto representation.
func toProtoShop(shop domain.VendingShop) *zonev1.VendingShopInfo {
	items := make([]*zonev1.VendingItemInfo, len(shop.Items))
	for i, item := range shop.Items {
		items[i] = &zonev1.VendingItemInfo{
			InventoryId: item.InventoryID,
			ItemId:      item.ItemID,
			Amount:      item.Amount,
			Price:       item.Price,
		}
	}
	return &zonev1.VendingShopInfo{
		ShopId:      shop.ID,
		OwnerCharId: shop.OwnerID,
		Title:       shop.Title,
		MapName:     shop.MapName,
		X:           shop.X,
		Y:           shop.Y,
		Items:       items,
	}
}

// toDomainItems converts proto vending items to domain items.
func toDomainItems(protoItems []*zonev1.VendingItemInfo) []domain.VendingItem {
	items := make([]domain.VendingItem, len(protoItems))
	for i, p := range protoItems {
		items[i] = domain.VendingItem{
			InventoryID: p.GetInventoryId(),
			ItemID:      p.GetItemId(),
			Amount:      p.GetAmount(),
			Price:       p.GetPrice(),
		}
	}
	return items
}

// OpenVendingShop opens a player vending shop.
func (h *grpcHandler) OpenVendingShop(
	ctx context.Context,
	req *zonev1.OpenVendingShopRequest,
) (*zonev1.OpenVendingShopResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetOwnerCharId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "owner_char_id must be > 0")
	}
	if req.GetTitle() == "" {
		return nil, status.Error(codes.InvalidArgument, "title is required")
	}

	h.logger.Debug().
		Uint32("owner_char_id", req.GetOwnerCharId()).
		Str("title", req.GetTitle()).
		Msg("vending: OpenVendingShop called")

	shop := domain.VendingShop{
		OwnerID: req.GetOwnerCharId(),
		Title:   req.GetTitle(),
		MapName: req.GetMapName(),
		X:       req.GetX(),
		Y:       req.GetY(),
		Items:   toDomainItems(req.GetItems()),
	}

	created, err := h.svc.OpenShop(ctx, shop)
	if err != nil {
		h.logger.Warn().Err(err).
			Uint32("owner_char_id", req.GetOwnerCharId()).
			Msg("vending: OpenShop failed")
		return &zonev1.OpenVendingShopResponse{
			Success:      false,
			ErrorMessage: mapError(err),
		}, nil
	}

	return &zonev1.OpenVendingShopResponse{
		Success: true,
		Shop:    toProtoShop(created),
	}, nil
}

// CloseVendingShop closes the owner's vending shop.
func (h *grpcHandler) CloseVendingShop(
	ctx context.Context,
	req *zonev1.CloseVendingShopRequest,
) (*zonev1.CloseVendingShopResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetOwnerCharId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "owner_char_id must be > 0")
	}

	err := h.svc.CloseShop(ctx, req.GetOwnerCharId())
	if err != nil {
		return &zonev1.CloseVendingShopResponse{
			Success:      false,
			ErrorMessage: mapError(err),
		}, nil
	}

	return &zonev1.CloseVendingShopResponse{Success: true}, nil
}

// BuyVendingItem processes a purchase from a vending shop.
func (h *grpcHandler) BuyVendingItem(
	ctx context.Context,
	req *zonev1.BuyVendingItemRequest,
) (*zonev1.BuyVendingItemResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetBuyerCharId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "buyer_char_id must be > 0")
	}
	if req.GetShopId() == "" {
		return nil, status.Error(codes.InvalidArgument, "shop_id is required")
	}
	if req.GetAmount() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "amount must be positive")
	}

	buyerZeny, err := h.svc.BuyItem(ctx, req.GetBuyerCharId(), req.GetShopId(), req.GetInventoryId(), req.GetAmount())
	if err != nil {
		h.logger.Warn().Err(err).
			Uint32("buyer_char_id", req.GetBuyerCharId()).
			Str("shop_id", req.GetShopId()).
			Msg("vending: BuyItem failed")
		return &zonev1.BuyVendingItemResponse{
			Success:      false,
			ErrorMessage: mapError(err),
		}, nil
	}

	return &zonev1.BuyVendingItemResponse{
		Success:   true,
		BuyerZeny: buyerZeny,
	}, nil
}

// ListVendingShops returns all open vending shops on a map.
func (h *grpcHandler) ListVendingShops(
	ctx context.Context,
	req *zonev1.ListVendingShopsRequest,
) (*zonev1.ListVendingShopsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetMapName() == "" {
		return nil, status.Error(codes.InvalidArgument, "map_name is required")
	}

	shops, err := h.svc.ListShopsOnMap(ctx, req.GetMapName())
	if err != nil {
		return &zonev1.ListVendingShopsResponse{
			Success:      false,
			ErrorMessage: mapError(err),
		}, nil
	}

	protoShops := make([]*zonev1.VendingShopInfo, len(shops))
	for i, shop := range shops {
		protoShops[i] = toProtoShop(shop)
	}

	return &zonev1.ListVendingShopsResponse{
		Success: true,
		Shops:   protoShops,
	}, nil
}

// GetVendingShop returns the shop owned by a character.
func (h *grpcHandler) GetVendingShop(
	ctx context.Context,
	req *zonev1.GetVendingShopRequest,
) (*zonev1.GetVendingShopResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetOwnerCharId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "owner_char_id must be > 0")
	}

	shop, err := h.svc.GetShop(ctx, req.GetOwnerCharId())
	if err != nil {
		return &zonev1.GetVendingShopResponse{
			Success:      false,
			ErrorMessage: mapError(err),
		}, nil
	}

	return &zonev1.GetVendingShopResponse{
		Success: true,
		Shop:    toProtoShop(shop),
	}, nil
}
