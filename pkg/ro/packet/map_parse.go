package packet

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
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

// Encode writes the CZ_ENTER packet to w, mirroring the on-wire layout
// documented on CZEnterRequest: [2:cmd=0x0072][4:accountID][4:charID]
// [4:authCode][4:clientTime][1:sex] = 19 bytes. Source:
// rathena/src/map/clif.cpp:10642 and the WantToConnection handler reading
// RFIFOL/AID/CID/login_id1/client_tick/sex at the documented offsets.
func (r CZEnterRequest) Encode(w io.Writer) error {
	buf := make([]byte, sizeCZEnter)
	// int16 packetType = 0x0072 (HeaderCZENTER).
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZENTER)
	// uint32 accountID at offset 2.
	binary.LittleEndian.PutUint32(buf[2:], r.AccountID)
	// uint32 charID at offset 6.
	binary.LittleEndian.PutUint32(buf[6:], r.CharID)
	// uint32 authCode at offset 10.
	binary.LittleEndian.PutUint32(buf[10:], r.AuthCode)
	// uint32 clientTime at offset 14.
	binary.LittleEndian.PutUint32(buf[14:], r.ClientTime)
	// uint8 sex at offset 18.
	buf[18] = r.Sex

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write CZ_ENTER: %w", err)
	}
	return nil
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

// Encode writes the CZ_REQUEST_MOVE packet to w, mirroring the on-wire
// layout documented on CZRequestMoveRequest: [2:cmd=0x0085][3:encodePos]
// = 5 bytes. The dir slot of the packed position is always written as
// zero — the move request carries no facing. Source:
// rathena/src/map/clif.cpp:11374 (WalkToXY handler calling RFIFOPOS at
// packet_db[..].pos[0]).
func (r CZRequestMoveRequest) Encode(w io.Writer) error {
	buf := make([]byte, sizeCZRequestMove)
	// int16 packetType = 0x0085 (HeaderCZREQUESTMOVE).
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZREQUESTMOVE)
	// uint8 dest[3] at offset 2 — kRO 3-byte packed position.
	encodePos(buf[2:5], r.DestX, r.DestY, 0)

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write CZ_REQUEST_MOVE: %w", err)
	}
	return nil
}

// CZRequestTimeRequest is the parsed CZ_REQUEST_TIME (0x007e) packet.
// The client sends this periodically to request the server's tick for
// latency estimation.
//
// Layout: [2:cmd=0x007e][4:clientTick].
// Source: rathena/src/map/clif.cpp:11198-11206.
type CZRequestTimeRequest struct {
	// ClientTick is the client's monotonic tick at the moment it
	// issued the request (rAthena's `client_tick`). Echoed only for
	// logging; the gateway does not need to round-trip it.
	ClientTick uint32
}

// ParseCZRequestTime decodes a CZ_REQUEST_TIME frame.
func ParseCZRequestTime(frame []byte) (CZRequestTimeRequest, error) {
	if len(frame) < sizeCZRequestTime {
		return CZRequestTimeRequest{}, fmt.Errorf("packet: parse CZ_REQUEST_TIME: frame too short: got %d bytes, want %d", len(frame), sizeCZRequestTime)
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZREQUESTTIME {
		return CZRequestTimeRequest{}, fmt.Errorf("packet: parse CZ_REQUEST_TIME: unexpected cmd 0x%04x", cmd)
	}
	return CZRequestTimeRequest{
		ClientTick: binary.LittleEndian.Uint32(frame[2:6]),
	}, nil
}

// Encode writes the CZ_REQUEST_TIME packet to w.
func (r CZRequestTimeRequest) Encode(w io.Writer) error {
	buf := make([]byte, sizeCZRequestTime)
	// int16 packetType = 0x007e (HeaderCZREQUESTTIME).
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZREQUESTTIME)
	// uint32 clientTick at offset 2.
	binary.LittleEndian.PutUint32(buf[2:], r.ClientTick)

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write CZ_REQUEST_TIME: %w", err)
	}
	return nil
}

// CZGlobalMessageRequest is the decoded form of a client → map-server
// CZ_GLOBAL_MESSAGE packet (header 0x008c, variable length). The wire
// layout (rathena/src/map/clif_packetdb.hpp:40 +
// rathena/src/map/clif.cpp:11507) is:
//
//	int16  packetType   (0x008c)
//	int16  packetLength (header + trailing NUL-terminated text bytes)
//	char   text[]       (NUL-terminated UTF-8)
//
// rAthena's clif_process_message prepends "<name> : " to the message
// before broadcasting ZC_NOTIFY_CHAT. The gateway does not run that
// pipeline (no AOI yet); the parser returns the raw text the client
// sent so the dispatcher can echo it back verbatim.
type CZGlobalMessageRequest struct {
	// Message is the raw text the client sent, NUL terminator stripped.
	Message string
}

// ParseCZGlobalMessage decodes a CZ_GLOBAL_MESSAGE frame. The frame must
// carry cmd 0x008c and contain at least the 4-byte header; the message
// is the NUL-terminated text starting at offset 4. Trailing bytes past
// the embedded packetLength are tolerated so the gateway can hand in a
// buffered frame without first stripping the tail.
//
// Returns a wrapped error if the frame is shorter than 4 bytes, the cmd
// id is not 0x008c, or the message body is empty (a chat packet with no
// payload is malformed — the client always NUL-terminates, including for
// the trivial single-character case).
func ParseCZGlobalMessage(frame []byte) (CZGlobalMessageRequest, error) {
	const minFrame = 4
	if len(frame) < minFrame {
		return CZGlobalMessageRequest{}, fmt.Errorf("packet: parse CZ_GLOBAL_MESSAGE: want at least %d bytes, got %d", minFrame, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZGLOBALMESSAGE {
		return CZGlobalMessageRequest{}, fmt.Errorf("packet: parse CZ_GLOBAL_MESSAGE: unexpected cmd 0x%04x", cmd)
	}

	body := frame[4:]
	if len(body) == 0 {
		return CZGlobalMessageRequest{}, fmt.Errorf("packet: parse CZ_GLOBAL_MESSAGE: empty message body")
	}
	// NUL-terminated text; trim the terminator. bytes.IndexByte is O(n) but
	// chat is bounded by CHAT_SIZE_MAX (rAthena's clif_process_message
	// truncation point) so the scan stays cheap.
	if idx := bytes.IndexByte(body, 0); idx >= 0 {
		body = body[:idx]
	}
	return CZGlobalMessageRequest{Message: string(body)}, nil
}

// CZActionRequestRequest is the decoded form of a client → map-server
// CZ_ACTION_REQUEST packet (header 0x0089, 7 bytes fixed). Source:
// rathena/src/map/clif_packetdb.hpp:38 +
// rathena/src/map/clif.cpp:11806-11829. The on-wire shape is
// `<targetGID>.L <action>.B`, where action is the rAthena sit/stand/
// attack selector. goAthena's M11 dispatch only honors action codes
// 0 (stand) and 1 (sit); codes 2/3 (attack) and 7/12 (continuous
// attack / touch skill) are ignored at the dispatcher layer until a
// combat system lands.
type CZActionRequestRequest struct {
	// TargetGID is the entity the action targets — for self-targeted
	// actions (sit / stand) this is the player's own GID; for attack
	// actions it is the victim's GID. The gateway echoes the caller's
	// own GID on sit/stand and ignores attacks.
	TargetGID uint32
	// Action is the rAthena action selector byte:
	//   0 = stand up (DMG_NORMAL — but rAthena reuses 0 for both
	//       "attack once" and "stand up" depending on session state; we
	//       map 0 → stand at the dispatcher for the echo path);
	//   1 = pick up item (rAthena) / sit down (goAthena M11 mapping);
	//   2 = sit down (rAthena) / ignored (goAthena M11);
	//   3 = stand up (rAthena) / ignored (goAthena M11);
	//   7 = continuous attack.
	// The action byte is preserved on the wire so the dispatcher can
	// branch on its own policy without re-deriving the selector.
	Action uint8
}

// ParseCZActionRequest decodes a CZ_ACTION_REQUEST frame. The frame must
// carry cmd 0x0089 and contain 7 bytes.
//
// Returns a wrapped error naming the byte count if the frame is short,
// or naming the unexpected cmd id if the header is not 0x0089.
func ParseCZActionRequest(frame []byte) (CZActionRequestRequest, error) {
	if len(frame) < sizeCZActionRequest {
		return CZActionRequestRequest{}, fmt.Errorf("packet: parse CZ_ACTION_REQUEST: want at least %d bytes, got %d", sizeCZActionRequest, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZACTIONREQUEST {
		return CZActionRequestRequest{}, fmt.Errorf("packet: parse CZ_ACTION_REQUEST: unexpected cmd 0x%04x", cmd)
	}
	return CZActionRequestRequest{
		TargetGID: binary.LittleEndian.Uint32(frame[2:6]),
		Action:    frame[6],
	}, nil
}

// Encode writes the CZ_ACTION_REQUEST packet to w. Mirrors the on-wire
// layout: [2:cmd=0x0089][4:targetGID][1:action] = 7 bytes. Used by the
// e2e test harness to drive sit/stand.
func (r CZActionRequestRequest) Encode(w io.Writer) error {
	buf := make([]byte, sizeCZActionRequest)
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZACTIONREQUEST)
	binary.LittleEndian.PutUint32(buf[2:], r.TargetGID)
	buf[6] = r.Action
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write CZ_ACTION_REQUEST: %w", err)
	}
	return nil
}

// Encode writes the CZ_GLOBAL_MESSAGE packet to w, appending the
// trailing NUL terminator that the parser strips. Mirrors the on-wire
// layout: [2:cmd=0x008c][2:packetLength][n:text+null]. The encoder
// computes packetLength from the message size rather than trusting a
// precomputed field.
func (r CZGlobalMessageRequest) Encode(w io.Writer) error {
	msg := []byte(r.Message)
	// 4 (header) + len(msg) + 1 (NUL terminator).
	total := 4 + len(msg) + 1
	buf := make([]byte, total)
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZGLOBALMESSAGE)
	binary.LittleEndian.PutUint16(buf[2:], uint16(total)) //nolint:gosec // total fits in uint16 for any message under CHAT_SIZE_MAX (≈ 255)
	copy(buf[4:], msg)
	// buf[4+len(msg)] is already 0x00 from make() — the trailing NUL.

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write CZ_GLOBAL_MESSAGE: %w", err)
	}
	return nil
}
