//go:build integration

package repository

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	valkeygo "github.com/valkey-io/valkey-go"

	"github.com/bouroo/goAthena/internal/features/identity/domain"
)

// testValkeyAddr is the default Valkey server address used by integration
// tests. It matches the port mapping declared in compose.yml (6379).
const testValkeyAddr = "127.0.0.1:6379"

// newIntegrationValkeyClient returns a valkey-go client connected to the
// local Valkey container declared in compose.yml. The client is closed via
// t.Cleanup so tests do not leak connections.
func newIntegrationValkeyClient(t *testing.T) valkeygo.Client {
	t.Helper()
	client, err := valkeygo.NewClient(valkeygo.ClientOption{
		InitAddress: []string{testValkeyAddr},
	})
	require.NoError(t, err, "create valkey client")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, client.Do(ctx, client.B().Ping().Build()).Error(),
		"ping valkey at %s; is the container running?", testValkeyAddr)

	t.Cleanup(func() { client.Close() })
	return client
}

// TestSessionRepository_Integration exercises Put / Get / Delete /
// TTL-expiry against a real Valkey instance. It is the L2 evidence that
// the SessionRepository port holds across the wire.
func TestSessionRepository_Integration(t *testing.T) {
	ctx := context.Background()
	client := newIntegrationValkeyClient(t)
	repo := NewSessionRepository(client)

	accountID := uint32(888000001)

	t.Cleanup(func() {
		_ = repo.Delete(ctx, accountID)
	})

	t.Run("put_get_delete", func(t *testing.T) {
		// Use a non-zero range that does not collide with seeded data.
		acct := uint32(888000002)
		defer func() { _ = repo.Delete(ctx, acct) }()

		want := &domain.Session{
			AccountID:  acct,
			LoginID1:   0xDEADBEEF,
			LoginID2:   0xCAFEBABE,
			ClientType: 0,
			Sex:        domain.SexMale,
			RemoteIP:   "203.0.113.7",
			CreatedAt:  time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC),
		}

		require.NoError(t, repo.Put(ctx, want, 30*time.Second))

		got, err := repo.Get(ctx, acct)
		require.NoError(t, err)
		require.NotNil(t, got, "session should be present after Put")
		assert.Equal(t, want.AccountID, got.AccountID)
		assert.Equal(t, want.LoginID1, got.LoginID1)
		assert.Equal(t, want.LoginID2, got.LoginID2)
		assert.Equal(t, want.ClientType, got.ClientType)
		assert.Equal(t, want.Sex, got.Sex)
		assert.Equal(t, want.RemoteIP, got.RemoteIP)
		assert.True(t, want.CreatedAt.Equal(got.CreatedAt),
			"created_at round-trip: want=%s got=%s", want.CreatedAt, got.CreatedAt)

		// Second Put overwrites (last-write-wins, matches rAthena).
		overwrite := &domain.Session{
			AccountID:  acct,
			LoginID1:   0x11111111,
			LoginID2:   0x22222222,
			ClientType: 1,
			Sex:        domain.SexFemale,
			RemoteIP:   "198.51.100.42",
			CreatedAt:  time.Date(2026, 7, 5, 13, 0, 0, 0, time.UTC),
		}
		require.NoError(t, repo.Put(ctx, overwrite, 30*time.Second))
		got, err = repo.Get(ctx, acct)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, overwrite.LoginID1, got.LoginID1)
		assert.Equal(t, overwrite.Sex, got.Sex)
		assert.Equal(t, overwrite.RemoteIP, got.RemoteIP)

		// Delete
		require.NoError(t, repo.Delete(ctx, acct))

		got, err = repo.Get(ctx, acct)
		require.NoError(t, err)
		assert.Nil(t, got, "Get after Delete must return nil, nil per domain contract")
	})

	t.Run("get on absent key returns nil not error", func(t *testing.T) {
		missing := uint32(888000099)
		got, err := repo.Get(ctx, missing)
		require.NoError(t, err, "missing key must not error")
		assert.Nil(t, got)
	})

	t.Run("ttl expiry", func(t *testing.T) {
		acct := uint32(888000003)
		defer func() { _ = repo.Delete(ctx, acct) }()

		sess := &domain.Session{
			AccountID: acct,
			LoginID1:  0xABCDEF01,
			LoginID2:  0x12345678,
			Sex:       domain.SexMale,
			RemoteIP:  "10.0.0.1",
			CreatedAt: time.Now().UTC(),
		}
		require.NoError(t, repo.Put(ctx, sess, 1*time.Second))

		// Confirm present before expiry.
		got, err := repo.Get(ctx, acct)
		require.NoError(t, err)
		require.NotNil(t, got)

		// Sleep past TTL + slack.
		time.Sleep(1500 * time.Millisecond)

		got, err = repo.Get(ctx, acct)
		require.NoError(t, err, "expired key must not surface a transport error")
		assert.Nil(t, got, "session must be gone after TTL")
	})
}

// TestWarehouseLock_Concurrency is the Phase-2 exit-gate proof:
// concurrent acquire attempts from N goroutines against the same
// account must produce exactly one winner and N-1 ErrLockHeld with no
// transport errors and no duplicate holders.
func TestWarehouseLock_Concurrency(t *testing.T) {
	client := newIntegrationValkeyClient(t)
	lock := NewWarehouseLock(client)

	const accountID = uint32(999999)
	ctx := context.Background()

	// Clean up any leftover lock from prior runs so the test is
	// independent of execution order.
	require.NoError(t, lock.Release(ctx, accountID, "cleanup-pre"))
	t.Cleanup(func() {
		_ = lock.Release(ctx, accountID, "cleanup-post")
	})

	t.Run("single acquire succeeds", func(t *testing.T) {
		// Start from a known-released state.
		require.NoError(t, lock.Release(ctx, accountID, "setup"))

		token, err := lock.Acquire(ctx, accountID, 5*time.Second)
		require.NoError(t, err)
		require.NotEmpty(t, token, "first Acquire must yield a token")

		// Second Acquire on the same key must fail with ErrLockHeld.
		_, err = lock.Acquire(ctx, accountID, 5*time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrLockHeld)

		// Release the held lock.
		require.NoError(t, lock.Release(ctx, accountID, token))

		// After release, Acquire must succeed again with a fresh token.
		token2, err := lock.Acquire(ctx, accountID, 5*time.Second)
		require.NoError(t, err)
		require.NotEmpty(t, token2)
		assert.NotEqual(t, token, token2, "re-acquired token must be different")

		// Leave the key released for subsequent subtests.
		require.NoError(t, lock.Release(ctx, accountID, token2))
	})

	t.Run("concurrent acquire: exactly one winner", func(t *testing.T) {
		// Make sure no stale lock is present before the race.
		require.NoError(t, lock.Release(ctx, accountID, "setup-race"))

		const numGoroutines = 100

		var wg sync.WaitGroup
		wins := atomic.Int32{}
		fails := atomic.Int32{}
		errs := atomic.Int32{}

		start := make(chan struct{})

		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start

				token, err := lock.Acquire(ctx, accountID, 5*time.Second)
				switch {
				case err == nil:
					wins.Add(1)
					t.Cleanup(func() { _ = lock.Release(ctx, accountID, token) })
				case errors.Is(err, domain.ErrLockHeld):
					fails.Add(1)
				default:
					errs.Add(1)
					t.Errorf("unexpected transport error: %v", err)
				}
			}()
		}

		close(start)
		wg.Wait()

		// Exit-gate assertions.
		assert.Equal(t, int32(0), errs.Load(),
			"transport errors must be zero — only ErrLockHeld is acceptable")
		assert.Equal(t, int32(1), wins.Load(),
			"exactly one goroutine must acquire the lock")
		assert.Equal(t, int32(numGoroutines-1), fails.Load(),
			"all other goroutines must observe ErrLockHeld")
	})

	t.Run("ttl expiry allows re-acquire", func(t *testing.T) {
		// Clean to start.
		require.NoError(t, lock.Release(ctx, accountID, "setup-ttl"))

		token, err := lock.Acquire(ctx, accountID, 1*time.Second)
		require.NoError(t, err)
		require.NotEmpty(t, token)

		// Wait for the 1s TTL to elapse plus slack.
		time.Sleep(1500 * time.Millisecond)

		// Re-acquire must succeed because the prior holder expired.
		token2, err := lock.Acquire(ctx, accountID, 5*time.Second)
		require.NoError(t, err)
		require.NotEmpty(t, token2)
		assert.NotEqual(t, token, token2)

		require.NoError(t, lock.Release(ctx, accountID, token2))
	})

	t.Run("release with wrong token is a no-op", func(t *testing.T) {
		// Clean to start.
		require.NoError(t, lock.Release(ctx, accountID, "setup-wrong"))

		token, err := lock.Acquire(ctx, accountID, 5*time.Second)
		require.NoError(t, err)
		require.NotEmpty(t, token)

		// Wrong-token release must not return an error: Lua returns 0,
		// the implementation surfaces (nil, nil) rather than ErrLockHeld.
		require.NoError(t, lock.Release(ctx, accountID, "wrong-token"))

		// Lock must still be held.
		_, err = lock.Acquire(ctx, accountID, 5*time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrLockHeld)

		// Clean up with the correct token.
		require.NoError(t, lock.Release(ctx, accountID, token))
	})

	t.Run("release of absent key is a no-op", func(t *testing.T) {
		require.NoError(t, lock.Release(ctx, accountID, "cleanup-warmup"))
		// No prior Acquire — releasing an absent key must not error.
		require.NoError(t, lock.Release(ctx, accountID, "any-token"))
	})
}
