package packet

import (
	"encoding/binary"
	"fmt"
	"io"
)

// MapAcceptEnterResponse encodes a ZC_ACCEPT_ENTER packet (command 0x02eb,
// active for PACKETVER < 20141022 || PACKETVER >= 20160330 — which covers
// Thai Classic 20250604). Layout source: rathena/src/map/packets.hpp:562-571.
//
// Fixed wire length: 13 bytes (int16 packetType + uint32 startTime +
// uint8 posDir[3] + uint8 xSize + uint8 ySize + uint16 font).
//
// The posDir[3] slot uses rAthena's kRO 3-byte packed position encoding
// (clif.cpp:173-178 WBUFPOS). xSize/ySize are written as the literal 5 in
// rAthena's clif.cpp output site (with an "ignored" comment) so callers
// can safely hardcode 5/5 and let the client ignore them.
type MapAcceptEnterResponse struct {
	// StartTime is the map server's monotone tick at the moment of the
	// enter handshake (rathena's `startTime`).
	StartTime uint32
	// PosX is the spawn cell X (cell coordinate).
	PosX int16
	// PosY is the spawn cell Y (cell coordinate).
	PosY int16
	// Dir is the spawn facing direction (0–15 in kRO; the lower 4 bits
	// are packed into posDir[2]).
	Dir uint8
	// XSize is the sprite width hint (rAthena hardcodes 5).
	XSize uint8
	// YSize is the sprite height hint (rAthena hardcodes 5).
	YSize uint8
	// Font is the font ID for the client-side UI overlay.
	Font uint16
}

// Size returns the on-wire byte length that Encode will write (always 13).
func (r MapAcceptEnterResponse) Size() int {
	return sizeZCAcceptEnter
}

// Encode writes the ZC_ACCEPT_ENTER packet to w.
func (r MapAcceptEnterResponse) Encode(w io.Writer) error {
	buf := make([]byte, sizeZCAcceptEnter)
	// int16 packetType = 0x02eb (HeaderZCACCEPTENTER).
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCACCEPTENTER)
	// uint32 startTime at offset 2.
	binary.LittleEndian.PutUint32(buf[2:], r.StartTime)
	// uint8 posDir[3] at offset 6 — kRO 3-byte packed position.
	encodePos(buf[6:9], r.PosX, r.PosY, r.Dir)
	// uint8 xSize at offset 9.
	buf[9] = r.XSize
	// uint8 ySize at offset 10.
	buf[10] = r.YSize
	// uint16 font at offset 11.
	binary.LittleEndian.PutUint16(buf[11:], r.Font)

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write ZC_ACCEPT_ENTER: %w", err)
	}
	return nil
}

// MapRefuseEnterResponse encodes a ZC_REFUSE_ENTER packet (command 0x0074).
// Layout source: rathena/src/map/packets.hpp:585-590.
//
// Fixed wire length: 3 bytes (int16 packetType + uint8 errorCode).
type MapRefuseEnterResponse struct {
	// Error is the 8-bit error code (rAthena's REFUSE_ENTER_* enum).
	Error uint8
}

// Size returns the on-wire byte length that Encode will write (always 3).
func (r MapRefuseEnterResponse) Size() int {
	return sizeZCRefuseEnter
}

// Encode writes the ZC_REFUSE_ENTER packet to w.
func (r MapRefuseEnterResponse) Encode(w io.Writer) error {
	buf := make([]byte, sizeZCRefuseEnter)
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCREFUSEENTER)
	buf[2] = r.Error

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write ZC_REFUSE_ENTER: %w", err)
	}
	return nil
}

// MapNotifyPlayerMoveResponse encodes a ZC_NOTIFY_PLAYERMOVE packet
// (command 0x0087). The server broadcasts this to nearby clients every
// time a player's path is computed, so each peer can interpolate the
// sprite from src to dest. Layout source:
//
//	rathena/src/map/clif.cpp clif_movemoveok (around the moveOk call site)
//		and rathena/src/map/packets.hpp ZC_NOTIFY_PLAYERMOVE.
//
// Fixed wire length: 12 bytes (int16 packetType + uint32 moveStartTime +
// uint8 srcPos[3] + uint8 destPos[3]).
//
// The srcPos[3] and destPos[3] slots use rAthena's kRO 3-byte packed
// position encoding (clif.cpp:173-178 WBUFPOS); the direction byte is
// zero on both ends because the broadcast packet only describes the
// path endpoints, not the per-cell facing.
//
// MoveStartTime is the server's monotone tick at the moment the path
// was accepted — rAthena writes the same value into the local player's
// session so subsequent CZ_REQUEST_MOVE packets can be anti-DoS-checked
// against it.
type MapNotifyPlayerMoveResponse struct {
	// MoveStartTime is the map server's monotone tick at the moment
	// the path was accepted (rathena's `startTime`).
	MoveStartTime uint32
	// SrcX, SrcY is the cell the move started from.
	SrcX, SrcY int16
	// DestX, DestY is the cell the path targets.
	DestX, DestY int16
}

// Size returns the on-wire byte length that Encode will write (always 12).
func (r MapNotifyPlayerMoveResponse) Size() int {
	return sizeZCNotifyPlayerMove
}

// Encode writes the ZC_NOTIFY_PLAYERMOVE packet to w.
func (r MapNotifyPlayerMoveResponse) Encode(w io.Writer) error {
	buf := make([]byte, sizeZCNotifyPlayerMove)
	// int16 packetType = 0x0087 (HeaderZCNOTIFYPLAYERMOVE).
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCNOTIFYPLAYERMOVE)
	// uint32 moveStartTime at offset 2.
	binary.LittleEndian.PutUint32(buf[2:], r.MoveStartTime)
	// uint8 srcPos[3] at offset 6 — kRO 3-byte packed source position.
	encodePos(buf[6:9], r.SrcX, r.SrcY, 0)
	// uint8 destPos[3] at offset 9 — kRO 3-byte packed destination position.
	encodePos(buf[9:12], r.DestX, r.DestY, 0)

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write ZC_NOTIFY_PLAYERMOVE: %w", err)
	}
	return nil
}

// SpawnUnitResponse encodes a ZC_SPAWN_UNIT packet (command 0x09fe,
// active for PACKETVER >= 20150513 — which covers Thai Classic
// 20250604). The server sends this to spawn an entity on the client's
// map view; for the player's own entity, the gateway emits it
// immediately after ZC_ACCEPT_ENTER so the client learns the AID/GID
// it should attribute local input to. Layout source:
//
//	rathena/src/map/packets.hpp ZC_SPAWN_UNIT (PACKETVER >= 20150513 branch)
//		rathena/src/map/clif.cpp clif_spawn (the clif_spawn→clif_spawn_unit
//		emission site for the self-spawn path).
//
// Fixed wire length: 107 bytes (uint16 packetType + uint16 packetLength
// + uint8 objectType + uint32 AID + uint32 GID + int16 speed + int16
// bodyState + int16 healthState + int32 effectState + int16 job +
// uint16 head + uint32 weapon + uint32 shield + uint16 accessory +
// uint16 accessory2 + uint16 accessory3 + int16 headPalette + int16
// bodyPalette + int16 headDir + uint16 robe + uint32 GUID + int16
// GEmblemVer + int16 honor + int32 virtue + uint8 isPKModeON + uint8
// sex + uint8 posDir[3] + uint8 xSize + uint8 ySize + int16 clevel +
// int16 font + int32 maxHP + int32 HP + uint8 isBoss + int16 body +
// char name[24]).
//
// PosDir uses rAthena's kRO 3-byte packed position encoding
// (clif.cpp:173-178 WBUFPOS). Name is written as a fixed-width
// 24-byte field; callers must supply a UTF-8 name and any bytes past
// len(Name) are null-padded, matching rAthena's memcpy-with-NUL-fill
// pattern. Names longer than 24 bytes are truncated.
type SpawnUnitResponse struct {
	// ObjectType is the entity class: 0=PC, 5=MOB (NPC_MOB_TYPE),
	// 6=NPC_EVT (rAthena's clif_bl_type). The gateway emits 0 for the
	// self-spawn, 6 for NPC entities (M14), and 5 for monsters (M17).
	ObjectType uint8
	// AID is the account ID (rAthena's `account_id`). For a PC self-spawn
	// this equals GID.
	AID uint32
	// GID is the entity ID (rAthena's `id`). For a PC self-spawn this
	// equals AID.
	GID uint32
	// Speed is the walk speed in kRO units (150 = default PC).
	Speed int16
	// BodyState is the body animation state (0 = standing).
	BodyState int16
	// HealthState is the health overlay state (0 = normal,
	// 1 = poisoned, 2 = dead sit, etc.).
	HealthState int16
	// EffectState is the cumulative status-effect bitmask (0 = none).
	EffectState int32
	// Job is the job class ID (0 = novice, 1 = swordsman, etc.).
	Job int16
	// Head is the hair-style view sprite ID.
	Head uint16
	// Weapon is the equipped weapon view sprite.
	Weapon uint32
	// Shield is the equipped shield view sprite.
	Shield uint32
	// Accessory is the headgear (bottom) view sprite.
	Accessory uint16
	// Accessory2 is the headgear (top) view sprite.
	Accessory2 uint16
	// Accessory3 is the headgear (mid) view sprite.
	Accessory3 uint16
	// HeadPalette is the hair color (palette index).
	HeadPalette int16
	// BodyPalette is the body/clothes color (palette index).
	BodyPalette int16
	// HeadDir is the head-facing direction (separate from body dir,
	// used for "looking sideways" overlays).
	HeadDir int16
	// Robe is the robe overlay sprite ID.
	Robe uint16
	// GUID is the guild ID (0 = no guild).
	GUID uint32
	// GEmblemVer is the guild-emblem version the client should request
	// (0 = no emblem; bumped each time the guild changes its emblem).
	GEmblemVer int16
	// Honor is the honor/rank points (rAthena's `honor`).
	Honor int16
	// Virtue is the fame/virtue value (rAthena's `virtue`).
	Virtue int32
	// IsPKModeON is the PK-mode flag (0 = off, 1 = on).
	IsPKModeON uint8
	// Sex is the sex byte: 0=female, 1=male, 2=server (rAthena's
	// account sex). The PC self-spawn uses the CZ_ENTER sex byte.
	Sex uint8
	// PosX, PosY are the spawn cell coordinates.
	PosX int16
	PosY int16
	// Dir is the spawn facing direction (0..15 in kRO; the lower 4
	// bits are packed into posDir[2]).
	Dir uint8
	// XSize, YSize are the collision-size hints (rAthena hardcodes 5
	// for PCs; clients tolerate any value but the server convention is
	// 5/5).
	XSize uint8
	YSize uint8
	// CLevel is the character level.
	CLevel int16
	// Font is the font ID for the client-side name overlay (0 = default).
	Font int16
	// MaxHP is the maximum HP.
	MaxHP int32
	// HP is the current HP at the moment of spawn.
	HP int32
	// IsBoss is the boss-monster flag (0 = normal; non-zero for MOB
	// spawns only — the PC self-spawn always writes 0).
	IsBoss uint8
	// Body is the body/clothes sprite ID.
	Body int16
	// Name is the character name (UTF-8 for Thai Classic). The encoder
	// null-pads to 24 bytes; names longer than 24 bytes are truncated
	// to the first 24 bytes (rAthena's memcpy(name, src, 24) pattern).
	Name string
}

// Size returns the on-wire byte length that Encode will write (always 107).
func (r SpawnUnitResponse) Size() int {
	return sizeZCSpawnUnit
}

// Encode writes the ZC_SPAWN_UNIT packet to w.
func (r SpawnUnitResponse) Encode(w io.Writer) error {
	return encodeSpawnUnitLike(w, HeaderZCSPAWNUNIT, r)
}

// encodeSpawnUnitLike writes a ZC_SPAWN_UNIT / ZC_SET_UNIT_IDLE packet
// to w using the supplied opcode. Both packets share the same struct
// layout (packet_spawn_unit / packet_idle_unit) for PACKETVER 20250604;
// only the opcode differs. rAthena's clif_spawn_unit and
// clif_set_unit_idle write the same fields in the same order.
func encodeSpawnUnitLike(w io.Writer, opcode uint16, r SpawnUnitResponse) error {
	buf := make([]byte, sizeZCSpawnUnit)
	binary.LittleEndian.PutUint16(buf[0:], opcode)
	binary.LittleEndian.PutUint16(buf[2:], sizeZCSpawnUnit)
	buf[4] = r.ObjectType
	binary.LittleEndian.PutUint32(buf[5:], r.AID)
	binary.LittleEndian.PutUint32(buf[9:], r.GID)
	binary.LittleEndian.PutUint16(buf[13:], uint16(r.Speed))       //nolint:gosec // wire slot is unsigned
	binary.LittleEndian.PutUint16(buf[15:], uint16(r.BodyState))   //nolint:gosec // ditto
	binary.LittleEndian.PutUint16(buf[17:], uint16(r.HealthState)) //nolint:gosec // ditto
	binary.LittleEndian.PutUint32(buf[19:], uint32(r.EffectState)) //nolint:gosec // ditto
	binary.LittleEndian.PutUint16(buf[23:], uint16(r.Job))         //nolint:gosec // ditto
	binary.LittleEndian.PutUint16(buf[25:], r.Head)
	binary.LittleEndian.PutUint32(buf[27:], r.Weapon)
	binary.LittleEndian.PutUint32(buf[31:], r.Shield)
	binary.LittleEndian.PutUint16(buf[35:], r.Accessory)
	binary.LittleEndian.PutUint16(buf[37:], r.Accessory2)
	binary.LittleEndian.PutUint16(buf[39:], r.Accessory3)
	binary.LittleEndian.PutUint16(buf[41:], uint16(r.HeadPalette)) //nolint:gosec // wire slot is unsigned
	binary.LittleEndian.PutUint16(buf[43:], uint16(r.BodyPalette)) //nolint:gosec // ditto
	binary.LittleEndian.PutUint16(buf[45:], uint16(r.HeadDir))     //nolint:gosec // ditto
	binary.LittleEndian.PutUint16(buf[47:], r.Robe)
	binary.LittleEndian.PutUint32(buf[49:], r.GUID)
	binary.LittleEndian.PutUint16(buf[53:], uint16(r.GEmblemVer)) //nolint:gosec // wire slot is unsigned
	binary.LittleEndian.PutUint16(buf[55:], uint16(r.Honor))      //nolint:gosec // ditto
	binary.LittleEndian.PutUint32(buf[57:], uint32(r.Virtue))     //nolint:gosec // ditto
	buf[61] = r.IsPKModeON
	buf[62] = r.Sex
	encodePos(buf[63:66], r.PosX, r.PosY, r.Dir)
	buf[66] = r.XSize
	buf[67] = r.YSize
	binary.LittleEndian.PutUint16(buf[68:], uint16(r.CLevel)) //nolint:gosec // wire slot is unsigned
	binary.LittleEndian.PutUint16(buf[70:], uint16(r.Font))   //nolint:gosec // ditto
	binary.LittleEndian.PutUint32(buf[72:], uint32(r.MaxHP))  //nolint:gosec // ditto
	binary.LittleEndian.PutUint32(buf[76:], uint32(r.HP))     //nolint:gosec // ditto
	buf[80] = r.IsBoss
	binary.LittleEndian.PutUint16(buf[81:], uint16(r.Body)) //nolint:gosec // wire slot is unsigned
	nameBytes := []byte(r.Name)
	if len(nameBytes) > sizeSpawnUnitName {
		nameBytes = nameBytes[:sizeSpawnUnitName]
	}
	copy(buf[83:83+len(nameBytes)], nameBytes)

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write 0x%04x: %w", opcode, err)
	}
	return nil
}

// SetUnitIdleResponse is ZC_SET_UNIT_IDLE (0x9ff) — same layout as
// SpawnUnitResponse but with a different opcode. NPCs use this packet
// instead of ZC_SPAWN_UNIT (see rAthena clif_set_unit_idle).
type SetUnitIdleResponse SpawnUnitResponse

// Size returns the on-wire byte length that Encode will write (always 107).
func (r SetUnitIdleResponse) Size() int {
	return sizeZCSetUnitIdle
}

// Encode writes the ZC_SET_UNIT_IDLE packet to w.
func (r SetUnitIdleResponse) Encode(w io.Writer) error {
	return encodeSpawnUnitLike(w, HeaderZCSETUNITIDLE, SpawnUnitResponse(r))
}

// MapPropertyResponse is the ZC_MAPPROPERTY_R2 packet (0x099b, 8 bytes).
// It tells the client the map's property type (PVP, GVG, normal) and
// feature flags. For normal maps PropertyType=0 (MAPPROPERTY_NOTHING)
// and Flags=0.
//
// Layout: [2:cmd=0x099b][2:propertyType][4:flags].
// Source: rathena/src/map/clif.cpp:6869-6902.
type MapPropertyResponse struct {
	// PropertyType is the map property class (rAthena's MAPPROPERTY_*
	// enum: 0 = MAPPROPERTY_NOTHING, 1 = PVP, 2 = GVG, 3 = GVG_TE,
	// etc.). The gateway advertises NOTHING for every map today —
	// map flag computation from zone config is deferred.
	PropertyType uint16
	// Flags is the cumulative bitmask of feature toggles
	// (PARTY, GUILD, WHISPER, etc. — rAthena's
	// MAPPROPERTY_PARTY / MAPPROPERTY_GUILD / etc. flags).
	// Always 0 today; the gateway has no map flag data yet.
	Flags uint32
}

// Size returns the on-wire byte length that Encode will write (always 8).
func (r MapPropertyResponse) Size() int {
	return sizeZCMapPropertyR2
}

// Encode writes the ZC_MAPPROPERTY_R2 packet to w.
func (r MapPropertyResponse) Encode(w io.Writer) error {
	buf := make([]byte, sizeZCMapPropertyR2)
	// int16 packetType = 0x099b (HeaderZCMAPPROPERTYR2).
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCMAPPROPERTYR2)
	// int16 propertyType at offset 2.
	binary.LittleEndian.PutUint16(buf[2:], r.PropertyType)
	// uint32 flags at offset 4.
	binary.LittleEndian.PutUint32(buf[4:], r.Flags)

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write ZC_MAPPROPERTY_R2: %w", err)
	}
	return nil
}

// NotifyTimeResponse is the ZC_NOTIFY_TIME packet (0x007f, 6 bytes).
// It carries the server's current tick so the client can estimate
// round-trip latency. rAthena uses gettick() (monotonic); the gateway
// uses unix millis (low 32 bits) as a stateless equivalent.
//
// Layout: [2:cmd=0x007f][4:time].
// Source: rathena/src/map/clif.cpp:11186-11193.
type NotifyTimeResponse struct {
	// Time is the server tick at the moment of the reply — unix
	// millis low 32 bits (rAthena's `gettick()` is monotonic; the
	// client only uses this for latency estimation, so an
	// epoch-relative value is the pragmatic equivalent for a
	// stateless gateway).
	Time uint32
}

// Size returns the on-wire byte length that Encode will write (always 6).
func (r NotifyTimeResponse) Size() int {
	return sizeZCNotifyTime
}

// Encode writes the ZC_NOTIFY_TIME packet to w.
func (r NotifyTimeResponse) Encode(w io.Writer) error {
	buf := make([]byte, sizeZCNotifyTime)
	// int16 packetType = 0x007f (HeaderZCNOTIFYTIME).
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCNOTIFYTIME)
	// uint32 time at offset 2.
	binary.LittleEndian.PutUint32(buf[2:], r.Time)

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write ZC_NOTIFY_TIME: %w", err)
	}
	return nil
}

// StatusResponse encodes ZC_STATUS (0x00bd) — the initial character
// status block sent after map load. Carries base stats, their upgrade
// costs, and derived combat values (ATK/DEF/MATK/MDEF/HIT/FLEE/CRIT/ASPD).
//
// Wire layout (rathena/src/map/packets.hpp:909-938):
//
//	int16  packetType
//	uint16 point           (status_point)
//	uint8  str, uint8 standardStr
//	uint8  agi, uint8 standardAgi
//	uint8  vit, uint8 standardVit
//	uint8  int, uint8 standardInt
//	uint8  dex, uint8 standardDex
//	uint8  luk, uint8 standardLuk
//	int16  attPower        (ATK1 / left-side ATK)
//	int16  refiningPower   (ATK2 / right-side ATK)
//	int16  maxMattPower    (MATK max)
//	int16  minMattPower    (MATK min)
//	int16  itemdefPower    (DEF1 / left-side DEF)
//	int16  plusdefPower    (DEF2 / right-side DEF)
//	int16  mdefPower       (MDEF1 / left-side MDEF)
//	int16  plusmdefPower   (MDEF2 / right-side MDEF)
//	int16  hitSuccessValue (HIT)
//	int16  avoidSuccessValue (FLEE)
//	int16  plusAvoidSuccessValue (FLEE2 / 10)
//	int16  criticalSuccessValue (CRITICAL / 10)
//	int16  ASPD
//	int16  plusASPD
type StatusResponse struct {
	StatusPoint  uint16
	Str, NeedStr uint8
	Agi, NeedAgi uint8
	Vit, NeedVit uint8
	Int, NeedInt uint8
	Dex, NeedDex uint8
	Luk, NeedLuk uint8
	Atk1         int16
	Atk2         int16
	MatkMax      int16
	MatkMin      int16
	Def1         int16
	Def2         int16
	Mdef1        int16
	Mdef2        int16
	Hit          int16
	Flee         int16
	Flee2        int16
	Critical     int16
	ASPD         int16
	PlusASPD     int16
}

// Size returns the on-wire byte length that Encode will write (always 44).
func (r StatusResponse) Size() int {
	return sizeZCStatus
}

// Encode writes the ZC_STATUS packet to w.
func (r StatusResponse) Encode(w io.Writer) error {
	var buf [sizeZCStatus]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCSTATUS)
	binary.LittleEndian.PutUint16(buf[2:], r.StatusPoint)
	buf[4] = r.Str
	buf[5] = r.NeedStr
	buf[6] = r.Agi
	buf[7] = r.NeedAgi
	buf[8] = r.Vit
	buf[9] = r.NeedVit
	buf[10] = r.Int
	buf[11] = r.NeedInt
	buf[12] = r.Dex
	buf[13] = r.NeedDex
	buf[14] = r.Luk
	buf[15] = r.NeedLuk
	binary.LittleEndian.PutUint16(buf[16:], uint16(r.Atk1))     //nolint:gosec // wire slot is unsigned
	binary.LittleEndian.PutUint16(buf[18:], uint16(r.Atk2))     //nolint:gosec // ditto
	binary.LittleEndian.PutUint16(buf[20:], uint16(r.MatkMax))  //nolint:gosec // ditto
	binary.LittleEndian.PutUint16(buf[22:], uint16(r.MatkMin))  //nolint:gosec // ditto
	binary.LittleEndian.PutUint16(buf[24:], uint16(r.Def1))     //nolint:gosec // ditto
	binary.LittleEndian.PutUint16(buf[26:], uint16(r.Def2))     //nolint:gosec // ditto
	binary.LittleEndian.PutUint16(buf[28:], uint16(r.Mdef1))    //nolint:gosec // ditto
	binary.LittleEndian.PutUint16(buf[30:], uint16(r.Mdef2))    //nolint:gosec // ditto
	binary.LittleEndian.PutUint16(buf[32:], uint16(r.Hit))      //nolint:gosec // ditto
	binary.LittleEndian.PutUint16(buf[34:], uint16(r.Flee))     //nolint:gosec // ditto
	binary.LittleEndian.PutUint16(buf[36:], uint16(r.Flee2))    //nolint:gosec // ditto
	binary.LittleEndian.PutUint16(buf[38:], uint16(r.Critical)) //nolint:gosec // ditto
	binary.LittleEndian.PutUint16(buf[40:], uint16(r.ASPD))     //nolint:gosec // ditto
	binary.LittleEndian.PutUint16(buf[42:], uint16(r.PlusASPD)) //nolint:gosec // ditto
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_STATUS: %w", err)
	}
	return nil
}

// ParChangeResponse encodes ZC_PAR_CHANGE (0x00b0) — a single 32-bit
// status parameter update. Used for HP, SP, level, stats, weight, etc.
//
// Wire layout (rathena/src/map/packets_struct.hpp:354-358):
//
//	int16  packetType
//	uint16 varID
//	int32  count
type ParChangeResponse struct {
	VarID uint16
	Count int32
}

// Size returns the on-wire byte length that Encode will write (always 8).
func (r ParChangeResponse) Size() int {
	return sizeZCParChange
}

// Encode writes the ZC_PAR_CHANGE packet to w.
func (r ParChangeResponse) Encode(w io.Writer) error {
	var buf [sizeZCParChange]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCPARCHANGE)
	binary.LittleEndian.PutUint16(buf[2:], r.VarID)
	binary.LittleEndian.PutUint32(buf[4:], uint32(r.Count)) //nolint:gosec // wire slot is unsigned
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_PAR_CHANGE: %w", err)
	}
	return nil
}

// LongParChangeResponse encodes ZC_LONGPAR_CHANGE (0x00b1) — a single
// 32-bit status parameter update. Used for zeny and (32-bit) exp.
//
// Wire layout (rathena/src/map/packets_struct.hpp:361-365):
//
//	int16  packetType
//	uint16 varID
//	int32  amount
type LongParChangeResponse struct {
	VarID  uint16
	Amount int32
}

// Size returns the on-wire byte length that Encode will write (always 8).
func (r LongParChangeResponse) Size() int {
	return sizeZCLongParChange
}

// Encode writes the ZC_LONGPAR_CHANGE packet to w.
func (r LongParChangeResponse) Encode(w io.Writer) error {
	var buf [sizeZCLongParChange]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCLONGPARCHANGE)
	binary.LittleEndian.PutUint16(buf[2:], r.VarID)
	binary.LittleEndian.PutUint32(buf[4:], uint32(r.Amount)) //nolint:gosec // wire slot is unsigned
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_LONGPAR_CHANGE: %w", err)
	}
	return nil
}

// StatusPointCost returns the status points needed to raise a stat from
// its current value by 1, using the pre-Renewal formula.
//
// Source: rathena/src/map/pc.cpp:8803 (non-RENEWAL branch):
//
//	#define PC_STATUS_POINT_COST(low) ((1 + ((low) + 9) / 10))
func StatusPointCost(currentVal uint8) uint8 {
	// Promote to int before arithmetic — uint8 + 9 overflows for
	// currentVal >= 247 and would wrap the cost back to 1.
	return uint8(1 + (int(currentVal)+9)/10) //nolint:gosec // max return is 1+(255+9)/10 = 27
}

// The four empty list packets are completely static — their bytes never
// change — so they are built once at package init and shared by every
// caller. Callers must treat the returned slices as read-only.
var (
	emptyInventoryListNormal []byte
	emptyInventoryListEquip  []byte
	emptySkillList           []byte
	emptyHotkeyList          []byte
)

func init() {
	emptyInventoryListNormal = make([]byte, sizeEmptyInventoryList)
	binary.LittleEndian.PutUint16(emptyInventoryListNormal[0:], HeaderZCINVENTORYITEMLISTNORMAL)
	binary.LittleEndian.PutUint16(emptyInventoryListNormal[2:], sizeEmptyInventoryList)

	emptyInventoryListEquip = make([]byte, sizeEmptyInventoryList)
	binary.LittleEndian.PutUint16(emptyInventoryListEquip[0:], HeaderZCINVENTORYITEMLISTEQUIP)
	binary.LittleEndian.PutUint16(emptyInventoryListEquip[2:], sizeEmptyInventoryList)

	emptySkillList = make([]byte, sizeEmptyInventoryList)
	binary.LittleEndian.PutUint16(emptySkillList[0:], HeaderZCSKILLINFOLIST)
	binary.LittleEndian.PutUint16(emptySkillList[2:], sizeEmptyInventoryList)

	emptyHotkeyList = make([]byte, sizeZCShortcutKeyList)
	binary.LittleEndian.PutUint16(emptyHotkeyList[0:], HeaderZCSHORTCUTKEYLIST)
	// Remaining 189 bytes are zero (make already initializes to 0).
}

// EncodeEmptyInventoryListNormal returns the pre-allocated on-wire bytes
// for an empty ZC_INVENTORY_ITEMLIST_NORMAL packet (opcode 0x00a3, 4
// bytes). Callers must not modify the returned slice.
//
// Layout: [2:cmd=0x00a3][2:packetLength=4] (no NORMALITEM_INFO entries).
//
// rAthena's clif_inventorylist (rathena/src/map/clif.cpp:3060) only sends
// this packet when at least one stackable item is present; for a fresh
// character with no items, an empty frame still needs to arrive so the
// client initializes its inventory UI. Sending the 4-byte header with
// packetLength=4 matches the rAthena minimum-frame convention: the
// flexible-array count is implicit (wire length minus the header
// divided by the per-entry size = 0).
//
// Source: rathena/src/map/packets_struct.hpp packet_itemlist_normal
// (ZC_INVENTORY_ITEMLIST_NORMAL); opcode at rathena/src/map/
// clif_packetdb.hpp.
func EncodeEmptyInventoryListNormal() []byte {
	return emptyInventoryListNormal
}

// EncodeEmptyInventoryListEquip returns the pre-allocated on-wire bytes
// for an empty ZC_INVENTORY_ITEMLIST_EQUIP packet (opcode 0x00a4, 4
// bytes). Callers must not modify the returned slice.
//
// Layout: [2:cmd=0x00a4][2:packetLength=4] (no EQUIPITEM_INFO entries).
//
// See EncodeEmptyInventoryListNormal for the empty-frame rationale. The
// equip variant carries the player's currently-equipped items, so for
// a fresh character without gear both list packets must arrive empty
// to populate the equipment and inventory slots in the client UI.
func EncodeEmptyInventoryListEquip() []byte {
	return emptyInventoryListEquip
}

// EncodeEmptySkillList returns the pre-allocated on-wire bytes for an
// empty ZC_SKILLINFO_LIST packet (opcode 0x010f, 4 bytes). Callers must
// not modify the returned slice.
//
// Layout: [2:cmd=0x010f][2:packetLength=4] (no SKILLDATA entries).
//
// rAthena's clif_skillinfoblock (rathena/src/map/clif.cpp:5694) sends
// this only when the player has at least one learned skill; for a
// fresh novice character the empty 4-byte header is what the client
// expects to see so it initializes the skill list pane without
// leaving it in an indeterminate state.
func EncodeEmptySkillList() []byte {
	return emptySkillList
}

// EncodeEmptyHotkeyList returns the pre-allocated on-wire bytes for an
// empty ZC_SHORTCUT_KEY_LIST packet (opcode 0x02b9, 191 bytes). Callers
// must not modify the returned slice.
//
// Layout: [2:cmd=0x02b9] then 27 zero-filled hotkey_data slots, each
// 7 bytes wide (int8 isSkill + uint32 id + int16 count). Total wire
// length is 2 + 27*7 = 191 bytes.
//
// Unlike the inventory/skill lists, the hotkey list is fixed-length:
// rAthena always emits every slot regardless of how many the client
// actually configured, and the slot count is encoded in the PACKETVER
// struct shape (rathena/src/map/packets_struct.hpp:1613-1619 — the
// PACKETVER < 20090603 branch gives MAX_HOTKEYS_PACKET=27 and opcode
// 0x02b9). Zero-filling every slot means "no hotkey bound" for the
// client. hotkey_data is declared at
// rathena/src/map/packets_struct.hpp:1576-1580.
func EncodeEmptyHotkeyList() []byte {
	return emptyHotkeyList
}

// NotifyChatResponse encodes a ZC_NOTIFY_CHAT packet (command 0x008d,
// variable length). The server sends this to broadcast a public chat
// message to nearby clients; the gateway uses it as the single-player
// echo path for CZ_GLOBAL_MESSAGE (no AOI yet).
//
// Source: rathena/src/map/packets_struct.hpp:2337
// (`PACKET_ZC_NOTIFY_CHAT { int16 PacketType; int16 PacketLength;
// uint32 GID; char Message[] }`) +
// rathena/src/map/clif.cpp:6752-6769 (clif_GlobalMessage). rAthena
// broadcasts `<name> : <text>` — the dispatcher layer is responsible
// for prepending the name; this encoder writes the message verbatim.
//
// The wire length is 2+2+4 + len(message)+1 (one byte for the NUL
// terminator rAthena writes via safestrncpy). The NUL is appended
// unconditionally even when message already ends with one — matching
// rAthena's `safestrncpy(p->Message, message, len)` semantics where
// `len = strlen(message) + 1`.
type NotifyChatResponse struct {
	// GID is the entity ID of the speaker (rAthena's `bl.id`). For the
	// gateway's single-player echo the gateway substitutes the
	// connection's authenticated AID until a true zone-resident GID is
	// available — AID is the only persistent identifier the gateway
	// caches today.
	GID uint32
	// Message is the text the client sees in the chat log. UTF-8 for
	// Thai Classic.
	Message string
}

// Encode writes the ZC_NOTIFY_CHAT packet to w. The wire shape is
// [2:cmd=0x008d][2:packetLength][4:GID][n:message+null]; the encoder
// computes packetLength from the message size rather than trusting a
// precomputed field so the caller cannot accidentally emit a packet
// whose length slot disagrees with the trailing bytes.
func (r NotifyChatResponse) Encode(w io.Writer) error {
	msgBytes := []byte(r.Message)
	// 4 (header) + 4 (GID) + len(msg) + 1 (NUL terminator).
	total := 4 + 4 + len(msgBytes) + 1
	if total > 0xffff {
		return fmt.Errorf("packet: write ZC_NOTIFY_CHAT: message too long (%d bytes)", len(msgBytes))
	}
	buf := make([]byte, total)
	// int16 packetType = 0x008d (HeaderZCNOTIFYCHAT).
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCNOTIFYCHAT)
	// int16 packetLength at offset 2 — full frame length including header.
	binary.LittleEndian.PutUint16(buf[2:], uint16(total))
	// uint32 GID at offset 4.
	binary.LittleEndian.PutUint32(buf[4:], r.GID)
	// char Message[] at offset 8 + trailing NUL.
	copy(buf[8:], msgBytes)
	// buf[8+len(msgBytes)] is already 0x00 from make().

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write ZC_NOTIFY_CHAT: %w", err)
	}
	return nil
}

// ActionResponse encodes a ZC_ACTION_RESPONSE packet (command 0x008b,
// 11 bytes fixed). The server broadcasts this to echo a player's
// sit/stand/attack action back to nearby clients; the gateway uses it
// as the single-player echo path for CZ_ACTION_REQUEST (no AOI yet).
//
// Wire shape: [2:cmd=0x008b][4:GID][1:action][4:targetGID] = 11 bytes.
//
// rAthena's modern sit/stand broadcast actually goes through
// ZC_NOTIFY_ACT (0x008a) — see clif_sitting/clif_standing in
// rathena/src/map/clif.cpp:5327-5358. We use the compact 0x008b shape
// for the single-player echo path because (a) it is what the rAthena
// packetdb registers at clif_packetdb.hpp:39 and (b) the only client
// behavior that depends on the opcode is the on-screen sprite update,
// which 0x008b also drives correctly for the local player.
//
// Source: rathena/src/map/clif_packetdb.hpp:39 +
// clif_parse_ActionRequest at rathena/src/map/clif.cpp:11816-11829
// (the request side) for the field naming.
type ActionResponse struct {
	// GID is the entity ID of the actor — the player whose action is
	// being echoed. For sit/stand this is the player's own GID.
	GID uint32
	// Action is the action selector byte, copied verbatim from the
	// incoming CZ_ACTION_REQUEST.
	Action uint8
	// TargetGID is the entity the action targets — for sit/stand this
	// is the player's own GID (self-targeted); for attack it is the
	// victim's GID. The single-player echo always sends 0 since there
	// is no separate "self" field on the wire that the client can
	// disambiguate from "no target".
	TargetGID uint32
}

// Size returns the on-wire byte length that Encode will write (always 11).
func (r ActionResponse) Size() int {
	return sizeZCActionResponse
}

// Encode writes the ZC_ACTION_RESPONSE packet to w.
func (r ActionResponse) Encode(w io.Writer) error {
	buf := make([]byte, sizeZCActionResponse)
	// int16 packetType = 0x008b (HeaderZCACTIONRESPONSE).
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCACTIONRESPONSE)
	// uint32 GID at offset 2.
	binary.LittleEndian.PutUint32(buf[2:], r.GID)
	// uint8 action at offset 6.
	buf[6] = r.Action
	// uint32 targetGID at offset 7.
	binary.LittleEndian.PutUint32(buf[7:], r.TargetGID)

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write ZC_ACTION_RESPONSE: %w", err)
	}
	return nil
}

// ChangeDirResponse encodes a ZC_CHANGE_DIRECTION packet (command
// 0x009c, 9 bytes fixed). The server broadcasts this to echo a
// player's body/head direction change to nearby clients; the gateway
// uses it as the single-player echo path for CZ_CHANGE_DIRECTION (no
// AOI yet).
//
// Wire shape (rathena/src/map/packets.hpp:688-694 +
// rathena/src/map/clif.cpp:11579-11600):
//
//	[2:cmd=0x009c][4:srcId uint32][2:headDir uint16][1:dir uint8] = 9 bytes
//
// HeadDir is the player's head-facing selector (rAthena's
// map_session_data::head_dir). For the single-player echo we forward
// the value the client sent in CZ_CHANGE_DIRECTION; rAthena's
// clif_changed_dir writes the session's head_dir verbatim
// (clif.cpp:11586).
type ChangeDirResponse struct {
	// SrcID is the entity ID whose direction changed (rAthena's
	// `bl.id`). For the gateway's single-player echo the gateway
	// substitutes the connection's authenticated AID until a true
	// zone-resident GID is available — see the AID-as-GID convention
	// documented on handleCZGlobalMessage.
	SrcID uint32
	// HeadDir is the head-facing selector (uint16 on the wire;
	// rAthena clamps the in-memory value to 0..2 at pc_setdir).
	HeadDir uint16
	// Dir is the body-direction selector (uint8 at wire offset 8).
	Dir uint8
}

// Size returns the on-wire byte length that Encode will write (always 9).
func (r ChangeDirResponse) Size() int {
	return sizeZCChangeDir
}

// Encode writes the ZC_CHANGE_DIRECTION packet to w.
func (r ChangeDirResponse) Encode(w io.Writer) error {
	var buf [sizeZCChangeDir]byte
	// int16 packetType = 0x009c (HeaderZCCHANGEDIR).
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCCHANGEDIR)
	// uint32 srcId at offset 2.
	binary.LittleEndian.PutUint32(buf[2:], r.SrcID)
	// uint16 headDir at offset 6.
	binary.LittleEndian.PutUint16(buf[6:], r.HeadDir)
	// uint8 dir at offset 8.
	buf[8] = r.Dir

	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_CHANGE_DIRECTION: %w", err)
	}
	return nil
}

// EmotionResponse encodes a ZC_EMOTION packet (command 0x00c0, 7 bytes
// fixed). The server broadcasts this to echo a player's emotion icon
// to nearby clients; the gateway uses it as the single-player echo
// path for CZ_REQ_EMOTION (no AOI yet).
//
// Wire shape (rathena/src/map/packets.hpp:1973-1978 +
// rathena/src/map/clif.cpp:9407-9418):
//
//	[2:cmd=0x00c0][4:GID int32][1:type uint8] = 7 bytes
//
// The type byte is the rAthena emotion_type enum value the client sent
// in CZ_REQ_EMOTION — the gateway forwards it verbatim. rAthena's
// clif_emotion ignores the GID's block type and sends to AREA on its
// own (clif.cpp:9417); the gateway's single-player path sends to SELF
// via the responder.
type EmotionResponse struct {
	// GID is the entity ID of the player performing the emotion
	// (rAthena's `bl.id`). For the single-player echo the gateway
	// substitutes the connection's authenticated AID — see the
	// AID-as-GID convention documented on handleCZGlobalMessage.
	GID uint32
	// Type is the emotion selector byte (rAthena's emotion_type
	// enum). Copied verbatim from the CZ_REQ_EMOTION request.
	Type uint8
}

// Size returns the on-wire byte length that Encode will write (always 7).
func (r EmotionResponse) Size() int {
	return sizeZCEmotion
}

// Encode writes the ZC_EMOTION packet to w.
func (r EmotionResponse) Encode(w io.Writer) error {
	var buf [sizeZCEmotion]byte
	// int16 packetType = 0x00c0 (HeaderZCEMOTION).
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCEMOTION)
	// int32 GID at offset 2 — written as the wire's int32 slot.
	binary.LittleEndian.PutUint32(buf[2:], r.GID)
	// uint8 type at offset 6.
	buf[6] = r.Type

	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_EMOTION: %w", err)
	}
	return nil
}

// AckReqNameResponse encodes a ZC_ACK_REQNAME packet (command 0x0095,
// 30 bytes fixed). The server sends this in response to
// CZ_GETCHARNAMEREQUEST to tell the client the character name for a
// given GID.
//
// Wire shape (rathena/src/map/packets_struct.hpp:3556-3560 +
// rathena/src/map/clif.cpp:9923):
//
//	[2:cmd=0x0095][4:GID int32][24:name char[24]] = 30 bytes
//
// Name is written as a fixed-width 24-byte field; callers must supply
// a UTF-8 name and any bytes past len(Name) are null-padded, matching
// rAthena's safestrncpy pattern. Names longer than 24 bytes are
// truncated.
type AckReqNameResponse struct {
	// GID is the entity ID whose name is being returned.
	GID uint32
	// Name is the character name (UTF-8 for Thai Classic). The encoder
	// null-pads to 24 bytes; names longer than 24 bytes are truncated.
	Name string
}

// Size returns the on-wire byte length that Encode will write (always 30).
func (r AckReqNameResponse) Size() int {
	return sizeZCAckReqName
}

// Encode writes the ZC_ACK_REQNAME packet to w.
func (r AckReqNameResponse) Encode(w io.Writer) error {
	var buf [sizeZCAckReqName]byte
	// int16 packetType = 0x0095 (HeaderZCACKREQNAME).
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCACKREQNAME)
	// int32 GID at offset 2.
	binary.LittleEndian.PutUint32(buf[2:], r.GID)
	// char name[24] at offset 6 — copy up to 24 bytes, null-pad the
	// rest. The array is zero-initialized so trailing bytes are 0x00.
	copy(buf[6:6+sizeZCAckReqNameName], r.Name)

	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_ACK_REQNAME: %w", err)
	}
	return nil
}

// SayDialog2Response encodes a ZC_SAY_DIALOG2 packet (command 0x0972,
// variable length, PACKETVER >= 20220504). The server sends this to
// display dialog text in the NPC dialog window.
//
// Wire layout (rathena/src/map/packets_struct.hpp: ZC_SAY_DIALOG2):
//
//	int16  packetType   (0x0972)
//	int16  packetLength (total size)
//	uint32 NpcID
//	uint8  type         (dialog type, 0 = normal)
//	char   message[]    (null-terminated dialog text)
//
// The wire length is 2+2+4+1 + len(message)+1 (one byte for the NUL
// terminator). The NUL is appended unconditionally.
type SayDialog2Response struct {
	// NpcID is the NPC entity ID.
	NpcID uint32
	// Type is the dialog type (0 = normal).
	Type uint8
	// Message is the dialog text (UTF-8 for Thai Classic).
	Message string
}

// Encode writes the ZC_SAY_DIALOG2 packet to w.
func (r SayDialog2Response) Encode(w io.Writer) error {
	msgBytes := []byte(r.Message)
	// 2 (cmd) + 2 (packetLength) + 4 (NpcID) + 1 (type) + len(msg) + 1 (NUL).
	total := 2 + 2 + 4 + 1 + len(msgBytes) + 1
	if total > 0xffff {
		return fmt.Errorf("packet: write ZC_SAY_DIALOG2: message too long (%d bytes)", len(msgBytes))
	}
	buf := make([]byte, total)
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCSAYDIALOG2)
	binary.LittleEndian.PutUint16(buf[2:], uint16(total))
	binary.LittleEndian.PutUint32(buf[4:], r.NpcID)
	buf[8] = r.Type
	copy(buf[9:], msgBytes)

	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write ZC_SAY_DIALOG2: %w", err)
	}
	return nil
}

// WaitDialog2Response encodes a ZC_WAIT_DIALOG2 packet (command 0x0973,
// 7 bytes fixed, PACKETVER >= 20220504). The server sends this to show
// the "Next" button in the NPC dialog window.
//
// Wire layout (rathena/src/map/packets_struct.hpp: ZC_WAIT_DIALOG2):
//
//	int16  packetType (0x0973)
//	uint32 NpcID
//	uint8  type       (dialog type, 0 = normal)
type WaitDialog2Response struct {
	// NpcID is the NPC entity ID.
	NpcID uint32
	// Type is the dialog type (0 = normal).
	Type uint8
}

// Size returns the on-wire byte length that Encode will write (always 7).
func (r WaitDialog2Response) Size() int {
	return sizeZCWaitDialog2
}

// Encode writes the ZC_WAIT_DIALOG2 packet to w.
func (r WaitDialog2Response) Encode(w io.Writer) error {
	var buf [sizeZCWaitDialog2]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCWAITDIALOG2)
	binary.LittleEndian.PutUint32(buf[2:], r.NpcID)
	buf[6] = r.Type
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_WAIT_DIALOG2: %w", err)
	}
	return nil
}

// CloseDialogResponse encodes a ZC_CLOSE_DIALOG packet (command 0x00b6,
// 6 bytes fixed). The server sends this to show the "Close" button in
// the NPC dialog window.
//
// Wire layout (rathena/src/map/clif_packetdb.hpp:58):
//
//	int16  packetType (0x00b6)
//	uint32 NpcID
type CloseDialogResponse struct {
	// NpcID is the NPC entity ID.
	NpcID uint32
}

// Size returns the on-wire byte length that Encode will write (always 6).
func (r CloseDialogResponse) Size() int {
	return sizeZCCloseDialog
}

// Encode writes the ZC_CLOSE_DIALOG packet to w.
func (r CloseDialogResponse) Encode(w io.Writer) error {
	var buf [sizeZCCloseDialog]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCCLOSEDIALOG)
	binary.LittleEndian.PutUint32(buf[2:], r.NpcID)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_CLOSE_DIALOG: %w", err)
	}
	return nil
}

// SelectDealtypeResponse encodes a ZC_SELECT_DEALTYPE packet (command
// 0x00c4, 6 bytes fixed). The server sends this after CZ_CONTACTNPC
// for shop-type NPCs so the client can pop up the Buy / Sell / Cancel
// deal-type selector. Dialog-type NPCs use the M15 dialog flow
// (ZC_SAY_DIALOG2 + ZC_WAIT_DIALOG2) instead.
//
// Wire layout (rathena/src/map/packets.hpp: ZC_SELECT_DEALTYPE):
//
//	int16  packetType (0x00c4)
//	uint32 NpcID
type SelectDealtypeResponse struct {
	// NpcID is the NPC entity ID the deal window is for.
	NpcID uint32
}

// Size returns the on-wire byte length that Encode will write (always 6).
func (r SelectDealtypeResponse) Size() int {
	return sizeZCSelectDealtype
}

// Encode writes the ZC_SELECT_DEALTYPE packet to w.
func (r SelectDealtypeResponse) Encode(w io.Writer) error {
	var buf [sizeZCSelectDealtype]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCSELECTDEALTYPE)
	binary.LittleEndian.PutUint32(buf[2:], r.NpcID)
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_SELECT_DEALTYPE: %w", err)
	}
	return nil
}

// ShopBuyItem is the per-item entry in a ZC_PC_PURCHASE_ITEMLIST
// packet (rathena/src/map/packets_struct.hpp ITEM_INFO / PACKETVER >=
// 20210203). The on-wire size is 19 bytes:
//
//	uint32 itemId        // item database ID
//	uint32 price         // base price in zeny
//	uint32 discountPrice // discounted price (often equal to price)
//	uint8  itemType      // 0=healing, 2=etc, 3=weapon, 4=armor, 5=card, 6=pet egg / ammunition
//	uint16 viewSprite    // sprite number for equipment
//	uint32 location      // equip location bitmask (EQP_* rAthena flags)
type ShopBuyItem struct {
	// ItemID is the item database ID.
	ItemID uint32
	// Price is the base price in zeny.
	Price uint32
	// DiscountPrice is the discounted price in zeny (often equal to
	// Price when the shop is not running a discount).
	DiscountPrice uint32
	// ItemType is the rAthena IT_* type byte (0=healing, 2=etc,
	// 3=weapon, 4=armor, 5=card, 6=pet egg/ammunition).
	ItemType uint8
	// ViewSprite is the sprite number rAthena's client uses to render
	// the item icon for equipment (0 for non-equippable items).
	ViewSprite uint16
	// Location is the EQP_* bitmask rAthena uses to restrict where the
	// item can be equipped (0 for non-equippable items).
	Location uint32
}

// PurchaseItemListResponse encodes a ZC_PC_PURCHASE_ITEMLIST packet
// (command 0x0b77, variable length, PACKETVER >= 20210203). The server
// sends this in response to CZ_ACK_SELECT_DEALTYPE (type=Buy) so the
// client can pop up the buy window with the NPC's stock list.
//
// Wire layout (rathena/src/map/packets_struct.hpp:
// PACKET_ZC_PC_PURCHASE_ITEMLIST, PACKETVER >= 20210203):
//
//	int16  packetType   (0x0b77)
//	int16  packetLength (4 + 19 * len(Items))
//	[per item, 19 bytes:] ShopBuyItem
type PurchaseItemListResponse struct {
	// Items is the list of items the NPC has for sale.
	Items []ShopBuyItem
}

// Encode writes the ZC_PC_PURCHASE_ITEMLIST packet to w. The wire
// length is 4 + 19 * len(Items); the encoder computes packetLength
// from the entry count so the caller cannot accidentally emit a frame
// whose length slot disagrees with the trailing bytes.
func (r PurchaseItemListResponse) Encode(w io.Writer) error {
	total := 4 + len(r.Items)*sizeShopBuyItem
	if total > 0xffff {
		return fmt.Errorf("packet: write ZC_PC_PURCHASE_ITEMLIST: too many items (%d)", len(r.Items))
	}
	buf := make([]byte, total)
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCPCPURCHASEITEMLIST)
	binary.LittleEndian.PutUint16(buf[2:], uint16(total))
	for i, it := range r.Items {
		off := 4 + i*sizeShopBuyItem
		binary.LittleEndian.PutUint32(buf[off:off+4], it.ItemID)
		binary.LittleEndian.PutUint32(buf[off+4:off+8], it.Price)
		binary.LittleEndian.PutUint32(buf[off+8:off+12], it.DiscountPrice)
		buf[off+12] = it.ItemType
		binary.LittleEndian.PutUint16(buf[off+13:off+15], it.ViewSprite)
		binary.LittleEndian.PutUint32(buf[off+15:off+19], it.Location)
	}
	if _, err := w.Write(buf); err != nil {
		return fmt.Errorf("packet: write ZC_PC_PURCHASE_ITEMLIST: %w", err)
	}
	return nil
}

// PurchaseResultResponse encodes a ZC_PC_PURCHASE_RESULT packet
// (command 0x00ca, 3 bytes fixed). The server sends this to acknowledge
// a CZ_PC_PURCHASE_ITEMLIST request.
//
// Wire layout (rathena/src/map/packets.hpp: ZC_PC_PURCHASE_RESULT):
//
//	int16 packetType (0x00ca)
//	uint8 result     (0=success, 1=zeny/weight/slots failed)
type PurchaseResultResponse struct {
	// Result is the purchase outcome byte (0=success, 1=failed).
	Result uint8
}

// Size returns the on-wire byte length that Encode will write (always 3).
func (r PurchaseResultResponse) Size() int {
	return sizeZCPCPurchaseResult
}

// Encode writes the ZC_PC_PURCHASE_RESULT packet to w.
func (r PurchaseResultResponse) Encode(w io.Writer) error {
	var buf [sizeZCPCPurchaseResult]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCPCPURCHASERESULT)
	buf[2] = r.Result
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_PC_PURCHASE_RESULT: %w", err)
	}
	return nil
}

// NotifyActResponse encodes ZC_NOTIFY_ACT (command 0x08c8, 34 bytes fixed,
// PACKETVER >= 20131223). The server sends this to broadcast damage,
// sit, and stand actions. rAthena's clif_sitting / clif_standing /
// clif_damage all emit this packet.
//
// Wire layout (rathena/src/map/packets.hpp:1413-1425):
//
//	int16  packetType (0x08c8)
//	int32  srcID        (attacker GID)
//	int32  targetID     (target GID)
//	int32  serverTick   (server tick)
//	int32  srcSpeed     (source amotion)
//	int32  dmgSpeed     (damage amotion)
//	int32  damage       (damage value)
//	int8   isSPDamage   (0=HP, 1=SP)
//	uint16 div          (hit count)
//	uint8  type         (damage/action type — DMG_* constants)
//	int32  damage2      (dual-wield second damage)
type NotifyActResponse struct {
	SrcID      uint32
	TargetID   uint32
	ServerTick uint32
	SrcSpeed   int32
	DmgSpeed   int32
	Damage     int32
	IsSPDamage int8
	Div        uint16
	Type       uint8
	Damage2    int32
}

// Size returns the on-wire byte length that Encode will write (always 34).
func (r NotifyActResponse) Size() int { return sizeZCNotifyAct }

// Encode writes the ZC_NOTIFY_ACT packet to w.
func (r NotifyActResponse) Encode(w io.Writer) error {
	var buf [sizeZCNotifyAct]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCNOTIFYACT)
	binary.LittleEndian.PutUint32(buf[2:], r.SrcID)
	binary.LittleEndian.PutUint32(buf[6:], r.TargetID)
	binary.LittleEndian.PutUint32(buf[10:], r.ServerTick)
	binary.LittleEndian.PutUint32(buf[14:], uint32(r.SrcSpeed)) //nolint:gosec // wire slot is unsigned
	binary.LittleEndian.PutUint32(buf[18:], uint32(r.DmgSpeed)) //nolint:gosec // ditto
	binary.LittleEndian.PutUint32(buf[22:], uint32(r.Damage))   //nolint:gosec // ditto
	buf[26] = uint8(r.IsSPDamage)                               //nolint:gosec // rAthena int8 slot is 0/1 in practice; the on-wire byte is unsigned
	binary.LittleEndian.PutUint16(buf[27:], r.Div)
	buf[29] = r.Type
	binary.LittleEndian.PutUint32(buf[30:], uint32(r.Damage2)) //nolint:gosec // ditto
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_NOTIFY_ACT: %w", err)
	}
	return nil
}

// NotifyVanishResponse encodes ZC_NOTIFY_VANISH (command 0x0080, 7 bytes
// fixed). The server sends this when an entity leaves the client's view —
// died, teleported, or moved out of sight range.
//
// Wire layout (rathena/src/map/packets.hpp:604-608):
//
//	int16  packetType (0x0080)
//	uint32 gid        (entity that vanished)
//	uint8  type       (vanish reason — Vanish* constants)
type NotifyVanishResponse struct {
	GID  uint32
	Type uint8
}

// Size returns the on-wire byte length that Encode will write (always 7).
func (r NotifyVanishResponse) Size() int { return sizeZCNotifyVanish }

// Encode writes the ZC_NOTIFY_VANISH packet to w.
func (r NotifyVanishResponse) Encode(w io.Writer) error {
	var buf [sizeZCNotifyVanish]byte
	binary.LittleEndian.PutUint16(buf[0:], HeaderZCNOTIFYVANISH)
	binary.LittleEndian.PutUint32(buf[2:], r.GID)
	buf[6] = r.Type
	if _, err := w.Write(buf[:]); err != nil {
		return fmt.Errorf("packet: write ZC_NOTIFY_VANISH: %w", err)
	}
	return nil
}
