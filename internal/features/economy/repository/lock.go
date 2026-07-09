package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	valkeygo "github.com/valkey-io/valkey-go"

	"github.com/bouroo/goAthena/internal/features/economy/domain"
)

// releaseScript atomically deletes the lock key only when its current
// value equals the caller's token (compare-and-delete). This prevents a
// slow holder from releasing a lock it no longer owns after the TTL
// expired and another operation re-acquired it.
//
// KEYS[1] = lock key, ARGV[1] = ownership token.
const releaseScript = `if redis.call('get', KEYS[1]) == ARGV[1] then return redis.call('del', KEYS[1]) else return 0 end`

// valkeyLockStore implements domain.LockStore on top of a valkey-go client
// using SET key token NX EX (acquire) and EVAL releaseScript (release).
type valkeyLockStore struct {
	client valkeygo.Client
}

// NewValkeyLockStore wires the production distributed lock onto a
// valkey-go client. The client must already be connected.
func NewValkeyLockStore(client valkeygo.Client) domain.LockStore {
	return &valkeyLockStore{client: client}
}

// Acquire takes the lock with a fresh ownership token. SET ... NX returns
// a nil reply (valkeygo.Nil) when the key already exists, which we map to
// ErrLockBusy. ttl is rounded up to a minimum of one second.
func (s *valkeyLockStore) Acquire(ctx context.Context, key string, ttl time.Duration) (string, error) {
	token, err := domain.NewLockToken()
	if err != nil {
		return "", fmt.Errorf("economy lock acquire %q: %w", key, err)
	}
	secs := max(int64(ttl.Seconds()), 1)
	cmd := s.client.B().Set().Key(key).Value(token).Nx().ExSeconds(secs).Build()
	if err := s.client.Do(ctx, cmd).Error(); err != nil {
		if errors.Is(err, valkeygo.Nil) {
			return "", fmt.Errorf("%w: key=%s", domain.ErrLockBusy, key)
		}
		return "", fmt.Errorf("economy lock acquire %q: %w", key, err)
	}
	return token, nil
}

// Release runs the compare-and-delete Lua script. A nil reply (the key was
// absent or owned by another token) is treated as success — the lock is
// already gone, which is the desired post-condition.
func (s *valkeyLockStore) Release(ctx context.Context, key, token string) error {
	cmd := s.client.B().Eval().Script(releaseScript).Numkeys(1).Key(key).Arg(token).Build()
	if err := s.client.Do(ctx, cmd).Error(); err != nil && !errors.Is(err, valkeygo.Nil) {
		return fmt.Errorf("economy lock release %q: %w", key, err)
	}
	return nil
}
