//go:build unit

package packet

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestRefuseEnterResponse_Encode_ByteExact(t *testing.T) {
	t.Parallel()

	resp := RefuseEnterResponse{Error: 0x03}

	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v, want nil", err)
	}
	got := buf.Bytes()

	if len(got) != sizeHCRefuseEnter {
		t.Fatalf("len = %d, want %d", len(got), sizeHCRefuseEnter)
	}

	// Header bytes: little-endian 0x006c → 0x6c 0x00.
	if got[0] != 0x6c || got[1] != 0x00 {
		t.Errorf("header bytes = %02x %02x, want 6c 00", got[0], got[1])
	}
	if got[2] != 0x03 {
		t.Errorf("error byte = 0x%02x, want 0x03", got[2])
	}
}

func TestRefuseEnterResponse_Encode_ZeroError(t *testing.T) {
	t.Parallel()

	resp := RefuseEnterResponse{}
	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v, want nil", err)
	}
	got := buf.Bytes()
	if len(got) != sizeHCRefuseEnter {
		t.Fatalf("len = %d, want %d", len(got), sizeHCRefuseEnter)
	}
	if got[0] != 0x6c || got[1] != 0x00 {
		t.Errorf("header bytes = %02x %02x, want 6c 00", got[0], got[1])
	}
	if got[2] != 0x00 {
		t.Errorf("error byte = 0x%02x, want 0x00", got[2])
	}
}

func TestRefuseEnterResponse_Size(t *testing.T) {
	t.Parallel()

	if got := (RefuseEnterResponse{}).Size(); got != sizeHCRefuseEnter {
		t.Errorf("Size() = %d, want %d", got, sizeHCRefuseEnter)
	}
}

func TestNotifyZoneServerResponse_Encode_ByteExact(t *testing.T) {
	t.Parallel()

	resp := NotifyZoneServerResponse{
		CID:     0x12345678,
		MapName: "prontera",
		IP:      0x7f000001, // 127.0.0.1 in network-order uint32 form
		Port:    5121,
		Domain:  "ro.example.com",
	}

	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v, want nil", err)
	}
	got := buf.Bytes()

	if len(got) != sizeHCNotifyZone {
		t.Fatalf("len = %d, want %d", len(got), sizeHCNotifyZone)
	}
	// Header bytes: little-endian 0x0ac5 → 0xc5 0x0a.
	if got[0] != 0xc5 || got[1] != 0x0a {
		t.Errorf("header bytes = %02x %02x, want c5 0a", got[0], got[1])
	}

	// uint32 CID at offset 2 (little-endian) — bytes 0x78 0x56 0x34 0x12.
	if !bytes.Equal(got[2:6], []byte{0x78, 0x56, 0x34, 0x12}) {
		t.Errorf("CID bytes = % x, want 78 56 34 12", got[2:6])
	}

	// mapname[16] at offset 6 — exact-fit string zero-padded.
	wantMap := make([]byte, mapNameExtSlot)
	copy(wantMap, "prontera")
	if !bytes.Equal(got[6:6+mapNameExtSlot], wantMap) {
		t.Errorf("mapname slot = % x, want % x", got[6:6+mapNameExtSlot], wantMap)
	}

	// uint32 ip at offset 22 (2+4+16) — bytes 0x01 0x00 0x00 0x7f.
	if !bytes.Equal(got[22:26], []byte{0x01, 0x00, 0x00, 0x7f}) {
		t.Errorf("ip bytes at offset 22 = % x, want 01 00 00 7f", got[22:26])
	}

	// uint16 port at offset 26 — bytes 0x01 0x14 (5121 LE = 0x1401).
	if !bytes.Equal(got[26:28], []byte{0x01, 0x14}) {
		t.Errorf("port bytes at offset 26 = % x, want 01 14", got[26:28])
	}

	// domain[128] at offset 28 — string zero-padded.
	wantDomain := make([]byte, domainSlot)
	copy(wantDomain, "ro.example.com")
	if !bytes.Equal(got[28:28+domainSlot], wantDomain) {
		t.Errorf("domain slot mismatch at offset 28")
	}

	// Spot-check the trailing tail is zero (string shorter than slot).
	for i := 28 + len("ro.example.com"); i < sizeHCNotifyZone; i++ {
		if got[i] != 0 {
			t.Errorf("tail byte at offset %d = 0x%02x, want 0x00", i, got[i])
		}
	}
}

func TestNotifyZoneServerResponse_Encode_EmptyStrings(t *testing.T) {
	t.Parallel()

	resp := NotifyZoneServerResponse{
		CID:     1,
		MapName: "",
		IP:      0,
		Port:    0,
		Domain:  "",
	}

	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v, want nil", err)
	}
	got := buf.Bytes()
	if len(got) != sizeHCNotifyZone {
		t.Fatalf("len = %d, want %d", len(got), sizeHCNotifyZone)
	}

	// After the fixed fields (CID[2:6], mapname[6:22], ip[22:26],
	// port[26:28]) the rest must be zero.
	for i := 28; i < sizeHCNotifyZone; i++ {
		if got[i] != 0 {
			t.Errorf("byte at offset %d = 0x%02x, want 0x00", i, got[i])
		}
	}
}

func TestNotifyZoneServerResponse_Encode_MapNameExactFit(t *testing.T) {
	t.Parallel()

	// Exact-fit mapname (16 bytes) exercises the no-zero-padding path.
	mapName := bytes.Repeat([]byte("M"), mapNameExtSlot)
	resp := NotifyZoneServerResponse{
		CID:     42,
		MapName: string(mapName),
		Port:    5121,
	}

	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v, want nil", err)
	}
	got := buf.Bytes()
	if !bytes.Equal(got[6:6+mapNameExtSlot], mapName) {
		t.Errorf("mapname slot = % x, want % x (exact-fit copy)", got[6:6+mapNameExtSlot], mapName)
	}
	// After the mapname slot, ip + port + domain must start clean.
	if !bytes.Equal(got[22:26], []byte{0, 0, 0, 0}) {
		t.Errorf("ip slot not zero at offset 22: % x", got[22:26])
	}
}

func TestNotifyZoneServerResponse_Encode_OverflowErrors(t *testing.T) {
	t.Parallel()

	tooLongMap := make([]byte, mapNameExtSlot+1)
	for i := range tooLongMap {
		tooLongMap[i] = 'a'
	}
	tooLongDomain := make([]byte, domainSlot+1)
	for i := range tooLongDomain {
		tooLongDomain[i] = 'd'
	}

	cases := []struct {
		name    string
		resp    NotifyZoneServerResponse
		wantErr error
	}{
		{
			name:    "mapname too long",
			resp:    NotifyZoneServerResponse{MapName: string(tooLongMap)},
			wantErr: ErrMapNameTooLong,
		},
		{
			name:    "domain too long",
			resp:    NotifyZoneServerResponse{Domain: string(tooLongDomain)},
			wantErr: ErrDomainTooLong,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			err := tc.resp.Encode(&buf)
			if err == nil {
				t.Fatalf("Encode err = nil, want %v", tc.wantErr)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("Encode err = %v, want errors.Is(.., %v)", err, tc.wantErr)
			}
			if buf.Len() != 0 {
				t.Errorf("partial output written: %d bytes (want 0 on error)", buf.Len())
			}
		})
	}
}

func TestNotifyZoneServerResponse_Size(t *testing.T) {
	t.Parallel()

	if got := (NotifyZoneServerResponse{}).Size(); got != sizeHCNotifyZone {
		t.Errorf("Size() = %d, want %d", got, sizeHCNotifyZone)
	}
}

// TestAcceptEnterResponse_Encode_ZeroChars asserts the 27-byte header-only
// layout for an account with no characters.
func TestAcceptEnterResponse_Encode_ZeroChars(t *testing.T) {
	t.Parallel()

	resp := AcceptEnterResponse{
		Total:        9,
		PremiumStart: 0,
		PremiumEnd:   0,
		Extension:    "",
		Characters:   nil,
	}

	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v, want nil", err)
	}
	got := buf.Bytes()

	if len(got) != acceptEnterHeaderSize {
		t.Fatalf("len = %d, want %d", len(got), acceptEnterHeaderSize)
	}
	// Header bytes: little-endian 0x006b → 0x6b 0x00.
	if got[0] != 0x6b || got[1] != 0x00 {
		t.Errorf("header bytes = %02x %02x, want 6b 00", got[0], got[1])
	}
	if pl := binary.LittleEndian.Uint16(got[2:4]); pl != acceptEnterHeaderSize {
		t.Errorf("packetLength = %d, want %d", pl, acceptEnterHeaderSize)
	}
	if got[4] != 9 {
		t.Errorf("total byte = %d, want 9", got[4])
	}
}

// TestAcceptEnterResponse_Encode_OneChar asserts 27 + 175 = 202 bytes and
// the embedded CHARACTER_INFO starts at offset 27.
func TestAcceptEnterResponse_Encode_OneChar(t *testing.T) {
	t.Parallel()

	ch := CharacterInfo{
		GID:      42,
		Name:     "Hero",
		MapName:  "prontera",
		Level:    99,
		Job:      7, // Knight-class placeholder
		JobLevel: 50,
		CharNum:  0,
		Str:      9,
		Agi:      9,
		Vit:      9,
		Int:      9,
		Dex:      9,
		Luk:      9,
		Sex:      1,
	}
	resp := AcceptEnterResponse{
		Total:      1,
		Characters: []CharacterInfo{ch},
	}

	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v, want nil", err)
	}
	got := buf.Bytes()

	wantLen := acceptEnterHeaderSize + CharacterInfoSize
	if len(got) != wantLen {
		t.Fatalf("len = %d, want %d (header %d + 1*%d char)", len(got), wantLen, acceptEnterHeaderSize, CharacterInfoSize)
	}
	if pl := binary.LittleEndian.Uint16(got[2:4]); int(pl) != wantLen {
		t.Errorf("packetLength = %d, want %d", pl, wantLen)
	}
	if got[0] != 0x6b || got[1] != 0x00 {
		t.Errorf("header bytes = %02x %02x, want 6b 00", got[0], got[1])
	}
	// The embedded CHARACTER_INFO starts at offset 27; its GID occupies
	// [27:31] little-endian.
	if g := binary.LittleEndian.Uint32(got[acceptEnterHeaderSize : acceptEnterHeaderSize+4]); g != 42 {
		t.Errorf("embedded GID at [%d:%d] = %d, want 42",
			acceptEnterHeaderSize, acceptEnterHeaderSize+4, g)
	}
}

// TestAcceptEnterResponse_Encode_TwoChars asserts 27 + 2*175 = 377 bytes.
func TestAcceptEnterResponse_Encode_TwoChars(t *testing.T) {
	t.Parallel()

	resp := AcceptEnterResponse{
		Total: 2,
		Characters: []CharacterInfo{
			{GID: 1, Name: "A", MapName: "prontera", Sex: 1},
			{GID: 2, Name: "B", MapName: "geffen", Sex: 0},
		},
	}

	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v, want nil", err)
	}
	got := buf.Bytes()

	wantLen := acceptEnterHeaderSize + 2*CharacterInfoSize
	if len(got) != wantLen {
		t.Fatalf("len = %d, want %d (header %d + 2*%d char)", len(got), wantLen, acceptEnterHeaderSize, CharacterInfoSize)
	}
	if pl := binary.LittleEndian.Uint16(got[2:4]); int(pl) != wantLen {
		t.Errorf("packetLength = %d, want %d", pl, wantLen)
	}
	// GID of the first embedded character.
	if g := binary.LittleEndian.Uint32(got[acceptEnterHeaderSize : acceptEnterHeaderSize+4]); g != 1 {
		t.Errorf("first embedded GID = %d, want 1", g)
	}
	// GID of the second embedded character at offset 27 + 175.
	secondStart := acceptEnterHeaderSize + CharacterInfoSize
	if g := binary.LittleEndian.Uint32(got[secondStart : secondStart+4]); g != 2 {
		t.Errorf("second embedded GID at offset %d = %d, want 2", secondStart, g)
	}
}

// TestAcceptEnterResponse_Encode_ExtensionOverflow asserts a too-long
// extension string fails with the sentinel error and writes zero bytes.
func TestAcceptEnterResponse_Encode_ExtensionOverflow(t *testing.T) {
	t.Parallel()

	resp := AcceptEnterResponse{
		Extension: string(bytes.Repeat([]byte("E"), acceptEnterExtensionSlot+1)),
	}

	var buf bytes.Buffer
	err := resp.Encode(&buf)
	if err == nil {
		t.Fatalf("Encode err = nil, want error")
	}
	if !errors.Is(err, ErrExtensionTooLong) {
		t.Errorf("Encode err = %v, want errors.Is(.., ErrExtensionTooLong)", err)
	}
	if buf.Len() != 0 {
		t.Errorf("partial output written: %d bytes (want 0 on error)", buf.Len())
	}
}

// TestAcceptEnterResponse_Size verifies Size with 0, 1, and N characters.
func TestAcceptEnterResponse_Size(t *testing.T) {
	t.Parallel()

	if got := (AcceptEnterResponse{}).Size(); got != acceptEnterHeaderSize {
		t.Errorf("Size() with 0 chars = %d, want %d", got, acceptEnterHeaderSize)
	}
	if got := (AcceptEnterResponse{Characters: []CharacterInfo{{}}}).Size(); got != acceptEnterHeaderSize+CharacterInfoSize {
		t.Errorf("Size() with 1 char = %d, want %d", got, acceptEnterHeaderSize+CharacterInfoSize)
	}
	if got := (AcceptEnterResponse{Characters: []CharacterInfo{{}, {}}}).Size(); got != acceptEnterHeaderSize+2*CharacterInfoSize {
		t.Errorf("Size() with 2 chars = %d, want %d", got, acceptEnterHeaderSize+2*CharacterInfoSize)
	}
}

// TestAcceptEnterResponse_Encode_PacketLengthOverflow asserts that
// AcceptEnterResponse with enough Characters to push the total packet
// length past uint16 max (65535) returns the ErrPacketTooLong sentinel
// from Encode without writing any bytes.
//
// Math: 27 (header) + N*175 (chars) > 65535 → N > (65535-27)/175 =
// 374.51. So 375 characters overflows by exactly 27 bytes
// (27 + 375*175 = 65652). The test uses 375 as the smallest count
// that triggers the guard.
func TestAcceptEnterResponse_Encode_PacketLengthOverflow(t *testing.T) {
	t.Parallel()

	const overflowCount = 375 // 27 + 375*175 = 65652 > 65535
	wantLen := acceptEnterHeaderSize + overflowCount*CharacterInfoSize
	if wantLen <= 0xffff {
		t.Fatalf("test setup wrong: wantLen=%d must exceed uint16 max for the overflow guard to fire", wantLen)
	}

	chars := make([]CharacterInfo, overflowCount)
	for i := range chars {
		// Short ASCII name + mapName so per-entry validate passes.
		chars[i] = CharacterInfo{
			GID:     uint32(i + 1),
			Name:    "n",
			MapName: "m",
		}
	}

	resp := AcceptEnterResponse{
		Total:      0, // under uint16-overflow guard; Total byte is irrelevant once validate fails
		Characters: chars,
	}

	var buf bytes.Buffer
	err := resp.Encode(&buf)
	if err == nil {
		t.Fatalf("Encode err = nil, want error (Size()=%d exceeds uint16 max)", resp.Size())
	}
	if !errors.Is(err, ErrPacketTooLong) {
		t.Errorf("Encode err = %v, want errors.Is(.., ErrPacketTooLong)", err)
	}
	if buf.Len() != 0 {
		t.Errorf("partial output written: %d bytes (want 0 on guard failure)", buf.Len())
	}
}

// TestAcceptEnterResponse_Encode_AtUint16Boundary confirms the
// overflow guard does NOT fire one byte below the limit. The encoded
// length must be < 65536 — 65535 itself fits in uint16 and remains
// encodable.
func TestAcceptEnterResponse_Encode_AtUint16Boundary(t *testing.T) {
	t.Parallel()

	// Smallest N that produces Size() == 65535 exactly: N*175 + 27 == 65535
	// → N == (65535-27)/175 == 374.51 — not integer; pick 374
	// (27 + 374*175 = 65477, well below 65535) as the largest safe
	// count to keep the test fast and obvious.
	const safeCount = 374
	chars := make([]CharacterInfo, safeCount)
	for i := range chars {
		chars[i] = CharacterInfo{
			GID:     uint32(i + 1),
			Name:    "n",
			MapName: "m",
		}
	}

	resp := AcceptEnterResponse{
		Total:      0, // under-slot value; boundary test cares only about Size
		Characters: chars,
	}

	var buf bytes.Buffer
	if err := resp.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v at safe size %d, want nil", err, resp.Size())
	}
	if got := buf.Len(); got != resp.Size() {
		t.Errorf("Encode wrote %d bytes, want %d", got, resp.Size())
	}
	if got := resp.Size(); got >= 0x10000 {
		t.Errorf("Size() = %d, want < 65536 for boundary test", got)
	}
}

// CHEnterRequest round-trip tests — exercise the request-side encoder that
// is the inverse of ParseCHEnter.

func TestCHEnterRequest_Encode_RoundTrip(t *testing.T) {
	t.Parallel()

	req := CHEnterRequest{
		AccountID: 0xAAAAAAAA,
		LoginID1:  0xBBBBBBBB,
		LoginID2:  0xCCCCCCCC,
		Sex:       0x01,
	}

	var buf bytes.Buffer
	if err := req.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v, want nil", err)
	}
	if got := buf.Len(); got != sizeCHEnter {
		t.Fatalf("Encode wrote %d bytes, want %d", got, sizeCHEnter)
	}

	got, err := ParseCHEnter(buf.Bytes())
	if err != nil {
		t.Fatalf("ParseCHEnter err = %v, want nil", err)
	}
	if got != req {
		t.Errorf("round-trip mismatch:\n got = %+v\nwant = %+v", got, req)
	}
}

func TestCHEnterRequest_Encode_ZeroValues(t *testing.T) {
	t.Parallel()

	req := CHEnterRequest{}

	var buf bytes.Buffer
	if err := req.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v", err)
	}

	got, err := ParseCHEnter(buf.Bytes())
	if err != nil {
		t.Fatalf("ParseCHEnter err = %v", err)
	}
	if got != req {
		t.Errorf("zero round-trip mismatch:\n got = %+v\nwant = %+v", got, req)
	}
}

func TestCHEnterRequest_Encode_ReservedSlotZero(t *testing.T) {
	t.Parallel()

	req := CHEnterRequest{
		AccountID: 0x01020304,
		LoginID1:  0x05060708,
		LoginID2:  0x090A0B0C,
		Sex:       0x01,
	}

	var buf bytes.Buffer
	if err := req.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v", err)
	}
	got := buf.Bytes()
	// Reserved uint16 at offset [14:16] must be zero.
	if got[14] != 0x00 || got[15] != 0x00 {
		t.Errorf("reserved slot bytes = %02x %02x, want 00 00", got[14], got[15])
	}
}

// CHSelectCharRequest round-trip tests.

func TestCHSelectCharRequest_Encode_RoundTrip(t *testing.T) {
	t.Parallel()

	req := CHSelectCharRequest{Slot: 0x05}

	var buf bytes.Buffer
	if err := req.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v, want nil", err)
	}
	if got := buf.Len(); got != sizeCHSelectChar {
		t.Fatalf("Encode wrote %d bytes, want %d", got, sizeCHSelectChar)
	}

	got, err := ParseCHSelectChar(buf.Bytes())
	if err != nil {
		t.Fatalf("ParseCHSelectChar err = %v, want nil", err)
	}
	if got != req {
		t.Errorf("round-trip mismatch:\n got = %+v\nwant = %+v", got, req)
	}
}

func TestCHSelectCharRequest_Encode_ZeroSlot(t *testing.T) {
	t.Parallel()

	req := CHSelectCharRequest{Slot: 0}

	var buf bytes.Buffer
	if err := req.Encode(&buf); err != nil {
		t.Fatalf("Encode err = %v", err)
	}

	got, err := ParseCHSelectChar(buf.Bytes())
	if err != nil {
		t.Fatalf("ParseCHSelectChar err = %v", err)
	}
	if got != req {
		t.Errorf("zero round-trip mismatch:\n got = %+v\nwant = %+v", got, req)
	}
}
