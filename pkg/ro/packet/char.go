package packet

// Char-server packet header IDs.
//
// Source: rathena/src/common/packets.hpp DEFINE_PACKET_HEADER(NAME, 0x...)
// for the PACKET_HC_* (S→C) entries, and rathena/src/char/char_clif.cpp
// (loginclif.cpp-style) for the PACKET_CH_* (C→S) entries.
//
// Only the minimum char-server surface required for Thai Classic
// (PACKETVER 20250604) is registered here. The full character-info and
// char-list encoders land in M2b alongside the gateway wiring.
const (
	// C→S — client → char server.
	HeaderCHENTER      uint16 = 0x0065
	HeaderCHSELECTCHAR uint16 = 0x0066

	// S→C — char server → client.
	HeaderHCACCEPTENTER   uint16 = 0x006b
	HeaderHCREFUSEENTER   uint16 = 0x006c
	HeaderHCNOTIFYZONESVR uint16 = 0x0ac5
)

// Fixed on-wire byte lengths derived from the packed struct layouts in
// rathena/src/common/packets.hpp. Constants used:
//
//	MAP_NAME_LENGTH_EXT = 16 (mmo.hpp:164-165)
//
// HC_NOTIFY_ZONESVR also carries a domain[] slot; rAthena declares it as a
// char[128] in struct ZSMSG (char_clif.cpp / packets.hpp:290-299).
const (
	// sizeCHEnter = int16 cmd + uint32 AID + uint32 login_id1 +
	// uint32 login_id2 + uint16 reserved + uint8 sex = 2+4+4+4+2+1 = 17
	// (rathena/src/common/packets.hpp PACKET_CH_ENTER / char_clif.cpp:821-829).
	// The 2 reserved bytes between login_id2 and sex are part of the
	// packed struct and are ignored by the parser.
	sizeCHEnter = 17
	// sizeCHSelectChar = int16 cmd + uint8 slot = 2+1 = 3
	// (rathena/src/common/packets.hpp:116-120).
	sizeCHSelectChar = 3
	// sizeHCRefuseEnter = int16 packetType + uint8 error = 2+1 = 3
	// (rathena/src/common/packets.hpp:253-257).
	sizeHCRefuseEnter = 3
	// sizeHCNotifyZone = int16 packetType + uint32 CID +
	// char mapname[MAP_NAME_LENGTH_EXT] + uint32 ip + uint16 port +
	// char domain[128] = 2+4+16+4+2+128 = 156.
	sizeHCNotifyZone = 156

	// mapNameExtSlot is the fixed byte width of the mapname[16] field
	// in HC_NOTIFY_ZONESVR (MAP_NAME_LENGTH_EXT = 16).
	mapNameExtSlot = 16
	// domainSlot is the fixed byte width of the domain[128] field in
	// HC_NOTIFY_ZONESVR.
	domainSlot = 128
)

// NewCharServerDB returns a packet database pre-populated with all known
// char-server packet definitions for Thai Classic (PACKETVER 20250604).
//
// Inbound (C→S) entries mirror rathena's char-clif packet table; outbound
// (S→C) entries register both the small fixed packets we encode here
// (HC_REFUSE_ENTER, HC_NOTIFY_ZONESVR) and HC_ACCEPT_ENTER, whose encoder
// carries the trailing CHARACTER_INFO[] flexible array and lands in M2b.
//
// HC_ACCEPT_ENTER is variable-length — the gateway must read the uint16
// length at byte offset 2 of the packet header to determine the on-wire
// size, per rathena/src/common/packets.hpp:653-739 (PacketDatabase::handle
// dynamic branch) and rathena/src/map/clif.cpp:25749 (RFIFOW(fd,2)).
func NewCharServerDB() *DB {
	db := NewDB()

	// --- C→S: client → char server.
	db.Register(Definition{
		ID:        HeaderCHENTER,
		Name:      "CH_ENTER",
		Length:    sizeCHEnter,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCHSELECTCHAR,
		Name:      "CH_SELECT_CHAR",
		Length:    sizeCHSelectChar,
		Direction: DirectionClientToServer,
	})

	// --- S→C: char server → client.
	db.Register(Definition{
		ID:        HeaderHCREFUSEENTER,
		Name:      "HC_REFUSE_ENTER",
		Length:    sizeHCRefuseEnter,
		Direction: DirectionServerToClient,
	})
	// HC_ACCEPT_ENTER (packets.hpp:283-288) carries the char_id+auth_code
	// header plus a trailing CHARACTER_INFO[] flexible array sized from
	// the char-server-side bill of CHAR slot count. The encoder for it
	// ships in M2b; registration here is required so the gateway codec
	// can hand the variable-length frame to whatever parses it.
	db.Register(Definition{
		ID:        HeaderHCACCEPTENTER,
		Name:      "HC_ACCEPT_ENTER",
		Length:    VariableLength,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderHCNOTIFYZONESVR,
		Name:      "HC_NOTIFY_ZONESVR",
		Length:    sizeHCNotifyZone,
		Direction: DirectionServerToClient,
	})

	return db
}
