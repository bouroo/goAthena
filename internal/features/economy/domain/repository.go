package domain

import (
	"context"
	"errors"
)

//go:generate go run go.uber.org/mock/mockgen -destination=mock/character_zeny_repository_mock.go -package=domainmock . CharacterZenyRepository

// Sentinel errors returned by CharacterZenyRepository.
// Service-layer callers compare these using errors.Is.
var (
	// ErrInsufficientZeny is returned when a buy transaction cost exceeds
	// the character's available zeny.
	ErrInsufficientZeny = errors.New("insufficient zeny")

	// ErrZenyOverflow is returned when a sell transaction results in
	// zeny exceeding the pre-renewal limit.
	ErrZenyOverflow = errors.New("zeny overflow")

	// ErrCharNotFound is returned when a character does not exist.
	ErrCharNotFound = errors.New("character not found")
)

// CharacterZenyRepository is the outbound port for zeny persistence.
// It manages atomic zeny/inventory state transitions.
type CharacterZenyRepository interface {
	// GetZeny returns the current zeny balance for a character.
	// Returns ErrCharNotFound if the character is missing.
	GetZeny(ctx context.Context, charID uint32) (uint32, error)

	// ExecuteBuyTx atomically deducts zeny and adds items to inventory.
	// Returns new balance on success.
	ExecuteBuyTx(ctx context.Context, charID uint32, totalCost uint32, items []AcquiredItem) (newZeny uint32, err error)

	// ExecuteSellTx atomically adds zeny and removes/reduces inventory items.
	// Returns new balance on success.
	ExecuteSellTx(ctx context.Context, charID uint32, totalCredit uint32, sales []SellLine) (newZeny uint32, err error)
}
