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
	// MobID is the rAthena mob_db.yml ID used to look up the matching
	// mobdb.MobEntry for combat formulas and drop tables. Resolved at
	// construction time by DispatchHandler.lookupMobEntry; zero means
	// "no mob_db entry" and the handler falls back to Def/Vit on this
	// struct (pre-mob_db behaviour).
	MobID int32
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
//
// MobID is the matching entry ID in mob_db.yml — used by
// DispatchHandler.lookupMobEntry to resolve stats and drop tables.
var monsterSpawns = []monsterSpawn{
	{GID: 110000005, Name: "Poring", SpriteID: 1002, X: 155, Y: 165, Dir: 0, Level: 1, HP: 50, MaxHP: 50, Speed: 400, BaseExp: 2, JobExp: 1, Def: 0, Vit: 0, MobID: 1002},
	{GID: 110000006, Name: "Lunatic", SpriteID: 1063, X: 165, Y: 165, Dir: 0, Level: 3, HP: 60, MaxHP: 60, Speed: 400, BaseExp: 6, JobExp: 2, Def: 0, Vit: 3, MobID: 1063},
	{GID: 110000007, Name: "Drops", SpriteID: 1113, X: 155, Y: 175, Dir: 0, Level: 3, HP: 55, MaxHP: 55, Speed: 400, BaseExp: 4, JobExp: 3, Def: 0, Vit: 2, MobID: 1113},
	{GID: 110000008, Name: "Spore", SpriteID: 1014, X: 165, Y: 175, Dir: 0, Level: 16, HP: 510, MaxHP: 510, Speed: 400, BaseExp: 66, JobExp: 108, Def: 0, Vit: 5, MobID: 1014},
}

// LookupMonsterStats finds the Def and Vit for a given monster GID.
// Returns Def, Vit, and true if found; 0, 0, false otherwise.
//
// Deprecated: prefer DispatchHandler.lookupMobEntry, which falls back
// to mobdb.Registry when available and only falls back to this struct
// when the mob_db has no entry for the spawn's MobID. Kept for the
// pre-mob_db combat formula tests and for any caller that has no
// registry handle.
func LookupMonsterStats(gid uint32) (int, int, bool) {
	for _, m := range monsterSpawns {
		if m.GID == gid {
			return m.Def, m.Vit, true
		}
	}
	return 0, 0, false
}

// spawnByGID returns the monsterSpawn for the given GID and true if
// found; nil, false otherwise. Used by DispatchHandler to resolve a
// drop packet's (X, Y) when the mob entry has no coordinates of its
// own (mob_db.yml carries stat tables, not per-instance spawn points).
func spawnByGID(gid uint32) (*monsterSpawn, bool) {
	for i := range monsterSpawns {
		if monsterSpawns[i].GID == gid {
			return &monsterSpawns[i], true
		}
	}
	return nil, false
}
