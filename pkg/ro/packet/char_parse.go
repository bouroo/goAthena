package packet

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
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

// Encode writes the CH_ENTER packet to w, mirroring the on-wire layout
// documented on CHEnterRequest: [2:cmd=0x0065][4:accountID][4:loginID1]
// [4:loginID2][2:reserved=0][1:sex] = 17 bytes. Source:
// rathena/src/common/packets.hpp PACKET_CH_ENTER and
// rathena/src/char/char_clif.cpp:821-829.
//
// The reserved uint16 slot between loginID2 and sex is always written as
// zero — the field is part of the packed struct but carries no payload.
func (r CHEnterRequest) Encode(w io.Writer) error {
	buf := make([]byte, sizeCHEnter)
	// int16 packetType = 0x0065 (HeaderCHENTER).
	binary.LittleEndian.PutUint16(buf[0:], HeaderCHENTER)
	// uint32 accountID at offset 2.
	binary.LittleEndian.PutUint32(buf[2:], r.AccountID)
	// uint32 loginID1 at offset 6.
	binary.LittleEndian.PutUint32(buf[6:], r.LoginID1)
	// uint32 loginID2 at offset 10.
	binary.LittleEndian.PutUint32(buf[10:], r.LoginID2)
	// uint16 reserved at offset 14 — always zero (make() zero-initialized).
	// uint8 sex at offset 16.
	buf[16] = r.Sex

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write CH_ENTER: %w", err)
	}
	return nil
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

// Encode writes the CH_SELECT_CHAR packet to w, mirroring the on-wire layout
// documented on CHSelectCharRequest: [2:cmd=0x0066][1:slot] = 3 bytes.
// Source: rathena/src/common/packets.hpp:116-120.
func (r CHSelectCharRequest) Encode(w io.Writer) error {
	buf := make([]byte, sizeCHSelectChar)
	// int16 packetType = 0x0066 (HeaderCHSELECTCHAR).
	binary.LittleEndian.PutUint16(buf[0:], HeaderCHSELECTCHAR)
	// uint8 slot at offset 2.
	buf[2] = r.Slot

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write CH_SELECT_CHAR: %w", err)
	}
	return nil
}

// CHMakeCharRequest is the decoded form of a client → char-server CH_MAKE_CHAR
// packet (header 0x0a39, 36 bytes). Source: rathena/src/common/packets.hpp
// PACKET_CH_MAKE_CHAR (PACKETVER >= 20151001 branch, active for 20250604) and
// rathena/src/char/char_clif.cpp chclif_parse_createnewchar:1265-1284.
//
// For this packetver the client does not send stats (str/agi/vit/int/dex/luk);
// the server assigns them (char_make_new_char uses 1 for each).
type CHMakeCharRequest struct {
	// Name is the requested character name (NAME_LENGTH = 24 on the wire).
	Name string
	// Slot is the target character slot (char_num), 0..char_slots-1.
	Slot uint8
	// HairColor is the requested hair-color palette (hair_color).
	HairColor uint16
	// HairStyle is the requested hair style (hairstyle); stored in the char.hair
	// column by char_make_new_char.
	HairStyle uint16
	// Job is the requested starting job (JOB_NOVICE, JOB_SUMMONER, …).
	Job uint32
	// Sex is the requested sex byte (0 = female, 1 = male).
	Sex uint8
}

// ParseCHMakeChar parses a full 36-byte CH_MAKE_CHAR frame (including the
// 2-byte cmd header) into a CHMakeCharRequest. Returns a wrapped error if the
// frame is not exactly 36 bytes or its cmd header is not HeaderCHMAKECHAR
// (0x0a39).
func ParseCHMakeChar(frame []byte) (CHMakeCharRequest, error) {
	if len(frame) != sizeCHMakeChar {
		return CHMakeCharRequest{}, fmt.Errorf("packet: parse CH_MAKE_CHAR: want %d bytes, got %d", sizeCHMakeChar, len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCHMAKECHAR {
		return CHMakeCharRequest{}, fmt.Errorf("packet: parse CH_MAKE_CHAR: unexpected cmd 0x%04x", cmd)
	}

	return CHMakeCharRequest{
		Name:      string(bytes.TrimRight(frame[2:26], "\x00")),
		Slot:      frame[26],
		HairColor: binary.LittleEndian.Uint16(frame[27:29]),
		HairStyle: binary.LittleEndian.Uint16(frame[29:31]),
		Job:       binary.LittleEndian.Uint32(frame[31:35]),
		Sex:       frame[35],
	}, nil
}

// Encode writes the CH_MAKE_CHAR packet to w, mirroring the on-wire layout
// documented on CHMakeCharRequest: [2:cmd=0x0a39][24:name][1:slot]
// [2:hair_color][2:hair_style][4:job][1:sex] = 36 bytes. Source:
// rathena/src/common/packets.hpp:123-132. The name field is zero-padded to
// NAME_LENGTH (24) and the request must fit or Encode returns an error.
func (r CHMakeCharRequest) Encode(w io.Writer) error {
	if err := r.validate(); err != nil {
		return err
	}
	buf := make([]byte, sizeCHMakeChar)
	// int16 packetType = 0x0a39 (HeaderCHMAKECHAR).
	binary.LittleEndian.PutUint16(buf[0:], HeaderCHMAKECHAR)
	// char name[24] at offset 2 — zero-padded by writeFixedString.
	writeFixedString(buf[2:26], r.Name)
	// uint8 slot at offset 26.
	buf[26] = r.Slot
	// uint16 hair_color at offset 27.
	binary.LittleEndian.PutUint16(buf[27:29], r.HairColor)
	// uint16 hair_style at offset 29.
	binary.LittleEndian.PutUint16(buf[29:31], r.HairStyle)
	// uint32 job at offset 31.
	binary.LittleEndian.PutUint32(buf[31:35], r.Job)
	// uint8 sex at offset 35.
	buf[35] = r.Sex

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write CH_MAKE_CHAR: %w", err)
	}
	return nil
}

func (r CHMakeCharRequest) validate() error {
	if len(r.Name) > nameSlot {
		return fmt.Errorf("packet: encode CH_MAKE_CHAR: %w", ErrCharNameTooLong)
	}
	return nil
}

// CHDeleteChar3ReservedRequest is decoded form of client → char-server
// CH_DELETE_CHAR3_RESERVED packet (header 0x0827, 6 bytes on wire).
// Source: rathena/src/common/packets.hpp:471-476, char_clif.cpp:530-610.
//
// The client sends this when the user clicks "delete" on a character slot.
// result semantics: 0 = already queued (date=0), 1 = OK, 3 = not found,
// 2/4/5 = restrictions (we do not emit these — no party/guild).
type CHDeleteChar3ReservedRequest struct {
	// CID is the character ID the client wants to delete.
	CID uint32
}

// ParseCHDeleteChar3Reserved parses a full 6-byte CH_DELETE_CHAR3_RESERVED
// frame (including the 2-byte cmd header). Returns wrapped error if frame
// is not exactly 6 bytes or cmd header is not HeaderCHDELETECHAR3RESERVED.
func ParseCHDeleteChar3Reserved(frame []byte) (CHDeleteChar3ReservedRequest, error) {
	if len(frame) != 6 { //nolint:gosec // G115: constant 6 fits in int.
		return CHDeleteChar3ReservedRequest{}, fmt.Errorf("packet: parse CH_DELETE_CHAR3_RESERVED: want 6 bytes, got %d", len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCHDELETECHAR3RESERVED {
		return CHDeleteChar3ReservedRequest{}, fmt.Errorf("packet: parse CH_DELETE_CHAR3_RESERVED: unexpected cmd 0x%04x", cmd)
	}
	return CHDeleteChar3ReservedRequest{
		CID: binary.LittleEndian.Uint32(frame[2:6]),
	}, nil
}

// CHDeleteChar3Request is decoded form of client → char-server CH_DELETE_CHAR3
// packet (header 0x0829, 12 bytes on wire). Source:
// rathena/src/common/packets.hpp:481-486, char_clif.cpp:650-700.
//
// The client sends this after the user types a birthdate (YYMMDD, 6 raw bytes)
// to confirm final deletion.
type CHDeleteChar3Request struct {
	// CID is the character ID to delete.
	CID uint32
	// Birthdate is the 6-byte raw "YYMMDD" birthdate sent by the client.
	Birthdate [6]byte
}

// ParseCHDeleteChar3 parses a full 12-byte CH_DELETE_CHAR3 frame
// (including the 2-byte cmd header). Returns wrapped error if frame
// is not exactly 12 bytes or cmd header is not HeaderCHDELETECHAR3.
func ParseCHDeleteChar3(frame []byte) (CHDeleteChar3Request, error) {
	if len(frame) != 12 { //nolint:gosec // G115: constant 12 fits in int.
		return CHDeleteChar3Request{}, fmt.Errorf("packet: parse CH_DELETE_CHAR3: want 12 bytes, got %d", len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCHDELETECHAR3 {
		return CHDeleteChar3Request{}, fmt.Errorf("packet: parse CH_DELETE_CHAR3: unexpected cmd 0x%04x", cmd)
	}
	return CHDeleteChar3Request{
		CID:       binary.LittleEndian.Uint32(frame[2:6]),
		Birthdate: [6]byte(frame[6:12]),
	}, nil
}

// CHDeleteChar3CancelRequest is decoded form of client → char-server
// CH_DELETE_CHAR3_CANCEL packet (header 0x082b, 6 bytes on wire).
// Source: rathena/src/common/packets.hpp:491-496, char_clif.cpp:750-790.
type CHDeleteChar3CancelRequest struct {
	// CID is the character ID whose pending deletion to cancel.
	CID uint32
}

// ParseCHDeleteChar3Cancel parses a full 6-byte CH_DELETE_CHAR3_CANCEL
// frame (including the 2-byte cmd header). Returns wrapped error if frame
// is not exactly 6 bytes or cmd header is not HeaderCHDELETECHAR3CANCEL.
func ParseCHDeleteChar3Cancel(frame []byte) (CHDeleteChar3CancelRequest, error) {
	if len(frame) != 6 { //nolint:gosec // G115: constant 6 fits in int.
		return CHDeleteChar3CancelRequest{}, fmt.Errorf("packet: parse CH_DELETE_CHAR3_CANCEL: want 6 bytes, got %d", len(frame))
	}
	if cmd := binary.LittleEndian.Uint16(frame[0:2]); cmd != HeaderCHDELETECHAR3CANCEL {
		return CHDeleteChar3CancelRequest{}, fmt.Errorf("packet: parse CH_DELETE_CHAR3_CANCEL: unexpected cmd 0x%04x", cmd)
	}
	return CHDeleteChar3CancelRequest{
		CID: binary.LittleEndian.Uint32(frame[2:6]),
	}, nil
}
