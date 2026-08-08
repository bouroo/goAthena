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
	// S2 add-item/ok opcodes (clif_packetdb.hpp:90,92; packets_struct.hpp:2642).
	HeaderCZADDEXCHANGEITEM uint16 = 0x00e8 // CZ_ADD_EXCHANGE_ITEM — add an item (or zeny, index==0) to my side
	HeaderCZTRADEOK         uint16 = 0x00eb // CZ_TRADE_OK — lock my side
	// 0x0b42 is the PACKETVER >= 20200916 ZC_ADD variant (packets_struct.hpp:2642)
	// — the active one at 20250604 (the 0x0a96/0x0a09/0x080f/0x00e9 variants are
	// for earlier clients). ZC_ACK_ADD and ZC_CONCLUDE are version-stable.
	HeaderZCADDEXCHANGEITEM      uint16 = 0x0b42 // ZC_ADD_EXCHANGE_ITEM — item staged, to PARTNER
	HeaderZCACKADDEXCHANGEITEM   uint16 = 0x00ea // ZC_ACK_ADD_EXCHANGE_ITEM — add result to SELF
	HeaderZCCONCLUDEEXCHANGEITEM uint16 = 0x00ec // ZC_CONCLUDE_EXCHANGE_ITEM — Ok pressed, to SELF
)

const (
	sizeCZTradeRequest   = 6  // int16 cmd + uint32 targetGID (clif_packetdb.hpp:87 binds 0x00e4,6)
	sizeCZTradeAck       = 3  // int16 cmd + uint8 type (clif_packetdb.hpp:89 binds 0x00e6,3)
	sizeCZTradeCancel    = 2  // int16 cmd only (clif_packetdb.hpp:93 binds 0x00ed,2)
	sizeZCReqExchange    = 32 // int16 cmd + char[24] requesterName + uint32 targetId + uint16 targetLv
	sizeZCAckExchange    = 9  // int16 cmd + uint8 result + uint32 targetId + uint16 targetLv
	sizeZCCancelExchange = 2  // int16 cmd only
	// S2.
	sizeCZAddExchangeItem  = 8 // int16 cmd + uint16 index + int32 amount (packets.hpp:1816)
	sizeCZTradeOk          = 2 // int16 cmd only (clif_packetdb.hpp:92 binds 0x00eb,2)
	sizeZCAckAddExchange   = 5 // int16 cmd + uint16 index + uint8 result (packets.hpp:405)
	sizeZCConcludeExchange = 3 // int16 cmd + uint8 who (packets.hpp:1067)
	// sizeZCAddExchangeItem = the PACKETVER >= 20200916 ZC_ADD layout
	// (packets_struct.hpp:2608-2639): cmd(2) + itemId(4) + itemType(1) +
	// amount(4) + identified(1) + damaged(1) + EQUIPSLOTINFO{card[4]uint32}(16)
	// + ItemOptions[5]{index,val,parm}(25) + location(4) + look(2) + refine(1)
	// + grade(1) = 2+4+1+4+1+1+16+25+4+2+1+1 = 62. NOTE refine moved POST-cards
	// at this gate (the older layout has it pre-cards).
	sizeZCAddExchangeItem = 62
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

// --- S2: add-item + ok + conclude ---

// TradeItemAddResult is the e_exitem_add_result enum carried in
// ZC_ACK_ADD_EXCHANGE_ITEM's result byte (clif.cpp:4803-4808 + clif.hpp enum):
// 0=success, 1=overweight, 2=canceled, 3=inventory full, 4=stack amount exceeded.
// S2 resolves the success/canceled cases; the overweight/full/exceed codes are
// PENDING the weight model and a fuller stack-split (S3 prereq).
const (
	TradeItemAddSuccess     uint8 = 0 // EXITEM_ADD_SUCCESS
	TradeItemAddOverweight  uint8 = 1 // EXITEM_ADD_FAILED_OVERWEIGHT
	TradeItemAddCanceled    uint8 = 2 // EXITEM_ADD_FAILED_CLOSED (trade canceled)
	TradeItemAddInvFull     uint8 = 3 // EXITEM_ADD_FAILED_INVFULL
	TradeItemAddStackExceed uint8 = 4 // EXITEM_ADD_FAILED_AMOUNT
)

// CZAddExchangeItem is the decoded form of a CZ_ADD_EXCHANGE_ITEM frame (header
// 0x00e8, 8 bytes; packets.hpp:1816). Index==0 means ZENY (the player is adding
// zeny, Amount the zeny count); a non-zero Index is the bag slot of an item.
type CZAddExchangeItem struct {
	Index  uint16
	Amount int32
}

// ParseCZAddExchangeItem decodes a CZ_ADD_EXCHANGE_ITEM frame.
func ParseCZAddExchangeItem(frame []byte) (CZAddExchangeItem, error) {
	if len(frame) < sizeCZAddExchangeItem {
		return CZAddExchangeItem{}, fmt.Errorf("packet: parse CZ_ADD_EXCHANGE_ITEM: want at least %d bytes, got %d", sizeCZAddExchangeItem, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZADDEXCHANGEITEM {
		return CZAddExchangeItem{}, fmt.Errorf("packet: parse CZ_ADD_EXCHANGE_ITEM: unexpected cmd 0x%04x", cmd)
	}
	return CZAddExchangeItem{
		Index:  binary.LittleEndian.Uint16(frame[2:4]),
		Amount: int32(binary.LittleEndian.Uint32(frame[4:8])), //nolint:gosec // G115: wire slot is signed int32
	}, nil
}

// Encode writes the CZ_ADD_EXCHANGE_ITEM packet to w (used by the e2e harness).
func (r CZAddExchangeItem) Encode(w io.Writer) error {
	var buf [sizeCZAddExchangeItem]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZADDEXCHANGEITEM)
	binary.LittleEndian.PutUint16(buf[2:], r.Index)
	binary.LittleEndian.PutUint32(buf[4:], uint32(r.Amount)) //nolint:gosec // G115: wire slot is signed int32
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write CZ_ADD_EXCHANGE_ITEM: %w", err)
	}
	return nil
}

// AckAddExchangeItem encodes ZC_ACK_ADD_EXCHANGE_ITEM (0x00ea, 5 bytes) — the
// per-add result sent to SELF (the adder): whether the staged item was accepted
// (success) or rejected (overweight/full/etc.). Index is the bag slot the adder
// tried to stage (rAthena's client_index of the server index). Source:
// packets.hpp:405 PACKET_ZC_ACK_ADD_EXCHANGE_ITEM.
type AckAddExchangeItem struct {
	Index  uint16
	Result uint8 // TradeItemAddResult enum
}

// Size returns the on-wire byte length Encode will write (always 5).
func (AckAddExchangeItem) Size() int { return sizeZCAckAddExchange }

// Encode writes the ZC_ACK_ADD_EXCHANGE_ITEM packet to w.
func (r AckAddExchangeItem) Encode(w io.Writer) error {
	var buf [sizeZCAckAddExchange]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCACKADDEXCHANGEITEM)
	binary.LittleEndian.PutUint16(buf[2:], r.Index)
	buf[4] = r.Result
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_ACK_ADD_EXCHANGE_ITEM: %w", err)
	}
	return nil
}

// ZCAddExchangeItem encodes ZC_ADD_EXCHANGE_ITEM (0x0b42, 62 bytes) — the frame
// the server sends to the PARTNER when the other player stages an item/zeny in
// the trade window. The 20250604 layout (packets_struct.hpp:2608-2639,
// PACKETVER >= 20200916 gate) carries the full item detail (cards + random
// options + location/look/refine/grade). Source: clif.cpp:4762 clif_tradeadditem.
//
// On-wire layout (62 bytes):
//
//	int16  packetType (0x0b42)
//	uint32 itemId        — item_db nameid
//	uint8  itemType      — IT_* wire byte (itemdb.WireType)
//	int32  amount        — stack count (zeny count when Index==0)
//	uint8  identified
//	uint8  damaged       — attribute (broken) flag
//	uint32 card[4]       — EQUIPSLOTINFO (4× uint32 at PACKETVER >= 20181121)
//	ItemOptions[5]       — 5 × {int16 index, int16 value, uint8 param} = 25B
//	uint32 location      — EQP_* equip-point bitmask
//	uint16 look          — view sprite
//	uint8  refine        — (+0..+10) — NOTE post-cards at this gate
//	uint8  grade         — enchant grade
type ZCAddExchangeItem struct {
	ItemID     uint32
	ItemType   uint8
	Amount     int32
	Identified uint8
	Damaged    uint8
	Cards      [4]uint32
	Options    [5]ItemOption
	Location   uint32
	Look       uint16
	Refine     uint8
	Grade      uint8
}

// Size returns the on-wire byte length Encode will write (always 62).
func (ZCAddExchangeItem) Size() int { return sizeZCAddExchangeItem }

// Encode writes the ZC_ADD_EXCHANGE_ITEM packet to w.
func (r ZCAddExchangeItem) Encode(w io.Writer) error {
	var buf [sizeZCAddExchangeItem]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCADDEXCHANGEITEM)
	binary.LittleEndian.PutUint32(buf[2:], r.ItemID)
	buf[6] = r.ItemType
	binary.LittleEndian.PutUint32(buf[7:], uint32(r.Amount)) //nolint:gosec // G115: wire slot is signed int32
	buf[11] = r.Identified
	buf[12] = r.Damaged
	off := 13
	for _, c := range r.Cards {
		binary.LittleEndian.PutUint32(buf[off:], c)
		off += 4
	}
	for _, opt := range r.Options {
		binary.LittleEndian.PutUint16(buf[off:], opt.Index)
		binary.LittleEndian.PutUint16(buf[off+2:], opt.Value)
		buf[off+4] = opt.Param
		off += 5
	}
	binary.LittleEndian.PutUint32(buf[off:], r.Location)
	binary.LittleEndian.PutUint16(buf[off+4:], r.Look)
	buf[off+6] = r.Refine
	buf[off+7] = r.Grade
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_ADD_EXCHANGE_ITEM: %w", err)
	}
	return nil
}

// ConcludeExchangeItem encodes ZC_CONCLUDE_EXCHANGE_ITEM (0x00ec, 3 bytes) — sent
// when a player presses Ok (locks their side). Who names WHICH player locked from
// the receiver's perspective: the Ok-presser gets Who=0, the partner gets Who=1
// (trade.cpp:512-514: clif_tradedeal_lock(self,false) + (partner,true)). Source:
// packets.hpp:1067 PACKET_ZC_CONCLUDE_EXCHANGE_ITEM.
type ConcludeExchangeItem struct {
	Who uint8 // 0 = you pressed Ok; 1 = your partner pressed Ok
}

// Size returns the on-wire byte length Encode will write (always 3).
func (ConcludeExchangeItem) Size() int { return sizeZCConcludeExchange }

// Encode writes the ZC_CONCLUDE_EXCHANGE_ITEM packet to w.
func (r ConcludeExchangeItem) Encode(w io.Writer) error {
	var buf [sizeZCConcludeExchange]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCCONCLUDEEXCHANGEITEM)
	buf[2] = r.Who
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_CONCLUDE_EXCHANGE_ITEM: %w", err)
	}
	return nil
}

// EncodeCZTradeOk writes the CZ_TRADE_OK packet to w (a bare 2-byte cmd; used by
// the e2e harness). There is no body — the handler only needs the verified actor.
func EncodeCZTradeOk(w io.Writer) error {
	var buf [sizeCZTradeOk]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZTRADEOK)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write CZ_TRADE_OK: %w", err)
	}
	return nil
}
