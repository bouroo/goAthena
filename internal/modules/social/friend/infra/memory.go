package infra

import (
	"context"
	"sort"
	"sync"

	"github.com/bouroo/goAthena/internal/modules/social/friend/domain"
)

// CharInfo is the per-char projection the roster read needs. It is only a
// concern of this in-memory fake: the GORM repository reads the real `char`
// table instead, so production never constructs one.
type CharInfo struct {
	AccountID uint32
	Name      string
	Online    bool
}

// MemoryFriendRepository is an in-memory friend store for unit tests. It
// mirrors the GORM repository's contract: pairs are stored per direction and
// every mutation is bidirectional.
type MemoryFriendRepository struct {
	mu    sync.RWMutex
	pairs map[uint32]map[uint32]struct{} // owner charID → friend charIDs
	chars map[uint32]CharInfo            // charID → roster projection
}

// NewMemoryFriendRepository creates an empty in-memory repo.
func NewMemoryFriendRepository() *MemoryFriendRepository {
	return &MemoryFriendRepository{
		pairs: make(map[uint32]map[uint32]struct{}),
		chars: make(map[uint32]CharInfo),
	}
}

// SetChar registers the roster projection for a char, standing in for the
// `char` row the GORM repository would join. Tests call it before adding.
func (r *MemoryFriendRepository) SetChar(charID uint32, ci CharInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chars[charID] = ci
}

// DropChar removes a char's roster projection, standing in for a deleted
// character row an orphaned pair points at.
func (r *MemoryFriendRepository) DropChar(charID uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.chars, charID)
}

// List returns the owner's friends ordered by friend char id, skipping pairs
// whose char row is gone (a deleted character).
func (r *MemoryFriendRepository) List(_ context.Context, charID uint32) ([]domain.Friend, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.Friend, 0)
	for friendID := range r.pairs[charID] {
		ci, ok := r.chars[friendID]
		if !ok {
			continue
		}
		out = append(out, domain.Friend{
			CharID:          charID,
			FriendCharID:    friendID,
			FriendAccountID: ci.AccountID,
			Name:            ci.Name,
			Online:          ci.Online,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FriendCharID < out[j].FriendCharID })
	return out, nil
}

// Add inserts both directions, enforcing capacity on both lists and absence of
// the pair.
func (r *MemoryFriendRepository) Add(_ context.Context, ownerCharID, friendCharID uint32) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.hasLocked(ownerCharID, friendCharID) {
		return domain.ErrFriendExists
	}
	if r.countLocked(ownerCharID) >= domain.MaxFriends {
		return domain.ErrFriendListFull
	}
	if r.countLocked(friendCharID) >= domain.MaxFriends {
		return domain.ErrAcceptorListFull
	}
	r.addOneLocked(ownerCharID, friendCharID)
	r.addOneLocked(friendCharID, ownerCharID)
	return nil
}

// Remove deletes both directions. ErrFriendNotFound when the owner's list
// lacks the pair.
func (r *MemoryFriendRepository) Remove(_ context.Context, ownerCharID, friendCharID uint32) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.hasLocked(ownerCharID, friendCharID) {
		return domain.ErrFriendNotFound
	}
	delete(r.pairs[ownerCharID], friendCharID)
	delete(r.pairs[friendCharID], ownerCharID)
	return nil
}

func (r *MemoryFriendRepository) hasLocked(owner, friend uint32) bool {
	_, ok := r.pairs[owner][friend]
	return ok
}

func (r *MemoryFriendRepository) countLocked(owner uint32) int {
	return len(r.pairs[owner])
}

func (r *MemoryFriendRepository) addOneLocked(owner, friend uint32) {
	if r.pairs[owner] == nil {
		r.pairs[owner] = make(map[uint32]struct{})
	}
	r.pairs[owner][friend] = struct{}{}
}
