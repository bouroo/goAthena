package domain

// ViewData carries the character-derived fields the gateway needs to
// populate the broadcast view packets (ZC_SPAWN_UNIT 0x09fe /
// ZC_UNIT_WALKING 0x09fd) for a connected session.
//
// The struct intentionally omits packet fields that have no source on
// the rAthena `char` row: body / health / effect state, headDir, guild
// info, honor / virtue / PK mode, boss flag, font, x/y size, the
// per-spawn position, and the per-walk moveStartTime. Those are
// defaulted inside the encoder at encode time — they are not character
// state and would never be sourced from the database.
//
// Field set was derived from pkg/ro/packet.SpawnUnitResponse and
// pkg/ro/packet.UnitWalkingResponse (PACKETVER >= 20150513 branch,
// which covers Thai Classic 20250604). Widths match the broadcast wire
// slots so callers can splat directly without per-field clamps.
type ViewData struct {
	// ObjectType is the entity class byte rAthena's bl_type emits.
	// 0 = PC (the only value the gateway emits today — set after
	// loading the character), 5 = NPC_MOB_TYPE, 6 = NPC_EVT_TYPE.
	ObjectType uint8
	// AID is the account ID (rAthena's `account_id` bl.id for a PC).
	// For a PC broadcast AID == GID; the encoding layer distinguishes
	// them only because the wire shape carries both slots.
	AID uint32
	// GID is the entity ID (rAthena's `id`). For a PC broadcast this
	// is the character's primary key (char_id), populated from
	// ConnectionInfo.CharID after a successful CZ_ENTER.
	GID uint32
	// Speed is the walk speed in kRO units. 150 is rAthena's default
	// PC amotion (pc_setnewpc); character classes override this via
	// status.speed.
	Speed int16
	// Job is the job class ID mapped from the `class` column
	// (CharModel.Class). 0 = novice, 1 = swordsman, etc.
	Job int16
	// Head is the hair-style view sprite ID, mapped from CharModel.Hair
	// (uint8 widened to uint16). Zero is a valid client-side sentinel
	// for a hairless sprite.
	Head uint16
	// Weapon is the equipped weapon view sprite, mapped from
	// CharModel.Weapon (uint16 widened to uint32 — item IDs exceed
	// 16 bits for some custom DBs).
	Weapon uint32
	// Shield is the equipped shield view sprite, mapped from
	// CharModel.Shield (uint16 → uint32).
	Shield uint32
	// Accessory carries the head_bottom (lower headgear) view sprite,
	// mapped from CharModel.HeadBottom.
	Accessory uint16
	// Accessory2 carries the head_top view sprite (CharModel.HeadTop).
	// rAthena's packet_spawn_unit layout calls this accessory2
	// regardless of slot name; the registry follows the wire name.
	Accessory2 uint16
	// Accessory3 carries the head_mid view sprite (CharModel.HeadMid).
	Accessory3 uint16
	// HeadPalette is the hair color (palette index), mapped from
	// CharModel.HairColor (uint16 widened to int16 to match the wire
	// slot).
	HeadPalette int16
	// BodyPalette is the body/clothes color (palette index), mapped
	// from CharModel.ClothesColor (uint16 → int16 to match the wire
	// slot).
	BodyPalette int16
	// Robe is the robe overlay sprite ID, mapped from CharModel.Robe.
	Robe uint16
	// Sex is the sex byte rAthena emits in the spawn packet:
	// 0 = female, 1 = male, 2 = server. Sourced from CharModel.Sex
	// (which is an ENUM('M','F') in the rAthena schema).
	Sex uint8
	// CLevel is the base level, mapped from CharModel.BaseLevel
	// (uint32 clamped to int16 in the encoder).
	CLevel int16
	// MaxHP is the maximum HP, mapped from CharModel.MaxHP (uint32
	// widened to int32 — the wire slot is signed).
	MaxHP int32
	// HP is the current HP at the moment the snapshot was taken,
	// mapped from CharModel.HP (uint32 → int32).
	HP int32
	// Name is the character name in UTF-8 (Thai Classic). The encoder
	// null-pads to the 24-byte wire slot; names longer than 24 bytes
	// are truncated to the first 24 bytes (rAthena's safestrncpy
	// pattern).
	Name string
}

// Session is the gateway-side view of a logged-in character: the
// outbound packet writer (Responder), the authenticated character ID,
// the current map name, and the character-derived view fields the
// gateway needs to broadcast unit-spawn / unit-walking packets on the
// client's behalf.
//
// MapName is empty until the session is attached to a map; the
// registry's ForEachOnMap skips sessions whose MapName is empty so a
// half-registered session cannot leak into a map-scoped broadcast.
//
// Session is a value type. The registry stores and returns copies —
// the responder interface inside is a fat pointer to its concrete
// transport, so copying the struct does not detach the connection.
type Session struct {
	// Responder is the outbound packet writer for this connection.
	// Each transport (gnet TCP, coder/websocket) supplies its own
	// implementation; SendPacket is safe to call from the dispatch
	// goroutine.
	Responder Responder
	// CharID is the character primary key the session is bound to.
	// Mirrors ConnectionInfo.CharID but persists across connection
	// reconnects for the same character (post-M3 / M4 the registry
	// outlives any single gnet conn).
	CharID uint32
	// MapName is the map the session currently inhabits. Empty until
	// the session has finished CZ_ENTER + actor-init; the registry
	// skips empty-MapName sessions in ForEachOnMap.
	MapName string
	// View carries the character-derived fields used to fill the
	// ZC_SPAWN_UNIT / ZC_UNIT_WALKING broadcasts.
	View ViewData
}
