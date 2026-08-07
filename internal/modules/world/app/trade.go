package app

import (
	"context"
	"errors"
	"fmt"

	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/combat"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// tradeDistance is the maximum Chebyshev distance at which two players may trade
// (rathena/src/map/trade.cpp:24 `#define TRADE_DISTANCE 2`). Both the request
// and the ack re-check it, since the original character could have warped between
// the two (trade.cpp:81 and :145).
const tradeDistance = 2

// TradeService owns the CZ_TRADE handshake (slice S1): the request/ack/cancel
// state machine between two online players, with NO item or zeny movement. It
// mirrors rathena/src/map/trade.cpp's traderequest/tradeack/cancel: a request
// sets both players' trade-partner state and opens the TARGET's dialog (ZC_REQ);
// an ack ACCEPT marks both windows open (ZC_ACK to both) or CANCEL tears the
// pending trade down (ZC_ACK CANCEL to both); a cancel from either side sends
// ZC_CANCEL to both. Every reject path emits a ZC_ACK with the e_ack_trade_response
// reason to the actor. Because S1 moves no inventory, it carries zero
// item-duplication surface — the atomic all-or-nothing commit (S3) is deferred.
type TradeService struct {
	players *domain.PlayerRegistry
}

// NewTradeService binds the trade handshake to the live player registry.
func NewTradeService(players *domain.PlayerRegistry) *TradeService {
	return &TradeService{players: players}
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
		// Initiate trade (trade.cpp:164-170): both windows open, ZC_ACK ACCEPT to both.
		acker.SetTrading(true)
		partner.SetTrading(true)
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

// Compile-time checks that the trade handlers satisfy the gateway handler shape.
var (
	_ gwdomain.PacketHandler = (*TradeRequestHandler)(nil).Handle
	_ gwdomain.PacketHandler = (*TradeAckHandler)(nil).Handle
	_ gwdomain.PacketHandler = (*TradeCancelHandler)(nil).Handle
)
