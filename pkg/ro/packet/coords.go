package packet

// encodePos writes the kRO 3-byte packed position (x, y, dir) into dst[0:3].
// Layout source: rathena/src/map/clif.cpp:173-178 (WBUFPOS).
//
//	p[0] = (uint8)(x >> 2)
//	p[1] = (uint8)((x << 6) | ((y >> 4) & 0x3f))
//	p[2] = (uint8)((y << 4) | (dir & 0x0f))
//
// The C implementation truncates int16 arithmetic to uint8; the Go form
// mirrors that by computing on uint16 and slicing the low byte, so callers
// may freely pass int16 coordinates (including negative values for the
// "invalid" sentinel used by the client when movement is dropped).
func encodePos(dst []byte, x, y int16, dir uint8) {
	ux := uint16(x) //nolint:gosec // int16 sign preserved via two's complement reinterpret, matches rAthena
	uy := uint16(y) //nolint:gosec // ditto

	dst[0] = byte(ux >> 2)                        //nolint:gosec // C truncates int16 → uint8 by &0xff; matching semantics
	dst[1] = byte((ux << 6) | ((uy >> 4) & 0x3f)) //nolint:gosec // ditto
	dst[2] = byte((uy << 4) | uint16(dir&0x0f))   //nolint:gosec // ditto
}

// decodePos reads the kRO 3-byte packed position from src[0:3]. Layout source:
// rathena/src/map/clif.cpp:197-211 (RBUFPOS).
//
//	x   = (src[0] << 2) | (src[1] >> 6)
//	y   = ((src[1] & 0x3f) << 4) | (src[2] >> 4)
//	dir = src[2] & 0x0f
//
// The C form stores x/y into int16*; the Go form widens to uint16 first to
// match the bit layout exactly and then reinterprets the low 16 bits as int16.
func decodePos(src []byte) (x, y int16, dir uint8) {
	x = int16((uint16(src[0]) << 2) | (uint16(src[1]) >> 6))      //nolint:gosec // wire bit layout; sign-preserving via uint16→int16
	y = int16((uint16(src[1]&0x3f) << 4) | (uint16(src[2]) >> 4)) //nolint:gosec // ditto
	dir = src[2] & 0x0f

	return x, y, dir
}
