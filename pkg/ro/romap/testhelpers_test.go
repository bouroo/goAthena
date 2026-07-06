//go:build unit

package romap

import (
	"encoding/binary"
	"math"
)

// buildGAT synthesizes a .gat buffer for tests. cells is row-major
// (y*width + x), each entry is the cell type byte (0..6). Heights are uniform
// per cell.
func buildGAT(width, height int, cells []byte, corner [4]float32) []byte {
	total := gatHeaderSkip + 2*4 + width*height*gatCellBytes
	buf := make([]byte, total)
	copy(buf[0:4], "GRAT")
	// Industry convention: "GRAT\0" + version byte. Bytes 4-5 are opaque to
	// rAthena but commonly hold a version byte.
	buf[4] = 0x00
	buf[5] = 0x01
	binary.LittleEndian.PutUint32(buf[gatXSOFF:], uint32(width))
	binary.LittleEndian.PutUint32(buf[gatYSOFF:], uint32(height))
	off := gatHeaderSkip + 2*4
	for i := range width * height {
		binary.LittleEndian.PutUint32(buf[off+0:off+4], math.Float32bits(corner[0]))
		binary.LittleEndian.PutUint32(buf[off+4:off+8], math.Float32bits(corner[1]))
		binary.LittleEndian.PutUint32(buf[off+8:off+12], math.Float32bits(corner[2]))
		binary.LittleEndian.PutUint32(buf[off+12:off+16], math.Float32bits(corner[3]))
		ct := byte(0)
		if i < len(cells) {
			ct = cells[i]
		}
		binary.LittleEndian.PutUint32(buf[off+16:off+20], uint32(ct))
		off += gatCellBytes
	}
	return buf
}

// writeRSW serializes a synthetic .rsw buffer carrying the given big-endian
// version and little-endian float32 water level. The buffer is trimmed to
// just past the embedded water float, matching what real RSW files provide.
func writeRSW(version uint16, water float32) []byte {
	const size = 256
	buf := make([]byte, size)
	copy(buf[:4], "GRSW")
	buf[4] = byte(version >> 8)
	// #nosec G115 -- RSM version numbers fit in a single byte (0x00..0x05).
	buf[5] = byte(version)
	var off int
	switch {
	case version >= 0x0205:
		off = rswWaterOff205
	case version >= 0x0202:
		off = rswWaterOff202
	default:
		off = rswWaterOffLow
	}
	binary.LittleEndian.PutUint32(buf[off:off+4], math.Float32bits(water))
	return buf[:off+4]
}
