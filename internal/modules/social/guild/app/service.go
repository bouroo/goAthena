// Package app implements the guild bounded context use cases: create, invite
// reply, leave, expel, and disband.
//
// GuildService owns only the guild state machine. Who may invite whom and
// where a result is delivered (each member's own connection) are gateway
// concerns — the service stays free of gnet, of the world registry, and of
// character identity.
package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/bouroo/goAthena/internal/modules/social/guild/domain"
)

// GuildService is the guild use-case service.
type GuildService struct {
	repo domain.GuildRepository
}

// NewGuildService builds a GuildService backed by repo.
func NewGuildService(repo domain.GuildRepository) *GuildService {
	return &GuildService{repo: repo}
}

// Create creates a guild led by the char and returns it. A blank name (after
// trimming) is rejected before it reaches the repo, matching rAthena's
// guild_create early return on an empty trimmed name (guild.cpp:694-699).
func (s *GuildService) Create(ctx context.Context, masterAccountID, masterCharID uint32, name string) (domain.Guild, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.Guild{}, domain.ErrEmptyGuildName
	}
	g, err := s.repo.Create(ctx, masterAccountID, masterCharID, name)
	if err != nil {
		return domain.Guild{}, fmt.Errorf("create guild: %w", err)
	}
	return g, nil
}

// Get returns the guild by id.
func (s *GuildService) Get(ctx context.Context, id domain.GuildID) (domain.Guild, error) {
	g, err := s.repo.Get(ctx, id)
	if err != nil {
		return domain.Guild{}, fmt.Errorf("get guild: %w", err)
	}
	return g, nil
}

// GetByMember returns the guild the char belongs to.
func (s *GuildService) GetByMember(ctx context.Context, charID uint32) (domain.Guild, error) {
	g, err := s.repo.GetByMember(ctx, charID)
	if err != nil {
		return domain.Guild{}, fmt.Errorf("get guild by member: %w", err)
	}
	return g, nil
}

// Members returns the roster ordered by char_id. This is the order the gateway
// writes into the ZC_MEMBERMGR_INFO burst.
func (s *GuildService) Members(ctx context.Context, id domain.GuildID) ([]domain.GuildMember, error) {
	members, err := s.repo.Members(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list guild members: %w", err)
	}
	return members, nil
}

// Accept joins charID to the guild. The caller (gateway) has already
// established that the invitee accepted an outstanding invitation; the service
// enforces the one-guild-per-char and size rules the repo applies.
func (s *GuildService) Accept(ctx context.Context, id domain.GuildID, charID uint32) error {
	if err := s.repo.AddMember(ctx, id, charID); err != nil {
		return fmt.Errorf("add guild member: %w", err)
	}
	return nil
}

// Leave removes charID from the guild. rAthena lets the master leave like any
// member — the guild breaks only when the LAST member departs
// (int_guild.cpp:1353 guild_check_empty). The caller must notify every member
// when the guild dies.
func (s *GuildService) Leave(ctx context.Context, id domain.GuildID, charID uint32) error {
	if err := s.repo.RemoveMember(ctx, id, charID); err != nil {
		return fmt.Errorf("remove guild member: %w", err)
	}
	return nil
}

// Kick removes targetCharID from the guild, but only if actorCharID is the
// master and the target is not the master (rAthena: expel permission +
// master-protected, guild.cpp:1189-1211). No self-ban guard: the master naming
// themselves falls through to the master-protected check and fails there, the
// same outcome rAthena produces.
func (s *GuildService) Kick(ctx context.Context, id domain.GuildID, actorCharID, targetCharID uint32) error {
	g, err := s.repo.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("kick: get guild: %w", err)
	}
	if g.Master != actorCharID {
		return domain.ErrNotMaster
	}
	if g.Master == targetCharID {
		return domain.ErrCantExpelMaster
	}
	if err := s.repo.RemoveMember(ctx, id, targetCharID); err != nil {
		return fmt.Errorf("kick member: %w", err)
	}
	return nil
}

// Break disbands the guild after validating the rAthena guild_break rules that
// live in repository-owned state: the key must echo the guild name
// (guild.cpp:2300), the actor must be the master (:2303), and no other member
// may be online (:2310 — flag 2). Teardown then delegates to the repo.
func (s *GuildService) Break(ctx context.Context, id domain.GuildID, actorCharID uint32, key string) error {
	g, err := s.repo.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("break: get guild: %w", err)
	}
	if g.Master != actorCharID {
		return domain.ErrNotMaster
	}
	if strings.TrimSpace(key) != g.Name {
		return domain.ErrBreakNameMismatch
	}
	members, err := s.repo.Members(ctx, id)
	if err != nil {
		return fmt.Errorf("break: list members: %w", err)
	}
	for _, m := range members {
		if m.CharID != actorCharID && m.Online {
			return domain.ErrMembersOnline
		}
	}
	if err := s.repo.Break(ctx, id); err != nil {
		return fmt.Errorf("break: %w", err)
	}
	return nil
}
