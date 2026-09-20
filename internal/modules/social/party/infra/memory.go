package infra

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/bouroo/goAthena/internal/modules/social/party/domain"
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

// MemoryPartyRepository is an in-memory party store for unit tests. It mirrors
// the GORM repository's contract and, in particular, its leader-leave semantics:
// the whole party is disbanded when its leader leaves.
type MemoryPartyRepository struct {
	mu      sync.RWMutex
	parties map[domain.PartyID]domain.Party
	members map[uint32]domain.PartyID // charID → partyID
	chars   map[uint32]CharInfo       // charID → roster projection
	next    domain.PartyID
}

// NewMemoryPartyRepository creates an empty in-memory repo.
func NewMemoryPartyRepository() *MemoryPartyRepository {
	return &MemoryPartyRepository{
		parties: make(map[domain.PartyID]domain.Party),
		members: make(map[uint32]domain.PartyID),
		chars:   make(map[uint32]CharInfo),
	}
}

// SetChar registers the roster projection for a char, standing in for the `char`
// row the GORM repository would read. Tests call it before adding members.
func (r *MemoryPartyRepository) SetChar(charID uint32, ci CharInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chars[charID] = ci
}

// Create inserts a party led by the given pair and joins that char to it.
func (r *MemoryPartyRepository) Create(_ context.Context, leaderAccountID, leaderCharID uint32, name string) (domain.Party, error) {
	if strings.TrimSpace(name) == "" {
		return domain.Party{}, domain.ErrEmptyPartyName
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.members[leaderCharID]; ok {
		return domain.Party{}, domain.ErrAlreadyInParty
	}
	r.next++
	p := domain.Party{
		ID:         r.next,
		Name:       strings.TrimSpace(name),
		LeaderID:   leaderAccountID,
		LeaderChar: leaderCharID,
	}
	r.parties[p.ID] = p
	r.members[leaderCharID] = p.ID
	return p, nil
}

// Get returns the party by id.
func (r *MemoryPartyRepository) Get(_ context.Context, id domain.PartyID) (domain.Party, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.parties[id]
	if !ok {
		return domain.Party{}, domain.ErrPartyNotFound
	}
	return p, nil
}

// GetByMember returns the party the char belongs to.
func (r *MemoryPartyRepository) GetByMember(_ context.Context, charID uint32) (domain.Party, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.members[charID]
	if !ok {
		return domain.Party{}, domain.ErrNotInParty
	}
	p, ok := r.parties[id]
	if !ok {
		return domain.Party{}, domain.ErrPartyNotFound
	}
	return p, nil
}

// Members returns the roster ordered by char_id.
func (r *MemoryPartyRepository) Members(_ context.Context, id domain.PartyID) ([]domain.PartyMember, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.parties[id]
	if !ok {
		return nil, domain.ErrPartyNotFound
	}
	out := make([]domain.PartyMember, 0)
	for charID, pid := range r.members {
		if pid != id {
			continue
		}
		ci := r.chars[charID]
		out = append(out, domain.PartyMember{
			CharID:    charID,
			AccountID: ci.AccountID,
			Name:      ci.Name,
			Map:       ci.Map,
			Class:     ci.Class,
			BaseLevel: ci.BaseLevel,
			Online:    ci.Online,
			Leader:    ci.AccountID == p.LeaderID && charID == p.LeaderChar,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CharID < out[j].CharID })
	return out, nil
}

// AddMember joins a char to the party.
func (r *MemoryPartyRepository) AddMember(_ context.Context, id domain.PartyID, charID uint32) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.parties[id]; !ok {
		return domain.ErrPartyNotFound
	}
	if _, ok := r.members[charID]; ok {
		return domain.ErrAlreadyInParty
	}
	if r.countLocked(id) >= domain.MaxPartySize {
		return domain.ErrPartyFull
	}
	r.members[charID] = id
	return nil
}

// RemoveMember takes a char out of the party. A departing leader disbands the
// whole party, matching rAthena (src/char/int_party.cpp:651-676).
func (r *MemoryPartyRepository) RemoveMember(_ context.Context, id domain.PartyID, charID uint32) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.parties[id]
	if !ok {
		return domain.ErrPartyNotFound
	}
	pid, ok := r.members[charID]
	if !ok || pid != id {
		return domain.ErrNotInParty
	}
	if charID == p.LeaderChar {
		for m, mp := range r.members {
			if mp == id {
				delete(r.members, m)
			}
		}
		delete(r.parties, id)
		return nil
	}
	delete(r.members, charID)
	return nil
}

// SetOptions writes the exp/item share rules.
func (r *MemoryPartyRepository) SetOptions(_ context.Context, id domain.PartyID, exp, item uint8) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.parties[id]
	if !ok {
		return domain.ErrPartyNotFound
	}
	p.Exp, p.Item = exp, item
	r.parties[id] = p
	return nil
}

// countLocked is the number of members in a party. Caller holds mu.
func (r *MemoryPartyRepository) countLocked(id domain.PartyID) int {
	n := 0
	for _, pid := range r.members {
		if pid == id {
			n++
		}
	}
	return n
}
