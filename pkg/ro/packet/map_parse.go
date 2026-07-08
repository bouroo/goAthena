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
// is the NUL-terminated text starting at offset 4. The body slice is
// bounded by the embedded packetLength slot at frame[2:4] rather than
// the frame's total length, so a buffered frame carrying trailing
// packets cannot leak bytes from the next packet into the parsed
// message.
//
// Returns a wrapped error if the frame is shorter than 4 bytes, the cmd
// id is not 0x008c, the embedded packetLength is below the header size
// or larger than the frame, or the message body is empty (a chat packet
// with no payload is malformed — the client always NUL-terminates,
// including for the trivial single-character case).
func ParseCZGlobalMessage(frame []byte) (CZGlobalMessageRequest, error) {
	const minFrame = 4
	if len(frame) < minFrame {
		return CZGlobalMessageRequest{}, fmt.Errorf("packet: parse CZ_GLOBAL_MESSAGE: want at least %d bytes, got %d", minFrame, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZGLOBALMESSAGE {
		return CZGlobalMessageRequest{}, fmt.Errorf("packet: parse CZ_GLOBAL_MESSAGE: unexpected cmd 0x%04x", cmd)
	}

	plen := binary.LittleEndian.Uint16(frame[2:4])
	if int(plen) < minFrame {
		return CZGlobalMessageRequest{}, fmt.Errorf("packet: parse CZ_GLOBAL_MESSAGE: packet length %d too short", plen)
	}
	if len(frame) < int(plen) {
		return CZGlobalMessageRequest{}, fmt.Errorf("packet: parse CZ_GLOBAL_MESSAGE: frame length %d shorter than packet length %d", len(frame), plen)
	}

	body := frame[4:plen]
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
	if total > 0xffff {
		return fmt.Errorf("packet: write CZ_GLOBAL_MESSAGE: message too long (%d bytes)", len(msg))
	}
	buf := make([]byte, total)
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZGLOBALMESSAGE)
	binary.LittleEndian.PutUint16(buf[2:], uint16(total))
	copy(buf[4:], msg)
	// buf[4+len(msg)] is already 0x00 from make() — the trailing NUL.

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write CZ_GLOBAL_MESSAGE: %w", err)
	}
	return nil
}

// CZChangeDirRequest is the decoded form of a client → map-server
// CZ_CHANGE_DIRECTION packet (header 0x009b, 5 bytes on the wire).
// Source: rathena/src/map/clif_packetdb.hpp:48
// (`parseable_packet(0x009b,5,clif_parse_ChangeDir,2,4)`) +
// rathena/src/map/clif.cpp:11607-11618 (clif_parse_ChangeDir).
//
// The on-wire shape documented in clif.cpp:11604-11605 is
// `<head dir>.W <dir>.B`. rAthena reads headDir via RFIFOB at offset 2
// (clif.cpp:11613) — the upper byte of the uint16 is reserved on this
// PACKETVER and is forwarded verbatim by the gateway but not used by
// the rAthena handler. Dir is a single byte at offset 4 (RFIFOB at
// clif.cpp:11614).
//
// Direction values follow rAthena's unit.hpp direction enum:
// 0=N, 1=NW, 2=W, 3=SW, 4=S, 5=SE, 6=E, 7=NE (clif.cpp:11571-11578).
// HeadDir values: 0=straight, 1=CW, 2=CCW (clif.cpp:11567-11569).
type CZChangeDirRequest struct {
	// HeadDir is the head-facing selector (rAthena's `headdir`,
	// uint16 on the wire; only the low byte is consumed by the
	// rAthena handler at clif.cpp:11613).
	HeadDir uint16
	// Dir is the body-direction selector (rAthena's `dir`, uint8 at
	// wire offset 4 — see clif.cpp:11571-11578 for the value table).
	Dir uint8
}

// ParseCZChangeDir decodes a CZ_CHANGE_DIRECTION frame. The frame must
// carry cmd 0x009b and contain 5 bytes.
//
// Returns a wrapped error naming the byte count if the frame is short,
// or naming the unexpected cmd id if the header is not 0x009b.
func ParseCZChangeDir(frame []byte) (CZChangeDirRequest, error) {
	if len(frame) < sizeCZChangeDir {
		return CZChangeDirRequest{}, fmt.Errorf("packet: parse CZ_CHANGE_DIRECTION: want at least %d bytes, got %d", sizeCZChangeDir, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZCHANGEDIR {
		return CZChangeDirRequest{}, fmt.Errorf("packet: parse CZ_CHANGE_DIRECTION: unexpected cmd 0x%04x", cmd)
	}
	return CZChangeDirRequest{
		HeadDir: binary.LittleEndian.Uint16(frame[2:4]),
		Dir:     frame[4],
	}, nil
}

// Encode writes the CZ_CHANGE_DIRECTION packet to w. Mirrors the
// on-wire layout: [2:cmd=0x009b][2:headDir uint16][1:dir uint8] = 5
// bytes. The encoder writes both bytes of the uint16 headDir so callers
// that supply a non-zero upper byte (out of scope for the default
// PACKETVER but allowed by the wire format) see their value preserved
// on the round-trip.
func (r CZChangeDirRequest) Encode(w io.Writer) error {
	var buf [sizeCZChangeDir]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZCHANGEDIR)
	binary.LittleEndian.PutUint16(buf[2:], r.HeadDir)
	buf[4] = r.Dir
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write CZ_CHANGE_DIRECTION: %w", err)
	}
	return nil
}

// CZReqEmotionRequest is the decoded form of a client → map-server
// CZ_REQ_EMOTION packet (header 0x00bf, 3 bytes on the wire). Source:
// rathena/src/map/packets.hpp:1406-1410 (`PACKET_CZ_REQ_EMOTION {
// int16 packetType; uint8 emotion_type }`) +
// rathena/src/map/clif.cpp:11623-11668 (clif_parse_Emotion).
//
// The emotion byte is rAthena's emotion_type enum (emap.hpp in
// rathena). rAthena rejects values >= ET_MAX at clif.cpp:11630; the
// goAthena parser preserves the byte verbatim so the dispatcher can
// apply its own policy (basic-skill check, flood throttle, ET_MAX
// guard) without losing information.
type CZReqEmotionRequest struct {
	// EmotionType is the emotion selector byte (rAthena's
	// emotion_type enum, e.g. ET_SMILE=1, ET_CRY=2, ET_ANGER=3,
	// ET_SWEAT=4, ET_THROB=5, ET_BLINK=6, ET_OK=7, … — see
	// rathena/src/map/emap.hpp for the full table).
	EmotionType uint8
}

// ParseCZReqEmotion decodes a CZ_REQ_EMOTION frame. The frame must
// carry cmd 0x00bf and contain 3 bytes.
//
// Returns a wrapped error naming the byte count if the frame is short,
// or naming the unexpected cmd id if the header is not 0x00bf.
func ParseCZReqEmotion(frame []byte) (CZReqEmotionRequest, error) {
	if len(frame) < sizeCZReqEmotion {
		return CZReqEmotionRequest{}, fmt.Errorf("packet: parse CZ_REQ_EMOTION: want at least %d bytes, got %d", sizeCZReqEmotion, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZREQEMOTION {
		return CZReqEmotionRequest{}, fmt.Errorf("packet: parse CZ_REQ_EMOTION: unexpected cmd 0x%04x", cmd)
	}
	return CZReqEmotionRequest{
		EmotionType: frame[2],
	}, nil
}

// Encode writes the CZ_REQ_EMOTION packet to w. Mirrors the on-wire
// layout: [2:cmd=0x00bf][1:emotion_type] = 3 bytes.
func (r CZReqEmotionRequest) Encode(w io.Writer) error {
	var buf [sizeCZReqEmotion]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZREQEMOTION)
	buf[2] = r.EmotionType
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write CZ_REQ_EMOTION: %w", err)
	}
	return nil
}

// CZGetCharNameRequestRequest is the decoded form of a client → map-server
// CZ_GETCHARNAMEREQUEST packet (header 0x0094, 6 bytes on the wire).
// Source: rathena/src/map/clif_packetdb.hpp:45
// (`parseable_packet(0x0094,6,clif_parse_GetCharNameRequest,2)`) +
// rathena/src/map/clif.cpp:11469-11503 (clif_parse_GetCharNameRequest).
//
// The on-wire shape is `<GID>.L` — the client sends the GID of the
// entity whose name it wants to look up. rAthena's handler resolves
// the GID via map_id2bl and calls clif_name(sd, bl, SELF) to send
// the full ZC_ACK_REQNAMEALL response.
type CZGetCharNameRequestRequest struct {
	// GID is the entity ID the client wants the name for.
	GID uint32
}

// ParseCZGetCharNameRequest decodes a CZ_GETCHARNAMEREQUEST frame.
// The frame must carry cmd 0x0094 and contain 6 bytes.
//
// Returns a wrapped error naming the byte count if the frame is short,
// or naming the unexpected cmd id if the header is not 0x0094.
func ParseCZGetCharNameRequest(frame []byte) (CZGetCharNameRequestRequest, error) {
	if len(frame) < sizeCZGetCharNameRequest {
		return CZGetCharNameRequestRequest{}, fmt.Errorf("packet: parse CZ_GETCHARNAMEREQUEST: want at least %d bytes, got %d", sizeCZGetCharNameRequest, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZGETCHARNAMEREQUEST {
		return CZGetCharNameRequestRequest{}, fmt.Errorf("packet: parse CZ_GETCHARNAMEREQUEST: unexpected cmd 0x%04x", cmd)
	}
	return CZGetCharNameRequestRequest{
		GID: binary.LittleEndian.Uint32(frame[2:6]),
	}, nil
}

// Encode writes the CZ_GETCHARNAMEREQUEST packet to w. Mirrors the
// on-wire layout: [2:cmd=0x0094][4:GID int32] = 6 bytes.
func (r CZGetCharNameRequestRequest) Encode(w io.Writer) error {
	var buf [sizeCZGetCharNameRequest]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZGETCHARNAMEREQUEST)
	binary.LittleEndian.PutUint32(buf[2:], r.GID)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write CZ_GETCHARNAMEREQUEST: %w", err)
	}
	return nil
}

// CZRestartRequest is the decoded form of a client → map-server
// CZ_RESTART packet (header 0x00b2, 3 bytes on the wire). Source:
// rathena/src/map/clif_packetdb.hpp:61
// (`parseable_packet(0x00b2,3,clif_parse_Restart,2)`) +
// rathena/src/map/clif.cpp:11837-11854 (clif_parse_Restart).
//
// The on-wire shape is `<type>.B` — the client sends a single byte:
// 0x00 = respawn (pc_respawn), 0x01 = return to character select
// (chrif_charselectreq). rAthena's handler branches on this byte
// and either calls pc_respawn or sends the char-select request.
type CZRestartRequest struct {
	// Type is the restart selector byte:
	//   0x00 = respawn at save point
	//   0x01 = return to character select screen
	Type uint8
}

// ParseCZRestart decodes a CZ_RESTART frame. The frame must carry
// cmd 0x00b2 and contain 3 bytes.
//
// Returns a wrapped error naming the byte count if the frame is short,
// or naming the unexpected cmd id if the header is not 0x00b2.
func ParseCZRestart(frame []byte) (CZRestartRequest, error) {
	if len(frame) < sizeCZRestart {
		return CZRestartRequest{}, fmt.Errorf("packet: parse CZ_RESTART: want at least %d bytes, got %d", sizeCZRestart, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCZRESTART {
		return CZRestartRequest{}, fmt.Errorf("packet: parse CZ_RESTART: unexpected cmd 0x%04x", cmd)
	}
	return CZRestartRequest{
		Type: frame[2],
	}, nil
}

// Encode writes the CZ_RESTART packet to w. Mirrors the on-wire
// layout: [2:cmd=0x00b2][1:type uint8] = 3 bytes.
func (r CZRestartRequest) Encode(w io.Writer) error {
	var buf [sizeCZRestart]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderCZRESTART)
	buf[2] = r.Type
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write CZ_RESTART: %w", err)
	}
	return nil
}
