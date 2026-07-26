package infra

import (
	"context"
	"slices"
	"sort"

	"github.com/bouroo/goAthena/internal/modules/character/domain"
)

// MemoryCharacterRepository is an in-memory domain.CharacterRepository for
// hermetic unit tests. It returns defensive copies so callers cannot mutate the
// store through a returned slice or pointer.
type MemoryCharacterRepository struct {
	bySlot map[accountSlot]domain.Character
}

type accountSlot struct {
	accountID uint32
	slot      uint8
}

// NewMemoryCharacterRepository seeds the store with copies of the given
// characters, keyed by (accountID, slot).
func NewMemoryCharacterRepository(seeds ...domain.Character) *MemoryCharacterRepository {
	r := &MemoryCharacterRepository{bySlot: make(map[accountSlot]domain.Character, len(seeds))}
	for _, c := range seeds {
		r.bySlot[accountSlot{c.AccountID, c.Slot}] = c
	}
	return r
}

// ListByAccount returns defensive copies of the account's characters, sorted by
// slot for a stable display. An account with no characters yields an empty
// (non-nil) slice and a nil error.
func (r *MemoryCharacterRepository) ListByAccount(_ context.Context, accountID uint32) ([]domain.Character, error) {
	var out []domain.Character
	for k, c := range r.bySlot {
		if k.accountID == accountID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return slices.Clone(out), nil
}

// GetBySlot returns a defensive copy or ErrCharacterNotFound.
func (r *MemoryCharacterRepository) GetBySlot(_ context.Context, accountID uint32, slot uint8) (*domain.Character, error) {
	c, ok := r.bySlot[accountSlot{accountID, slot}]
	if !ok {
		return nil, domain.ErrCharacterNotFound
	}
	cp := c
	return &cp, nil
}
