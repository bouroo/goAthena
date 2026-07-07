package packet

// Map-server packet header IDs for PACKETVER 20250604 (Thai Classic pre-Renewal).
//
// Sources cited per constant; the map-server packet database in rAthena is
// build-time generated from rathena/src/map/clif_packetdb.hpp + the per-PACKETVER
// shuffle files. Until codegen lands (deferred to P1.2b) we hand-register the
// minimal set required for the M3a map-server handshake:
//
//	ZC_ACCEPT_ENTER  (S→C, sent on successful CZ_ENTER)
//	ZC_REFUSE_ENTER  (S→C, sent on rejected CZ_ENTER)
//	ZC_NOTIFY_PLAYERMOVE (S→C, sent on accepted CZ_REQUEST_MOVE)
//	ZC_SPAWN_UNIT    (S→C, sent after ZC_ACCEPT_ENTER for own entity)
//	CZ_ENTER         (C→S, client requests to enter the map server)
//	CZ_REQUEST_MOVE  (C→S, client requests a single step in a cardinal dir)
const (
	// C→S — client → map server.
	HeaderCZENTER       uint16 = 0x0072 // rathena/src/map/clif.cpp:10642 (CZ_ENTER)
	HeaderCZREQUESTMOVE uint16 = 0x0085 // rathena/src/map/clif.cpp:11374 (CZ_REQUEST_MOVE)

	// S→C — map server → client.
	HeaderZCACCEPTENTER      uint16 = 0x02eb // rathena/src/map/packets.hpp:571 (ZC_ACCEPT_ENTER, PACKETVER >= 20160330 branch)
	HeaderZCREFUSEENTER      uint16 = 0x0074 // rathena/src/map/packets.hpp:590 (ZC_REFUSE_ENTER)
	HeaderZCNOTIFYPLAYERMOVE uint16 = 0x0087 // rathena/src/map/packets.hpp (ZC_NOTIFY_PLAYERMOVE)
	HeaderZCSPAWNUNIT        uint16 = 0x09fe // rathena/src/map/packets.hpp ZC_SPAWN_UNIT (PACKETVER >= 20150513 branch)
)

// Fixed on-wire byte lengths derived from the packed struct layouts in
// rathena/src/map/clif.cpp (CZ_*, parsed from the per-packet comment) and
// rathena/src/map/packets.hpp (ZC_*).
const (
	// sizeZCAcceptEnter = int16 packetType + uint32 startTime +
	// uint8 posDir[3] + uint8 xSize + uint8 ySize + uint16 font =
	// 2+4+3+1+1+2 = 13 (rathena/src/map/packets.hpp:562-571).
	sizeZCAcceptEnter = 13
	// sizeZCRefuseEnter = int16 packetType + uint8 errorCode = 2+1 = 3
	// (rathena/src/map/packets.hpp:585-589, static_assert at :589).
	sizeZCRefuseEnter = 3
	// sizeCZEnter = int16 packetType + uint32 AID + uint32 CID +
	// uint32 authCode + uint32 clientTime + uint8 sex = 2+4+4+4+4+1 = 19
	// (rathena/src/map/clif.cpp:10642 + the WantToConnection handler
	// reading RFIFO* at the documented offsets).
	sizeCZEnter = 19
	// sizeCZRequestMove = int16 packetType + uint8 dest[3] = 2+3 = 5
	// (rathena/src/map/clif.cpp:11374; the WalkToXY handler calls RFIFOPOS
	// at packet_db[..].pos[0], which is at offset 2 right after the cmd).
	sizeCZRequestMove = 5
	// sizeZCNotifyPlayerMove = int16 packetType + uint32 moveStartTime +
	// uint8 srcPos[3] + uint8 destPos[3] = 2+4+3+3 = 12
	// (rathena/src/map/packets.hpp ZC_NOTIFY_PLAYERMOVE).
	sizeZCNotifyPlayerMove = 12
	// sizeZCSpawnUnit = uint16 packetType + uint16 packetLength +
	// uint8 objectType + uint32 AID + uint32 GID + int16 speed + int16 bodyState
	// + int16 healthState + int32 effectState + int16 job + uint16 head
	// + uint32 weapon + uint32 shield + uint16 accessory + uint16 accessory2
	// + uint16 accessory3 + int16 headPalette + int16 bodyPalette + int16 headDir
	// + uint16 robe + uint32 GUID + int16 GEmblemVer + int16 honor
	// + int32 virtue + uint8 isPKModeON + uint8 sex + uint8 posDir[3]
	// + uint8 xSize + uint8 ySize + int16 clevel + int16 font
	// + int32 maxHP + int32 HP + uint8 isBoss + int16 body + char name[24]
	// = 2+2+1+4+4+2+2+2+4+2+2+4+4+2+2+2+2+2+2+2+4+2+2+4+1+1+3+1+1+2+2+4+4+1+2+24
	// = 107 (rathena/src/map/packets.hpp ZC_SPAWN_UNIT, PACKETVER >= 20150513).
	sizeZCSpawnUnit = 107
	// sizeSpawnUnitName is the on-wire name field width in
	// ZC_SPAWN_UNIT (rathena/src/map/packets.hpp ZC_SPAWN_UNIT::name).
	sizeSpawnUnitName = 24
)

// NewMapServerDB returns a packet database pre-populated with all known
// map-server packet definitions for Thai Classic (PACKETVER 20250604).
//
// Outbound (S→C) entries register both the small fixed packets we encode
// here (ZC_REFUSE_ENTER, ZC_ACCEPT_ENTER) and the inbound (C→S) parsers.
// Unlike the login and char databases, the map server has no single large
// accept packet; the connection sequence is a short CZ_ENTER exchange
// followed by the bulk packet stream (movement, chat, skills, etc.) which
// land in M3b+ as their own packet tables.
//
// All on-wire sizes and opcode IDs are sourced from rathena/src/map/
// packets.hpp (ZC_*) and rathena/src/map/clif.cpp (CZ_*).
func NewMapServerDB() *DB {
	db := NewDB()

	// --- C→S: client → map server.
	db.Register(Definition{
		ID:        HeaderCZENTER,
		Name:      "CZ_ENTER",
		Length:    sizeCZEnter,
		Direction: DirectionClientToServer,
	})
	db.Register(Definition{
		ID:        HeaderCZREQUESTMOVE,
		Name:      "CZ_REQUEST_MOVE",
		Length:    sizeCZRequestMove,
		Direction: DirectionClientToServer,
	})

	// --- S→C: map server → client.
	db.Register(Definition{
		ID:        HeaderZCREFUSEENTER,
		Name:      "ZC_REFUSE_ENTER",
		Length:    sizeZCRefuseEnter,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCACCEPTENTER,
		Name:      "ZC_ACCEPT_ENTER",
		Length:    sizeZCAcceptEnter,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCNOTIFYPLAYERMOVE,
		Name:      "ZC_NOTIFY_PLAYERMOVE",
		Length:    sizeZCNotifyPlayerMove,
		Direction: DirectionServerToClient,
	})
	db.Register(Definition{
		ID:        HeaderZCSPAWNUNIT,
		Name:      "ZC_SPAWN_UNIT",
		Length:    sizeZCSpawnUnit,
		Direction: DirectionServerToClient,
	})

	return db
}
