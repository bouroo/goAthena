package service

// npcSpawn defines a static NPC entity to spawn on the map.
// GIDs start at 110000000 (rAthena START_NPC_NUM).
type npcSpawn struct {
	GID      uint32
	Name     string
	SpriteID int16
	X, Y     int16
	Dir      uint8
}

// npcSpawns is the hardcoded NPC spawn list for the default map.
// These are sent during CZ_NOTIFY_ACTORINIT after the status burst
// and empty list packets, using ZC_SET_UNIT_IDLE (0x09ff).
//
// Sprite IDs are rAthena NPC class IDs (nd->class_):
//
//	114 = Kafra Employee (4_KAFRA)
//	104 = Weapon Shop (4_M_03)
//	 45 = Warp Portal (4_F_01)
//	111 = Guide (4_M_02)
var npcSpawns = []npcSpawn{
	{
		GID:      110000001,
		Name:     "Kafra Employee",
		SpriteID: 114,
		X:        150,
		Y:        180,
		Dir:      0,
	},
	{
		GID:      110000002,
		Name:     "Weapon Shop",
		SpriteID: 104,
		X:        160,
		Y:        180,
		Dir:      0,
	},
	{
		GID:      110000003,
		Name:     "Warp",
		SpriteID: 45,
		X:        150,
		Y:        190,
		Dir:      0,
	},
	{
		GID:      110000004,
		Name:     "Guide",
		SpriteID: 111,
		X:        160,
		Y:        190,
		Dir:      0,
	},
}
