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

// DeductZeny subtracts amount from the character's balance and persists it.
func (s *EconomyService) DeductZeny(ctx context.Context, charID uint32, amount int32) error {
	c, err := s.findChar(ctx, charID)
	if err != nil {
		return err
	}
	z, err := domain.NewZeny(int32(c.Zeny)) //nolint:gosec // G115
	if err != nil {
		return fmt.Errorf("zeny load: %w", err)
	}
	next, err := z.Deduct(amount)
	if err != nil {
		return fmt.Errorf("deduct: %w", err)
	}
	if err := s.repos.UpdateZeny(ctx, c.ID, uint32(next.Amount())); err != nil { //nolint:gosec // G115
		return fmt.Errorf("update zeny: %w", err)
	}
	return nil
}

// CreditZeny adds amount to the character's balance and persists it.
func (s *EconomyService) CreditZeny(ctx context.Context, charID uint32, amount int32) error {
	c, err := s.findChar(ctx, charID)
	if err != nil {
		return err
	}
	z, err := domain.NewZeny(int32(c.Zeny)) //nolint:gosec // G115
	if err != nil {
		return fmt.Errorf("zeny load: %w", err)
	}
	next, err := z.Credit(amount)
	if err != nil {
		return fmt.Errorf("credit: %w", err)
	}
	if err := s.repos.UpdateZeny(ctx, c.ID, uint32(next.Amount())); err != nil { //nolint:gosec // G115
		return fmt.Errorf("update zeny: %w", err)
	}
	return nil
}

// findChar loads a character by charID (used when accountID is not available;
// commerce callers have charID from the conn auth).
func (s *EconomyService) findChar(ctx context.Context, charID uint32) (chardomain.Character, error) {
	c, err := s.repos.FindByID(ctx, chardomain.CharID(charID))
	if err != nil {
		return chardomain.Character{}, fmt.Errorf("find char %d: %w", charID, err)
	}
	return c, nil
}
