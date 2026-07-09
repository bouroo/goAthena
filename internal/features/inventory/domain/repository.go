package domain

import (
	"context"
	"errors"
)

//go:generate go run go.uber.org/mock/mockgen -destination=mock/inventory_repository_mock.go -package=domainmock . InventoryRepository

// Sentinel errors returned by InventoryRepository implementations and
// service-layer use cases. Service-layer callers compare with errors.Is
// so wrapping is preserved across fmt.Errorf("%w: ...", Err...)
// boundaries.
var (
	// ErrItemNotFound is returned when a single-row operation (Update,
	// Remove, SetEquip) targets an id that does not exist. Bulk
	// ListByChar returns an empty slice with a nil error in the same
	// case — "not found" is a single-row concept.
	ErrItemNotFound = errors.New("inventory item not found")

	// ErrWeightExceeded is returned when adding an item would push
	// the character's carried weight past MaxWeight. It is raised by
	// the service-layer checkWeight helper, not by the repository
	// itself — the repository stays a thin persistence adapter and
	// does not consult the itemdb. The sentinel lives here so callers
	// (acquire handlers, future shop/mail/drop flows) can branch with
	// errors.Is without depending on the service package.
	ErrWeightExceeded = errors.New("inventory weight capacity exceeded")
)

// InventoryRepository is the outbound port for inventory persistence.
// Concrete implementations live under
// internal/features/inventory/repository and back onto MariaDB (GORM)
// or PostgreSQL via the configured driver. The interface is
// intentionally narrow: this feature only owns the CRUD shape. Weight
// recalculation, equipment slot validation, and itemdb joins are
// service-layer concerns that consume this port.
type InventoryRepository interface {
	// ListByChar returns every item the given character owns, in row-id
	// order (which matches the rAthena `inventory.id` autoincrement
	// order and is the closest stable proxy for "slot"). An unknown
	// char_id returns an empty slice with a nil error — the absence of
	// rows is not an error.
	ListByChar(ctx context.Context, charID uint32) ([]InventoryItem, error)

	// Add inserts a new item row for charID and returns the autoincrement
	// id assigned by the database. The repository does NOT enforce a
	// uniqueness constraint on (char_id, nameid, equip); callers that
	// want stack-merge semantics must do so before calling Add. The
	// returned id is always > 0 on success.
	Add(ctx context.Context, charID uint32, item InventoryItem) (id uint32, err error)

	// UpdateAmount sets the stack count for the given item id. Returns
	// ErrItemNotFound when no row matches.
	UpdateAmount(ctx context.Context, id uint32, amount uint32) error

	// Remove deletes the row with the given id. Returns ErrItemNotFound
	// when no row matches (mirrors UpdateAmount's not-found semantics
	// so callers can decide whether to treat the missing case as a soft
	// idempotent success or a hard error).
	Remove(ctx context.Context, id uint32) error

	// SetEquip overwrites the equip-position bitfield for the given
	// item id. Returns ErrItemNotFound when no row matches.
	SetEquip(ctx context.Context, id uint32, equipMask uint32) error
}
