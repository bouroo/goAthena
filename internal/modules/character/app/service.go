// Package app implements the character bounded context use cases: char-select
// (validate session + list characters) and create/delete. It owns no game
// world state; progression (leveling) lands in later milestones.
package app

import (
	"context"
	"errors"
	"fmt"
	"time"

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

// FindByID returns a character by char_id, without checking ownership.
// Used by the char server to inspect character state before deletion.
func (s *CharService) FindByID(ctx context.Context, id domain.CharID) (domain.Character, error) {
	c, err := s.repos.FindByID(ctx, id)
	if err != nil {
		return domain.Character{}, fmt.Errorf("find character: %w", err)
	}
	return c, nil
}

// ReserveDeleteResult holds the outcome of a deletion-slot reservation.
// DeleteDate is the remaining seconds until deletion; 0 means the character
// is already queued (result=0) or there was no delay to enforce.
type ReserveDeleteResult struct {
	Result     int32 // 0 = already queued, 1 = OK, 3 = not found
	DeleteDate uint32
}

// ReserveDelete places a character into the deletion queue. The caller (char
// server handler) must verify the character belongs to the account before
// calling. result semantics mirror rAthena char_clif.cpp: result 0 = already
// queued (date=0), 1 = OK (date = char_del_delay seconds from now), 3 = not
// found. char_del_delay is taken from the server config; when 0 the delay is
// bypassed (we use 0 as "no delay" here — a later milestone injects it).
func (s *CharService) ReserveDelete(ctx context.Context, id domain.CharID, accountID uint32) (ReserveDeleteResult, error) {
	c, err := s.repos.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, domain.ErrCharacterNotFound) {
			return ReserveDeleteResult{Result: 3}, nil
		}
		return ReserveDeleteResult{}, fmt.Errorf("find character: %w", err)
	}
	if c.AccountID != accountID {
		return ReserveDeleteResult{Result: 3}, nil
	}
	if c.DeleteDate != 0 {
		// Already queued — date=0 on wire signals this.
		return ReserveDeleteResult{Result: 0, DeleteDate: 0}, nil
	}
	// char_del_delay seconds from now; 0 = immediate (config-driven, default 0 here).
	// TODO(milestone): read char_del_delay from config; use 0 for now (no delay).
	const charDelDelay = 0
	delDate := uint32(time.Now().Unix()) + charDelDelay //nolint:gosec // epoch seconds fit uint32 until 2106
	if err := s.repos.SetDeleteDate(ctx, id, accountID, delDate); err != nil {
		return ReserveDeleteResult{}, fmt.Errorf("set delete date: %w", err)
	}
	// PACKETVER ≥20150513 carries REMAINING seconds (delete_date − now) in the
	// date field, not the absolute epoch (rAthena chclif_char_delete2_ack).
	remaining := uint32(0)
	if rem := int64(delDate) - time.Now().Unix(); rem > 0 {
		remaining = uint32(rem) //nolint:gosec // bounded by charDelDelay
	}
	return ReserveDeleteResult{Result: 1, DeleteDate: remaining}, nil
}

// CancelDeleteResult mirrors rAthena char_clif.cpp: result 1 = cancelled,
// 2 = character not found.
type CancelDeleteResult struct {
	Result int32
}

// CancelDelete clears the pending deletion slot for a character, returning
// 1 on success or 2 if the character was not found or not owned by accountID.
func (s *CharService) CancelDelete(ctx context.Context, id domain.CharID, accountID uint32) (CancelDeleteResult, error) {
	c, err := s.repos.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, domain.ErrCharacterNotFound) {
			return CancelDeleteResult{Result: 2}, nil
		}
		return CancelDeleteResult{}, fmt.Errorf("find character: %w", err)
	}
	if c.AccountID != accountID {
		return CancelDeleteResult{Result: 2}, nil
	}
	if c.DeleteDate == 0 {
		// Not queued — cancel is a no-op, still success.
		return CancelDeleteResult{Result: 1}, nil
	}
	if err := s.repos.SetDeleteDate(ctx, id, accountID, 0); err != nil {
		return CancelDeleteResult{}, fmt.Errorf("clear delete date: %w", err)
	}
	return CancelDeleteResult{Result: 1}, nil
}
