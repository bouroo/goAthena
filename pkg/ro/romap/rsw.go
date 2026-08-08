package romap

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

// rAthena sentinel returned by grfio_read_rsw_water_level when the .rsw file
// cannot be parsed or has no water plane. See src/common/grfio.hpp:9.
const rswNoWater = 1000000

// Water-level byte offsets within the .rsw file. The version is read big-endian
// at offset 4 (src/common/grfio.cpp:473).
const (
	rswMagicLEN    = 4
	rswVersionOFF  = 4
	rswVersionLEN  = 2
	rswWaterMinVer = 0x104
	rswWaterMaxVer = 0x205
	rswWaterOff205 = 171
	rswWaterOff202 = 167
	rswWaterOffLow = 166
)

// parseRSW extracts the int32 water level from a .rsw buffer. The level is
// the truncation of the embedded float32 (matching rAthena's
// (int32)*(float*) cast in src/common/grfio.cpp:483-488).
//
// Returns rswNoWater when the buffer is malformed or the version is unsupported.
func parseRSW(buf []byte) (int32, error) {
	const minLen = rswVersionOFF + rswVersionLEN + 4
	if len(buf) < minLen {
		return rswNoWater, io.ErrUnexpectedEOF
	}
	if string(buf[:rswMagicLEN]) != "GRSW" {
		return rswNoWater, fmt.Errorf("romap: invalid rsw signature")
	}
	// Version is stored big-endian: (rsw[4]<<8) | rsw[5] (grfio.cpp:473).
	version := uint16(buf[rswVersionOFF])<<8 | uint16(buf[rswVersionOFF+1])
	if version < rswWaterMinVer || version > rswWaterMaxVer {
		return rswNoWater, fmt.Errorf("romap: unsupported rsw version 0x%04x", version)
	}

	var off int
	switch {
	case version >= 0x205:
		off = rswWaterOff205
	case version >= 0x202:
		off = rswWaterOff202
	default:
		off = rswWaterOffLow
	}
	if len(buf) < off+4 {
		return rswNoWater, io.ErrUnexpectedEOF
	}
	bits := binary.LittleEndian.Uint32(buf[off : off+4])
	f := math.Float32frombits(bits)
	// C-style cast: truncate toward zero. Negative water levels are nonsensical
	// but preserved in case a custom client uses them.
	return int32(f), nil
}
