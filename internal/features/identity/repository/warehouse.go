package repository

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	valkeygo "github.com/valkey-io/valkey-go"

	"github.com/bouroo/goAthena/internal/features/identity/domain"
)

// DefaultWarehouseLockTTL is the default lock duration used when callers
// pass a zero value (decision D-009). The 5s window is wider than the
// worst-case storage round-trip but short enough that a crashed holder
// returns the lock promptly for the next login.
const DefaultWarehouseLockTTL = 5 * time.Second

// warehouseUnlockScript implements compare-and-delete: the key is
// deleted only if the stored value still equals the caller's token.
// Returning 0 on mismatch prevents a stale holder from releasing a
// lock that has expired and been re-acquired by another caller.
var warehouseUnlockScript = "if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) else return 0 end"

// warehouseLockKey returns the Valkey key used for the storage lock.
// One lock per account — the same account may have multiple concurrent
// logins (different pods), only one of which may touch storage at a
// time (int_storage.cpp:38-86 in rAthena, which lacked any lock at all).
func warehouseLockKey(accountID uint32) string {
	return fmt.Sprintf("lock:storage:account:%d", accountID)
}

type warehouseLock struct {
	client valkeygo.Client
}

// NewWarehouseLock returns a Valkey-backed WarehouseLock. The caller
// owns the valkey client and is responsible for closing it.
func NewWarehouseLock(client valkeygo.Client) domain.WarehouseLock {
	return &warehouseLock{client: client}
}

// newToken returns a 32-char hex token (128 bits of entropy) used as
// the lock value. The token is opaque — Release uses the same string
// to prove ownership.
func newToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate lock token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// Acquire tries to take the storage lock atomically via SET NX PX. A
// nil reply from Valkey means another holder owns the key; that case
// is mapped to domain.ErrLockHeld. A zero ttl falls back to
// DefaultWarehouseLockTTL.
func (r *warehouseLock) Acquire(ctx context.Context, accountID uint32, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = DefaultWarehouseLockTTL
	}
	token, err := newToken()
	if err != nil {
		return "", err
	}

	resp := r.client.Do(ctx, r.client.B().
		Set().
		Key(warehouseLockKey(accountID)).
		Value(token).
		Nx().
		Px(ttl).
		Build())

	if err := resp.Error(); err != nil {
		// SET ... NX returns a Valkey nil reply when the key already
		// exists. IsValkeyNil is the documented way to distinguish
		// "lock held" from real transport errors (matches valkey-go's
		// own valkeylock package).
		if valkeygo.IsValkeyNil(err) {
			return "", fmt.Errorf("%w: account %d", domain.ErrLockHeld, accountID)
		}
		return "", fmt.Errorf("acquire warehouse lock for account %d: %w", accountID, err)
	}
	return token, nil
}

// Release releases the lock if and only if the supplied token still
// matches the stored value. The compare-and-delete runs inside a Lua
// script so the GET and DEL are atomic on the Valkey server — without
// this, a lock that expired and was re-acquired by another caller
// could be released by the original (stale) holder.
func (r *warehouseLock) Release(ctx context.Context, accountID uint32, token string) error {
	resp := r.client.Do(ctx, r.client.B().
		Eval().
		Script(warehouseUnlockScript).
		Numkeys(1).
		Key(warehouseLockKey(accountID)).
		Arg(token).
		Build())

	if err := resp.Error(); err != nil {
		// A nil reply is impossible for EVAL (the script always returns
		// an integer), but treat it defensively as success — the lock
		// is not present, which is the desired post-condition.
		if valkeygo.IsValkeyNil(err) {
			return nil
		}
		return fmt.Errorf("release warehouse lock for account %d: %w", accountID, err)
	}
	return nil
}
