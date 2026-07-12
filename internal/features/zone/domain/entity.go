// Package domain contains entities and port interfaces for the zone
// feature (WS-C): map instances, AOI tower-grid, tick loop, pathfinding.
package domain

import (
	"time"

	"github.com/bouroo/goAthena/pkg/ro/pathfinding"
)

// EntityID uniquely identifies an entity in the zone.
type EntityID uint32

// EntityType classifies entities for game logic (e.g., who can be
// attacked, who shows a name plate, who broadcasts to players only).
//
// Distinct from pkg/ro/aoi.EntityType: zone domain extends the AOI
// classification with future types (items, skills). For Phase 4 the
// two enums overlap; the AOI manager accepts a converted type.
type EntityType uint8

const (
	// EntityPlayer covers player characters.
	EntityPlayer EntityType = iota
	// EntityNPC covers non-player characters (merchants, warps, etc.).
	EntityNPC
	// EntityMob covers monsters.
	EntityMob
)

// Entity is a zone-tracked actor with position, movement intent, and
// AOI bookkeeping state. The TickLoop mutates X/Y/Path/NextMoveTick;
// gRPC handlers mutate Path/TargetX/TargetY via the service layer.
//
// The struct is shared across goroutines: the tick loop reads it under
// the TickLoop's RWMutex; service methods from gRPC handlers take the
// write lock. Keep the struct small and avoid pointers to mutable
// sub-fields so it can be locked as a single value.
//
// Mob-specific fields (MobID, HP, MaxHP, AI, SpawnOrigin*, WanderTimer,
// RespawnDelay, Name) are zero for player and NPC entities; only mobs
// maintain combat and AI state in the zone.
type Entity struct {
	ID           EntityID
	Type         EntityType
	X, Y         int // current position (cells)
	TargetX      int // movement destination X (zero when idle)
	TargetY      int // movement destination Y (zero when idle)
	Path         []pathfinding.Point
	NextMoveTick uint64 // tick number when next step occurs
	MoveSpeed    int    // ms per cell (status data; default from ZoneConfig.MoveSpeed)

	// Mob-specific fields (zero for players/NPCs)
	MobID        int32         // mob_db ID (e.g. 1002 for Poring)
	HP           int32         // current HP
	MaxHP        int32         // maximum HP
	AI           uint8         // AI type from mob_db (02=passive, 04=aggressive)
	SpawnOriginX int           // original spawn X (mob wanders near this)
	SpawnOriginY int           // original spawn Y
	WanderTimer  uint64        // tick number when next wander attempt occurs
	RespawnDelay time.Duration // respawn delay after death
	Name         string        // display name for broadcast events

	// Item-specific fields (zero for all other entity types)
	ItemID     uint32   // numeric item ID (e.g., 501 for Zeny)
	ItemAmount int32    // item quantity (typically 1)
	Owner      EntityID // entity ID of the player who owns this item (0 = public ground item)
}
