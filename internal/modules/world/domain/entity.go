// Package domain holds the world bounded context's pure domain model: entity
// types, the entity registry port, and map-session primitives. No infrastructure.
package domain

import (
	"context"
	"errors"
)

// EntityID uniquely identifies an on-map entity (PC, mob, NPC). It wraps the
// kernel's aoi.EntityID (uint32). For a PC, GID = char_id; AID = account_id.
type EntityID uint32

// EntityType classifies an on-map entity for the AOI/spawn system.
type EntityType uint8

const (
	// EntityTypePC is a player character.
	EntityTypePC EntityType = 0
	// EntityTypeNPC is a static NPC/prop.
	EntityTypeNPC EntityType = 6
	// EntityTypeMob is a monster.
	EntityTypeMob EntityType = 5
)

// Position is a cell coordinate on a map.
type Position struct {
	X int16
	Y int16
}

// Entity is the base on-map entity: identity + position + type + the runtime
// properties the map server needs to spawn it on the client.
type Entity struct {
	ID      EntityID
	Account uint32 // AID (account_id); 0 for mobs/NPCs
	Type    EntityType
	// Class is the mob_db class id for EntityTypeMob (0 for PC/NPC); the combat
	// service resolves the mob's DEF/stats from mob_db by this id.
	Class int32
	Map   string
	Pos   Position
	// SaveMap/SavePos are a runtime cache of the character's save (respawn) point,
	// populated at map-enter from the char table (save_map/save_x/save_y). They are
	// NOT authoritative — the char table is — but RespawnPlayer reads them so a
	// killed PC reappears at its save point without a round-trip to the DB.
	SaveMap string
	SavePos Position
	Dir     uint8
	Speed   int16
	Job     int16
	Sex     uint8
	Name    string
	Head    uint16
	Weapon  uint32
	Shield  uint32
	HP      int32
	MaxHP   int32
	// SP/MaxSP are the skill-point vitals; PercentHeal (and later skill cost)
	// mutate them. Mobs leave them zero — only PCs load them from the char table.
	SP    int32
	MaxSP int32
	// Sitting is runtime-only seated state: true halves natural HP/SP regen
	// intervals (status_natural_heal). Not persisted — map-enter leaves it
	// false and a reconnecting PC re-sits. Mutated only via WorldService.SetSitting.
	Sitting bool
	Level   int16
	// BaseExp/JobExp are a runtime cache of the character's accumulated EXP,
	// populated at map-enter from the char table (base_exp/job_exp) and mutated
	// by WorldService.GrantExp on a mob kill. They are NOT authoritative — the
	// char table is — but GrantExp/LeaveMap/SaveAll read them so EXP accrues and
	// persists without a DB round-trip per kill. uint64 matches the char-table
	// column type and avoids overflow for the large, accumulating EXP total.
	// Leveling (threshold-crossing, HP/SP/stat recalc) is deferred: GrantExp only
	// accrues EXP here; a future track consumes these for level-up.
	BaseExp uint64
	JobExp  uint64
	// Str/Agi/Vit/Int/Dex/Luk are the six base stats the combat service feeds to
	// the kernel's Attacker/Defender profiles. PCs load them from the char table
	// at map-enter; mobs leave them zero (mob stats come from mob_db by Class).
	Str uint16
	Agi uint16
	Vit uint16
	Int uint16
	Dex uint16
	Luk uint16
}

// PlayerEntity wraps a PC entity with its connection session for the map server.
type PlayerEntity struct {
	Entity
	CharID uint32 // GID = char_id
	Online bool
}

// WorldRepository is the persistence port for world-state reads the map server
// needs at enter (the character's last map + position).
type WorldRepository interface {
	// LoadEnterState returns the char's map/position/look for map-enter.
	LoadEnterState(ctx context.Context, charID uint32) (Entity, error)
	// SetOnline marks a char online/offline and updates last position.
	SetOnline(ctx context.Context, charID uint32, online bool, pos Position) error
	// SetPosition persists the char's destination map + position (used by warp/
	// transit before the client reconnects to re-enter the new map).
	SetPosition(ctx context.Context, charID uint32, mapName string, pos Position) error
	// SaveState persists the char's full runtime snapshot — hp/sp plus the
	// accumulated base_exp/job_exp — so in-session combat/regen/heal/respawn and
	// EXP-from-kill changes survive disconnect and restart. hp/sp are the runtime
	// int32 vitals (clamped >= 0 by AddVitals/clampVitals); baseExp/jobExp are the
	// uint64 EXP totals (clamped at math.MaxUint64 by GrantExp/clampExpAdd).
	// Folding EXP into the same persist path means disconnect (LeaveMap),
	// shutdown (SaveAll) and warp all persist EXP from one primitive.
	SaveState(ctx context.Context, charID uint32, hp, sp int32, baseExp, jobExp uint64) error
}

// errors  for the world domain.
var (
	// ErrEntityNotFound is returned when an entity is not in the registry.
	ErrEntityNotFound = errors.New("entity not found")
	// ErrEntityAlreadyExists is returned when adding a duplicate entity.
	ErrEntityAlreadyExists = errors.New("entity already exists")
	// ErrMapFull is returned when an entity cannot be placed (map at capacity).
	ErrMapFull = errors.New("map full")
)
