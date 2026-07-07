//go:build unit

package packet

import (
	"bytes"
	"encoding/binary"
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
