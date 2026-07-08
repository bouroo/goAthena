package service

// monsterSpawn defines a static monster entity to spawn on the map.
// GIDs continue from the NPC range (110000005+); in rAthena mob IDs
// and NPC IDs share the same global namespace (nd->id and md->id
// are both checked against START_NPC_NUM=110000000).
type monsterSpawn struct {
	GID      uint32
	Name     string
	SpriteID int16
	X, Y     int16
	Dir      uint8
	Level    int16
	HP       int32
	MaxHP    int32
	Speed    int16
}

// monsterSpawns is the hardcoded monster spawn list for the default
// map. Sent during CZ_NOTIFY_ACTORINIT after the NPC spawns, using
// ZC_SET_UNIT_IDLE (0x09ff) with objectType=0x05 (NPC_MOB_TYPE).
//
// Monster data is from rAthena db/pre-re/mob_db.yml:
//
//	1002 = Poring (level 1, HP 50)
//	1063 = Lunatic (level 3, HP 150)
//	1113 = Drops (level 1, HP 55)
//	1157 = Spore (level 2, HP 120)
var monsterSpawns = []monsterSpawn{
	{GID: 110000005, Name: "Poring", SpriteID: 1002, X: 155, Y: 165, Dir: 0, Level: 1, HP: 50, MaxHP: 50, Speed: 400},
	{GID: 110000006, Name: "Lunatic", SpriteID: 1063, X: 165, Y: 165, Dir: 0, Level: 3, HP: 150, MaxHP: 150, Speed: 400},
	{GID: 110000007, Name: "Drops", SpriteID: 1113, X: 155, Y: 175, Dir: 0, Level: 1, HP: 55, MaxHP: 55, Speed: 400},
	{GID: 110000008, Name: "Spore", SpriteID: 1157, X: 165, Y: 175, Dir: 0, Level: 2, HP: 120, MaxHP: 120, Speed: 400},
}
