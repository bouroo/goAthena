package infra

import (
	"context"
	"sync"

	"github.com/bouroo/goAthena/internal/modules/character/domain"
)

// MemoryCharacterRepository is an in-memory character store for unit tests.
type MemoryCharacterRepository struct {
	mu    sync.Mutex
	byID  map[domain.CharID]domain.Character
	names map[string]bool
	next  uint32
}

// NewMemoryCharacterRepository creates an empty in-memory repo.
func NewMemoryCharacterRepository() *MemoryCharacterRepository {
	return &MemoryCharacterRepository{
		byID:  make(map[domain.CharID]domain.Character),
		names: make(map[string]bool),
		next:  150000,
	}
}

// ListByAccount returns the account’s characters (in-memory).
func (r *MemoryCharacterRepository) ListByAccount(_ context.Context, accountID uint32) ([]domain.Character, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]domain.Character, 0)
	for _, c := range r.byID {
		if c.AccountID == accountID {
			out = append(out, c)
		}
	}
	return out, nil
}

// Create inserts a character and assigns an id (in-memory).
func (r *MemoryCharacterRepository) Create(_ context.Context, c domain.Character) (domain.Character, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.names[c.Name] {
		return domain.Character{}, domain.ErrNameTaken
	}
	r.next++
	c.ID = domain.CharID(r.next)
	r.byID[c.ID] = c
	r.names[c.Name] = true
	return c, nil
}

// Delete removes a character owned by accountID (in-memory).
func (r *MemoryCharacterRepository) Delete(_ context.Context, id domain.CharID, accountID uint32) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.byID[id]
	if !ok || c.AccountID != accountID {
		return domain.ErrCharacterNotFound
	}
	delete(r.byID, id)
	delete(r.names, c.Name)
	return nil
}

// NameExists reports whether a name is in use (in-memory).
func (r *MemoryCharacterRepository) NameExists(_ context.Context, name string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.names[name], nil
}

// MemorySessionStore is an in-memory session store for unit tests.
type MemorySessionStore struct {
	mu       sync.Mutex
	sessions map[uint32]domain.Session
}

// NewMemorySessionStore creates an empty in-memory session store.
func NewMemorySessionStore() *MemorySessionStore {
	return &MemorySessionStore{sessions: make(map[uint32]domain.Session)}
}

// PutSession stores the session (in-memory).
func (s *MemorySessionStore) PutSession(_ context.Context, sess domain.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sess.AccountID] = sess
	return nil
}

// GetSession returns the stored session (in-memory).
func (s *MemorySessionStore) GetSession(_ context.Context, accountID uint32) (domain.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[accountID]
	if !ok {
		return domain.Session{}, domain.ErrSessionNotFound
	}
	return sess, nil
}

// DeleteSession removes the session (in-memory).
func (s *MemorySessionStore) DeleteSession(_ context.Context, accountID uint32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, accountID)
	return nil
}
