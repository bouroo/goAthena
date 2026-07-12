package domain

import (
	"context"

	"google.golang.org/protobuf/proto"

	"github.com/bouroo/goAthena/pkg/ro/romap"
)

// ZoneService is the inbound port for zone operations. The service
// implementation is the zone service use-case layer; gRPC handlers
// (future Phase 5+) and the tick loop itself invoke it.
//
// All methods are context-aware and return wrapped errors so callers
// can use errors.Is / errors.As to identify root causes.
type ZoneService interface {
	// AddEntity registers e in the tick loop and AOI grid. The entity
	// must have a unique ID and valid in-bounds coordinates on a walkable
	// cell. The first player added triggers Agones Allocated().
	AddEntity(ctx context.Context, e *Entity) error

	// RemoveEntity unregisters e from the tick loop and AOI grid.
	// Removing the last player schedules an Agones Shutdown() after
	// the configured grace period.
	RemoveEntity(ctx context.Context, id EntityID) error

	// MoveEntity sets a movement target for e. Computes an A* path and
	// stores it on the entity; the tick loop will consume it cell by
	// cell until exhausted. Returns an error if no walkable path exists.
	MoveEntity(ctx context.Context, id EntityID, x, y int) error

	// GetVisible returns the entities in the broadcast viewport around
	// the entity with id. Uses AOI squeezing on dense areas.
	GetVisible(ctx context.Context, id EntityID) ([]*Entity, error)

	// GetEntity returns a snapshot copy of the entity with id, or an
	// error if the ID is unknown. Used by gRPC handlers to inspect state.
	GetEntity(ctx context.Context, id EntityID) (*Entity, error)

	// EntityCount returns the number of entities tracked by the zone.
	EntityCount(ctx context.Context) int

	// DamageEntity applies damage to the target entity. Validates the entity
	// exists and is a mob (or player). Reduces HP, publishes EntityDamaged
	// event, and if HP <= 0, calls KillEntity and publishes EntityKilled event.
	// Returns the damage response with updated HP and death status.
	DamageEntity(ctx context.Context, entityID EntityID, damage int32, attackerID EntityID, skillID, skillLevel uint32) (*DamageResponse, error)

	// KillEntity removes an entity from the zone and publishes EntityKilled event.
	// Used internally by DamageEntity when HP <= 0.
	KillEntity(ctx context.Context, entityID EntityID) error

	// PickupItem processes a ground-item pickup request. Validates the item
	// exists, is in range, and is owned by the requesting player. Removes it
	// from the ground registry and publishes ItemPickedUp event.
	PickupItem(ctx context.Context, groundItemID EntityID, playerID EntityID) (*PickupResponse, error)
}

// DamageResponse contains the result of applying damage.
type DamageResponse struct {
	Success       bool
	TargetDied    bool
	DamageApplied int32
	CurrentHP     int32
	MaxHP         int32
}

// PickupResponse contains the result of picking up an item.
type PickupResponse struct {
	Success bool
	ItemID  uint32
	Amount  int32
}

// MapRepository is the outbound port for loading map data. The default
// implementation reads .gat/.rsw files from disk; tests can substitute
// a synthetic in-memory implementation.
type MapRepository interface {
	// LoadMap parses a map by name and returns its grid. Returns an error
	// if the map files are missing or malformed.
	LoadMap(ctx context.Context, name string) (*romap.MapData, error)
}

// Publisher is the outbound port for publishing zone events to the cluster.
// Implementations marshal the proto message and publish it to the address
// space their transport expects (currently: a NATS subject per map).
//
// All methods are context-aware so callers can apply request-scoped
// deadlines or cancellation. Implementations must return wrapped errors
// so the caller can use errors.Is / errors.As to inspect root causes.
type Publisher interface {
	// PublishEvent marshals event and publishes it to the given map's
	// zone-event subject. The mapName selects the subject partition; the
	// message is any protobuf message (typically a *zonev1.ZoneEvent
	// carrying a Moved/Spawned/Vanished oneof).
	PublishEvent(ctx context.Context, mapName string, event proto.Message) error
}
