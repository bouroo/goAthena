package packet

import (
	"encoding/binary"
	"fmt"
	"io"
)

// MapAcceptEnterResponse encodes a ZC_ACCEPT_ENTER packet (command 0x02eb,
// active for PACKETVER < 20141022 || PACKETVER >= 20160330 — which covers
// Thai Classic 20250604). Layout source: rathena/src/map/packets.hpp:562-571.
//
// Fixed wire length: 13 bytes (int16 packetType + uint32 startTime +
// uint8 posDir[3] + uint8 xSize + uint8 ySize + uint16 font).
//
// The posDir[3] slot uses rAthena's kRO 3-byte packed position encoding
// (clif.cpp:173-178 WBUFPOS). xSize/ySize are written as the literal 5 in
// rAthena's clif.cpp output site (with an "ignored" comment) so callers
// can safely hardcode 5/5 and let the client ignore them.
type MapAcceptEnterResponse struct {
	// StartTime is the map server's monotone tick at the moment of the
	// enter handshake (rathena's `startTime`).
	StartTime uint32
	// PosX is the spawn cell X (cell coordinate).
	PosX int16
	// PosY is the spawn cell Y (cell coordinate).
	PosY int16
	// Dir is the spawn facing direction (0–15 in kRO; the lower 4 bits
	// are packed into posDir[2]).
	Dir uint8
	// XSize is the sprite width hint (rAthena hardcodes 5).
	XSize uint8
	// YSize is the sprite height hint (rAthena hardcodes 5).
	YSize uint8
	// Font is the font ID for the client-side UI overlay.
	Font uint16
}

// Size returns the on-wire byte length that Encode will write (always 13).
func (r MapAcceptEnterResponse) Size() int {
	return sizeZCAcceptEnter
}

// Encode writes the ZC_ACCEPT_ENTER packet to w.
func (r MapAcceptEnterResponse) Encode(w io.Writer) error {
	buf := make([]byte, sizeZCAcceptEnter)
	// int16 packetType = 0x02eb (HeaderZCACCEPTENTER).
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCACCEPTENTER)
	// uint32 startTime at offset 2.
	binary.LittleEndian.PutUint32(buf[2:], r.StartTime)
	// uint8 posDir[3] at offset 6 — kRO 3-byte packed position.
	encodePos(buf[6:9], r.PosX, r.PosY, r.Dir)
	// uint8 xSize at offset 9.
	buf[9] = r.XSize
	// uint8 ySize at offset 10.
	buf[10] = r.YSize
	// uint16 font at offset 11.
	binary.LittleEndian.PutUint16(buf[11:], r.Font)

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write ZC_ACCEPT_ENTER: %w", err)
	}
	return nil
}

// MapRefuseEnterResponse encodes a ZC_REFUSE_ENTER packet (command 0x0074).
// Layout source: rathena/src/map/packets.hpp:585-590.
//
// Fixed wire length: 3 bytes (int16 packetType + uint8 errorCode).
type MapRefuseEnterResponse struct {
	// Error is the 8-bit error code (rAthena's REFUSE_ENTER_* enum).
	Error uint8
}

// Size returns the on-wire byte length that Encode will write (always 3).
func (r MapRefuseEnterResponse) Size() int {
	return sizeZCRefuseEnter
}

// Encode writes the ZC_REFUSE_ENTER packet to w.
func (r MapRefuseEnterResponse) Encode(w io.Writer) error {
	buf := make([]byte, sizeZCRefuseEnter)
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCREFUSEENTER)
	buf[2] = r.Error

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write ZC_REFUSE_ENTER: %w", err)
	}
	return nil
}

// MapNotifyPlayerMoveResponse encodes a ZC_NOTIFY_PLAYERMOVE packet
// (command 0x0087). The server broadcasts this to nearby clients every
// time a player's path is computed, so each peer can interpolate the
// sprite from src to dest. Layout source:
//
//	rathena/src/map/clif.cpp clif_movemoveok (around the moveOk call site)
//		and rathena/src/map/packets.hpp ZC_NOTIFY_PLAYERMOVE.
//
// Fixed wire length: 12 bytes (int16 packetType + uint32 moveStartTime +
// uint8 srcPos[3] + uint8 destPos[3]).
//
// The srcPos[3] and destPos[3] slots use rAthena's kRO 3-byte packed
// position encoding (clif.cpp:173-178 WBUFPOS); the direction byte is
// zero on both ends because the broadcast packet only describes the
// path endpoints, not the per-cell facing.
//
// MoveStartTime is the server's monotone tick at the moment the path
// was accepted — rAthena writes the same value into the local player's
// session so subsequent CZ_REQUEST_MOVE packets can be anti-DoS-checked
// against it.
type MapNotifyPlayerMoveResponse struct {
	// MoveStartTime is the map server's monotone tick at the moment
	// the path was accepted (rathena's `startTime`).
	MoveStartTime uint32
	// SrcX, SrcY is the cell the move started from.
	SrcX, SrcY int16
	// DestX, DestY is the cell the path targets.
	DestX, DestY int16
}

// Size returns the on-wire byte length that Encode will write (always 12).
func (r MapNotifyPlayerMoveResponse) Size() int {
	return sizeZCNotifyPlayerMove
}

// Encode writes the ZC_NOTIFY_PLAYERMOVE packet to w.
func (r MapNotifyPlayerMoveResponse) Encode(w io.Writer) error {
	buf := make([]byte, sizeZCNotifyPlayerMove)
	// int16 packetType = 0x0087 (HeaderZCNOTIFYPLAYERMOVE).
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCNOTIFYPLAYERMOVE)
	// uint32 moveStartTime at offset 2.
	binary.LittleEndian.PutUint32(buf[2:], r.MoveStartTime)
	// uint8 srcPos[3] at offset 6 — kRO 3-byte packed source position.
	encodePos(buf[6:9], r.SrcX, r.SrcY, 0)
	// uint8 destPos[3] at offset 9 — kRO 3-byte packed destination position.
	encodePos(buf[9:12], r.DestX, r.DestY, 0)

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write ZC_NOTIFY_PLAYERMOVE: %w", err)
	}
	return nil
}
