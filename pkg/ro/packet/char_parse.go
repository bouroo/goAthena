package packet

import (
	"encoding/binary"
	"fmt"
)

// CHEnterRequest is the decoded form of a client → char-server CH_ENTER
// packet (header 0x0065, 17 bytes on the wire). Source: rathena/src/common/
// packets.hpp PACKET_CH_ENTER and rathena/src/char/char_clif.cpp:821-829.
//
// The packed struct contains a reserved uint16 slot between login_id2 and
// sex (2+4+4+4+2+1 bytes); the parser ignores it.
type CHEnterRequest struct {
	// AccountID is the upper 32-bit account ID echoed by the login server.
	AccountID uint32
	// LoginID1 is the upper 32 bits of the session token.
	LoginID1 uint32
	// LoginID2 is the lower 32 bits of the session token.
	LoginID2 uint32
	// Sex is the account sex byte (0x0 female, 0x1 male in kRO).
	Sex uint8
}

// ParseCHEnter parses a full 17-byte CH_ENTER frame (including the 2-byte
// cmd header) into a CHEnterRequest. Returns a wrapped error if the frame
// is not exactly 17 bytes or its cmd header is not HeaderCHENTER (0x0065).
func ParseCHEnter(frame []byte) (CHEnterRequest, error) {
	if len(frame) != sizeCHEnter {
		return CHEnterRequest{}, fmt.Errorf("packet: parse CH_ENTER: want %d bytes, got %d", sizeCHEnter, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCHENTER {
		return CHEnterRequest{}, fmt.Errorf("packet: parse CH_ENTER: unexpected cmd 0x%04x", cmd)
	}

	return CHEnterRequest{
		AccountID: binary.LittleEndian.Uint32(frame[2:6]),
		LoginID1:  binary.LittleEndian.Uint32(frame[6:10]),
		LoginID2:  binary.LittleEndian.Uint32(frame[10:14]),
		Sex:       frame[16],
	}, nil
}

// CHSelectCharRequest is the decoded form of a client → char-server
// CH_SELECT_CHAR packet (header 0x0066, 3 bytes on the wire). Source:
// rathena/src/common/packets.hpp:116-120.
type CHSelectCharRequest struct {
	// Slot is the zero-based character slot index (typically 0–MAX_CHARS-1).
	Slot uint8
}

// ParseCHSelectChar parses a full 3-byte CH_SELECT_CHAR frame (including the
// 2-byte cmd header) into a CHSelectCharRequest. Returns a wrapped error if
// the frame is not exactly 3 bytes or its cmd header is not
// HeaderCHSELECTCHAR (0x0066).
func ParseCHSelectChar(frame []byte) (CHSelectCharRequest, error) {
	if len(frame) != sizeCHSelectChar {
		return CHSelectCharRequest{}, fmt.Errorf("packet: parse CH_SELECT_CHAR: want %d bytes, got %d", sizeCHSelectChar, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCHSELECTCHAR {
		return CHSelectCharRequest{}, fmt.Errorf("packet: parse CH_SELECT_CHAR: unexpected cmd 0x%04x", cmd)
	}

	return CHSelectCharRequest{
		Slot: frame[2],
	}, nil
}
