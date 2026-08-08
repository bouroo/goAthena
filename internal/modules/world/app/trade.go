package app

import (
	"context"
	"errors"
	"fmt"

	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/combat"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// tradeDistance is the maximum Chebyshev distance at which two players may trade
// (rathena/src/map/trade.cpp:24 `#define TRADE_DISTANCE 2`). Both the request
// and the ack re-check it, since the original character could have warped between
// the two (trade.cpp:81 and :145).
const tradeDistance = 2

// TradeService owns the CZ_TRADE handshake + staging (slices S1+S2): the
// request/ack/cancel state machine between two online players (S1) plus add-item
// staging and Ok/lock (S2), with NO item or zeny MOVEMENT. A request sets both
// players' trade-partner state and opens the TARGET's dialog (ZC_REQ); an ack
// ACCEPT binds a shared TradeDeal and marks both windows open (ZC_ACK to both);
// add-item stages a REFERENCE (index+amount) in the deal and emits ZC_ADD to the
// partner + ZC_ACK_ADD to the adder; Ok locks a side (ZC_CONCLUDE to both); a
// cancel from either side sends ZC_CANCEL to both and clears the deal. Because S2
// stages only references (never removing items from either bag, never moving
// zeny), it carries zero item-duplication surface — the atomic all-or-nothing
// commit (S3, CZ_TRADE_COMMIT) is deferred.
type TradeService struct {
	players *domain.PlayerRegistry
	items   invdomain.InventoryRepository
	itemDB  *itemdb.Registry
}

// NewTradeService binds the trade handshake+staging to the live player registry,
// the inventory repository (to resolve staged item attributes for ZC_ADD), and
// the item DB (for the item type / equip location / look).
func NewTradeService(players *domain.PlayerRegistry, items invdomain.InventoryRepository, itemDB *itemdb.Registry) *TradeService {
	return &TradeService{players: players, items: items, itemDB: itemDB}
}

// RequestTrade resolves a CZ_TRADE_REQUEST from requesterAID against req. The
// target is resolved by CharID (the GID field the client received in the target's
// ZC_SPAWN_UNIT). A target that is offline, is the requester, is already in a
// trade, or stands beyond tradeDistance on a different map rejects with a
// ZC_ACK reason to the requester. On success both players' trade-partner state is
// set and the target's dialog opens via ZC_REQ_EXCHANGE_ITEM. Only an infra fault
// (an encode failure) is returned so ProcessBytes logs it.
func (s *TradeService) RequestTrade(ctx context.Context, requesterAID uint32, req packet.CZTradeRequest) error {
	requester, ok := s.players.ByAccount(requesterAID)
	if !ok {
		return nil
	}
	target, ok := s.players.ByCharID(req.TargetGID)
	if !ok || target.AccountID == requesterAID {
		return s.tradeAckReply(requester, packet.TradeAckCharNotExist)
	}
	// A player already holding a pending/open trade rejects a new request. rAthena
	// cancels the previous trade first; S1 keeps it simple (reject) since the
	// cancel path remains reachable via CZ_TRADE_CANCEL.
	if pid, _ := requester.TradePartner(); pid != 0 {
		return s.tradeAckReply(requester, packet.TradeAckFailed)
	}
	if pid, _ := target.TradePartner(); pid != 0 || target.IsTrading() {
		// trade.cpp:68-69: a target already holding a trade is FAILED ("person is
		// in another trade"). rAthena's BUSY (5) is an unrelated mail_writing path
		// (clif.cpp:12529), not this gate.
		return s.tradeAckReply(requester, packet.TradeAckFailed)
	}
	if !s.inRange(requester, target) {
		return s.tradeAckReply(requester, packet.TradeAckTooFar)
	}

	// Mirror trade.cpp:86-90: each player records the OTHER's account id + level.
	requester.SetTradePartner(target.AccountID, target.CLevel)
	target.SetTradePartner(requester.AccountID, requester.CLevel)

	// ZC_REQ goes to the TARGET only; its fields carry the REQUESTER's identity
	// (clif_traderequest, clif.cpp:4714). The packet's TargetID/TargetLv name the
	// "other guy" from the client's perspective — i.e. the requester.
	reqPkt := packet.TradeRequestResponse{
		RequesterName: requester.Name,
		TargetID:      requester.AccountID,
		TargetLv:      requester.CLevel,
	}
	if err := reqPkt.Encode(connWriter{target.Conn}); err != nil {
		return fmt.Errorf("trade: encode ZC_REQ_EXCHANGE_ITEM account %d: %w", target.AccountID, err)
	}
	return nil
}

// AckTrade resolves a CZ_TRADE_ACK from ackerAID against req. Type==3 (accept)
// opens both trade windows (ZC_ACK ACCEPT to both); type==4 (cancel) tears the
// pending trade down (ZC_ACK CANCEL to both). A stale ack (no partner, partner
// offline, warped apart) rejects with a reason and clears the state. Any other
// type is a broken packet and is ignored (trade.cpp:139).
func (s *TradeService) AckTrade(ctx context.Context, ackerAID uint32, req packet.CZTradeAck) error {
	acker, ok := s.players.ByAccount(ackerAID)
	if !ok {
		return nil
	}
	partnerID, _ := acker.TradePartner()
	if acker.IsTrading() || partnerID == 0 {
		return nil // late ack after open/close — nothing to resume.
	}
	partner, ok := s.players.ByAccount(partnerID)
	if !ok {
		// Partner went offline mid-handshake: clear and tell the acker.
		acker.ClearTrade()
		return s.tradeAckReply(acker, packet.TradeAckCharNotExist)
	}

	switch req.Type {
	case packet.CZTradeAckAccept:
		if !s.inRange(acker, partner) {
			// Warped apart since the request — reply first (so the ZC_ACK still
			// carries the partner's id/level, matching clif_traderesponse reading
			// sd.trade_partner before trade.cpp:147 clears it), then clear both.
			if err := s.tradeAckReply(acker, packet.TradeAckTooFar); err != nil {
				return err
			}
			acker.ClearTrade()
			partner.ClearTrade()
			return nil
		}
		// Initiate trade (trade.cpp:164-170): bind a SHARED staging deal, open both
		// windows, ZC_ACK ACCEPT to both. The deal is shared so S2 staging is
		// consistent; slot 0 = the acker (who received the request), slot 1 = the
		// original requester (the partner).
		deal := domain.NewTradeDeal(acker.AccountID, partner.AccountID)
		acker.SetTrading(true, deal)
		partner.SetTrading(true, deal)
		if err := s.tradeAckReply(acker, packet.TradeAckAccept); err != nil {
			return err
		}
		return s.tradeAckReply(partner, packet.TradeAckAccept)
	case packet.CZTradeAckCancel:
		// trade.cpp:124-133: ZC_ACK CANCEL to both, clear state.
		if err := s.tradeAckReply(acker, packet.TradeAckCancel); err != nil {
			return err
		}
		if err := s.tradeAckReply(partner, packet.TradeAckCancel); err != nil {
			return err
		}
		acker.ClearTrade()
		partner.ClearTrade()
		return nil
	default:
		return nil // broken packet — trade.cpp:139 ignores anything but 3/4.
	}
}

// CancelTrade resolves a CZ_TRADE_CANCEL from cancelerAID: it tears down any
// pending/open trade by sending ZC_CANCEL_EXCHANGE_ITEM to both parties and
// clearing their state (rathena clif_tradecancelled + trade_tradecancel). A cancel
// with no partner is a silent no-op.
func (s *TradeService) CancelTrade(ctx context.Context, cancelerAID uint32) error {
	canceler, ok := s.players.ByAccount(cancelerAID)
	if !ok {
		return nil
	}
	partnerID, _ := canceler.TradePartner()
	cancel := packet.CancelExchangeResponse{}
	if err := cancel.Encode(connWriter{canceler.Conn}); err != nil {
		return fmt.Errorf("trade: encode ZC_CANCEL_EXCHANGE_ITEM account %d: %w", canceler.AccountID, err)
	}
	canceler.ClearTrade()
	if partnerID != 0 {
		if partner, ok := s.players.ByAccount(partnerID); ok {
			if err := cancel.Encode(connWriter{partner.Conn}); err != nil {
				return fmt.Errorf("trade: encode ZC_CANCEL_EXCHANGE_ITEM account %d: %w", partner.AccountID, err)
			}
			partner.ClearTrade()
		}
	}
	return nil
}

// AddItem resolves a CZ_ADD_EXCHANGE_ITEM from adderAID against req (S2 staging).
// Index==0 stages zeny (Amount the zeny count); a non-zero Index stages the bag
// item at that slot. It stages a REFERENCE (index+amount) in the shared deal and
// emits ZC_ADD to the PARTNER (the full item detail for an item, a zeroed item-id
// frame for zeny) + ZC_ACK_ADD success to the adder. It moves NO inventory and NO
// zeny — staging is references-only, so zero dupe surface; the actual move is S3.
// A stage after the partner pressed Ok re-locks the partner (rAthena re-lock). A
// reject (no open trade, bad index, amount<=0, over-stack, equipped item, list
// full) emits ZC_ACK_ADD with the reason to the adder and stages nothing.
func (s *TradeService) AddItem(ctx context.Context, adderAID uint32, req packet.CZAddExchangeItem) error {
	adder, ok := s.players.ByAccount(adderAID)
	if !ok {
		return nil
	}
	deal := adder.TradeDeal()
	if deal == nil || !adder.IsTrading() {
		// No open window — rAthena drops silently; emit a CANCELED ack so the
		// client resets its add cursor.
		return s.ackAddReply(adder, req.Index, packet.TradeItemAddCanceled)
	}
	partnerID, _ := adder.TradePartner()
	partner, ok := s.players.ByAccount(partnerID)
	if !ok {
		return s.ackAddReply(adder, req.Index, packet.TradeItemAddCanceled)
	}

	if req.Amount <= 0 {
		return s.ackAddReply(adder, req.Index, packet.TradeItemAddStackExceed)
	}

	if req.Index == 0 {
		// Zeny staging. Index==0 is the rAthena zeny sentinel (trade.cpp clif_parse
		// _TradeAddItem). S2 records the CLAIMED amount only; the actual zeny
		// debit/credit + the against-balance check is S3's commit. rAthena's ZC_ADD
		// for zeny zeroes the item-detail fields (clif_tradeadditem p={}).
		//
		// KNOWN LIMITATION (tracked: inventory-index ±2 reconciliation unit):
		// rAthena uses client_index = server_index + 2, so a real client NEVER sends
		// 0 for an item (0/1 are invalid server indices, 0 = zeny). goAthena's bag is
		// 0-based (LoadByChar assigns Index 0,1,2… and the list emits them verbatim —
		// no +2 translation, matching drop/use/equip), so under goAthena's own
		// convention a real item CAN occupy Index 0 and is here aliased to zeny.
		// Until the codebase adopts the rAthena client_index±2 convention (a
		// cross-cutting change to the list emission + drop/use/equip handlers), slot 0
		// is untradeable. Slots 1+ stage correctly. Bounded severity, no dupe (S2
		// stages references only; commit is S3).
		if !deal.Add(adderAID, domain.StagedItem{Index: 0, Amount: req.Amount}) {
			return s.ackAddReply(adder, req.Index, packet.TradeItemAddInvFull)
		}
		if err := (packet.ZCAddExchangeItem{Amount: req.Amount}).Encode(connWriter{partner.Conn}); err != nil {
			return fmt.Errorf("trade: encode ZC_ADD zeny to partner %d: %w", partner.AccountID, err)
		}
		return s.ackAddReply(adder, req.Index, packet.TradeItemAddSuccess)
	}
	return s.stageItem(ctx, adder, partner, deal, adderAID, req)
}

// stageItem resolves the bag item at req.Index, validates it, stages a reference
// in the deal, and emits ZC_ADD (full item detail) to the partner + ZC_ACK_ADD
// success to the adder. It uses goAthena's 0-based wire-index convention (the bag
// row at req.Index directly — matching drop/use-item/equip; no rAthena client_index
// +2 translation, which goAthena does not implement). A reject (bad index, empty
// slot, over-stack, equipped item, list full) NACKs the adder and stages nothing.
func (s *TradeService) stageItem(ctx context.Context, adder, partner *domain.Player, deal *domain.TradeDeal, adderAID uint32, req packet.CZAddExchangeItem) error {
	rows, err := s.items.LoadByChar(ctx, adder.AccountID, adder.CharID)
	if err != nil {
		return fmt.Errorf("trade add-item: load bag account %d char %d: %w", adder.AccountID, adder.CharID, err)
	}
	if int(req.Index) >= len(rows) { //nolint:gosec // G115: bag slot < 100
		return s.ackAddReply(adder, req.Index, packet.TradeItemAddStackExceed)
	}
	item := rows[req.Index]
	if item.NameID == 0 || item.Equip != 0 {
		// rAthena rejects an empty slot or an equipped item from trade.
		return s.ackAddReply(adder, req.Index, packet.TradeItemAddStackExceed)
	}
	if int32(item.Amount) < req.Amount { //nolint:gosec // G115: stack < 30k, fits int32
		return s.ackAddReply(adder, req.Index, packet.TradeItemAddStackExceed)
	}
	if !deal.Add(adderAID, domain.StagedItem{Index: req.Index, Amount: req.Amount}) {
		return s.ackAddReply(adder, req.Index, packet.TradeItemAddInvFull)
	}
	if err := s.emitItemAdd(partner, item, req.Amount); err != nil {
		return err
	}
	return s.ackAddReply(adder, req.Index, packet.TradeItemAddSuccess)
}

// emitItemAdd builds the ZC_ADD frame for one inventory row + amount and sends it
// to the partner (the OTHER trader), resolving the item type/location/look from
// the item_db.
func (s *TradeService) emitItemAdd(partner *domain.Player, item invdomain.InventoryItem, amount int32) error {
	add := packet.ZCAddExchangeItem{
		ItemID:     item.NameID,
		Amount:     amount,
		Identified: boolToUint8(item.Identified),
		Damaged:    item.Attribute,
		Cards:      item.Cards,
		Refine:     item.Refine,
		Grade:      item.EnchantGrade,
	}
	if entry := s.itemDB.Get(int32(item.NameID)); entry != nil { //nolint:gosec // G115: NameID < 2^31
		add.ItemType = uint8(itemdb.WireType(entry.Type)) //nolint:gosec // IT_* wire byte 0..13
		add.Location = entry.EquipLocations
		add.Look = uint16(entry.View) //nolint:gosec // G115: view sprite < 2^16
	}
	add.Options = toPacketOptions(item.Options)
	if err := add.Encode(connWriter{partner.Conn}); err != nil {
		return fmt.Errorf("trade: encode ZC_ADD item to partner %d: %w", partner.AccountID, err)
	}
	return nil
}

// TradeOk resolves a CZ_TRADE_OK from okAID (S2): locks okAID's side of the deal
// and emits ZC_CONCLUDE to both — Who=0 to the Ok-presser, Who=1 to the partner
// (trade.cpp:512-514). When BOTH sides are Ok, rAthena waits for CZ_TRADE_COMMIT
// (S3); S2 stops here (the commit is the deferred, economy-critical slice). A
// re-Ok is a no-op (rAthena drops a second Ok). An Ok with no open trade is a no-op.
func (s *TradeService) TradeOk(ctx context.Context, okAID uint32) error {
	presser, ok := s.players.ByAccount(okAID)
	if !ok || !presser.IsTrading() {
		return nil
	}
	partnerID, _ := presser.TradePartner()
	partner, ok := s.players.ByAccount(partnerID)
	if !ok {
		return nil
	}
	deal := presser.TradeDeal()
	if deal == nil {
		return nil
	}
	_, _ = deal.Ok(okAID) // already-Ok is a no-op (rAthena drops it)
	// trade.cpp:511 clif_tradeitemok(*sd, -2, EXITEM_ADD_SUCCEED): the Ok-presser
	// gets a ZC_ACK_ADD (index=0 — client_index of -2, result=success) BEFORE the
	// ZC_CONCLUDE frames, signalling the lock took.
	if err := (packet.AckAddExchangeItem{Index: 0, Result: packet.TradeItemAddSuccess}).Encode(connWriter{presser.Conn}); err != nil {
		return fmt.Errorf("trade: encode ZC_ACK_ADD on ok account %d: %w", presser.AccountID, err)
	}
	// trade.cpp:512-514: ZC_CONCLUDE Who=0 to the presser, Who=1 to the partner.
	if err := (packet.ConcludeExchangeItem{Who: 0}).Encode(connWriter{presser.Conn}); err != nil {
		return fmt.Errorf("trade: encode ZC_CONCLUDE (self) account %d: %w", presser.AccountID, err)
	}
	if err := (packet.ConcludeExchangeItem{Who: 1}).Encode(connWriter{partner.Conn}); err != nil {
		return fmt.Errorf("trade: encode ZC_CONCLUDE (partner) account %d: %w", partner.AccountID, err)
	}
	return nil
}

// ackAddReply sends a ZC_ACK_ADD_EXCHANGE_ITEM (result byte) to p for the adder's
// bag slot index. Only an infra fault (encode) is returned.
func (s *TradeService) ackAddReply(p *domain.Player, index uint16, result uint8) error {
	if err := (packet.AckAddExchangeItem{Index: index, Result: result}).Encode(connWriter{p.Conn}); err != nil {
		return fmt.Errorf("trade: encode ZC_ACK_ADD_EXCHANGE_ITEM account %d: %w", p.AccountID, err)
	}
	return nil
}

// toPacketOptions copies the domain [5]ItemOption into the packet [5]ItemOption,
// casting the signed domain slots to the wire's unsigned slots.
func toPacketOptions(src [5]invdomain.ItemOption) [5]packet.ItemOption {
	var dst [5]packet.ItemOption
	for i, o := range src {
		dst[i] = packet.ItemOption{Index: o.ID, Value: uint16(o.Value), Param: uint8(o.Param)} //nolint:gosec // G115: signed→unsigned wire slots
	}
	return dst
}

// boolToUint8 returns 1 for true, 0 for false (the wire identified flag).
func boolToUint8(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

// inRange reports whether a and b are on the same map within tradeDistance
// (Chebyshev), matching rAthena's `sd->m == tsd->m && check_distance_bl`.
func (s *TradeService) inRange(a, b *domain.Player) bool {
	if a.MapName != b.MapName {
		return false
	}
	ax, ay, _ := a.Position()
	bx, by, _ := b.Position()
	return combat.Chebyshev(int(ax), int(ay), int(bx), int(by)) <= tradeDistance
}

// tradeAckReply sends a ZC_ACK_EXCHANGE_ITEM carrying result to p, echoing p's OWN
// trade partner's identity (account id + level) — the packet's TargetID/TargetLv
// fields name the partner from the client's perspective (clif_traderesponse,
// clif.cpp:4738). A player with no partner (e.g. a reject before any was set)
// sends zeroes.
func (s *TradeService) tradeAckReply(p *domain.Player, result uint8) error {
	partnerID, partnerLv := p.TradePartner()
	ack := packet.TradeAckResponse{Result: result, TargetID: partnerID, TargetLv: partnerLv}
	if err := ack.Encode(connWriter{p.Conn}); err != nil {
		return fmt.Errorf("trade: encode ZC_ACK_EXCHANGE_ITEM account %d: %w", p.AccountID, err)
	}
	return nil
}

// TradeRequestHandler serves CZ_TRADE_REQUEST (0x00e4) on the map-role dispatch
// table. The actor is sourced from conn.Auth().AccountID (impersonation-guarded,
// like every handler); the packet carries only the target's GID.
type TradeRequestHandler struct {
	svc *TradeService
}

// NewTradeRequestHandler binds the handler to its trade service.
func NewTradeRequestHandler(svc *TradeService) *TradeRequestHandler {
	return &TradeRequestHandler{svc: svc}
}

// Handle implements gateway/domain.PacketHandler for CZ_TRADE_REQUEST.
func (h *TradeRequestHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCZTradeRequest(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CZ_TRADE_REQUEST: %w", err)
	}
	accountID := conn.Auth().AccountID
	if accountID == 0 {
		return errors.New("trade: connection has no verified account (CZ_ENTER not completed)")
	}
	return h.svc.RequestTrade(ctx, accountID, req)
}

// TradeAckHandler serves CZ_TRADE_ACK (0x00e6).
type TradeAckHandler struct {
	svc *TradeService
}

// NewTradeAckHandler binds the handler to its trade service.
func NewTradeAckHandler(svc *TradeService) *TradeAckHandler {
	return &TradeAckHandler{svc: svc}
}

// Handle implements gateway/domain.PacketHandler for CZ_TRADE_ACK.
func (h *TradeAckHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCZTradeAck(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CZ_TRADE_ACK: %w", err)
	}
	accountID := conn.Auth().AccountID
	if accountID == 0 {
		return errors.New("trade: connection has no verified account (CZ_ENTER not completed)")
	}
	return h.svc.AckTrade(ctx, accountID, req)
}

// TradeCancelHandler serves CZ_TRADE_CANCEL (0x00ed) — a bare 2-byte cmd with no
// body, so the handler only needs the verified actor.
type TradeCancelHandler struct {
	svc *TradeService
}

// NewTradeCancelHandler binds the handler to its trade service.
func NewTradeCancelHandler(svc *TradeService) *TradeCancelHandler {
	return &TradeCancelHandler{svc: svc}
}

// Handle implements gateway/domain.PacketHandler for CZ_TRADE_CANCEL. It is a
// bare 2-byte cmd (no body); the gateway dispatch table already enforced the
// declared length (sizeCZTradeCancel) before routing, so the handler needs only
// the verified actor.
func (h *TradeCancelHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	accountID := conn.Auth().AccountID
	if accountID == 0 {
		return errors.New("trade: connection has no verified account (CZ_ENTER not completed)")
	}
	return h.svc.CancelTrade(ctx, accountID)
}

// AddItemHandler serves CZ_ADD_EXCHANGE_ITEM (0x00e8) — stage an item/zeny in the
// caller's trade window (S2).
type AddItemHandler struct {
	svc *TradeService
}

// NewAddItemHandler binds the handler to its trade service.
func NewAddItemHandler(svc *TradeService) *AddItemHandler {
	return &AddItemHandler{svc: svc}
}

// Handle implements gateway/domain.PacketHandler for CZ_ADD_EXCHANGE_ITEM.
func (h *AddItemHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	req, err := packet.ParseCZAddExchangeItem(frame.Raw)
	if err != nil {
		return fmt.Errorf("parse CZ_ADD_EXCHANGE_ITEM: %w", err)
	}
	accountID := conn.Auth().AccountID
	if accountID == 0 {
		return errors.New("trade: connection has no verified account (CZ_ENTER not completed)")
	}
	return h.svc.AddItem(ctx, accountID, req)
}

// TradeOkHandler serves CZ_TRADE_OK (0x00eb) — lock the caller's trade side (S2).
// A bare 2-byte cmd; the gateway enforced the declared length before routing.
type TradeOkHandler struct {
	svc *TradeService
}

// NewTradeOkHandler binds the handler to its trade service.
func NewTradeOkHandler(svc *TradeService) *TradeOkHandler {
	return &TradeOkHandler{svc: svc}
}

// Handle implements gateway/domain.PacketHandler for CZ_TRADE_OK.
func (h *TradeOkHandler) Handle(ctx context.Context, conn gwdomain.Conn, frame gwdomain.Frame) error {
	accountID := conn.Auth().AccountID
	if accountID == 0 {
		return errors.New("trade: connection has no verified account (CZ_ENTER not completed)")
	}
	return h.svc.TradeOk(ctx, accountID)
}

// Compile-time checks that the trade handlers satisfy the gateway handler shape.
var (
	_ gwdomain.PacketHandler = (*TradeRequestHandler)(nil).Handle
	_ gwdomain.PacketHandler = (*TradeAckHandler)(nil).Handle
	_ gwdomain.PacketHandler = (*TradeCancelHandler)(nil).Handle
	_ gwdomain.PacketHandler = (*AddItemHandler)(nil).Handle
	_ gwdomain.PacketHandler = (*TradeOkHandler)(nil).Handle
)
