package repository

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/bouroo/goAthena/internal/features/trade/domain"
)

type memoryLockStore struct {
	mu    sync.Mutex
	locks map[string]*lockEntry
}

type lockEntry struct {
	token     string
	expiresAt time.Time
}

// NewMemoryLockStore creates an in-memory LockStore for testing.
func NewMemoryLockStore() domain.LockStore {
	return &memoryLockStore{locks: make(map[string]*lockEntry)}
}

func (s *memoryLockStore) Acquire(ctx context.Context, key string, ttl time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if entry, exists := s.locks[key]; exists {
		if now.Before(entry.expiresAt) {
			return "", domain.ErrLockBusy
		}
		delete(s.locks, key)
	}

	token := generateToken()
	s.locks[key] = &lockEntry{
		token:     token,
		expiresAt: now.Add(ttl),
	}

	return token, nil
}

func (s *memoryLockStore) Release(ctx context.Context, key string, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, exists := s.locks[key]
	if !exists {
		return nil
	}

	if time.Now().After(entry.expiresAt) {
		delete(s.locks, key)
		return nil
	}

	if entry.token != token {
		return nil
	}

	delete(s.locks, key)
	return nil
}

func generateToken() string {
	return fmt.Sprintf("%x", time.Now().UnixNano())
}
