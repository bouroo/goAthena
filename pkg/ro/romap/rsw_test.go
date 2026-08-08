//go:build unit

package romap

import (
	"encoding/binary"
	"math"
	"testing"
)

func TestParseRSW_HappyPath_V205(t *testing.T) {
	t.Parallel()
	// Version 0x205 → water at offset 171.
	buf := writeRSW(0x0205, 12.5)
	got, err := parseRSW(buf)
	if err != nil {
		t.Fatalf("parseRSW: %v", err)
	}
	if got != 12 {
		t.Errorf("water level = %d, want 12 (truncation of 12.5)", got)
	}
}

func TestParseRSW_HappyPath_V104(t *testing.T) {
	t.Parallel()
	// Oldest supported version → water at offset 166.
	buf := writeRSW(0x0104, -3.7)
	got, err := parseRSW(buf)
	if err != nil {
		t.Fatalf("parseRSW: %v", err)
	}
	if got != -3 {
		t.Errorf("water level = %d, want -3 (truncation toward zero)", got)
	}
}

func TestParseRSW_V202_Boundary(t *testing.T) {
	t.Parallel()
	// 0x0202 → water at offset 167.
	buf := writeRSW(0x0202, 0.0)
	got, err := parseRSW(buf)
	if err != nil {
		t.Fatalf("parseRSW: %v", err)
	}
	if got != 0 {
		t.Errorf("water level = %d, want 0", got)
	}
}

func TestParseRSW_AllSupportedVersions(t *testing.T) {
	t.Parallel()
	versions := []uint16{0x0104, 0x0201, 0x0202, 0x0204, 0x0205}
	for _, v := range versions {
		buf := writeRSW(v, 7.0)
		got, err := parseRSW(buf)
		if err != nil {
			t.Errorf("version 0x%04x: %v", v, err)
			continue
		}
		if got != 7 {
			t.Errorf("version 0x%04x: water level = %d, want 7", v, got)
		}
	}
}

func TestParseRSW_BadMagic(t *testing.T) {
	t.Parallel()
	buf := writeRSW(0x0205, 1.0)
	copy(buf[:4], "XXXX")
	if _, err := parseRSW(buf); err == nil {
		t.Fatal("expected error for bad magic")
	}
}

func TestParseRSW_UnsupportedVersion(t *testing.T) {
	t.Parallel()
	// Versions outside 0x104..0x205 → error.
	cases := []uint16{0x0000, 0x0100, 0x0103, 0x0300, 0xffff}
	for _, v := range cases {
		buf := writeRSW(v, 1.0)
		if _, err := parseRSW(buf); err == nil {
			t.Errorf("version 0x%04x should be rejected", v)
		}
	}
}

func TestParseRSW_Truncated(t *testing.T) {
	t.Parallel()
	buf := writeRSW(0x0205, 1.0)
	for _, cut := range []int{0, 3, 5, 100, len(buf) - 2, len(buf) - 1} {
		if _, err := parseRSW(buf[:cut]); err == nil {
			t.Errorf("truncated buffer of %d bytes should error", cut)
		}
	}
}

func TestParseRSW_VersionIsBigEndian(t *testing.T) {
	t.Parallel()
	// Version is stored big-endian. 0x0205 → rsw[4]=0x02, rsw[5]=0x05.
	buf := writeRSW(0x0205, 0.0)
	if buf[4] != 0x02 || buf[5] != 0x05 {
		t.Fatalf("version encoding wrong: %02x %02x, want 02 05", buf[4], buf[5])
	}
	// Sanity: with the bytes swapped (little-endian), parsing must fail.
	buf[4], buf[5] = buf[5], buf[4]
	if _, err := parseRSW(buf); err == nil {
		t.Fatal("little-endian version should be rejected")
	}
}

func TestWriteRSW_Compact(t *testing.T) {
	t.Parallel()
	// writeRSW trims to just past the water offset.
	for _, v := range []uint16{0x0104, 0x0202, 0x0205} {
		buf := writeRSW(v, 5.0)
		var wantOff int
		switch {
		case v >= 0x205:
			wantOff = rswWaterOff205
		case v >= 0x202:
			wantOff = rswWaterOff202
		default:
			wantOff = rswWaterOffLow
		}
		if len(buf) != wantOff+4 {
			t.Errorf("v=0x%04x: buf len = %d, want %d", v, len(buf), wantOff+4)
		}
	}
}

func TestRSWNoWaterSentinel(t *testing.T) {
	t.Parallel()
	// Sentinel value the spec requires callers to recognize.
	if rswNoWater != 1000000 {
		t.Errorf("rswNoWater = %d, want 1000000", rswNoWater)
	}
	// Round-trip: float32(1e6) truncates to the sentinel.
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, math.Float32bits(1_000_000))
	if int32(math.Float32frombits(binary.LittleEndian.Uint32(buf))) != rswNoWater {
		t.Errorf("float32(1e6) → int32 = %d, want %d",
			int32(math.Float32frombits(binary.LittleEndian.Uint32(buf))), rswNoWater)
	}
}
