//go:build e2e

package e2e

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bouroo/goAthena/internal/features/identity/domain"
	"github.com/bouroo/goAthena/internal/features/identity/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newWarehouseLock wires the production WarehouseLock implementation
// against the harness's Valkey client. This is the same code path the
// cluster runs, so a passing test here is a strong signal that the
// distributed lock will behave correctly in production.
func newWarehouseLock(h *E2EHarness) domain.WarehouseLock {
	return repository.NewWarehouseLock(h.ValkeyClient)
}

// TestE2E_WarehouseLockAntiDupe exercises the canonical anti-dupe
// scenario: 10 goroutines race for the same lock, exactly one must
// win and the other nine must receive ErrLockHeld. After the winner
// releases, the lock must be re-acquirable.
func TestE2E_WarehouseLockAntiDupe(t *testing.T) {
	h := NewE2EHarness(t)
	ctx := TestContext(t)

	lock := newWarehouseLock(h)

	// Use a unique account id per test so parallel runs don't
	// contend on the same Valkey key. account_id is a uint32 so a
	// high-bit value is safe.
	accountID := uint32(0xE2E70000) | uint32(time.Now().UnixNano()&0xFFFF)
	ctx2s, cancel2s := context.WithTimeout(ctx, 2*time.Second)
	defer cancel2s()
	// Pre-clean any stale key from a previous failed run.
	_ = h.ValkeyClient.Do(ctx2s,
		h.ValkeyClient.B().Del().Key(warehouseKey(accountID)).Build()).Error()

	// First acquisition — this goroutine holds the lock for the
	// duration of the test.
	token, err := lock.Acquire(ctx, accountID, 5*time.Second)
	require.NoError(t, err, "first Acquire must succeed")
	require.NotEmpty(t, token, "first Acquire must return a token")

	// Second acquisition from the SAME holder must also fail with
	// ErrLockHeld — the lock is already taken by the same logical
	// holder, but the contract is "one per account".
	_, err = lock.Acquire(ctx, accountID, 5*time.Second)
	require.Error(t, err, "duplicate Acquire must fail")
	require.ErrorIs(t, err, domain.ErrLockHeld,
		"duplicate Acquire must surface ErrLockHeld")

	// Concurrent goroutines: 9 racing acquires while the holder is
	// still alive. Every one of them must observe ErrLockHeld.
	const racers = 9
	var wg sync.WaitGroup
	var wins atomic.Int32
	errCh := make(chan error, racers)
	wg.Add(racers)
	for range racers {
		go func() {
			defer wg.Done()
			rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			_, err := lock.Acquire(rctx, accountID, 5*time.Second)
			if err == nil {
				wins.Add(1)
				return
			}
			errCh <- err
		}()
	}
	wg.Wait()
	close(errCh)
	assert.Equal(t, int32(0), wins.Load(),
		"no racing goroutine may win the lock while the holder is alive")
	for err := range errCh {
		require.ErrorIs(t, err, domain.ErrLockHeld,
			"every racing goroutine must observe ErrLockHeld")
	}

	// Release the lock and verify it can be re-acquired.
	require.NoError(t, lock.Release(ctx, accountID, token),
		"Release must succeed with the matching token")

	newToken, err := lock.Acquire(ctx, accountID, 5*time.Second)
	require.NoError(t, err, "Acquire after Release must succeed")
	require.NotEmpty(t, newToken)
	assert.NotEqual(t, token, newToken,
		"the re-issued token must be fresh (compare-and-delete correctness)")
	require.NoError(t, lock.Release(ctx, accountID, newToken))
}

// TestE2E_WarehouseLockReleaseMismatch verifies that Release with a
// stale token is a no-op: the compare-and-delete Lua script must
// leave the current holder's lock intact when the supplied token
// does not match.
func TestE2E_WarehouseLockReleaseMismatch(t *testing.T) {
	h := NewE2EHarness(t)
	ctx := TestContext(t)

	lock := newWarehouseLock(h)
	accountID := uint32(0xE2E80000) | uint32(time.Now().UnixNano()&0xFFFF)

	token, err := lock.Acquire(ctx, accountID, 5*time.Second)
	require.NoError(t, err)

	// Wrong token — Release must NOT delete the lock.
	require.NoError(t, lock.Release(ctx, accountID, "stale-token-xyz"))

	// Lock must still be held — a fresh Acquire must fail.
	_, err = lock.Acquire(ctx, accountID, 5*time.Second)
	require.Error(t, err, "lock must remain held after a mismatched Release")
	require.ErrorIs(t, err, domain.ErrLockHeld)

	// Correct token cleans up.
	require.NoError(t, lock.Release(ctx, accountID, token))
}

// TestE2E_WarehouseLockTTLExpiry verifies the TTL fallback path: a
// zero TTL passed to Acquire falls back to DefaultWarehouseLockTTL
// (5s in production). We assert the key is created by attempting a
// duplicate acquire and observing ErrLockHeld.
func TestE2E_WarehouseLockTTLExpiry(t *testing.T) {
	h := NewE2EHarness(t)
	ctx := TestContext(t)

	lock := newWarehouseLock(h)
	accountID := uint32(0xE2E90000) | uint32(time.Now().UnixNano()&0xFFFF)

	token, err := lock.Acquire(ctx, accountID, 0)
	require.NoError(t, err, "Acquire with zero TTL must use DefaultWarehouseLockTTL")
	require.NotEmpty(t, token)

	_, err = lock.Acquire(ctx, accountID, 0)
	require.Error(t, err)
	require.ErrorIs(t, err, domain.ErrLockHeld)

	require.NoError(t, lock.Release(ctx, accountID, token))
}

// warehouseKey mirrors the format used by the production repository
// in internal/features/identity/repository/warehouse.go. Duplicated
// here to avoid exporting an internal helper.
func warehouseKey(accountID uint32) string {
	return "lock:storage:account:" + uintToString(accountID)
}
