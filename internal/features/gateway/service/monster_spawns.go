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
	BaseExp  int32
	JobExp   int32
	Def      int
	Vit      int
}

// monsterSpawns is the hardcoded monster spawn list for the default
// map. Sent during CZ_NOTIFY_ACTORINIT after the NPC spawns, using
// ZC_SET_UNIT_IDLE (0x09ff) with objectType=0x05 (NPC_MOB_TYPE).
//
// Monster data is from rAthena db/pre-re/mob_db.yml:
//
//	1002 = Poring (level 1, HP 50, Base 2, Job 1, Def 0, Vit 0)
//	1063 = Lunatic (level 3, HP 60, Base 6, Job 2, Def 0, Vit 3)
//	1113 = Drops (level 3, HP 55, Base 4, Job 3, Def 0, Vit 2)
//	1014 = Spore (level 16, HP 510, Base 66, Job 108, Def 0, Vit 5)
var monsterSpawns = []monsterSpawn{
	{GID: 110000005, Name: "Poring", SpriteID: 1002, X: 155, Y: 165, Dir: 0, Level: 1, HP: 50, MaxHP: 50, Speed: 400, BaseExp: 2, JobExp: 1, Def: 0, Vit: 0},
	{GID: 110000006, Name: "Lunatic", SpriteID: 1063, X: 165, Y: 165, Dir: 0, Level: 3, HP: 60, MaxHP: 60, Speed: 400, BaseExp: 6, JobExp: 2, Def: 0, Vit: 3},
	{GID: 110000007, Name: "Drops", SpriteID: 1113, X: 155, Y: 175, Dir: 0, Level: 3, HP: 55, MaxHP: 55, Speed: 400, BaseExp: 4, JobExp: 3, Def: 0, Vit: 2},
	{GID: 110000008, Name: "Spore", SpriteID: 1014, X: 165, Y: 175, Dir: 0, Level: 16, HP: 510, MaxHP: 510, Speed: 400, BaseExp: 66, JobExp: 108, Def: 0, Vit: 5},
}

// LookupMonsterStats finds the Def and Vit for a given monster GID.
// Returns Def, Vit, and true if found; 0, 0, false otherwise.
func LookupMonsterStats(gid uint32) (int, int, bool) {
	for _, m := range monsterSpawns {
		if m.GID == gid {
			return m.Def, m.Vit, true
		}
	}
	return 0, 0, false
}
