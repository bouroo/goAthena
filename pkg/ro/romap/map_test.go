//go:build unit

package romap

import (
	"math"
	"testing"
)

func TestLoadMap_HappyPath(t *testing.T) {
	t.Parallel()
	gat := buildGAT(3, 3, []byte{
		0, 0, 0,
		0, 1, 0,
		0, 0, 3,
	}, [4]float32{2.0, 4.0, 6.0, 8.0})
	rsw := writeRSW(0x0205, 10.0)

	md, err := LoadMap("prontera", gat, rsw)
	if err != nil {
		t.Fatalf("LoadMap: %v", err)
	}
	if md.Name != "prontera" {
		t.Errorf("name = %q, want prontera", md.Name)
	}
	if md.Width != 3 || md.Height != 3 {
		t.Errorf("dims = %dx%d", md.Width, md.Height)
	}
	// Cell (1,1) is a wall; all others walkable.
	if md.IsWalkable(1, 1) {
		t.Error("(1,1) should be a wall")
	}
	if !md.IsWalkable(2, 2) {
		t.Error("(2,2) should be walkable (type=3)")
	}
	// Average height = (2+4+6+8)/4 = 5.0
	for i, h := range md.Heights {
		if math.Abs(float64(h-5.0)) > 1e-5 {
			t.Errorf("heights[%d] = %v, want 5.0", i, h)
		}
	}
	if md.WaterLevel != 10.0 {
		t.Errorf("water level = %v, want 10.0", md.WaterLevel)
	}
}

func TestLoadMap_NoRSW(t *testing.T) {
	t.Parallel()
	gat := buildGAT(2, 2, []byte{0, 0, 0, 0}, [4]float32{0, 0, 0, 0})
	md, err := LoadMap("test", gat, nil)
	if err != nil {
		t.Fatalf("LoadMap: %v", err)
	}
	if md.WaterLevel != WaterAbsent {
		t.Errorf("WaterLevel = %v, want WaterAbsent (%v)", md.WaterLevel, WaterAbsent)
	}
}

func TestLoadMap_BadRSWIsSoftFail(t *testing.T) {
	t.Parallel()
	gat := buildGAT(2, 2, []byte{0, 0, 0, 0}, [4]float32{0, 0, 0, 0})
	// Garbage .rsw — LoadMap must not error out.
	md, err := LoadMap("test", gat, []byte("not an rsw file at all"))
	if err != nil {
		t.Fatalf("LoadMap should soft-fail on bad rsw, got %v", err)
	}
	if md.WaterLevel != WaterAbsent {
		t.Errorf("WaterLevel = %v, want WaterAbsent", md.WaterLevel)
	}
}

func TestLoadMap_BadGAT(t *testing.T) {
	t.Parallel()
	if _, err := LoadMap("test", []byte("not a gat"), nil); err == nil {
		t.Fatal("expected error for bad gat buffer")
	}
}

func TestLoadMap_IsWalkableOutOfBounds(t *testing.T) {
	t.Parallel()
	gat := buildGAT(3, 3, make([]byte, 9), [4]float32{0, 0, 0, 0})
	md, err := LoadMap("test", gat, nil)
	if err != nil {
		t.Fatalf("LoadMap: %v", err)
	}
	if md.IsWalkable(-1, 0) {
		t.Error("(-1,0) should be non-walkable (out of bounds)")
	}
	if md.IsWalkable(0, -1) {
		t.Error("(0,-1) should be non-walkable (out of bounds)")
	}
	if md.IsWalkable(3, 0) {
		t.Error("(3,0) should be non-walkable (x == Width)")
	}
	if md.IsWalkable(0, 3) {
		t.Error("(0,3) should be non-walkable (y == Height)")
	}
	if !md.IsWalkable(0, 0) {
		t.Error("(0,0) should be walkable")
	}
}

func TestLoadMap_HeightAtOutOfBounds(t *testing.T) {
	t.Parallel()
	gat := buildGAT(3, 3, make([]byte, 9), [4]float32{4, 4, 4, 4})
	md, err := LoadMap("test", gat, nil)
	if err != nil {
		t.Fatalf("LoadMap: %v", err)
	}
	if got := md.HeightAt(-1, 0); got != 0 {
		t.Errorf("HeightAt(-1,0) = %v, want 0", got)
	}
	if got := md.HeightAt(0, 0); got != 4 {
		t.Errorf("HeightAt(0,0) = %v, want 4", got)
	}
}

func TestLoadMap_WalkableIndexing(t *testing.T) {
	t.Parallel()
	// Row-major indexing: y*Width + x.
	gat := buildGAT(4, 3, []byte{
		0, 1, 1, 0, // row 0
		0, 0, 0, 0, // row 1
		3, 3, 0, 1, // row 2
	}, [4]float32{0, 0, 0, 0})
	md, err := LoadMap("test", gat, nil)
	if err != nil {
		t.Fatalf("LoadMap: %v", err)
	}
	// Walkable[y*W + x] == IsWalkable(x, y)
	for y := 0; y < md.Height; y++ {
		for x := 0; x < md.Width; x++ {
			idx := y*md.Width + x
			if md.Walkable[idx] != md.IsWalkable(x, y) {
				t.Errorf("mismatch at (%d,%d): Walkable=%v IsWalkable=%v",
					x, y, md.Walkable[idx], md.IsWalkable(x, y))
			}
		}
	}
}

func TestWaterAbsentConstant(t *testing.T) {
	t.Parallel()
	// The exported constant must equal the internal sentinel.
	if WaterAbsent != rswNoWater {
		t.Errorf("WaterAbsent = %v, want %v", WaterAbsent, rswNoWater)
	}
}
