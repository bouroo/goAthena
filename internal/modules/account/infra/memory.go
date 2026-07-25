// Package infra provides the account bounded context's persistence adapters:
// an in-memory implementation (unit tests and the no-DB composition root) and a
// GORM implementation backed by the login table. Both satisfy the domain ports.
package infra

import (
	"context"
	"sync"
	"time"

	"github.com/bouroo/goAthena/internal/modules/account/domain"
)

// MemoryAccountRepository is an in-memory domain.AccountRepository. It is
// concurrency-safe and returns defensive copies so callers cannot mutate the
// store through a returned pointer.
type MemoryAccountRepository struct {
	mu       sync.RWMutex
	byUserID map[string]*domain.Account
}

// NewMemoryAccountRepository seeds the store with copies of the given accounts,
// keyed by UserID.
func NewMemoryAccountRepository(seeds ...*domain.Account) *MemoryAccountRepository {
	r := &MemoryAccountRepository{byUserID: make(map[string]*domain.Account, len(seeds))}
	for _, a := range seeds {
		cp := *a
		r.byUserID[a.UserID] = &cp
	}
	return r
}

// LoadByUserID returns a defensive copy of the stored account; the returned
// pointer does not alias the store's row.
func (r *MemoryAccountRepository) LoadByUserID(_ context.Context, userID string) (*domain.Account, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.byUserID[userID]
	if !ok {
		return nil, domain.ErrAccountNotFound
	}
	cp := *a
	return &cp, nil
}

// UpdateLoginInfo writes the post-login bookkeeping fields in place. A missing
// account id surfaces as ErrAccountNotFound so the service treats it as a
// refusal rather than an infra fault.
func (r *MemoryAccountRepository) UpdateLoginInfo(_ context.Context, accountID uint32, ip string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.byUserID {
		if a.AccountID == accountID {
			a.LoginCount++
			a.LastIP = ip
			a.LastLogin = now
			return nil
		}
	}
	return domain.ErrAccountNotFound
}

// MemorySessionStore is an in-memory domain.SessionStore.
type MemorySessionStore struct {
	mu    sync.RWMutex
	byAID map[uint32]*domain.Session
}

// NewMemorySessionStore returns an empty session store keyed by account id.
func NewMemorySessionStore() *MemorySessionStore {
	return &MemorySessionStore{byAID: make(map[uint32]*domain.Session)}
}

// Put stores a copy of the session keyed by account id, overwriting any prior
// entry so re-login replaces the old token.
func (s *MemorySessionStore) Put(_ context.Context, sess *domain.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *sess
	s.byAID[sess.AccountID] = &cp
	return nil
}

// Get returns a defensive copy of the session or ErrSessionNotFound.
func (s *MemorySessionStore) Get(_ context.Context, accountID uint32) (*domain.Session, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.byAID[accountID]
	if !ok {
		return nil, domain.ErrSessionNotFound
	}
	cp := *sess
	return &cp, nil
}

// Delete removes the session for the given id. It is idempotent: deleting a
// key that was never stored (or already removed) is not an error.
func (s *MemorySessionStore) Delete(_ context.Context, accountID uint32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byAID, accountID)
	return nil
}
