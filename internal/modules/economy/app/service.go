// Package app implements the economy bounded context use cases: get, credit,
// and deduct zeny for a character, persisting via the character repo.
package app

import (
	"context"
	"fmt"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	"github.com/bouroo/goAthena/internal/modules/economy/domain"
)

// EconomyService moves zeny for a character. It reads the current balance from
// the character repo, applies overflow-safe arithmetic, and writes it back.
type EconomyService struct {
	repos chardomain.CharacterRepository
}

// NewEconomyService builds an EconomyService backed by the character repo.
func NewEconomyService(repos chardomain.CharacterRepository) *EconomyService {
	return &EconomyService{repos: repos}
}

// Balance returns the character's current zeny.
func (s *EconomyService) Balance(ctx context.Context, accountID, charID uint32) (domain.Zeny, error) {
	chars, err := s.repos.ListByAccount(ctx, accountID)
	if err != nil {
		return domain.Zeny{}, fmt.Errorf("list chars: %w", err)
	}
	for _, c := range chars {
		if uint32(c.ID) == charID {
			z, err := domain.Zeny{}.Credit(int32(c.Zeny)) //nolint:gosec // G115: zeny bounded.
			if err != nil {
				return domain.Zeny{}, fmt.Errorf("zeny credit: %w", err)
			}
			return z, nil
		}
	}
	return domain.Zeny{}, fmt.Errorf("char %d: %w", charID, chardomain.ErrCharacterNotFound)
}
