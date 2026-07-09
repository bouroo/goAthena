// Package handler contains transport-layer adapters for the identity
// feature (WS-B): the gRPC server that implements identityv1.IdentityService
// and is invoked by the gateway (gRPC) and identity echo endpoints.
package handler

import (
	"context"
	"errors"
	"net/netip"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	identityv1 "github.com/bouroo/goAthena/api/pb/identity/v1"
	economydomain "github.com/bouroo/goAthena/internal/features/economy/domain"
	"github.com/bouroo/goAthena/internal/features/identity/domain"
	"github.com/bouroo/goAthena/internal/features/identity/service"
	inventorydomain "github.com/bouroo/goAthena/internal/features/inventory/domain"
)

// errItemNotOwnedByChar is the soft-failure error string shared by the
// three inventory mutations. Lifted out of the handler bodies so
// goconst (and the next reader) sees it as a single concept.
const errItemNotOwnedByChar = "item not found or not owned by this character"

// grpcHandler implements identityv1.IdentityServiceServer. It is a thin
// adapter: proto <-> domain mapping, error code translation, and the
// protocol-level dichotomy between a wire failure (carried inside the
// AuthenticateResponse as AuthResult + ErrorCode) and an internal failure
// (surfaced as a gRPC status error).
type grpcHandler struct {
	identityv1.UnimplementedIdentityServiceServer
	svc  domain.IdentityService
	shop economydomain.ShopService
}

// NewGRPCHandler creates a gRPC handler for the IdentityService. shop is
// the NPC-shop economy use-case (BuyFromShop / SellToShop) wired in
// from the economy feature. The returned value is registered onto a
// *grpc.Server by the identity DI package via
// identityv1.RegisterIdentityServiceServer.
func NewGRPCHandler(svc domain.IdentityService, shop economydomain.ShopService) identityv1.IdentityServiceServer {
	return &grpcHandler{svc: svc, shop: shop}
}

// Authenticate handles CA_LOGIN* packets forwarded from the gateway as
// structured gRPC. The wire-level outcome lives inside the response
// (AuthResult + ErrorCode), not the gRPC status: AC_REFUSE_LOGIN is a
// normal flow outcome for wrong credentials, not a server error. Only
// genuinely unexpected failures (db / valkey outage, etc.) surface as
// gRPC Internal.
func (h *grpcHandler) Authenticate(
	ctx context.Context,
	req *identityv1.AuthenticateRequest,
) (*identityv1.AuthenticateResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	encoding := mapAuthMethod(req.GetMethod())

	ip, err := netip.ParseAddr(req.GetClientIp())
	if err != nil || !ip.IsValid() {
		ip = netip.Addr{}
	}

	resp, err := h.svc.Login(ctx, domain.LoginRequest{
		UserID:     req.GetUsername(),
		Password:   string(req.GetPassword()),
		ClientType: uint8(req.GetClientType()), //nolint:gosec // G115: client_type is uint8 on the wire; the wider proto type is just for forward-compat.
		Method:     encoding,
		RemoteIP:   ip,
	})
	if err != nil {
		return mapLoginError(err), nil
	}

	protoServers := make([]*identityv1.CharServerInfo, 0, len(resp.CharServers))
	for _, s := range resp.CharServers {
		protoServers = append(protoServers, &identityv1.CharServerInfo{
			Ip:         s.IP,
			Port:       uint32(s.Port),
			Name:       s.Name,
			ServerType: 0,
		})
	}

	return &identityv1.AuthenticateResponse{
		Result:      identityv1.AuthResult_AUTH_RESULT_OK,
		AccountId:   resp.Account.AccountID,
		LoginId1:    uint64(resp.Session.LoginID1),
		LoginId2:    uint64(resp.Session.LoginID2),
		LastIp:      resp.Account.LastIP,
		LastLogin:   resp.Account.LastLogin.Format("2006-01-02 15:04:05"),
		Sex:         string(resp.Account.Sex),
		Token:       resp.Account.WebAuthToken,
		CharServers: protoServers,
	}, nil
}

// GetCharacterList handles HC_ACCEPT_ENTER requests. Unlike Authenticate,
// the absence of characters or a repository failure surfaces as a gRPC
// error: an empty roster is encoded as a non-nil empty slice so callers
// can distinguish "no characters" from "server failure" by status code.
func (h *grpcHandler) GetCharacterList(
	ctx context.Context,
	req *identityv1.GetCharacterListRequest,
) (*identityv1.GetCharacterListResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	chars, err := h.svc.ListCharacters(ctx, req.GetAccountId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list characters: %v", err)
	}

	protoChars := make([]*identityv1.CharacterInfo, 0, len(chars))
	for _, c := range chars {
		protoChars = append(protoChars, &identityv1.CharacterInfo{
			CharId:    c.CharID,
			Slot:      uint32(c.Slot),
			Name:      c.Name,
			ClassId:   uint32(c.Class),
			BaseLevel: c.BaseLevel,
			JobLevel:  c.JobLevel,
		})
	}

	return &identityv1.GetCharacterListResponse{
		Characters: protoChars,
	}, nil
}

// GetCharacter handles the per-character detail fetch used by the
// gateway to populate the entity spawn packet. Unlike the other
// IdentityService methods, a "not found" outcome is encoded inside the
// response as success=false (with a short error string) rather than a
// gRPC status: the gateway treats a missing character as a soft
// failure and falls back to a zero-filled spawn packet so the map
// enter handshake still completes.
//
// GORM / Valkey outages still surface as gRPC Internal so the gateway
// can distinguish "data is missing" from "backend is down" by status
// code.
func (h *grpcHandler) GetCharacter(
	ctx context.Context,
	req *identityv1.GetCharacterRequest,
) (*identityv1.GetCharacterResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetAccountId() == 0 || req.GetCharId() == 0 {
		// Reject zero keys with InvalidArgument so a buggy caller
		// cannot trigger a doomed round-trip; the gateway's success
		// fallback would otherwise silently swallow the malformed
		// request.
		return nil, status.Error(codes.InvalidArgument, "account_id and char_id must be non-zero")
	}

	char, err := h.svc.GetCharacter(ctx, req.GetAccountId(), req.GetCharId())
	if err != nil {
		if errors.Is(err, domain.ErrCharacterNotFound) {
			return &identityv1.GetCharacterResponse{
				Success: false,
				Error:   "character not found",
			}, nil
		}
		return nil, status.Errorf(codes.Internal, "get character: %v", err)
	}

	return &identityv1.GetCharacterResponse{
		Success: true,
		Character: &identityv1.CharacterDetail{
			CharId:       char.CharID,
			Name:         char.Name,
			ClassId:      uint32(char.Class),
			BaseLevel:    char.BaseLevel,
			JobLevel:     char.JobLevel,
			Hp:           char.HP,
			MaxHp:        char.MaxHP,
			Sp:           char.SP,
			MaxSp:        char.MaxSP,
			Hair:         uint32(char.Hair),
			HairColor:    uint32(char.HairColor),
			ClothesColor: uint32(char.ClothesColor),
			Weapon:       uint32(char.Weapon),
			Shield:       uint32(char.Shield),
			HeadTop:      uint32(char.HeadTop),
			HeadMid:      uint32(char.HeadMid),
			HeadBottom:   uint32(char.HeadBottom),
			Robe:         uint32(char.Robe),
			Sex:          sexToProtoByte(char.Sex),
			Str:          uint32(char.Str),
			Agi:          uint32(char.Agi),
			Vit:          uint32(char.Vit),
			Int:          uint32(char.Int),
			Dex:          uint32(char.Dex),
			Luk:          uint32(char.Luk),
			StatusPoint:  char.StatusPoint,
			SkillPoint:   char.SkillPoint,
		},
	}, nil
}

// GetInventory returns every item owned by the requested character.
// An empty inventory is a normal outcome and is returned as an empty
// (non-nil) repeated field; repository failures surface as gRPC
// Internal so the gateway can distinguish "empty backpack" from
// "backend is down".
func (h *grpcHandler) GetInventory(
	ctx context.Context,
	req *identityv1.GetInventoryRequest,
) (*identityv1.GetInventoryResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetAccountId() == 0 || req.GetCharId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "account_id and char_id must be non-zero")
	}

	items, err := h.svc.GetInventory(ctx, req.GetAccountId(), req.GetCharId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "get inventory: %v", err)
	}

	protoItems := make([]*identityv1.InventoryItem, 0, len(items))
	for i := range items {
		protoItems = append(protoItems, inventoryItemToProto(&items[i]))
	}
	return &identityv1.GetInventoryResponse{Items: protoItems}, nil
}

// EquipItem handles the CZ_REQ_EQUIP flow. Mirrors GetCharacter's
// success=false encoding for the "item does not belong to this
// character" sentinel — the gateway treats that as a soft failure and
// skips the equip rather than disconnecting.
func (h *grpcHandler) EquipItem(
	ctx context.Context,
	req *identityv1.EquipItemRequest,
) (*identityv1.EquipItemResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetAccountId() == 0 || req.GetCharId() == 0 || req.GetItemId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "account_id, char_id and item_id must be non-zero")
	}

	err := h.svc.EquipItem(ctx, req.GetAccountId(), req.GetCharId(), req.GetItemId(), req.GetEquipPosition())
	if err != nil {
		if errors.Is(err, inventorydomain.ErrItemNotFound) {
			return &identityv1.EquipItemResponse{
				Success: false,
				ItemId:  req.GetItemId(),
				Error:   errItemNotOwnedByChar,
			}, nil
		}
		return nil, status.Errorf(codes.Internal, "equip item: %v", err)
	}
	return &identityv1.EquipItemResponse{
		Success:       true,
		ItemId:        req.GetItemId(),
		EquipPosition: req.GetEquipPosition(),
	}, nil
}

// UnequipItem clears the EQP_* bitmask on the requested item. Same
// not-found mapping as EquipItem.
func (h *grpcHandler) UnequipItem(
	ctx context.Context,
	req *identityv1.UnequipItemRequest,
) (*identityv1.UnequipItemResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetAccountId() == 0 || req.GetCharId() == 0 || req.GetItemId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "account_id, char_id and item_id must be non-zero")
	}

	priorEquip, err := h.svc.UnequipItem(ctx, req.GetAccountId(), req.GetCharId(), req.GetItemId())
	if err != nil {
		if errors.Is(err, inventorydomain.ErrItemNotFound) {
			return &identityv1.UnequipItemResponse{
				Success: false,
				ItemId:  req.GetItemId(),
				Error:   errItemNotOwnedByChar,
			}, nil
		}
		return nil, status.Errorf(codes.Internal, "unequip item: %v", err)
	}
	return &identityv1.UnequipItemResponse{
		Success:       true,
		ItemId:        req.GetItemId(),
		EquipPosition: priorEquip,
	}, nil
}

// UseItem decrements the stack count and returns the new amount.
// On the sentinel not-found we return success=false with a short
// error; on any other failure (db outage, etc.) we surface gRPC Internal.
func (h *grpcHandler) UseItem(
	ctx context.Context,
	req *identityv1.UseItemRequest,
) (*identityv1.UseItemResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetAccountId() == 0 || req.GetCharId() == 0 || req.GetItemId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "account_id, char_id and item_id must be non-zero")
	}

	remaining, err := h.svc.UseItem(ctx, req.GetAccountId(), req.GetCharId(), req.GetItemId())
	if err != nil {
		if errors.Is(err, inventorydomain.ErrItemNotFound) {
			return &identityv1.UseItemResponse{
				Success: false,
				ItemId:  req.GetItemId(),
				Error:   errItemNotOwnedByChar,
			}, nil
		}
		return nil, status.Errorf(codes.Internal, "use item: %v", err)
	}
	return &identityv1.UseItemResponse{
		Success:         true,
		ItemId:          req.GetItemId(),
		RemainingAmount: remaining,
	}, nil
}

// BuyFromShop commits one or more shop buy orders for a character.
// Like the other inventory mutations, expected business outcomes
// (insufficient zeny, lock busy) are returned inside the response as a
// BuyResult code; only a backend failure (db / lock outage) surfaces
// as gRPC Internal. The server validates each order's UnitPrice against
// the economy catalog (D-204) — the client-supplied price is not
// trusted end-to-end.
func (h *grpcHandler) BuyFromShop(
	ctx context.Context,
	req *identityv1.BuyFromShopRequest,
) (*identityv1.BuyFromShopResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetAccountId() == 0 || req.GetCharId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "account_id and char_id must be non-zero")
	}

	orders := make([]economydomain.ShopOrder, 0, len(req.GetOrders()))
	for _, o := range req.GetOrders() {
		orders = append(orders, economydomain.ShopOrder{
			ItemID:    o.GetItemId(),
			Amount:    o.GetAmount(),
			UnitPrice: o.GetUnitPrice(),
		})
	}

	newZeny, result, err := h.shop.BuyFromShop(ctx, req.GetCharId(), orders)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "buy from shop: %v", err)
	}

	resp := &identityv1.BuyFromShopResponse{
		Result: mapBuyResult(result),
	}
	if result == economydomain.BuyOK {
		resp.NewZeny = newZeny
	}
	return resp, nil
}

// SellToShop credits a character for one or more inventory items sold
// to an NPC. Same wire-vs-internal dichotomy as BuyFromShop.
func (h *grpcHandler) SellToShop(
	ctx context.Context,
	req *identityv1.SellToShopRequest,
) (*identityv1.SellToShopResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	if req.GetAccountId() == 0 || req.GetCharId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "account_id and char_id must be non-zero")
	}

	sales := make([]economydomain.SellLine, 0, len(req.GetSales()))
	for _, s := range req.GetSales() {
		sales = append(sales, economydomain.SellLine{
			InvID:     s.GetInvId(),
			Amount:    s.GetAmount(),
			UnitPrice: s.GetUnitPrice(),
		})
	}

	newZeny, result, err := h.shop.SellToShop(ctx, req.GetCharId(), sales)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "sell to shop: %v", err)
	}

	resp := &identityv1.SellToShopResponse{
		Result: mapSellResult(result),
	}
	if result == economydomain.SellOK {
		resp.NewZeny = newZeny
	}
	return resp, nil
}

// inventoryItemToProto projects a domain InventoryItem onto the
// wire-relevant subset declared in identity.proto. Cards, options,
// expire_time, favorite, bound, unique_id, equip_switch and
// enchantgrade are intentionally omitted — they belong to a future
// proto revision, not this one.
//
// Identify is signed smallint on the schema; the rAthena convention
// only uses 0/1, so we clamp negatives to 0 before widening to
// uint32. Clamping explicitly keeps gosec G115 happy without losing
// the (impossible-but-recorded) -1 sentinel that rAthena historically
// used for "identify failed".
func inventoryItemToProto(item *inventorydomain.InventoryItem) *identityv1.InventoryItem {
	if item == nil {
		return nil
	}
	identify := max(0, item.Identify)
	return &identityv1.InventoryItem{
		Id:        item.ID,
		Nameid:    item.NameID,
		Amount:    item.Amount,
		Equip:     uint32(item.Equip),
		Identify:  uint32(identify), //nolint:gosec // G115: clamped to >=0 above; rAthena only uses 0/1.
		Refine:    uint32(item.Refine),
		Attribute: uint32(item.Attribute),
	}
}

// sexToProtoByte maps the domain Sex string onto the uint32 the proto
// expects. The proto field is uint32 for forward-compat with future
// 4-state sex enums; today we use the kRO 0=F/1=M/2=S convention.
func sexToProtoByte(s domain.Sex) uint32 {
	switch s {
	case domain.SexFemale:
		return 0
	case domain.SexMale:
		return 1
	case domain.SexServer:
		return 2
	}
	return 0
}

// mapAuthMethod converts the proto AuthMethod enum into the domain
// PasswordEncoding used by the service. SSOs, pc-bang and channel
// variants all carry the same plaintext / MD5 credential as their base
// CA_LOGIN counterpart, so they fold onto the same encodings. Anything
// unrecognized or unspecified is treated as plaintext to match
// rAthena's behavior (loginclif.cpp:279 — fall through to strcmp).
//
//nolint:exhaustive // PCBANG/CHANNEL/SSO/UNSPECIFIED carry the same plaintext credential as PASSWORD; folding them is intentional.
func mapAuthMethod(method identityv1.AuthMethod) domain.PasswordEncoding {
	switch method {
	case identityv1.AuthMethod_AUTH_METHOD_MD5,
		identityv1.AuthMethod_AUTH_METHOD_MD5_SALTED:
		return domain.PassEncodingMD5
	case identityv1.AuthMethod_AUTH_METHOD_PASSWORD,
		identityv1.AuthMethod_AUTH_METHOD_PCBANG,
		identityv1.AuthMethod_AUTH_METHOD_CHANNEL,
		identityv1.AuthMethod_AUTH_METHOD_SSO,
		identityv1.AuthMethod_AUTH_METHOD_UNSPECIFIED:
		return domain.PassEncodingPlain
	}
	return domain.PassEncodingPlain
}

// mapAuthResult translates the domain AuthError into the proto
// AuthResult. Banned and already-online are their own result classes;
// every other rejected outcome collapses onto AUTH_RESULT_REJECTED —
// the client only needs to know "no" vs. "yes" vs. "banned" vs.
// "already logged in" to pick the right disconnect.
//
// Note: domain.AuthOK and domain.AuthUnknownID both alias wire code 0,
// but the service never returns AuthOK inside a LoginError — the success
// path bypasses this mapper entirely — so AuthOK is not enumerated here.
//
//nolint:exhaustive // AuthOK never flows through this mapper; the rest collapse onto REJECTED on purpose.
func mapAuthResult(code domain.AuthError) identityv1.AuthResult {
	switch code {
	case domain.AuthBanned:
		return identityv1.AuthResult_AUTH_RESULT_BANNED
	case domain.AuthAlreadyOnline:
		return identityv1.AuthResult_AUTH_RESULT_ALREADY_LOGGED_IN
	case domain.AuthInvalidPassword,
		domain.AuthExpired,
		domain.AuthRejected,
		domain.AuthGMBlocked,
		domain.AuthHashMismatch,
		domain.AuthServerJammed,
		domain.AuthUnknownID:
		return identityv1.AuthResult_AUTH_RESULT_REJECTED
	}
	return identityv1.AuthResult_AUTH_RESULT_REJECTED
}

// mapLoginError builds an AuthenticateResponse carrying the wire-level
// failure. If err is (or wraps) a *service.LoginError the AuthError code
// is propagated both as AuthResult and as the numeric ErrorCode; for any
// other error (db / valkey outage, programming mistake) the response
// collapses to a generic SERVER_CLOSED with sentinel code 99 so the
// client never sees an internal error code.
func mapLoginError(err error) *identityv1.AuthenticateResponse {
	var loginErr *service.LoginError
	if errors.As(err, &loginErr) {
		return &identityv1.AuthenticateResponse{
			Result:    mapAuthResult(loginErr.Code),
			ErrorCode: uint32(loginErr.Code),
		}
	}
	return &identityv1.AuthenticateResponse{
		Result:    identityv1.AuthResult_AUTH_RESULT_SERVER_CLOSED,
		ErrorCode: 99,
	}
}

// mapBuyResult projects the economy domain.BuyResult onto the proto
// BuyResult. BuyOK → OK; the two failure outcomes map onto their named
// proto enums. A zero BuyResult (the iota zero value) collapses to
// UNSPECIFIED so a programmer who forgets to set the result never
// reads back as success.
func mapBuyResult(r economydomain.BuyResult) identityv1.BuyResult {
	switch r {
	case economydomain.BuyOK:
		return identityv1.BuyResult_BUY_RESULT_OK
	case economydomain.BuyFailInsufficientZeny:
		return identityv1.BuyResult_BUY_RESULT_INSUFFICIENT_ZENY
	case economydomain.BuyFailLockBusy:
		return identityv1.BuyResult_BUY_RESULT_LOCK_BUSY
	}
	return identityv1.BuyResult_BUY_RESULT_UNSPECIFIED
}

// mapSellResult projects the economy domain.SellResult onto the proto
// SellResult. SellOK → OK; the three failure outcomes map onto their
// named proto enums. A zero SellResult collapses to UNSPECIFIED.
func mapSellResult(r economydomain.SellResult) identityv1.SellResult {
	switch r {
	case economydomain.SellOK:
		return identityv1.SellResult_SELL_RESULT_OK
	case economydomain.SellFailZenyFull:
		return identityv1.SellResult_SELL_RESULT_ZENY_FULL
	case economydomain.SellFailInvalidItem:
		return identityv1.SellResult_SELL_RESULT_INVALID_ITEM
	case economydomain.SellFailLockBusy:
		return identityv1.SellResult_SELL_RESULT_LOCK_BUSY
	}
	return identityv1.SellResult_SELL_RESULT_UNSPECIFIED
}
