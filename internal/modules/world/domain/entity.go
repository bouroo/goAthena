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
	Map     string
	Pos     Position
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
	Level   int16
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
