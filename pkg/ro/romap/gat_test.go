//go:build unit

package romap

import (
	"encoding/binary"
	"io"
	"math"
	"testing"
)

func TestParseGAT_HappyPath(t *testing.T) {
	t.Parallel()
	buf := buildGAT(3, 2, []byte{
		0, 1, 3, // row 0: walkable, wall, walkable-water
		0, 0, 0, // row 1: all walkable
	}, [4]float32{1.0, 2.0, 3.0, 4.0})

	w, h, walkable, heights, err := parseGAT(buf)
	if err != nil {
		t.Fatalf("parseGAT: %v", err)
	}
	if w != 3 || h != 2 {
		t.Fatalf("dims = %dx%d, want 3x2", w, h)
	}
	want := []bool{
		true, false, true,
		true, true, true,
	}
	if got := walkable; !equalBool(got, want) {
		t.Fatalf("walkable = %v, want %v", got, want)
	}
	// Average of 4 corner heights = (1+2+3+4)/4 = 2.5
	for i, got := range heights {
		if math.Abs(float64(got-2.5)) > 1e-6 {
			t.Errorf("heights[%d] = %v, want 2.5", i, got)
		}
	}
}

func TestParseGAT_OneByOne(t *testing.T) {
	t.Parallel()
	buf := buildGAT(1, 1, []byte{0}, [4]float32{0, 0, 0, 0})
	w, h, walkable, heights, err := parseGAT(buf)
	if err != nil {
		t.Fatalf("parseGAT: %v", err)
	}
	if w != 1 || h != 1 {
		t.Fatalf("dims = %dx%d", w, h)
	}
	if !walkable[0] || heights[0] != 0 {
		t.Fatalf("1x1 cell wrong: walkable=%v height=%v", walkable[0], heights[0])
	}
}

func TestParseGAT_AllWalls(t *testing.T) {
	t.Parallel()
	cells := make([]byte, 9)
	for i := range cells {
		cells[i] = 1 // wall
	}
	buf := buildGAT(3, 3, cells, [4]float32{0, 0, 0, 0})
	_, _, walkable, _, err := parseGAT(buf)
	if err != nil {
		t.Fatalf("parseGAT: %v", err)
	}
	for i, w := range walkable {
		if w {
			t.Errorf("cell %d should be non-walkable", i)
		}
	}
}

func TestParseGAT_AllWalkable(t *testing.T) {
	t.Parallel()
	cells := make([]byte, 9)
	for i := range cells {
		cells[i] = 3 // walkable water
	}
	buf := buildGAT(3, 3, cells, [4]float32{0, 0, 0, 0})
	_, _, walkable, _, err := parseGAT(buf)
	if err != nil {
		t.Fatalf("parseGAT: %v", err)
	}
	for i, w := range walkable {
		if !w {
			t.Errorf("cell %d should be walkable", i)
		}
	}
}

func TestParseGAT_CellTypeTranslation(t *testing.T) {
	t.Parallel()
	// Map all 7 type codes through parseGAT and assert walkability.
	cases := []struct {
		code     byte
		walkable bool
	}{
		{0, true},  // walkable ground
		{1, false}, // wall
		{2, false}, // non-walkable water
		{3, true},  // walkable water
		{4, false}, // non-walkable water (snipable)
		{5, false}, // cliff/gap
		{6, false}, // non-walkable water3
	}
	for _, c := range cases {
		if got := isGATWalkable(c.code); got != c.walkable {
			t.Errorf("isGATWalkable(%d) = %v, want %v", c.code, got, c.walkable)
		}
	}
}

func TestParseGAT_BadMagic(t *testing.T) {
	t.Parallel()
	buf := buildGAT(2, 2, []byte{0, 0, 0, 0}, [4]float32{0, 0, 0, 0})
	// Overwrite magic with something else.
	copy(buf[0:4], "NOPE")
	_, _, _, _, err := parseGAT(buf)
	// rAthena skips the magic entirely; we also skip it (gates on dims+length).
	// The buffer still parses successfully — assert it does, then verify a
	// different failure mode below.
	if err != nil {
		t.Fatalf("parseGAT with bad magic should still parse (rAthena skips magic), got %v", err)
	}
}

func TestParseGAT_Truncated(t *testing.T) {
	t.Parallel()
	full := buildGAT(4, 4, make([]byte, 16), [4]float32{0, 0, 0, 0})
	for _, cut := range []int{0, 6, 13, 20, len(full) - 5, len(full) - 1} {
		_, _, _, _, err := parseGAT(full[:cut])
		if err == nil {
			t.Errorf("expected error for truncated buffer of %d bytes", cut)
		}
	}
}

func TestParseGAT_NoCells(t *testing.T) {
	t.Parallel()
	// Buffer with valid dims but missing the cell payload (truncation).
	buf := buildGAT(2, 2, []byte{0, 0, 0, 0}, [4]float32{0, 0, 0, 0})
	// Truncate by removing one full cell.
	truncated := buf[:len(buf)-gatCellBytes]
	_, _, _, _, err := parseGAT(truncated)
	if err == nil {
		t.Fatal("expected error for missing cells")
	}
}

func TestParseGAT_BadDimensions(t *testing.T) {
	t.Parallel()
	// xs=0 is rejected by the validator.
	buf := buildGAT(2, 2, []byte{0, 0, 0, 0}, [4]float32{0, 0, 0, 0})
	binary.LittleEndian.PutUint32(buf[gatXSOFF:], 0)
	_, _, _, _, err := parseGAT(buf)
	if err == nil {
		t.Fatal("expected error for xs=0")
	}
	// xs too large.
	binary.LittleEndian.PutUint32(buf[gatXSOFF:], uint32(maxMapDim+1))
	binary.LittleEndian.PutUint32(buf[gatYSOFF:], 2)
	_, _, _, _, err = parseGAT(buf)
	if err == nil {
		t.Fatal("expected error for xs > maxMapDim")
	}
}

func TestParseGAT_ExactEOF(t *testing.T) {
	t.Parallel()
	// Buffer exactly header + cells, no extra padding → must succeed.
	full := buildGAT(5, 5, make([]byte, 25), [4]float32{1, 1, 1, 1})
	if _, _, _, _, err := parseGAT(full); err != nil {
		t.Fatalf("exact-size buffer should parse cleanly: %v", err)
	}
}

func TestParseGAT_Empty(t *testing.T) {
	t.Parallel()
	_, _, _, _, err := parseGAT(nil)
	if err != io.ErrUnexpectedEOF {
		t.Fatalf("empty buffer: got %v, want io.ErrUnexpectedEOF", err)
	}
}

func equalBool(a, b []bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
