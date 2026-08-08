package romap

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

// .gat cell type codes (rAthena src/map/map.cpp:3280-3299, map_gat2cell).
const (
	gatWalkableGround byte = 0
	gatWall           byte = 1
	gatWalkableWater  byte = 3
)

// gat header layout: 6 opaque bytes (magic "GRAT\0\0" + version u16) that
// rAthena skips — see map_readgat in rAthena src/map/map.cpp:3871-3873.
const (
	gatHeaderSkip = 6
	gatXSOFF      = 6
	gatYSOFF      = 10
	gatCellBytes  = 20
)

// maxMapDim bounds the per-axis grid dimension. The task spec sets 10000; the
// rAthena ceiling is 512 cells per side (src/map/map.hpp:82). Per-axis 10000
// is the stricter guard and prevents integer overflow on byte-size math.
const maxMapDim = 10000

func parseGAT(buf []byte) (width, height int, walkable []bool, heights []float32, err error) {
	need := gatHeaderSkip + 2*4
	if len(buf) < need {
		return 0, 0, nil, nil, io.ErrUnexpectedEOF
	}
	rawXS := binary.LittleEndian.Uint32(buf[gatXSOFF:])
	rawYS := binary.LittleEndian.Uint32(buf[gatYSOFF:])
	if rawXS == 0 || rawXS > maxMapDim || rawYS == 0 || rawYS > maxMapDim {
		return 0, 0, nil, nil, fmt.Errorf("romap: invalid gat dimensions %dx%d", rawXS, rawYS)
	}
	xs := int(rawXS)
	ys := int(rawYS)
	cells := xs * ys
	expected := need + cells*gatCellBytes
	if len(buf) < expected {
		return 0, 0, nil, nil, fmt.Errorf("romap: truncated gat: got %d bytes, want %d", len(buf), expected)
	}

	walkable = make([]bool, cells)
	heights = make([]float32, cells)
	off := need
	for i := range cells {
		h0 := math.Float32frombits(binary.LittleEndian.Uint32(buf[off+0 : off+4]))
		h1 := math.Float32frombits(binary.LittleEndian.Uint32(buf[off+4 : off+8]))
		h2 := math.Float32frombits(binary.LittleEndian.Uint32(buf[off+8 : off+12]))
		h3 := math.Float32frombits(binary.LittleEndian.Uint32(buf[off+12 : off+16]))
		// Cell type is the LSB of the uint32 LE at off+16; spec values 0..6.
		cellType := buf[off+16]
		walkable[i] = isGATWalkable(cellType)
		heights[i] = (h0 + h1 + h2 + h3) * 0.25
		off += gatCellBytes
	}
	return xs, ys, walkable, heights, nil
}

func isGATWalkable(cellType byte) bool {
	return cellType == gatWalkableGround || cellType == gatWalkableWater
}
