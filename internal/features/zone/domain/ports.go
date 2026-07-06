package domain

import (
	"context"

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
}

// MapRepository is the outbound port for loading map data. The default
// implementation reads .gat/.rsw files from disk; tests can substitute
// a synthetic in-memory implementation.
type MapRepository interface {
	// LoadMap parses a map by name and returns its grid. Returns an error
	// if the map files are missing or malformed.
	LoadMap(ctx context.Context, name string) (*romap.MapData, error)
}
