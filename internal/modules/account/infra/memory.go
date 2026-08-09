package infra

import (
	"context"
	"sync"
	"time"

	"github.com/bouroo/goAthena/internal/modules/account/domain"
)

// MemoryAccountRepository is an in-memory account store for unit tests and the
// local dev profile. It is safe for concurrent use.
type MemoryAccountRepository struct {
	mu    sync.RWMutex
	byID  map[domain.AccountID]domain.Account
	byUID map[string]domain.AccountID
}

// NewMemoryAccountRepository seeds the store with the given accounts.
func NewMemoryAccountRepository(accounts ...domain.Account) *MemoryAccountRepository {
	r := &MemoryAccountRepository{
		byID:  make(map[domain.AccountID]domain.Account, len(accounts)),
		byUID: make(map[string]domain.AccountID, len(accounts)),
	}
	for _, a := range accounts {
		r.byID[a.ID] = a
		r.byUID[a.UserID] = a.ID
	}
	return r
}

// FindByUserID returns the account for the login name or domain.ErrAccountNotFound.
func (r *MemoryAccountRepository) FindByUserID(_ context.Context, userID string) (domain.Account, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.byUID[userID]
	if !ok {
		return domain.Account{}, domain.ErrAccountNotFound
	}
	return r.byID[id], nil
}

// RecordLogin bumps logincount and stamps lastlogin/last_ip in memory.
func (r *MemoryAccountRepository) RecordLogin(_ context.Context, id domain.AccountID, ip string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.byID[id]
	if !ok {
		return domain.ErrAccountNotFound
	}
	a.LoginCount++
	now := time.Now()
	a.LastLogin = &now
	a.LastIP = ip
	r.byID[id] = a
	return nil
}
