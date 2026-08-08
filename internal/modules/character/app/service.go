// Package app implements the character bounded context use cases: char-select
// (validate session + list characters) and create/delete. It owns no game
// world state; progression (leveling) lands in later milestones.
package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/bouroo/goAthena/internal/modules/character/domain"
)

// CharService is the character use-case service.
type CharService struct {
	repos    domain.CharacterRepository
	sess     domain.SessionStore
	maxSlots int // mirrors rAthena chars_per_account / Identity.MaxChars
}

// NewCharService builds a CharService. maxSlots caps how many characters an
// account may hold.
func NewCharService(repos domain.CharacterRepository, sess domain.SessionStore, maxSlots int) *CharService {
	return &CharService{repos: repos, sess: sess, maxSlots: maxSlots}
}

// Authorize validates a CH_ENTER: the account must have a live login session and
// the echoed LoginID1 must match. Returns the session on success. The sex and
// LoginID2 are carried forward for the char/map handshake.
func (s *CharService) Authorize(ctx context.Context, accountID, loginID1 uint32) (domain.Session, error) {
	sess, err := s.sess.GetSession(ctx, accountID)
	if err != nil {
		if errors.Is(err, domain.ErrSessionNotFound) {
			return domain.Session{}, domain.ErrSessionNotFound
		}
		return domain.Session{}, fmt.Errorf("session lookup: %w", err)
	}
	if sess.LoginID1 != loginID1 {
		return domain.Session{}, domain.ErrInvalidSession
	}
	return sess, nil
}

// List returns the account's characters for char-select.
func (s *CharService) List(ctx context.Context, accountID uint32) ([]domain.Character, error) {
	chars, err := s.repos.ListByAccount(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("list characters: %w", err)
	}
	return chars, nil
}

// Create inserts a new character at the given slot if free. A rAthena-style
// default novice (class 0, level 1) is applied to unsupplied progression fields.
func (s *CharService) Create(ctx context.Context, accountID uint32, slot int8, name string) (domain.Character, error) {
	existing, err := s.repos.ListByAccount(ctx, accountID)
	if err != nil {
		return domain.Character{}, fmt.Errorf("check slots: %w", err)
	}
	if len(existing) >= s.maxSlots {
		return domain.Character{}, domain.ErrSlotTaken
	}
	for _, c := range existing {
		if c.CharNum == slot {
			return domain.Character{}, domain.ErrSlotTaken
		}
	}
	taken, err := s.repos.NameExists(ctx, name)
	if err != nil {
		return domain.Character{}, fmt.Errorf("check name: %w", err)
	}
	if taken {
		return domain.Character{}, domain.ErrNameTaken
	}
	c := domain.Character{
		AccountID: accountID,
		CharNum:   slot,
		Name:      name,
		BaseLevel: 1,
		JobLevel:  1,
		MaxHP:     1000,
		HP:        1000,
		MaxSP:     50,
		SP:        50,
		LastMap:   "new_1-1",
		SaveMap:   "new_1-1",
	}
	created, err := s.repos.Create(ctx, c)
	if err != nil {
		return domain.Character{}, fmt.Errorf("create character: %w", err)
	}
	return created, nil
}

// Delete removes a character owned by accountID.
func (s *CharService) Delete(ctx context.Context, id domain.CharID, accountID uint32) error {
	if err := s.repos.Delete(ctx, id, accountID); err != nil {
		return fmt.Errorf("delete character: %w", err)
	}
	return nil
}
