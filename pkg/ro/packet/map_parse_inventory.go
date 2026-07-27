package packet

import (
	"encoding/binary"
	"fmt"
	"io"
)

// CZUseItemRequest is the decoded form of a client → map-server
// CZ_USE_ITEM2 packet (header 0x0439, 8 bytes on the wire). Source:
// rathena/src/map/clif_packetdb.hpp:1151
// (`parseable_packet(0x0439,8,clif_parse_UseItem,2,4)`).
//
// On-wire layout:
//
//	int16 packetType (0x0439)
//	uint16 index     — inventory grid index (NOT the item DB nameid)
//	uint32 AID       — rAthena's session account id, ignored by the
//	                   gateway today (the dispatcher already has the
//	                   authenticated AID on the ConnectionInfo).
//
// rAthena's clif_parse_UseItem treats the 2-byte "item id" as an
// inventory index and uses it to look up the row in `sd->inventory`
// (rathena/src/map/clif.cpp:12099-12113). The proto UseItemRequest
// field is called item_id for contract stability, but semantically
// the dispatcher forwards the inventory index. The identity service
// is the single point that maps inventory index → item DB row.
type CZUseItemRequest struct {
	// Index is the inventory grid index the client clicked on.
	// Forwarded as the proto UseItemRequest.item_id field.
	Index uint16
	// AID is the account id the client stamped on the request. The
	// gateway does not cross-check it against the authenticated
	// session — the dispatcher already gates the request on
	// conn.AccountID, and a malicious client that lies about the AID
	// in this slot cannot reach another player's inventory.
	AID uint32
}

// ParseCZUseItem decodes a CZ_USE_ITEM2 frame (including the 2-byte
// cmd header) into a CZUseItemRequest. The frame must carry cmd
// 0x0439 and contain at least 8 bytes; trailing bytes are ignored
// to allow the parser to accept frames where the caller has already
// buffered more than the fixed header.
//
// Returns a wrapped error naming the off-by-one byte count if the
// frame is shorter than 8 bytes, or naming the unexpected cmd id if
// the header is not 0x0439.
func ParseCZUseItem(frame []byte) (CZUseItemRequest, error) {
	if len(frame) < sizeCZUseItem2 {
		return CZUseItemRequest{}, fmt.Errorf("packet: parse CZ_USE_ITEM2: want at least %d bytes, got %d", sizeCZUseItem2, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZUSEITEM2 {
		return CZUseItemRequest{}, fmt.Errorf("packet: parse CZ_USE_ITEM2: unexpected cmd 0x%04x", cmd)
	}
	return CZUseItemRequest{
		Index: binary.LittleEndian.Uint16(frame[2:4]),
		AID:   binary.LittleEndian.Uint32(frame[4:8]),
	}, nil
}

// Encode writes the CZ_USE_ITEM2 packet to w, mirroring the on-wire
// layout documented on CZUseItemRequest: [2:cmd=0x0439][2:index]
// [4:AID] = 8 bytes.
func (r CZUseItemRequest) Encode(w io.Writer) error {
	buf := make([]byte, sizeCZUseItem2)
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZUSEITEM2)
	binary.LittleEndian.PutUint16(buf[2:], r.Index)
	binary.LittleEndian.PutUint32(buf[4:], r.AID)
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write CZ_USE_ITEM2: %w", err)
	}
	return nil
}

// CZReqWearEquipRequest is the decoded form of a client → map-server
// CZ_REQ_WEAR_EQUIP_V5 packet (header 0x0998, 8 bytes on the wire).
// Source: rathena/src/map/packets.hpp:1504-1509
// (PACKET_CZ_REQ_WEAR_EQUIP, PACKETVER >= 20120925 branch).
//
// On-wire layout:
//
//	int16 packetType (0x0998)
//	uint16 index     — inventory grid index
//	uint32 position  — EQP_* bitmask the client requests for the slot
//
// The 32-bit position is what makes this the "V5" shape; the
// pre-20120925 opcode 0x00a9 uses uint16 position. rAthena's
// clif_parse_EquipItem reads the inventory index via
// packet_db[..].pos[0] and resolves the slot from the position
// bitmask vs. the item's allowed EQP_* flags.
type CZReqWearEquipRequest struct {
	// Index is the inventory grid index of the item to equip.
	Index uint16
	// Position is the EQP_* bitmask the client requested. The
	// identity service is responsible for validating the bitmask
	// against the item's allowed locations (item_db.equip).
	Position uint32
}

// ParseCZReqWearEquip decodes a CZ_REQ_WEAR_EQUIP_V5 frame
// (including the 2-byte cmd header) into a CZReqWearEquipRequest.
// The frame must carry cmd 0x0998 and contain at least 8 bytes;
// trailing bytes are ignored.
//
// Returns a wrapped error naming the off-by-one byte count if the
// frame is shorter than 8 bytes, or naming the unexpected cmd id if
// the header is not 0x0998.
func ParseCZReqWearEquip(frame []byte) (CZReqWearEquipRequest, error) {
	if len(frame) < sizeCZReqWearEquipV5 {
		return CZReqWearEquipRequest{}, fmt.Errorf("packet: parse CZ_REQ_WEAR_EQUIP_V5: want at least %d bytes, got %d", sizeCZReqWearEquipV5, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZREQWEAREQUIPV5 {
		return CZReqWearEquipRequest{}, fmt.Errorf("packet: parse CZ_REQ_WEAR_EQUIP_V5: unexpected cmd 0x%04x", cmd)
	}
	return CZReqWearEquipRequest{
		Index:    binary.LittleEndian.Uint16(frame[2:4]),
		Position: binary.LittleEndian.Uint32(frame[4:8]),
	}, nil
}

// Encode writes the CZ_REQ_WEAR_EQUIP_V5 packet to w, mirroring the
// on-wire layout documented on CZReqWearEquipRequest:
// [2:cmd=0x0998][2:index][4:position] = 8 bytes.
func (r CZReqWearEquipRequest) Encode(w io.Writer) error {
	buf := make([]byte, sizeCZReqWearEquipV5)
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZREQWEAREQUIPV5)
	binary.LittleEndian.PutUint16(buf[2:], r.Index)
	binary.LittleEndian.PutUint32(buf[4:], r.Position)
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write CZ_REQ_WEAR_EQUIP_V5: %w", err)
	}
	return nil
}

// CZReqTakeoffEquipRequest is the decoded form of a client →
// map-server CZ_REQ_TAKEOFF_EQUIP packet (header 0x00ab, 4 bytes on
// the wire). Source: rathena/src/map/clif_packetdb.hpp:59
// (`parseable_packet(0x00ab,4,clif_parse_UnequipItem,2)`).
//
// On-wire layout:
//
//	int16 packetType (0x00ab)
//	uint16 index     — inventory grid index
//
// rAthena's clif_parse_UnequipItem ignores the position field the
// client may send; the server derives the position from the row's
// `inventory.equip` column (clif.cpp).
type CZReqTakeoffEquipRequest struct {
	// Index is the inventory grid index of the item to unequip.
	Index uint16
}

// ParseCZReqTakeoffEquip decodes a CZ_REQ_TAKEOFF_EQUIP frame
// (including the 2-byte cmd header) into a CZReqTakeoffEquipRequest.
// The frame must carry cmd 0x00ab and contain at least 4 bytes;
// trailing bytes are ignored.
//
// Returns a wrapped error naming the off-by-one byte count if the
// frame is shorter than 4 bytes, or naming the unexpected cmd id if
// the header is not 0x00ab.
func ParseCZReqTakeoffEquip(frame []byte) (CZReqTakeoffEquipRequest, error) {
	if len(frame) < sizeCZReqTakeoffEquip {
		return CZReqTakeoffEquipRequest{}, fmt.Errorf("packet: parse CZ_REQ_TAKEOFF_EQUIP: want at least %d bytes, got %d", sizeCZReqTakeoffEquip, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZREQTAKEOFFEQUIP {
		return CZReqTakeoffEquipRequest{}, fmt.Errorf("packet: parse CZ_REQ_TAKEOFF_EQUIP: unexpected cmd 0x%04x", cmd)
	}
	return CZReqTakeoffEquipRequest{
		Index: binary.LittleEndian.Uint16(frame[2:4]),
	}, nil
}

// Encode writes the CZ_REQ_TAKEOFF_EQUIP packet to w, mirroring the
// on-wire layout documented on CZReqTakeoffEquipRequest:
// [2:cmd=0x00ab][2:index] = 4 bytes.
func (r CZReqTakeoffEquipRequest) Encode(w io.Writer) error {
	buf := make([]byte, sizeCZReqTakeoffEquip)
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZREQTAKEOFFEQUIP)
	binary.LittleEndian.PutUint16(buf[2:], r.Index)
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write CZ_REQ_TAKEOFF_EQUIP: %w", err)
	}
	return nil
}
