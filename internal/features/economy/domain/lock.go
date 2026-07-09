package domain

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// ErrLockBusy is returned by LockStore.Acquire when the named lock is
// already held by another operation. Callers (the economy service) treat
// this as "another economy op for this character is in flight" and map it
// to a shop result indicating the player should retry, rather than
// blocking or retrying server-side.
var ErrLockBusy = errors.New("economy lock busy")

//go:generate go run go.uber.org/mock/mockgen -destination=mock/lock_store_mock.go -package=domainmock . LockStore

// LockStore is the outbound port for the per-character economy mutex. It
// is the fast-path layer of dupe prevention (design D-203): it rejects
// concurrent buy/sell attempts for the same character before they reach
// the database, reducing contention and double-spend windows. The hard
// correctness guarantee remains the DB transaction with
// SELECT ... FOR UPDATE in CharacterZenyRepository.
//
// Implementations must make Release idempotent: releasing an absent or
// expired lock, or a lock owned by a different token, is a no-op (nil
// error) so a deferred Release after TTL expiry never errors.
type LockStore interface {
	// Acquire attempts to take the lock named key, holding it for at
	// most ttl. On success it returns an opaque ownership token that
	// Release must present back. A held lock yields a wrapped
	// ErrLockBusy (not a retry loop — the caller decides whether to
	// reject or back off).
	Acquire(ctx context.Context, key string, ttl time.Duration) (token string, err error)

	// Release frees the lock only if it is still owned by token
	// (compare-and-delete via the stored Lua script). Releasing an
	// absent/expired lock is a no-op.
	Release(ctx context.Context, key string, token string) error
}

// NewLockToken returns a fresh 128-bit ownership token (hex-encoded) used
// to safely release only a lock the caller still owns. It reads from
// crypto/rand so tokens are unguessable by other holders.
func NewLockToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("read random token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// CharLockKey returns the Valkey key for a character's economy mutex. The
// prefix namespaces economy locks away from session/registry keys.
func CharLockKey(charID uint32) string {
	return fmt.Sprintf("economy:char:%d", charID)
}
