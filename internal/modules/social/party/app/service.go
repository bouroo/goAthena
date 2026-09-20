// Package app implements the party bounded context use cases: create, invite
// reply, leave, kick, option change, and the EXP-share computation.
//
// PartyService owns only the party state machine. Who may invite whom and where
// a result is delivered (each member's own connection) are gateway concerns —
// the service stays free of gnet, of the world registry, and of character
// identity.
package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/bouroo/goAthena/internal/modules/social/party/domain"
)

// PartyService is the party use-case service.
type PartyService struct {
	repo domain.PartyRepository
}

// NewPartyService builds a PartyService backed by repo.
func NewPartyService(repo domain.PartyRepository) *PartyService {
	return &PartyService{repo: repo}
}

// Create creates a party led by the char and returns it. A blank name (after
// trimming) is rejected before it reaches the repo, matching rAthena's
// party_create early return on an empty trimmed name.
func (s *PartyService) Create(ctx context.Context, leaderAccountID, leaderCharID uint32, name string) (domain.Party, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.Party{}, domain.ErrEmptyPartyName
	}
	p, err := s.repo.Create(ctx, leaderAccountID, leaderCharID, name)
	if err != nil {
		return domain.Party{}, fmt.Errorf("create party: %w", err)
	}
	return p, nil
}

// Get returns the party by id.
func (s *PartyService) Get(ctx context.Context, id domain.PartyID) (domain.Party, error) {
	p, err := s.repo.Get(ctx, id)
	if err != nil {
		return domain.Party{}, fmt.Errorf("get party: %w", err)
	}
	return p, nil
}

// GetByMember returns the party the char belongs to.
func (s *PartyService) GetByMember(ctx context.Context, charID uint32) (domain.Party, error) {
	p, err := s.repo.GetByMember(ctx, charID)
	if err != nil {
		return domain.Party{}, fmt.Errorf("get party by member: %w", err)
	}
	return p, nil
}

// Members returns the roster ordered by char_id. This is the order the gateway
// writes into the ZC_GROUP_LIST member burst.
func (s *PartyService) Members(ctx context.Context, id domain.PartyID) ([]domain.PartyMember, error) {
	members, err := s.repo.Members(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list party members: %w", err)
	}
	return members, nil
}

// Accept joins charID to the party. The caller (gateway) has already established
// that the invitee accepted an outstanding invitation; the service enforces the
// one-party-per-char and size rules the repo applies.
func (s *PartyService) Accept(ctx context.Context, id domain.PartyID, charID uint32) error {
	if err := s.repo.AddMember(ctx, id, charID); err != nil {
		return fmt.Errorf("add party member: %w", err)
	}
	return nil
}

// Leave removes charID from the party. If it was the leader the repo disbands
// the whole party (rAthena semantics), so the caller must notify every member,
// not just the leaver.
func (s *PartyService) Leave(ctx context.Context, id domain.PartyID, charID uint32) error {
	if err := s.repo.RemoveMember(ctx, id, charID); err != nil {
		return fmt.Errorf("remove party member: %w", err)
	}
	return nil
}

// Kick removes targetCharID from the party, but only if actorCharID is the
// leader. The authority check lives here rather than in the repo so the repo
// stays a pure persistence port; the gateway maps ErrNotLeader onto rAthena's
// ZC_DELETE_MEMBER_FROM_GROUP result code.
//
// No self-kick guard: rAthena's party_removemember validates only that the
// REQUESTER is the leader and that the target is a member, so a leader naming
// themselves falls through to the withdraw path, which disbands
// (src/map/party.cpp:718-730 → src/char/int_party.cpp:660-671). Delegating to
// RemoveMember reproduces that for free.
func (s *PartyService) Kick(ctx context.Context, id domain.PartyID, actorCharID, targetCharID uint32) error {
	p, err := s.repo.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("kick: get party: %w", err)
	}
	if p.LeaderChar != actorCharID {
		return domain.ErrNotLeader
	}
	if err := s.repo.RemoveMember(ctx, id, targetCharID); err != nil {
		return fmt.Errorf("kick member: %w", err)
	}
	return nil
}

// SetOptions writes the exp/item share rules, leader-only.
func (s *PartyService) SetOptions(ctx context.Context, id domain.PartyID, actorCharID uint32, exp, item uint8) error {
	p, err := s.repo.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("set options: get party: %w", err)
	}
	if p.LeaderChar != actorCharID {
		return domain.ErrNotLeader
	}
	exp &= 0x01
	item &= 0x03
	if err := s.repo.SetOptions(ctx, id, exp, item); err != nil {
		return fmt.Errorf("set party options: %w", err)
	}
	return nil
}

// SplitExp divides a mob-kill reward across the party members eligible to share
// it and returns the per-char awards; the caller grants each through
// WorldService.GrantExp.
//
// Eligibility mirrors rAthena's party_exp_share (src/map/party.cpp:1255-1259):
// a member counts only while online AND standing on the killer's map. Dead
// members are excluded there too, but death is runtime state the party module
// does not own, so the gateway filters those before calling.
//
// The reward is divided with integer truncation per member and the remainder is
// DROPPED — rAthena does `base_exp/=c; job_exp/=c` and pays each member that
// quotient with no leader correction (src/map/party.cpp:1263-1266,
// :1289-1291). The party_even_share_bonus multiplier is off by default and is
// not modelled.
func (s *PartyService) SplitExp(ctx context.Context, id domain.PartyID, killerMap string, base, job uint64) (map[uint32][2]uint64, error) {
	members, err := s.repo.Members(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("split exp: list members: %w", err)
	}
	eligible := make([]domain.PartyMember, 0, len(members))
	for _, m := range members {
		if m.Online && m.Map == killerMap {
			eligible = append(eligible, m)
		}
	}
	if len(eligible) == 0 {
		return nil, nil
	}
	n := uint64(len(eligible)) //nolint:gosec // G115: bounded by MaxPartySize.
	awards := make(map[uint32][2]uint64, len(eligible))
	for _, m := range eligible {
		awards[m.CharID] = [2]uint64{base / n, job / n}
	}
	return awards, nil
}
