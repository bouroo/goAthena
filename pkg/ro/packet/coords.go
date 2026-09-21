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

// ClientIndex converts an inventory server row (0-based) to the index the
// rAthena client uses on the wire (server row + 2). Every S→C inventory packet
// that echoes a slot uses this form, and every C→S request carries it.
// Source: rathena/src/map/clif.cpp:122-124 (client_index).
//
// The +2 offset is a fixed client convention for this PACKETVER; the client's
// own grid slots 0/1 are reserved, so server row 0 is client index 2.
func ClientIndex(serverRow uint16) uint16 {
	return serverRow + clientInventoryIndexOffset
}

// ServerIndex converts the inventory index a client sent on the wire to the
// server's 0-based row in the LoadByChar list (client index − 2). Source:
// rathena/src/map/clif.cpp:126-128 (server_index), applied by every C→S
// inventory parser: clif_parse_UseItem (clif.cpp:12121), clif_parse_DropItem
// (clif.cpp:12063), clif_parse_EquipItem (clif.cpp:12139), takeoff
// (clif.cpp:12201), clif_parse_AddExchangeItem (clif.cpp:12568).
//
// A client index below the offset wraps around to a huge uint16 rather than
// going negative, so callers MUST range-check the result against the row count
// before indexing — a wrapped value fails a `>= len(rows)` bound as intended.
func ServerIndex(clientIndex uint16) uint16 {
	return clientIndex - clientInventoryIndexOffset
}

// clientInventoryIndexOffset is the fixed gap between an inventory server row
// and the index the rAthena client sends/receives (clif.cpp:122-128). It is NOT
// the storage offset (client_storage_index uses +1, clif.cpp:130-132).
const clientInventoryIndexOffset uint16 = 2
