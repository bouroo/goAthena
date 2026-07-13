// Package handler contains the transport-layer adapter for the zone
// feature (WS-C): the gRPC server that implements zonev1.ZoneService
// and is invoked by the gateway when a client enters the map server.
package handler

import (
	"context"
	"errors"
	"fmt"

	"github.com/rs/zerolog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	tradedomain "github.com/bouroo/goAthena/internal/features/trade/domain"
	vendingdomain "github.com/bouroo/goAthena/internal/features/vending/domain"
	"github.com/bouroo/goAthena/internal/features/zone/domain"
)

// grpcHandler implements zonev1.ZoneServiceServer. It is a thin adapter:
// proto <-> domain mapping, request validation, and error translation.
type grpcHandler struct {
	zonev1.UnimplementedZoneServiceServer
	svc        domain.ZoneService
	tradeSvc   tradedomain.TradeService
	vendingSvc vendingdomain.VendingService
	mapName    string
	spawnX     int
	spawnY     int
	logger     *zerolog.Logger
}

// NewGRPCHandler creates a gRPC handler for the ZoneService. The returned
// value is registered onto a *grpc.Server by the zone DI package via
// zonev1.RegisterZoneServiceServer.
func NewGRPCHandler(
	svc domain.ZoneService,
	tradeSvc tradedomain.TradeService,
	vendingSvc vendingdomain.VendingService,
	mapName string,
	spawnX, spawnY int,
	logger *zerolog.Logger,
) zonev1.ZoneServiceServer {
	return &grpcHandler{
		svc:        svc,
		tradeSvc:   tradeSvc,
		vendingSvc: vendingSvc,
		mapName:    mapName,
		spawnX:     spawnX,
		spawnY:     spawnY,
		logger:     logger,
	}
}

// EnterZone handles CZ_ENTER (0x0072 / WantToConnection) packets forwarded
// from the gateway as structured gRPC. It validates the request, registers
// the player in the zone's tick loop and AOI grid, and returns the spawn
// map + coordinates on success.
//
// Like identity.Authenticate, an AddEntity failure is a wire outcome
// carried inside EnterZoneResponse (Success=false, Error=...) rather than a
// gRPC status error: a rejected map entry is a normal flow outcome for the
// caller, not a server-side fault.
func (h *grpcHandler) EnterZone(
	ctx context.Context,
	req *zonev1.EnterZoneRequest,
) (*zonev1.EnterZoneResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetAccountId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "account_id must be > 0")
	}
	if req.GetCharId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "char_id must be > 0")
	}

	entity := domain.Entity{
		ID:   domain.EntityID(req.GetAccountId()), //nolint:gosec // account_id is validated > 0 and fits uint32 range
		Type: domain.EntityPlayer,
		X:    h.spawnX,
		Y:    h.spawnY,
	}

	if err := h.svc.AddEntity(ctx, &entity); err != nil {
		h.logger.Warn().
			Err(err).
			Uint32("account_id", req.GetAccountId()).
			Uint32("char_id", req.GetCharId()).
			Str("map", h.mapName).
			Msg("zone: EnterZone AddEntity failed")
		return &zonev1.EnterZoneResponse{
			Success: false,
			Error:   fmt.Sprintf("zone entry failed: %v", err),
		}, nil
	}

	h.logger.Info().
		Uint32("account_id", req.GetAccountId()).
		Uint32("char_id", req.GetCharId()).
		Str("map", h.mapName).
		Int("x", h.spawnX).
		Int("y", h.spawnY).
		Msg("zone: player entered")

	return &zonev1.EnterZoneResponse{
		Success: true,
		MapName: h.mapName,
		MapX:    uint32(h.spawnX), //nolint:gosec // spawn coords are map cell positions (0-512)
		MapY:    uint32(h.spawnY), //nolint:gosec // spawn coords are map cell positions (0-512)
	}, nil
}

// MoveEntity handles CZ_REQUEST_MOVE (0x0085 / WalkToXY) packets
// forwarded from the gateway as structured gRPC. It snapshots the
// entity's current position (the source), invokes MoveEntity to compute
// an A* path toward (destX, destY), and returns both endpoints so the
// gateway can encode ZC_NOTIFY_PLAYERMOVE 0x0087.
//
// Like EnterZone, transport failures (gRPC status errors from the
// service layer) bubble up unchanged; wire-level outcomes — invalid
// account, unknown entity, no walkable path — are carried inside
// MoveEntityResponse (Success=false, Error=...) so the gateway can log
// the reason without tearing the connection down.
func (h *grpcHandler) MoveEntity(
	ctx context.Context,
	req *zonev1.MoveEntityRequest,
) (*zonev1.MoveEntityResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetAccountId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "account_id must be > 0")
	}

	entityID := domain.EntityID(req.GetAccountId()) //nolint:gosec // validated > 0 above

	entity, err := h.svc.GetEntity(ctx, entityID)
	if err != nil {
		h.logger.Warn().
			Err(err).
			Uint32("account_id", req.GetAccountId()).
			Msg("zone: MoveEntity GetEntity failed")
		return &zonev1.MoveEntityResponse{
			Success: false,
			Error:   fmt.Sprintf("entity not found: %v", err),
		}, nil
	}

	srcX, srcY := entity.X, entity.Y

	destX := int(req.GetDestX()) //nolint:gosec // map cell position (0-512)
	destY := int(req.GetDestY()) //nolint:gosec // map cell position (0-512)

	if err := h.svc.MoveEntity(ctx, entityID, destX, destY); err != nil {
		h.logger.Warn().
			Err(err).
			Uint32("account_id", req.GetAccountId()).
			Int("src_x", srcX).
			Int("src_y", srcY).
			Int("dest_x", destX).
			Int("dest_y", destY).
			Msg("zone: MoveEntity path compute failed")
		return &zonev1.MoveEntityResponse{
			Success: false,
			SrcX:    uint32(srcX),  //nolint:gosec // map cell position
			SrcY:    uint32(srcY),  //nolint:gosec // map cell position
			DestX:   uint32(destX), //nolint:gosec // map cell position
			DestY:   uint32(destY), //nolint:gosec // map cell position
			Error:   err.Error(),
		}, nil
	}

	h.logger.Debug().
		Uint32("account_id", req.GetAccountId()).
		Int("src_x", srcX).
		Int("src_y", srcY).
		Int("dest_x", destX).
		Int("dest_y", destY).
		Msg("zone: move accepted")

	return &zonev1.MoveEntityResponse{
		Success: true,
		SrcX:    uint32(srcX),  //nolint:gosec // map cell position
		SrcY:    uint32(srcY),  //nolint:gosec // map cell position
		DestX:   uint32(destX), //nolint:gosec // map cell position
		DestY:   uint32(destY), //nolint:gosec // map cell position
	}, nil
}

// AttackEntity handles ZC_DAMAGE_PACKET (0x003E / CheckAttack) packets
// forwarded from the gateway as structured gRPC. It validates the request,
// invokes DamageEntity on the zone service, and returns the damage response
// with updated HP and death status.
func (h *grpcHandler) AttackEntity(
	ctx context.Context,
	req *zonev1.AttackEntityRequest,
) (*zonev1.AttackEntityResponse, error) {
	if req.GetAttackerId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "attacker_id must be > 0")
	}
	if req.GetEntityId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "entity_id is required")
	}

	entityID := domain.EntityID(req.GetEntityId())
	damage := int32(req.GetDamage()) //nolint:gosec // G115: damage is validated and safe to convert
	if damage <= 0 {
		damage = 1 // auto-attack default
	}
	attackerID := domain.EntityID(req.GetAttackerId())

	resp, err := h.svc.DamageEntity(ctx, entityID, damage, attackerID, req.GetSkillId(), req.GetSkillLevel())
	if err != nil {
		h.logger.Error().Err(err).Uint32("entity_id", req.GetEntityId()).Msg("zone: AttackEntity failed")
		return nil, status.Error(codes.Internal, "zone: attack failed")
	}

	if !resp.Success {
		return nil, status.Error(codes.NotFound, "zone: entity not found")
	}

	h.logger.Debug().
		Uint32("entity_id", req.GetEntityId()).
		Bool("died", resp.TargetDied).
		Int32("damage_applied", resp.DamageApplied).
		Msg("zone: attack processed")

	return &zonev1.AttackEntityResponse{
		Success:       resp.Success,
		TargetDied:    resp.TargetDied,
		DamageApplied: resp.DamageApplied,
		CurrentHp:     resp.CurrentHP,
		MaxHp:         resp.MaxHP,
	}, nil
}

// PickupItem handles ZC_ITEM_TAKE (0x0032 / TakeItem) packets forwarded
// from the gateway as structured gRPC. It validates the request, invokes
// PickupItem on the zone service, and returns the pickup response with
// item_id and amount.
func (h *grpcHandler) PickupItem(
	ctx context.Context,
	req *zonev1.PickupItemRequest,
) (*zonev1.PickupItemResponse, error) {
	if req.GetPlayerId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "player_id must be > 0")
	}
	if req.GetGroundItemId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "ground_item_id is required")
	}

	groundItemID := domain.EntityID(req.GetGroundItemId())
	playerID := domain.EntityID(req.GetPlayerId())

	resp, err := h.svc.PickupItem(ctx, groundItemID, playerID)
	if err != nil {
		h.logger.Error().Err(err).Uint32("ground_item_id", req.GetGroundItemId()).Msg("zone: PickupItem failed")
		return nil, status.Error(codes.Internal, "zone: pickup failed")
	}

	if !resp.Success {
		return nil, status.Error(codes.NotFound, "zone: ground item not found")
	}

	h.logger.Debug().
		Uint32("ground_item_id", req.GetGroundItemId()).
		Uint32("item_id", resp.ItemID).
		Msg("zone: pickup processed")

	return &zonev1.PickupItemResponse{
		Success: resp.Success,
		ItemId:  resp.ItemID,
		Amount:  uint32(resp.Amount), //nolint:gosec // G115: amount is validated and safe to convert
	}, nil
}

// mapTradeError converts domain errors to human-readable error messages.
func mapTradeError(err error) string {
	switch {
	case errors.Is(err, tradedomain.ErrTradeNotFound):
		return "trade not found"
	case errors.Is(err, tradedomain.ErrInvalidTradeState):
		return "invalid trade state"
	case errors.Is(err, tradedomain.ErrInsufficientInventory):
		return "insufficient inventory"
	case errors.Is(err, tradedomain.ErrLockBusy):
		return "trade lock busy"
	case errors.Is(err, tradedomain.ErrTradeTargetUnavailable):
		return "trade target unavailable"
	default:
		return fmt.Sprintf("trade error: %v", err)
	}
}

// RequestTrade handles trade initiation requests.
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

	trade, err := h.tradeSvc.RequestTrade(ctx, req.GetRequesterCharId(), req.GetTargetCharId())
	if err != nil {
		h.logger.Error().Stack().Err(err).
			Uint32("requester_char_id", req.GetRequesterCharId()).
			Uint32("target_char_id", req.GetTargetCharId()).
			Msg("zone: RequestTrade failed")

		return &zonev1.RequestTradeResponse{
			Success: false,
			Error:   mapTradeError(err),
		}, nil
	}

	h.logger.Debug().
		Str("trade_id", trade.ID).
		Uint32("requester_char_id", req.GetRequesterCharId()).
		Uint32("target_char_id", req.GetTargetCharId()).
		Msg("zone: trade requested")

	return &zonev1.RequestTradeResponse{
		Success: true,
		TradeId: trade.ID,
	}, nil
}

// AddTradeItem handles adding items to the trade window.
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

	err := h.tradeSvc.AddTradeItem(ctx, req.GetTradeId(), req.GetCharId(), req.GetInventoryId(), req.GetAmount())
	if err != nil {
		h.logger.Error().Stack().Err(err).
			Str("trade_id", req.GetTradeId()).
			Uint32("char_id", req.GetCharId()).
			Uint32("inventory_id", req.GetInventoryId()).
			Int32("amount", req.GetAmount()).
			Msg("zone: AddTradeItem failed")

		return &zonev1.AddTradeItemResponse{
			Success: false,
			Error:   mapTradeError(err),
		}, nil
	}

	h.logger.Debug().
		Str("trade_id", req.GetTradeId()).
		Uint32("char_id", req.GetCharId()).
		Uint32("inventory_id", req.GetInventoryId()).
		Int32("amount", req.GetAmount()).
		Msg("zone: trade item added")

	return &zonev1.AddTradeItemResponse{
		Success: true,
	}, nil
}

// AddTradeZeny handles adding zeny to the trade window.
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

	err := h.tradeSvc.AddTradeZeny(ctx, req.GetTradeId(), req.GetCharId(), req.GetZeny())
	if err != nil {
		h.logger.Error().Stack().Err(err).
			Str("trade_id", req.GetTradeId()).
			Uint32("char_id", req.GetCharId()).
			Uint32("zeny", req.GetZeny()).
			Msg("zone: AddTradeZeny failed")

		return &zonev1.AddTradeZenyResponse{
			Success: false,
			Error:   mapTradeError(err),
		}, nil
	}

	h.logger.Debug().
		Str("trade_id", req.GetTradeId()).
		Uint32("char_id", req.GetCharId()).
		Uint32("zeny", req.GetZeny()).
		Msg("zone: trade zeny added")

	return &zonev1.AddTradeZenyResponse{
		Success: true,
	}, nil
}

// ConfirmTrade handles trade confirmation requests.
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

	err := h.tradeSvc.ConfirmTrade(ctx, req.GetTradeId(), req.GetCharId())
	if err != nil {
		h.logger.Error().Stack().Err(err).
			Str("trade_id", req.GetTradeId()).
			Uint32("char_id", req.GetCharId()).
			Msg("zone: ConfirmTrade failed")

		return &zonev1.ConfirmTradeResponse{
			Success: false,
			Error:   mapTradeError(err),
		}, nil
	}

	h.logger.Debug().
		Str("trade_id", req.GetTradeId()).
		Uint32("char_id", req.GetCharId()).
		Msg("zone: trade confirmed")

	return &zonev1.ConfirmTradeResponse{
		Success: true,
	}, nil
}

// CompleteTrade handles trade completion requests.
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

	err := h.tradeSvc.CompleteTrade(ctx, req.GetTradeId(), req.GetCharId())
	if err != nil {
		h.logger.Error().Stack().Err(err).
			Str("trade_id", req.GetTradeId()).
			Uint32("char_id", req.GetCharId()).
			Msg("zone: CompleteTrade failed")

		return &zonev1.CompleteTradeResponse{
			Success: false,
			Error:   mapTradeError(err),
		}, nil
	}

	h.logger.Debug().
		Str("trade_id", req.GetTradeId()).
		Uint32("char_id", req.GetCharId()).
		Msg("zone: trade completed")

	return &zonev1.CompleteTradeResponse{
		Success: true,
	}, nil
}

// CancelTrade handles trade cancellation requests.
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

	err := h.tradeSvc.CancelTrade(ctx, req.GetTradeId(), req.GetCharId())
	if err != nil {
		h.logger.Error().Stack().Err(err).
			Str("trade_id", req.GetTradeId()).
			Uint32("char_id", req.GetCharId()).
			Msg("zone: CancelTrade failed")

		return &zonev1.CancelTradeResponse{
			Success: false,
			Error:   mapTradeError(err),
		}, nil
	}

	h.logger.Debug().
		Str("trade_id", req.GetTradeId()).
		Uint32("char_id", req.GetCharId()).
		Msg("zone: trade cancelled")

	return &zonev1.CancelTradeResponse{
		Success: true,
	}, nil
}

// mapVendingError converts domain errors to human-readable vending error messages.
func mapVendingError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, vendingdomain.ErrShopNotFound):
		return "shop not found"
	case errors.Is(err, vendingdomain.ErrShopAlreadyOpen):
		return "shop already open"
	case errors.Is(err, vendingdomain.ErrShopClosed):
		return "shop is closed"
	case errors.Is(err, vendingdomain.ErrInsufficientItems):
		return "insufficient items in shop"
	case errors.Is(err, vendingdomain.ErrInsufficientFunds):
		return "insufficient zeny"
	case errors.Is(err, vendingdomain.ErrInvalidItem):
		return "invalid item"
	case errors.Is(err, vendingdomain.ErrLockBusy):
		return "operation in progress"
	default:
		return fmt.Sprintf("vending error: %v", err)
	}
}

// toProtoVendingShop converts a domain vending shop to its proto representation.
func toProtoVendingShop(shop vendingdomain.VendingShop) *zonev1.VendingShopInfo {
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

	items := make([]vendingdomain.VendingItem, len(req.GetItems()))
	for i, p := range req.GetItems() {
		items[i] = vendingdomain.VendingItem{
			InventoryID: p.GetInventoryId(),
			ItemID:      p.GetItemId(),
			Amount:      p.GetAmount(),
			Price:       p.GetPrice(),
		}
	}

	shop := vendingdomain.VendingShop{
		OwnerID: req.GetOwnerCharId(),
		Title:   req.GetTitle(),
		MapName: req.GetMapName(),
		X:       req.GetX(),
		Y:       req.GetY(),
		Items:   items,
	}

	created, err := h.vendingSvc.OpenShop(ctx, shop)
	if err != nil {
		return &zonev1.OpenVendingShopResponse{
			Success:      false,
			ErrorMessage: mapVendingError(err),
		}, nil
	}

	return &zonev1.OpenVendingShopResponse{
		Success: true,
		Shop:    toProtoVendingShop(created),
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

	err := h.vendingSvc.CloseShop(ctx, req.GetOwnerCharId())
	if err != nil {
		return &zonev1.CloseVendingShopResponse{
			Success:      false,
			ErrorMessage: mapVendingError(err),
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

	buyerZeny, err := h.vendingSvc.BuyItem(ctx, req.GetBuyerCharId(), req.GetShopId(), req.GetInventoryId(), req.GetAmount())
	if err != nil {
		return &zonev1.BuyVendingItemResponse{
			Success:      false,
			ErrorMessage: mapVendingError(err),
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

	shops, err := h.vendingSvc.ListShopsOnMap(ctx, req.GetMapName())
	if err != nil {
		return &zonev1.ListVendingShopsResponse{
			Success:      false,
			ErrorMessage: mapVendingError(err),
		}, nil
	}

	protoShops := make([]*zonev1.VendingShopInfo, len(shops))
	for i, shop := range shops {
		protoShops[i] = toProtoVendingShop(shop)
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

	shop, err := h.vendingSvc.GetShop(ctx, req.GetOwnerCharId())
	if err != nil {
		return &zonev1.GetVendingShopResponse{
			Success:      false,
			ErrorMessage: mapVendingError(err),
		}, nil
	}

	return &zonev1.GetVendingShopResponse{
		Success: true,
		Shop:    toProtoVendingShop(shop),
	}, nil
}
