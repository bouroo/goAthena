package repository

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/bouroo/goAthena/internal/features/storage/domain"
)

type memoryLockEntry struct {
	token   string
	expires time.Time
}

type memoryLockStore struct {
	mu    sync.Mutex
	locks map[string]memoryLockEntry
}

// NewMemoryLockStore creates a new in-memory lock store for storage operations.
func NewMemoryLockStore() domain.LockStore {
	return &memoryLockStore{
		locks: make(map[string]memoryLockEntry),
	}
}

func (s *memoryLockStore) Acquire(ctx context.Context, key string, ttl time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()

	if entry, exists := s.locks[key]; exists {
		if entry.expires.After(now) {
			return "", fmt.Errorf("%w: lock %s held until %s", domain.ErrStorageLocked, key, entry.expires.Format(time.RFC3339))
		}
	}

	token := fmt.Sprintf("token-%s-%d", key, now.UnixNano())
	s.locks[key] = memoryLockEntry{
		token:   token,
		expires: now.Add(ttl),
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

	if entry.token != token {
		return nil
	}

	delete(s.locks, key)
	return nil
}
