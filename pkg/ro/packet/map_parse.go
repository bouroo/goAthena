package packet

import (
	"encoding/binary"
	"fmt"
)

// CZEnterRequest is the decoded form of a client → map-server CZ_ENTER packet
// (header 0x0072, 19 bytes on the wire). Source: rathena/src/map/clif.cpp:10642
// and the WantToConnection handler reading RFIFOL/AID/CID/login_id1/client_tick/
// sex at the documented offsets.
//
// The on-wire field name "auth code" maps to rAthena's local login_id1 (the
// upper 32 bits of the session token echoed by the char server).
type CZEnterRequest struct {
	// AccountID is the account ID echoed by the char server (AID).
	AccountID uint32
	// CharID is the character ID the client wants to enter the map server with.
	CharID uint32
	// AuthCode is the upper 32 bits of the session token (rAthena's login_id1).
	AuthCode uint32
	// ClientTime is the client's monotonic tick at the moment it issued the
	// enter request (rAthena's client_tick). The map server uses it as a
	// soft anti-DoS check, not a session field.
	ClientTime uint32
	// Sex is the character's sex byte (0x0 female, 0x1 male in kRO).
	Sex uint8
}

// ParseCZEnter parses a CZ_ENTER frame (including the 2-byte cmd header) into
// a CZEnterRequest. The frame must carry the cmd header 0x0072 and contain
// at least 19 bytes; trailing bytes are ignored to allow the parser to accept
// frames where the caller has already buffered more than the fixed header.
//
// Returns a wrapped error naming the off-by-one byte count if the frame is
// shorter than 19 bytes, or naming the unexpected cmd id if the header is
// not 0x0072.
func ParseCZEnter(frame []byte) (CZEnterRequest, error) {
	if len(frame) < sizeCZEnter {
		return CZEnterRequest{}, fmt.Errorf("packet: parse CZ_ENTER: want at least %d bytes, got %d", sizeCZEnter, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZENTER {
		return CZEnterRequest{}, fmt.Errorf("packet: parse CZ_ENTER: unexpected cmd 0x%04x", cmd)
	}

	return CZEnterRequest{
		AccountID:  binary.LittleEndian.Uint32(frame[2:6]),
		CharID:     binary.LittleEndian.Uint32(frame[6:10]),
		AuthCode:   binary.LittleEndian.Uint32(frame[10:14]),
		ClientTime: binary.LittleEndian.Uint32(frame[14:18]),
		Sex:        frame[18],
	}, nil
}

// CZRequestMoveRequest is the decoded form of a client → map-server
// CZ_REQUEST_MOVE packet (header 0x0085, 5 bytes on the wire). Source:
// rathena/src/map/clif.cpp:11374 (WalkToXY handler calling RFIFOPOS at
// packet_db[..].pos[0]).
//
// The on-wire dest[3] is a kRO 3-byte packed position (clif.cpp:173-211
// WBUFPOS/RBUFPOS); the direction slot is unused by the move request —
// the client picks the cardinal direction from the (x,y) delta itself —
// so the decoded struct exposes only DestX / DestY.
type CZRequestMoveRequest struct {
	// DestX is the requested destination X (cell coordinate).
	DestX int16
	// DestY is the requested destination Y (cell coordinate).
	DestY int16
}

// ParseCZRequestMove parses a CZ_REQUEST_MOVE frame (including the 2-byte
// cmd header) into a CZRequestMoveRequest. The frame must carry the cmd
// header 0x0085 and contain at least 5 bytes; the dir slot of the packed
// position is discarded.
//
// Returns a wrapped error naming the off-by-one byte count if the frame is
// shorter than 5 bytes, or naming the unexpected cmd id if the header is
// not 0x0085.
func ParseCZRequestMove(frame []byte) (CZRequestMoveRequest, error) {
	if len(frame) < sizeCZRequestMove {
		return CZRequestMoveRequest{}, fmt.Errorf("packet: parse CZ_REQUEST_MOVE: want at least %d bytes, got %d", sizeCZRequestMove, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZREQUESTMOVE {
		return CZRequestMoveRequest{}, fmt.Errorf("packet: parse CZ_REQUEST_MOVE: unexpected cmd 0x%04x", cmd)
	}

	destX, destY, _ := decodePos(frame[2:5])
	return CZRequestMoveRequest{
		DestX: destX,
		DestY: destY,
	}, nil
}
