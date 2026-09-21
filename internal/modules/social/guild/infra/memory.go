package infra

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/bouroo/goAthena/internal/modules/social/guild/domain"
)

// CharInfo is the per-char projection the roster read needs. It is only a
// concern of this in-memory fake: the GORM repository reads the real `char`
// table instead, so production never constructs one.
type CharInfo struct {
	AccountID uint32
	Name      string
	Map       string
	Class     uint16
	BaseLevel uint16
	Online    bool
}

// MemoryGuildRepository is an in-memory guild store for unit tests. It mirrors
// the GORM repository's contract, in particular the empty-guild break rule:
// the guild row is deleted when its last member leaves.
type MemoryGuildRepository struct {
	mu     sync.RWMutex
	guilds map[domain.GuildID]domain.Guild
	roster map[uint32]domain.GuildID // charID → guildID
	chars  map[uint32]CharInfo       // charID → roster projection
	next   domain.GuildID
}

// NewMemoryGuildRepository creates an empty in-memory repo.
func NewMemoryGuildRepository() *MemoryGuildRepository {
	return &MemoryGuildRepository{
		guilds: make(map[domain.GuildID]domain.Guild),
		roster: make(map[uint32]domain.GuildID),
		chars:  make(map[uint32]CharInfo),
	}
}

// SetChar registers the roster projection for a char, standing in for the
// `char` row the GORM repository would read. Tests call it before adding
// members.
func (r *MemoryGuildRepository) SetChar(charID uint32, ci CharInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chars[charID] = ci
}

// Create inserts a guild led by the given char and joins that char to it.
func (r *MemoryGuildRepository) Create(_ context.Context, _ uint32, masterCharID uint32, name string) (domain.Guild, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.Guild{}, domain.ErrEmptyGuildName
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.roster[masterCharID]; ok {
		return domain.Guild{}, domain.ErrAlreadyInGuild
	}
	for _, g := range r.guilds {
		if g.Name == name {
			return domain.Guild{}, domain.ErrNameExists
		}
	}
	r.next++
	g := domain.Guild{
		ID:        r.next,
		Name:      name,
		Master:    masterCharID,
		MasterNam: r.chars[masterCharID].Name,
		GuildLv:   1,
		MaxMember: domain.MaxGuildSize,
		AverageLv: r.chars[masterCharID].BaseLevel,
	}
	r.guilds[g.ID] = g
	r.roster[masterCharID] = g.ID
	return g, nil
}

// Get returns the guild by id.
func (r *MemoryGuildRepository) Get(_ context.Context, id domain.GuildID) (domain.Guild, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	g, ok := r.guilds[id]
	if !ok {
		return domain.Guild{}, domain.ErrGuildNotFound
	}
	return g, nil
}

// GetByMember returns the guild the char belongs to.
func (r *MemoryGuildRepository) GetByMember(_ context.Context, charID uint32) (domain.Guild, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.roster[charID]
	if !ok {
		return domain.Guild{}, domain.ErrNotInGuild
	}
	g, ok := r.guilds[id]
	if !ok {
		return domain.Guild{}, domain.ErrGuildNotFound
	}
	return g, nil
}

// Members returns the roster ordered by char_id.
func (r *MemoryGuildRepository) Members(_ context.Context, id domain.GuildID) ([]domain.GuildMember, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	g, ok := r.guilds[id]
	if !ok {
		return nil, domain.ErrGuildNotFound
	}
	out := make([]domain.GuildMember, 0)
	for charID, gid := range r.roster {
		if gid != id {
			continue
		}
		ci := r.chars[charID]
		out = append(out, domain.GuildMember{
			CharID:    charID,
			AccountID: ci.AccountID,
			Name:      ci.Name,
			Map:       ci.Map,
			Class:     ci.Class,
			BaseLevel: ci.BaseLevel,
			Online:    ci.Online,
			Master:    charID == g.Master,
			Position:  1,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CharID < out[j].CharID })
	for i := range out {
		if out[i].Master {
			out[i].Position = 0
		}
	}
	return out, nil
}

// AddMember joins a char to the guild.
func (r *MemoryGuildRepository) AddMember(_ context.Context, id domain.GuildID, charID uint32) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.guilds[id]; !ok {
		return domain.ErrGuildNotFound
	}
	if _, ok := r.roster[charID]; ok {
		return domain.ErrAlreadyInGuild
	}
	n := 0
	for _, gid := range r.roster {
		if gid == id {
			n++
		}
	}
	if n >= int(r.guilds[id].MaxMember) {
		return domain.ErrGuildFull
	}
	r.roster[charID] = id
	return nil
}

// RemoveMember clears the char's guild_id. When the departing char was the
// last member the guild row is deleted (rAthena guild_check_empty →
// mapif_parse_BreakGuild, src/char/int_guild.cpp:1353).
func (r *MemoryGuildRepository) RemoveMember(_ context.Context, id domain.GuildID, charID uint32) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.guilds[id]; !ok {
		return domain.ErrGuildNotFound
	}
	if r.roster[charID] != id {
		return domain.ErrNotInGuild
	}
	delete(r.roster, charID)
	for _, gid := range r.roster {
		if gid == id {
			return nil // survivors remain
		}
	}
	delete(r.guilds, id)
	return nil
}

// Break tears the guild down: every member's guild_id is cleared and the row
// deleted.
func (r *MemoryGuildRepository) Break(_ context.Context, id domain.GuildID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.guilds[id]; !ok {
		return domain.ErrGuildNotFound
	}
	for charID, gid := range r.roster {
		if gid == id {
			delete(r.roster, charID)
		}
	}
	delete(r.guilds, id)
	return nil
}
