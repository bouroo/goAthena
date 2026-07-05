//go:build unit

package net

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/bouroo/goAthena/pkg/ro/crypto"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// PACKETVER 20110817 map-server keys (rathena/src/map/clif_obfuscation.hpp:23).
const (
	testKey0       = uint32(0x053D5CED)
	testKey1       = uint32(0x3DED6DED)
	testKey2       = uint32(0x6DED6DED)
	testFirstCmd   = uint16(0x0072) // WantToConnection
	testFirstSize  = 19
	testSecondCmd  = uint16(0x0085) // arbitrary known packet id we can look up
	testSecondSize = 3
	testThirdCmd   = uint16(0x0090)
	testThirdSize  = 6
)

func newLoginDBWithExtra(t *testing.T) *packet.DB {
	t.Helper()
	db := packet.NewLoginServerDB()

	// Fabricated map-side entries used to validate post-first-packet
	// session decoding. Lengths are intentional stable small values.
	db.Register(packet.Definition{ID: testSecondCmd, Name: "TEST_SECOND", Length: testSecondSize})
	db.Register(packet.Definition{ID: testThirdCmd, Name: "TEST_THIRD_VAR", Length: packet.VariableLength})
	db.Register(packet.Definition{ID: 0x0001, Name: "TEST_FIRST", Length: testFirstSize})
	return db
}

func newMapDB(t *testing.T) *packet.DB {
	t.Helper()
	db := newLoginDBWithExtra(t)
	db.Register(packet.Definition{ID: testFirstCmd, Name: "CZ_ENTER", Length: testFirstSize})
	return db
}

// putCmd writes a little-endian uint16 into b[0:2].
func putCmd(b []byte, cmd uint16) {
	binary.LittleEndian.PutUint16(b[0:2], cmd)
}

func TestNext_PublicSurface(t *testing.T) {
	db := packet.NewLoginServerDB()
	dec := NewLoginDecoder(db)
	if got := dec.Buffered(); got != 0 {
		t.Fatalf("Buffered() on fresh decoder = %d, want 0", got)
	}
	dec.Feed([]byte{0x64, 0x00})
	if got := dec.Buffered(); got != 2 {
		t.Fatalf("Buffered() after Feed(2 bytes) = %d, want 2", got)
	}
}

func TestNext_LoginFixedLength(t *testing.T) {
	dec := NewLoginDecoder(packet.NewLoginServerDB())
	frame := make([]byte, packetVariable(t, packet.HeaderCALOGIN))
	putCmd(frame, packet.HeaderCALOGIN)
	dec.Feed(frame)

	cmd, got, err := dec.Next()
	if err != nil {
		t.Fatalf("Next() err = %v", err)
	}
	if cmd != packet.HeaderCALOGIN {
		t.Fatalf("cmd = 0x%04x, want 0x%04x", cmd, packet.HeaderCALOGIN)
	}
	if len(got) != len(frame) {
		t.Fatalf("len(frame) = %d, want %d", len(got), len(frame))
	}
	if !bytes.Equal(got, frame) {
		t.Fatalf("frame mismatch")
	}
}

func TestNext_LoginVariableLength(t *testing.T) {
	db := packet.NewLoginServerDB()
	dec := NewLoginDecoder(db)

	const payloadLen = 32
	totalLen := 4 + payloadLen
	frame := make([]byte, totalLen)
	putCmd(frame, packet.HeaderACACCEPTLOGIN)
	binary.LittleEndian.PutUint16(frame[2:4], uint16(totalLen))
	for i := 4; i < totalLen; i++ {
		frame[i] = byte(i)
	}
	dec.Feed(frame)

	cmd, got, err := dec.Next()
	if err != nil {
		t.Fatalf("Next() err = %v", err)
	}
	if cmd != packet.HeaderACACCEPTLOGIN {
		t.Fatalf("cmd = 0x%04x, want 0x%04x", cmd, packet.HeaderACACCEPTLOGIN)
	}
	if len(got) != totalLen {
		t.Fatalf("len(frame) = %d, want %d", len(got), totalLen)
	}
	if wireLen := binary.LittleEndian.Uint16(got[2:4]); wireLen != uint16(totalLen) {
		t.Fatalf("wireLen = %d, want %d", wireLen, totalLen)
	}
}

func TestNext_LoginPartial(t *testing.T) {
	dec := NewLoginDecoder(packet.NewLoginServerDB())
	frame := make([]byte, packetVariable(t, packet.HeaderCALOGIN))
	putCmd(frame, packet.HeaderCALOGIN)

	dec.Feed(frame[:10])
	if _, _, err := dec.Next(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Next() after 10 bytes err = %v, want ErrIncomplete", err)
	}
	if got := dec.Buffered(); got != 10 {
		t.Fatalf("Buffered() after partial Feed = %d, want 10", got)
	}

	dec.Feed(frame[10:])
	cmd, got, err := dec.Next()
	if err != nil {
		t.Fatalf("Next() after full Feed err = %v", err)
	}
	if cmd != packet.HeaderCALOGIN {
		t.Fatalf("cmd = 0x%04x, want 0x%04x", cmd, packet.HeaderCALOGIN)
	}
	if len(got) != len(frame) {
		t.Fatalf("len(frame) = %d, want %d", len(got), len(frame))
	}
}

func TestNext_LoginMultiplePackets(t *testing.T) {
	dec := NewLoginDecoder(packet.NewLoginServerDB())

	caLoginSize := packetVariable(t, packet.HeaderCALOGIN)
	reqHashSize := packetVariable(t, packet.HeaderCAREQHASH)

	buf := make([]byte, 0, caLoginSize+reqHashSize+10)
	first := make([]byte, caLoginSize)
	putCmd(first, packet.HeaderCALOGIN)
	buf = append(buf, first...)
	second := make([]byte, reqHashSize)
	putCmd(second, packet.HeaderCAREQHASH)
	buf = append(buf, second...)
	buf = append(buf, 0x64) // single stray byte — not enough for a header

	dec.Feed(buf)

	cmd1, got1, err := dec.Next()
	if err != nil {
		t.Fatalf("Next #1 err = %v", err)
	}
	if cmd1 != packet.HeaderCALOGIN || len(got1) != caLoginSize {
		t.Fatalf("Next #1 cmd=0x%04x len=%d, want 0x%04x len=%d", cmd1, len(got1), packet.HeaderCALOGIN, caLoginSize)
	}

	cmd2, got2, err := dec.Next()
	if err != nil {
		t.Fatalf("Next #2 err = %v", err)
	}
	if cmd2 != packet.HeaderCAREQHASH || len(got2) != reqHashSize {
		t.Fatalf("Next #2 cmd=0x%04x len=%d, want 0x%04x len=%d", cmd2, len(got2), packet.HeaderCAREQHASH, reqHashSize)
	}

	if _, _, err := dec.Next(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Next #3 err = %v, want ErrIncomplete", err)
	}
}

func TestNext_LoginUnknownPacket(t *testing.T) {
	dec := NewLoginDecoder(packet.NewLoginServerDB())
	frame := []byte{0xAA, 0xBB, 0x01, 0x02}
	dec.Feed(frame)

	_, _, err := dec.Next()
	if !errors.Is(err, ErrUnknownPacket) {
		t.Fatalf("Next() err = %v, want ErrUnknownPacket", err)
	}
	if len(dec.buf) != len(frame) {
		t.Fatalf("buffer mutated on unknown packet: got %d want %d", len(dec.buf), len(frame))
	}
}

func TestNext_MapFirstPacketDeobfuscation(t *testing.T) {
	db := newMapDB(t)
	dec := NewMapDecoder(db, testKey0, testKey1, testKey2)

	rawFirst := make([]byte, testFirstSize)
	rawCmd := firstPacketRawFromPlain(testKey0, testKey1, testKey2, testFirstCmd)
	putCmd(rawFirst, rawCmd)

	dec.Feed(rawFirst)
	cmd, frame, err := dec.Next()
	if err != nil {
		t.Fatalf("Next() err = %v", err)
	}
	if cmd != testFirstCmd {
		t.Fatalf("decoded cmd = 0x%04x, want 0x%04x", cmd, testFirstCmd)
	}
	if got := binary.LittleEndian.Uint16(frame[0:2]); got != testFirstCmd {
		t.Fatalf("frame[0:2] = 0x%04x, want patched decoded cmd 0x%04x", got, testFirstCmd)
	}
	if len(frame) != testFirstSize {
		t.Fatalf("len(frame) = %d, want %d", len(frame), testFirstSize)
	}
	if !dec.firstDone {
		t.Fatalf("firstDone not set after first packet")
	}
	if dec.obf == nil {
		t.Fatalf("obf not initialized after first packet")
	}
}

func TestNext_MapSubsequentPacketUsesSessionObfuscator(t *testing.T) {
	db := newMapDB(t)
	dec := NewMapDecoder(db, testKey0, testKey1, testKey2)

	rawFirst := make([]byte, testFirstSize)
	putCmd(rawFirst, firstPacketRawFromPlain(testKey0, testKey1, testKey2, testFirstCmd))
	dec.Feed(rawFirst)
	if _, _, err := dec.Next(); err != nil {
		t.Fatalf("first Next err = %v", err)
	}

	sessionObf := crypto.NewObfuscator(testKey0, testKey1, testKey2)
	obfuscatedCmd := sessionObf.Encode(testSecondCmd)

	rawSecond := make([]byte, testSecondSize)
	putCmd(rawSecond, obfuscatedCmd)
	dec.Feed(rawSecond)

	cmd, frame, err := dec.Next()
	if err != nil {
		t.Fatalf("Next #2 err = %v", err)
	}
	if cmd != testSecondCmd {
		t.Fatalf("decoded cmd = 0x%04x, want 0x%04x", cmd, testSecondCmd)
	}
	if got := binary.LittleEndian.Uint16(frame[0:2]); got != testSecondCmd {
		t.Fatalf("frame[0:2] = 0x%04x, want 0x%04x", got, testSecondCmd)
	}
}

func TestNext_MapDisabledKeys(t *testing.T) {
	db := newMapDB(t)
	dec := NewMapDecoder(db, 0, 0, 0)

	rawFirst := make([]byte, testFirstSize)
	putCmd(rawFirst, testFirstCmd)
	dec.Feed(rawFirst)

	cmd, frame, err := dec.Next()
	if err != nil {
		t.Fatalf("Next() err = %v", err)
	}
	if cmd != testFirstCmd {
		t.Fatalf("decoded cmd = 0x%04x, want 0x%04x", cmd, testFirstCmd)
	}
	if got := binary.LittleEndian.Uint16(frame[0:2]); got != testFirstCmd {
		t.Fatalf("frame[0:2] = 0x%04x, want 0x%04x", got, testFirstCmd)
	}
	if !dec.firstDone {
		t.Fatalf("firstDone not set under disabled mode")
	}
	if dec.obf == nil || !dec.obf.Disabled() {
		t.Fatalf("session obfuscator should be disabled when keys are (0,0,0)")
	}
}

func TestNext_MapCmdPatch(t *testing.T) {
	db := newMapDB(t)
	dec := NewMapDecoder(db, testKey0, testKey1, testKey2)

	rawFirst := make([]byte, testFirstSize)
	obfCmd := firstPacketRawFromPlain(testKey0, testKey1, testKey2, testFirstCmd)
	putCmd(rawFirst, obfCmd)
	dec.Feed(rawFirst)

	_, frame, err := dec.Next()
	if err != nil {
		t.Fatalf("Next() err = %v", err)
	}
	got := binary.LittleEndian.Uint16(frame[0:2])
	if got != testFirstCmd {
		t.Fatalf("patched cmd = 0x%04x, want 0x%04x", got, testFirstCmd)
	}
	if got == obfCmd {
		t.Fatalf("frame still contains obfuscated cmd 0x%04x, expected patched decoded cmd 0x%04x", obfCmd, testFirstCmd)
	}
}

func TestNext_FrameIsIndependentCopy(t *testing.T) {
	dec := NewLoginDecoder(packet.NewLoginServerDB())
	frame := make([]byte, packetVariable(t, packet.HeaderCALOGIN))
	putCmd(frame, packet.HeaderCALOGIN)
	dec.Feed(frame)

	_, got, err := dec.Next()
	if err != nil {
		t.Fatalf("Next() err = %v", err)
	}
	got[0] = 0xFF
	if frame[0] == 0xFF {
		t.Fatalf("returned frame aliases internal buffer; mutating got[0] mutated source")
	}
}

func TestNext_FeedCopiesInput(t *testing.T) {
	dec := NewLoginDecoder(packet.NewLoginServerDB())
	frame := make([]byte, packetVariable(t, packet.HeaderCALOGIN))
	putCmd(frame, packet.HeaderCALOGIN)
	dec.Feed(frame)

	for i := range frame {
		frame[i] = 0xCC
	}

	cmd, _, err := dec.Next()
	if err != nil {
		t.Fatalf("Next() err = %v", err)
	}
	if cmd != packet.HeaderCALOGIN {
		t.Fatalf("cmd after caller mutation = 0x%04x, want 0x%04x (Feed should copy)", cmd, packet.HeaderCALOGIN)
	}
}

func TestNext_VariableLengthInvalid(t *testing.T) {
	db := packet.NewLoginServerDB()
	dec := NewLoginDecoder(db)

	// wire length too small (< MinVariableLength=4)
	bad := []byte{0xc4, 0x0a, 0x00, 0x00}
	dec.Feed(bad)
	if _, _, err := dec.Next(); err == nil {
		t.Fatalf("Next() with sub-min wire length returned nil, want error")
	}
}

func TestNext_VariableLengthTooLarge(t *testing.T) {
	db := packet.NewLoginServerDB()
	dec := NewLoginDecoder(db)

	// wire length > MaxFrameSize (32 KiB)
	bad := []byte{0xc4, 0x0a, 0x00, 0x01} // 0x010000 = 65536
	dec.Feed(bad)
	if _, _, err := dec.Next(); err == nil {
		t.Fatalf("Next() with oversized wire length returned nil, want error")
	}
}

// packetVariable looks up the registered on-wire length for a cmd in the
// login-server DB. testFirstCmd and the others we registered ourselves; for
// real login-server cmds we use packet.DB.Length.
func packetVariable(t *testing.T, cmd uint16) int {
	t.Helper()
	switch cmd {
	case testFirstCmd:
		return testFirstSize
	case testSecondCmd:
		return testSecondSize
	}
	db := packet.NewLoginServerDB()
	l, ok := db.Length(cmd)
	if !ok {
		t.Fatalf("packet cmd 0x%04x not registered", cmd)
	}
	if l < 0 {
		t.Fatalf("test helper expects fixed-length packet, got variable for 0x%04x", cmd)
	}
	return l
}
