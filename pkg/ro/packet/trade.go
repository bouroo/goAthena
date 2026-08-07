package packet

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Player-to-player trade family for PACKETVER 20250604 (slice S1: the
// request/ack/cancel handshake + in-memory state machine, NO item or zeny
// movement). Sources:
//   - third_party/rathenaThailand/src/map/clif_packetdb.hpp:87-94 (the C→S
//     bindings) + :1062 (ZC_CANCEL_EXCHANGE_ITEM 0x00ee).
//   - third_party/rathenaThailand/src/map/packets.hpp:373-403 (the S→C
//     PACKETVER > 6 structs — active at 20250604).
//   - third_party/rathenaThailand/src/map/clif.cpp:4714 (clif_traderequest) +
//     :4738 (clif_traderesponse) — the encoders whose field mapping this mirrors.
//   - third_party/rathenaThailand/src/map/trade.cpp:40-171 (the state machine).
//
// S1 moves NO inventory or zeny, so it carries zero item-duplication surface;
// the atomic-commit slice (S3, CZ_TRADE_COMMIT 0x00ef) is a separate,
// economy-critical unit and is intentionally NOT implemented here.
const (
	// C→S opcodes (clif_packetdb.hpp:87,89,93).
	HeaderCZTRADEREQUEST uint16 = 0x00e4 // CZ_TRADE_REQUEST — clif_parse_TradeRequest
	HeaderCZTRADEACK     uint16 = 0x00e6 // CZ_TRADE_ACK — clif_parse_TradeAck (type==3 accept / type==4 cancel)
	HeaderCZTRADECANCEL  uint16 = 0x00ed // CZ_TRADE_CANCEL — clif_parse_TradeCancel
	// S→C opcodes. ZC_REQ/ZC_ACK are the PACKETVER > 6 layouts (packets.hpp:373,
	// :387) — the pre-6 0x9a/0x0e7 aliases are NOT active at 20250604. ZC_CANCEL
	// is a plain 2-byte cmd (packets.hpp:1062).
	HeaderZCREQEXCHANGEITEM    uint16 = 0x01f4 // ZC_REQ_EXCHANGE_ITEM — to TARGET, opens its trade dialog
	HeaderZCACKEXCHANGEITEM    uint16 = 0x01f5 // ZC_ACK_EXCHANGE_ITEM — result to one or both parties
	HeaderZCCANCELEXCHANGEITEM uint16 = 0x00ee // ZC_CANCEL_EXCHANGE_ITEM — to both parties on cancel
)

const (
	sizeCZTradeRequest   = 6  // int16 cmd + uint32 targetGID (clif_packetdb.hpp:87 binds 0x00e4,6)
	sizeCZTradeAck       = 3  // int16 cmd + uint8 type (clif_packetdb.hpp:89 binds 0x00e6,3)
	sizeCZTradeCancel    = 2  // int16 cmd only (clif_packetdb.hpp:93 binds 0x00ed,2)
	sizeZCReqExchange    = 32 // int16 cmd + char[24] requesterName + uint32 targetId + uint16 targetLv
	sizeZCAckExchange    = 9  // int16 cmd + uint8 result + uint32 targetId + uint16 targetLv
	sizeZCCancelExchange = 2  // int16 cmd only
)

// TradeAckResult is the e_ack_trade_response enum carried in ZC_ACK_EXCHANGE_ITEM's
// result byte (clif.cpp:4730-4737): the reason a trade request/ack failed, or the
// ACCEPT/CANCEL outcome. NOTE this is the S→C enum ONLY — CZ_TRADE_ACK (0x00e6)
// uses a RAW type byte (3=accept, 4=cancel), not these values.
const (
	TradeAckTooFar       uint8 = 0 // Char is too far (TRADE_DISTANCE)
	TradeAckCharNotExist uint8 = 1 // Target does not exist / not online
	TradeAckFailed       uint8 = 2 // Trade failed (busy, in NPC dialog, already partnered, ...)
	TradeAckAccept       uint8 = 3 // Target accepted; trade window opens (type==3)
	TradeAckCancel       uint8 = 4 // Trade cancelled
	TradeAckBusy         uint8 = 5 // Target is busy
)

// CZTradeAckAccept is the RAW type byte the client sends in CZ_TRADE_ACK for an
// accept (trade.cpp:139); CZTradeAckCancel for a cancel (trade.cpp:129). Anything
// else is a broken packet the server ignores.
const (
	CZTradeAckAccept uint8 = 3
	CZTradeAckCancel uint8 = 4
)

// --- C→S request types (parse + Encode for the e2e harness) ---

// CZTradeRequest is the decoded form of a client → map-server CZ_TRADE_REQUEST
// frame (header 0x00e4, 6 bytes). The client sends the target player's GID — the
// GID field it received in the target's ZC_SPAWN_UNIT, which goAthena sets to the
// target's CharID — so the server resolves the partner by CharID.
type CZTradeRequest struct {
	// TargetGID is the target player's GID (CharID in goAthena's spawn convention).
	TargetGID uint32
}

// ParseCZTradeRequest decodes a CZ_TRADE_REQUEST frame.
func ParseCZTradeRequest(frame []byte) (CZTradeRequest, error) {
	if len(frame) < sizeCZTradeRequest {
		return CZTradeRequest{}, fmt.Errorf("packet: parse CZ_TRADE_REQUEST: want at least %d bytes, got %d", sizeCZTradeRequest, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZTRADEREQUEST {
		return CZTradeRequest{}, fmt.Errorf("packet: parse CZ_TRADE_REQUEST: unexpected cmd 0x%04x", cmd)
	}
	return CZTradeRequest{TargetGID: binary.LittleEndian.Uint32(frame[2:6])}, nil
}

// Encode writes the CZ_TRADE_REQUEST packet to w (used by the e2e harness).
func (r CZTradeRequest) Encode(w io.Writer) error {
	var buf [sizeCZTradeRequest]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZTRADEREQUEST)
	binary.LittleEndian.PutUint32(buf[2:], r.TargetGID)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write CZ_TRADE_REQUEST: %w", err)
	}
	return nil
}

// CZTradeAck is the decoded form of a CZ_TRADE_ACK frame (header 0x00e6, 3 bytes).
// Type is a RAW byte: CZTradeAckAccept(3) or CZTradeAckCancel(4); the server
// ignores any other value as a broken packet.
type CZTradeAck struct {
	Type uint8
}

// ParseCZTradeAck decodes a CZ_TRADE_ACK frame.
func ParseCZTradeAck(frame []byte) (CZTradeAck, error) {
	if len(frame) < sizeCZTradeAck {
		return CZTradeAck{}, fmt.Errorf("packet: parse CZ_TRADE_ACK: want at least %d bytes, got %d", sizeCZTradeAck, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZTRADEACK {
		return CZTradeAck{}, fmt.Errorf("packet: parse CZ_TRADE_ACK: unexpected cmd 0x%04x", cmd)
	}
	return CZTradeAck{Type: frame[2]}, nil
}

// Encode writes the CZ_TRADE_ACK packet to w (used by the e2e harness).
func (r CZTradeAck) Encode(w io.Writer) error {
	var buf [sizeCZTradeAck]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZTRADEACK)
	buf[2] = r.Type
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write CZ_TRADE_ACK: %w", err)
	}
	return nil
}

// EncodeCZTradeCancel writes the CZ_TRADE_CANCEL packet to w (a bare 2-byte cmd;
// used by the e2e harness). There is no body to parse — the handler validates the
// cmd only.
func EncodeCZTradeCancel(w io.Writer) error {
	var buf [sizeCZTradeCancel]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZTRADECANCEL)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write CZ_TRADE_CANCEL: %w", err)
	}
	return nil
}

// --- S→C reply encoders ---

// TradeRequestResponse encodes ZC_REQ_EXCHANGE_ITEM (0x01f4, 32 bytes) — sent to
// the TARGET only, it opens the target's trade dialog. Field mapping mirrors
// clif_traderequest (clif.cpp:4714): RequesterName is the REQUESTER's name shown
// in the target's dialog; TargetID/TargetLv are the REQUESTER's account_id /
// base_level (the packet's "target" fields name the OTHER guy from the client's
// perspective — confusingly — per rAthena trade.cpp:86-87). Source:
// packets.hpp:373-383 PACKET_ZC_REQ_EXCHANGE_ITEM (PACKETVER > 6).
type TradeRequestResponse struct {
	// RequesterName is the REQUESTER's character name (NUL-padded to 24 bytes).
	RequesterName string
	// TargetID is the REQUESTER's account_id.
	TargetID uint32
	// TargetLv is the REQUESTER's base level.
	TargetLv uint16
}

// Size returns the on-wire byte length Encode will write (always 32).
func (r TradeRequestResponse) Size() int { return sizeZCReqExchange }

// Encode writes the ZC_REQ_EXCHANGE_ITEM packet to w.
func (r TradeRequestResponse) Encode(w io.Writer) error {
	var buf [sizeZCReqExchange]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCREQEXCHANGEITEM)
	writeNameField(buf[:], 2, r.RequesterName)
	binary.LittleEndian.PutUint32(buf[26:], r.TargetID)
	binary.LittleEndian.PutUint16(buf[30:], r.TargetLv)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_REQ_EXCHANGE_ITEM: %w", err)
	}
	return nil
}

// TradeAckResponse encodes ZC_ACK_EXCHANGE_ITEM (0x01f5, 9 bytes) — the trade
// request/ack result, sent to one party (a reject reason) or both (ACCEPT/CANCEL
// outcome). Result is the TradeAckResult enum; TargetID/TargetLv echo the partner's
// account_id/base_level (clif_traderesponse, clif.cpp:4738 + trade.cpp:165-170).
type TradeAckResponse struct {
	Result   uint8 // TradeAckResult enum
	TargetID uint32
	TargetLv uint16
}

// Size returns the on-wire byte length Encode will write (always 9).
func (r TradeAckResponse) Size() int { return sizeZCAckExchange }

// Encode writes the ZC_ACK_EXCHANGE_ITEM packet to w.
func (r TradeAckResponse) Encode(w io.Writer) error {
	var buf [sizeZCAckExchange]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCACKEXCHANGEITEM)
	buf[2] = r.Result
	binary.LittleEndian.PutUint32(buf[3:], r.TargetID)
	binary.LittleEndian.PutUint16(buf[7:], r.TargetLv)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_ACK_EXCHANGE_ITEM: %w", err)
	}
	return nil
}

// CancelExchangeResponse encodes ZC_CANCEL_EXCHANGE_ITEM (0x00ee, 2 bytes) — a bare
// cmd sent to both parties when the trade is cancelled (either side's
// CZ_TRADE_CANCEL, or a server-initiated cancel on desync/disconnect). Source:
// packets.hpp:1062 PACKET_ZC_CANCEL_EXCHANGE_ITEM.
type CancelExchangeResponse struct{}

// Size returns the on-wire byte length Encode will write (always 2).
func (CancelExchangeResponse) Size() int { return sizeZCCancelExchange }

// Encode writes the ZC_CANCEL_EXCHANGE_ITEM packet to w.
func (CancelExchangeResponse) Encode(w io.Writer) error {
	var buf [sizeZCCancelExchange]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCCANCELEXCHANGEITEM)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_CANCEL_EXCHANGE_ITEM: %w", err)
	}
	return nil
}
