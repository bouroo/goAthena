//go:build unit

package packet

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestMapAcceptEnterResponse_Size(t *testing.T) {
	t.Parallel()

	var r MapAcceptEnterResponse
	if got, want := r.Size(), sizeZCAcceptEnter; got != want {
		t.Errorf("Size() = %d, want %d", got, want)
	}
}

func TestMapAcceptEnterResponse_Encode(t *testing.T) {
	t.Parallel()

	resp := MapAcceptEnterResponse{
		StartTime: 1000,
		PosX:      150,
		PosY:      200,
		Dir:       3,
		XSize:     5,
		YSize:     5,
		Font:      0,
	}

	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode() unexpected error: %v", err)
	}
	got := buf.Bytes()

	const wantLen = 13
	if len(got) != wantLen {
		t.Fatalf("len(got) = %d, want %d", len(got), wantLen)
	}

	if got[0] != 0xeb || got[1] != 0x02 {
		t.Errorf("header bytes = %02x %02x, want eb 02 (LE 0x02eb)", got[0], got[1])
	}

	if startTime := binary.LittleEndian.Uint32(got[2:6]); startTime != 1000 {
		t.Errorf("startTime = %d, want 1000", startTime)
	}

	// posDir[3] at offset 6 must match encodePos(150, 200, 3) exactly.
	var wantPos [3]byte
	encodePos(wantPos[:], 150, 200, 3)
	if !bytes.Equal(got[6:9], wantPos[:]) {
		t.Errorf("posDir = %v, want %v", got[6:9], wantPos[:])
	}
	// Spot the y-coord decodes cleanly to 200.
	if _, gotY, _ := decodePos(got[6:9]); gotY != 200 {
		t.Errorf("decoded posDir Y = %d, want 200", gotY)
	}

	if got[9] != 5 {
		t.Errorf("xSize byte at [9] = %d, want 5", got[9])
	}
	if got[10] != 5 {
		t.Errorf("ySize byte at [10] = %d, want 5", got[10])
	}

	if font := binary.LittleEndian.Uint16(got[11:13]); font != 0 {
		t.Errorf("font = %d, want 0", font)
	}
}

func TestMapAcceptEnterResponse_Encode_FontAndSizeFields(t *testing.T) {
	t.Parallel()

	// Cover the non-zero font + non-default sizes path so the field offsets
	// and little-endian writes are exercised end-to-end.
	resp := MapAcceptEnterResponse{
		StartTime: 0xdeadbeef,
		PosX:      100,
		PosY:      200,
		Dir:       0,
		XSize:     7,
		YSize:     9,
		Font:      0x0ff0,
	}

	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode() unexpected error: %v", err)
	}
	got := buf.Bytes()

	if got[9] != 7 || got[10] != 9 {
		t.Errorf("xSize/ySize = %d/%d, want 7/9", got[9], got[10])
	}
	if font := binary.LittleEndian.Uint16(got[11:13]); font != 0x0ff0 {
		t.Errorf("font = 0x%04x, want 0x0ff0", font)
	}
	if startTime := binary.LittleEndian.Uint32(got[2:6]); startTime != 0xdeadbeef {
		t.Errorf("startTime = 0x%08x, want 0xdeadbeef", startTime)
	}
}

func TestMapRefuseEnterResponse_Size(t *testing.T) {
	t.Parallel()

	var r MapRefuseEnterResponse
	if got, want := r.Size(), sizeZCRefuseEnter; got != want {
		t.Errorf("Size() = %d, want %d", got, want)
	}
}

func TestMapRefuseEnterResponse_Encode(t *testing.T) {
	t.Parallel()

	resp := MapRefuseEnterResponse{Error: 2}

	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode() unexpected error: %v", err)
	}
	got := buf.Bytes()

	const wantLen = 3
	if len(got) != wantLen {
		t.Fatalf("len(got) = %d, want %d", len(got), wantLen)
	}

	if got[0] != 0x74 || got[1] != 0x00 {
		t.Errorf("header bytes = %02x %02x, want 74 00 (LE 0x0074)", got[0], got[1])
	}
	if got[2] != 2 {
		t.Errorf("error byte at [2] = %d, want 2", got[2])
	}
}

func TestMapNotifyPlayerMoveResponse_Size(t *testing.T) {
	t.Parallel()

	var r MapNotifyPlayerMoveResponse
	if got, want := r.Size(), sizeZCNotifyPlayerMove; got != want {
		t.Errorf("Size() = %d, want %d", got, want)
	}
}

func TestMapNotifyPlayerMoveResponse_Encode(t *testing.T) {
	t.Parallel()

	resp := MapNotifyPlayerMoveResponse{
		MoveStartTime: 0x12345678,
		SrcX:          150,
		SrcY:          200,
		DestX:         165,
		DestY:         210,
	}

	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode() unexpected error: %v", err)
	}
	got := buf.Bytes()

	const wantLen = 12
	if len(got) != wantLen {
		t.Fatalf("len(got) = %d, want %d", len(got), wantLen)
	}

	// Opcode at [0:2] = 0x0087 LE.
	if got[0] != 0x87 || got[1] != 0x00 {
		t.Errorf("header bytes = %02x %02x, want 87 00 (LE 0x0087)", got[0], got[1])
	}

	// moveStartTime at [2:6] = uint32 LE.
	if startTime := binary.LittleEndian.Uint32(got[2:6]); startTime != 0x12345678 {
		t.Errorf("moveStartTime = 0x%x, want 0x12345678", startTime)
	}

	// srcPos[3] at [6:9] must round-trip through encodePos/decodePos.
	gotSrcX, gotSrcY, gotSrcDir := decodePos(got[6:9])
	if gotSrcX != 150 || gotSrcY != 200 || gotSrcDir != 0 {
		t.Errorf("srcPos unpacked = (%d, %d, dir=%d), want (150, 200, 0); bytes = %x",
			gotSrcX, gotSrcY, gotSrcDir, got[6:9])
	}
	var wantSrc [3]byte
	encodePos(wantSrc[:], 150, 200, 0)
	if !bytes.Equal(got[6:9], wantSrc[:]) {
		t.Errorf("srcPos = %v, want %v", got[6:9], wantSrc[:])
	}

	// destPos[3] at [9:12] must round-trip too.
	gotDestX, gotDestY, gotDestDir := decodePos(got[9:12])
	if gotDestX != 165 || gotDestY != 210 || gotDestDir != 0 {
		t.Errorf("destPos unpacked = (%d, %d, dir=%d), want (165, 210, 0); bytes = %x",
			gotDestX, gotDestY, gotDestDir, got[9:12])
	}
	var wantDest [3]byte
	encodePos(wantDest[:], 165, 210, 0)
	if !bytes.Equal(got[9:12], wantDest[:]) {
		t.Errorf("destPos = %v, want %v", got[9:12], wantDest[:])
	}
}

func TestMapNotifyPlayerMoveResponse_Encode_ZeroValues(t *testing.T) {
	t.Parallel()

	// Zero-value encode is well-defined (an entity that hasn't moved
	// yet sends a degenerate ZC_NOTIFY_PLAYERMOVE with src == dest ==
	// (0,0)); the field offset layout must still produce 12 bytes.
	resp := MapNotifyPlayerMoveResponse{}

	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode() unexpected error: %v", err)
	}
	got := buf.Bytes()

	if len(got) != 12 {
		t.Fatalf("zero-value len = %d, want 12", len(got))
	}
	if got[0] != 0x87 || got[1] != 0x00 {
		t.Errorf("header = %02x %02x, want 87 00", got[0], got[1])
	}
	if startTime := binary.LittleEndian.Uint32(got[2:6]); startTime != 0 {
		t.Errorf("moveStartTime = %d, want 0", startTime)
	}
	gotX, gotY, _ := decodePos(got[6:9])
	if gotX != 0 || gotY != 0 {
		t.Errorf("srcPos = (%d, %d), want (0, 0)", gotX, gotY)
	}
}

func TestSpawnUnitResponse_Size(t *testing.T) {
	t.Parallel()

	var r SpawnUnitResponse
	if got, want := r.Size(), sizeZCSpawnUnit; got != want {
		t.Errorf("Size() = %d, want %d", got, want)
	}
}

func TestSpawnUnitResponse_Encode(t *testing.T) {
	t.Parallel()

	resp := SpawnUnitResponse{
		ObjectType:  0, // PC
		AID:         4242,
		GID:         4242,
		Speed:       150,
		BodyState:   0,
		HealthState: 0,
		EffectState: 0,
		Job:         1, // swordsman
		Head:        5,
		Weapon:      0x00010002,
		Shield:      0x00010003,
		Accessory:   0x0004,
		Accessory2:  0x0005,
		Accessory3:  0x0006,
		HeadPalette: 7,
		BodyPalette: 8,
		HeadDir:     0,
		Robe:        9,
		GUID:        0,
		GEmblemVer:  0,
		Honor:       0,
		Virtue:      0,
		IsPKModeON:  0,
		Sex:         1, // male
		PosX:        150,
		PosY:        200,
		Dir:         3,
		XSize:       5,
		YSize:       5,
		CLevel:      50,
		Font:        0,
		MaxHP:       1000,
		HP:          1000,
		IsBoss:      0,
		Body:        0,
		Name:        "Tester",
	}

	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode() unexpected error: %v", err)
	}
	got := buf.Bytes()

	// 1. Total size must be exactly 107 bytes.
	const wantLen = 107
	if len(got) != wantLen {
		t.Fatalf("len(got) = %d, want %d", len(got), wantLen)
	}

	// 2. Opcode at [0:2] = 0x09fe LE.
	if got[0] != 0xfe || got[1] != 0x09 {
		t.Errorf("opcode bytes = %02x %02x, want fe 09 (LE 0x09fe)", got[0], got[1])
	}

	// 3. PacketLength at [2:4] = 107 LE.
	if plen := binary.LittleEndian.Uint16(got[2:4]); plen != 107 {
		t.Errorf("packetLength = %d, want 107", plen)
	}

	// 4. Spot-check all field offsets with byte-exact values.
	if got[4] != 0 {
		t.Errorf("objectType = %d, want 0 (PC)", got[4])
	}
	if aid := binary.LittleEndian.Uint32(got[5:9]); aid != 4242 {
		t.Errorf("AID = %d, want 4242", aid)
	}
	if gid := binary.LittleEndian.Uint32(got[9:13]); gid != 4242 {
		t.Errorf("GID = %d, want 4242", gid)
	}
	if speed := binary.LittleEndian.Uint16(got[13:15]); speed != 150 {
		t.Errorf("speed = %d, want 150", speed)
	}
	if bodyState := binary.LittleEndian.Uint16(got[15:17]); bodyState != 0 {
		t.Errorf("bodyState = %d, want 0", bodyState)
	}
	if healthState := binary.LittleEndian.Uint16(got[17:19]); healthState != 0 {
		t.Errorf("healthState = %d, want 0", healthState)
	}
	if effectState := binary.LittleEndian.Uint32(got[19:23]); effectState != 0 {
		t.Errorf("effectState = %d, want 0", effectState)
	}
	if job := binary.LittleEndian.Uint16(got[23:25]); job != 1 {
		t.Errorf("job = %d, want 1", job)
	}
	if head := binary.LittleEndian.Uint16(got[25:27]); head != 5 {
		t.Errorf("head = %d, want 5", head)
	}
	if weapon := binary.LittleEndian.Uint32(got[27:31]); weapon != 0x00010002 {
		t.Errorf("weapon = 0x%08x, want 0x00010002", weapon)
	}
	if shield := binary.LittleEndian.Uint32(got[31:35]); shield != 0x00010003 {
		t.Errorf("shield = 0x%08x, want 0x00010003", shield)
	}
	if acc := binary.LittleEndian.Uint16(got[35:37]); acc != 0x0004 {
		t.Errorf("accessory = 0x%04x, want 0x0004", acc)
	}
	if acc2 := binary.LittleEndian.Uint16(got[37:39]); acc2 != 0x0005 {
		t.Errorf("accessory2 = 0x%04x, want 0x0005", acc2)
	}
	if acc3 := binary.LittleEndian.Uint16(got[39:41]); acc3 != 0x0006 {
		t.Errorf("accessory3 = 0x%04x, want 0x0006", acc3)
	}
	if hp := binary.LittleEndian.Uint16(got[41:43]); hp != 7 {
		t.Errorf("headPalette = %d, want 7", hp)
	}
	if bp := binary.LittleEndian.Uint16(got[43:45]); bp != 8 {
		t.Errorf("bodyPalette = %d, want 8", bp)
	}
	if hd := binary.LittleEndian.Uint16(got[45:47]); hd != 0 {
		t.Errorf("headDir = %d, want 0", hd)
	}
	if robe := binary.LittleEndian.Uint16(got[47:49]); robe != 9 {
		t.Errorf("robe = %d, want 9", robe)
	}
	if guid := binary.LittleEndian.Uint32(got[49:53]); guid != 0 {
		t.Errorf("GUID = %d, want 0", guid)
	}
	if gev := binary.LittleEndian.Uint16(got[53:55]); gev != 0 {
		t.Errorf("GEmblemVer = %d, want 0", gev)
	}
	if honor := binary.LittleEndian.Uint16(got[55:57]); honor != 0 {
		t.Errorf("honor = %d, want 0", honor)
	}
	if virtue := binary.LittleEndian.Uint32(got[57:61]); virtue != 0 {
		t.Errorf("virtue = %d, want 0", virtue)
	}
	if got[61] != 0 {
		t.Errorf("isPKModeON = %d, want 0", got[61])
	}
	if got[62] != 1 {
		t.Errorf("sex = %d, want 1 (male)", got[62])
	}

	// 5. PosDir at [63:66] must round-trip through encodePos/decodePos.
	gotX, gotY, gotDir := decodePos(got[63:66])
	if gotX != 150 || gotY != 200 || gotDir != 3 {
		t.Errorf("PosDir unpacked = (%d, %d, dir=%d), want (150, 200, 3); bytes = %x",
			gotX, gotY, gotDir, got[63:66])
	}
	var wantPos [3]byte
	encodePos(wantPos[:], 150, 200, 3)
	if !bytes.Equal(got[63:66], wantPos[:]) {
		t.Errorf("PosDir = %v, want %v", got[63:66], wantPos[:])
	}

	// 6. Sizes + level + HP at [66:81].
	if got[66] != 5 {
		t.Errorf("xSize = %d, want 5", got[66])
	}
	if got[67] != 5 {
		t.Errorf("ySize = %d, want 5", got[67])
	}
	if clevel := binary.LittleEndian.Uint16(got[68:70]); clevel != 50 {
		t.Errorf("clevel = %d, want 50", clevel)
	}
	if font := binary.LittleEndian.Uint16(got[70:72]); font != 0 {
		t.Errorf("font = %d, want 0", font)
	}
	if maxHP := binary.LittleEndian.Uint32(got[72:76]); maxHP != 1000 {
		t.Errorf("maxHP = %d, want 1000", maxHP)
	}
	if hp := binary.LittleEndian.Uint32(got[76:80]); hp != 1000 {
		t.Errorf("HP = %d, want 1000", hp)
	}
	if got[80] != 0 {
		t.Errorf("isBoss = %d, want 0", got[80])
	}

	// 7. Body at [81:83] and name at [83:107] (24 bytes).
	if body := binary.LittleEndian.Uint16(got[81:83]); body != 0 {
		t.Errorf("body = %d, want 0", body)
	}
	nameBytes := got[83:107]
	wantName := []byte("Tester")
	if !bytes.Equal(nameBytes[:len(wantName)], wantName) {
		t.Errorf("name prefix = %q, want %q", nameBytes[:len(wantName)], wantName)
	}
	for i := len(wantName); i < 24; i++ {
		if nameBytes[i] != 0 {
			t.Errorf("name byte at [%d] = 0x%02x, want 0x00 (null pad)", i, nameBytes[i])
		}
	}
}

func TestSpawnUnitResponse_Encode_NameTruncation(t *testing.T) {
	t.Parallel()

	// Names longer than 24 bytes must be truncated to 24 bytes (the
	// on-wire name field width) — rAthena's memcpy(name, src, 24)
	// pattern. The remaining 24 bytes of the packet must be exactly
	// the first 24 bytes of the input string, no NUL padding needed
	// because truncation fills the slot.
	longName := strings.Repeat("A", 40)
	resp := SpawnUnitResponse{Name: longName}

	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode() unexpected error: %v", err)
	}
	got := buf.Bytes()

	if len(got) != 107 {
		t.Fatalf("len(got) = %d, want 107", len(got))
	}
	wantName := []byte(strings.Repeat("A", 24))
	if !bytes.Equal(got[83:107], wantName) {
		t.Errorf("truncated name = %q, want 24 'A's", got[83:107])
	}
}

func TestSpawnUnitResponse_Encode_ZeroValues(t *testing.T) {
	t.Parallel()

	// Zero-value encode must still produce 107 bytes with all fields
	// at zero and a 24-byte NUL-padded name field.
	resp := SpawnUnitResponse{}

	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode() unexpected error: %v", err)
	}
	got := buf.Bytes()

	if len(got) != 107 {
		t.Fatalf("zero-value len = %d, want 107", len(got))
	}
	if got[0] != 0xfe || got[1] != 0x09 {
		t.Errorf("opcode = %02x %02x, want fe 09", got[0], got[1])
	}
	if plen := binary.LittleEndian.Uint16(got[2:4]); plen != 107 {
		t.Errorf("packetLength = %d, want 107", plen)
	}
	if aid := binary.LittleEndian.Uint32(got[5:9]); aid != 0 {
		t.Errorf("AID = %d, want 0", aid)
	}
	if gid := binary.LittleEndian.Uint32(got[9:13]); gid != 0 {
		t.Errorf("GID = %d, want 0", gid)
	}
	// Name field must be 24 zero bytes.
	for i := 83; i < 107; i++ {
		if got[i] != 0 {
			t.Errorf("name byte at [%d] = 0x%02x, want 0x00", i, got[i])
		}
	}
	// PosDir round-trips to (0, 0, 0).
	gotX, gotY, gotDir := decodePos(got[63:66])
	if gotX != 0 || gotY != 0 || gotDir != 0 {
		t.Errorf("PosDir = (%d, %d, dir=%d), want (0, 0, 0)", gotX, gotY, gotDir)
	}
}

// CZEnterRequest round-trip tests — exercise the request-side encoder that
// is the inverse of ParseCZEnter.

func TestCZEnterRequest_Encode_RoundTrip(t *testing.T) {
	t.Parallel()

	req := CZEnterRequest{
		AccountID:  0xAAAAAAAA,
		CharID:     0xBBBBBBBB,
		AuthCode:   0xCCCCCCCC,
		ClientTime: 0xDDDDDDDD,
		Sex:        0x01,
	}

	var buf bytes.Buffer
	if err := req.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v, want nil", err)
	}
	if got := buf.Len(); got != sizeCZEnter {
		t.Fatalf("Encode wrote %d bytes, want %d", got, sizeCZEnter)
	}

	got, err := ParseCZEnter(buf.Bytes())
	if err != nil {
		t.Fatalf("ParseCZEnter err = %v, want nil", err)
	}
	if got != req {
		t.Errorf("round-trip mismatch:\n got = %+v\nwant = %+v", got, req)
	}
}

func TestCZEnterRequest_Encode_ZeroValues(t *testing.T) {
	t.Parallel()

	req := CZEnterRequest{}

	var buf bytes.Buffer
	if err := req.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v", err)
	}

	got, err := ParseCZEnter(buf.Bytes())
	if err != nil {
		t.Fatalf("ParseCZEnter err = %v", err)
	}
	if got != req {
		t.Errorf("zero round-trip mismatch:\n got = %+v\nwant = %+v", got, req)
	}
}

// CZRequestMoveRequest round-trip tests.

func TestCZRequestMoveRequest_Encode_RoundTrip(t *testing.T) {
	t.Parallel()

	req := CZRequestMoveRequest{DestX: 150, DestY: 200}

	var buf bytes.Buffer
	if err := req.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v, want nil", err)
	}
	if got := buf.Len(); got != sizeCZRequestMove {
		t.Fatalf("Encode wrote %d bytes, want %d", got, sizeCZRequestMove)
	}

	got, err := ParseCZRequestMove(buf.Bytes())
	if err != nil {
		t.Fatalf("ParseCZRequestMove err = %v, want nil", err)
	}
	if got != req {
		t.Errorf("round-trip mismatch:\n got = %+v\nwant = %+v", got, req)
	}
}

func TestCZRequestMoveRequest_Encode_ZeroOrigin(t *testing.T) {
	t.Parallel()

	req := CZRequestMoveRequest{DestX: 0, DestY: 0}

	var buf bytes.Buffer
	if err := req.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v", err)
	}

	got, err := ParseCZRequestMove(buf.Bytes())
	if err != nil {
		t.Fatalf("ParseCZRequestMove err = %v", err)
	}
	if got != req {
		t.Errorf("zero round-trip mismatch:\n got = %+v\nwant = %+v", got, req)
	}
}

func TestCZRequestMoveRequest_Encode_DirSlotZero(t *testing.T) {
	t.Parallel()

	req := CZRequestMoveRequest{DestX: 50, DestY: 75}

	var buf bytes.Buffer
	if err := req.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v", err)
	}
	parsed, err := ParseCZRequestMove(buf.Bytes())
	if err != nil {
		t.Fatalf("ParseCZRequestMove err = %v", err)
	}
	if parsed.DestX != req.DestX || parsed.DestY != req.DestY {
		t.Errorf("pos round-trip = (%d,%d), want (%d,%d)",
			parsed.DestX, parsed.DestY, req.DestX, req.DestY)
	}
}
