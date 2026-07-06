//go:build unit

package repository

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/features/identity/domain"
)

func TestWarehouseLockKey_Format(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		accountID uint32
		want      string
	}{
		{"zero", 0, "lock:storage:account:0"},
		{"small", 7, "lock:storage:account:7"},
		{"max", 1<<32 - 1, fmt.Sprintf("lock:storage:account:%d", uint32(1<<32-1))},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, warehouseLockKey(tc.accountID))
		})
	}
}

func TestWarehouseLockKey_DeterministicAndUnique(t *testing.T) {
	t.Parallel()

	// Same account_id must always map to the same key. Cache and
	// re-call to prove determinism without depending on call folding.
	first := warehouseLockKey(99)
	for range 3 {
		assert.Equal(t, first, warehouseLockKey(99))
	}
	// Different account_ids must not collide.
	assert.NotEqual(t, warehouseLockKey(1), warehouseLockKey(2))
	// Key prefix must be stable and distinct from the session prefix
	// so ops can grep storage locks separately.
	for _, aid := range []uint32{1, 1000, 1 << 20} {
		key := warehouseLockKey(aid)
		assert.True(t, strings.HasPrefix(key, "lock:storage:account:"))
		assert.NotEqual(t, key, sessionKey(aid), "lock and session keys must differ")
	}
}

func TestNewToken_RandomAndUnique(t *testing.T) {
	t.Parallel()

	// 1000 draws must produce 1000 distinct 32-char hex strings; the
	// probability of a collision with 128 bits of entropy is ~5e-35.
	seen := make(map[string]struct{}, 1000)
	for i := range 1000 {
		tok, err := newToken()
		require.NoError(t, err)
		assert.Len(t, tok, 32, "hex(16 bytes) should be 32 chars")
		_, dup := seen[tok]
		assert.False(t, dup, "duplicate token %q after %d draws", tok, i)
		seen[tok] = struct{}{}
	}
}

func TestNewToken_OnlyHexChars(t *testing.T) {
	t.Parallel()

	tok, err := newToken()
	require.NoError(t, err)
	for _, r := range tok {
		assert.True(t,
			(r >= '0' && r <= '9') || (r >= 'a' && r <= 'f'),
			"token char %q is not lowercase hex", r)
	}
}

func TestWarehouseUnlockScript_MatchesDesign(t *testing.T) {
	t.Parallel()

	// The script must compare the current value to ARGV[1] before
	// deleting. Lock this string against accidental edits — a typo
	// here silently degrades Release into a no-op-or-worse.
	assert.Contains(t, warehouseUnlockScript, "GET")
	assert.Contains(t, warehouseUnlockScript, "DEL")
	assert.Contains(t, warehouseUnlockScript, "ARGV[1]")
	// Must not call UNLINK / FLUSHDB / anything destructive of other keys.
	assert.NotContains(t, warehouseUnlockScript, "UNLINK")
	assert.NotContains(t, warehouseUnlockScript, "FLUSH")
}

func TestNewWarehouseLock_ReturnsNonNil(t *testing.T) {
	t.Parallel()

	// Wiring smoke test: constructor must not return nil. We pass a nil
	// client because the constructor does not dereference it.
	lock := NewWarehouseLock(nil)
	assert.NotNil(t, lock)
}

func TestErrLockHeld_IsSentinel(t *testing.T) {
	t.Parallel()

	// Acquire wraps ErrLockHeld with %w — verify errors.Is still matches.
	wrapped := fmt.Errorf("%w: account %d", domain.ErrLockHeld, 42)
	require.Error(t, wrapped)
	assert.ErrorIs(t, wrapped, domain.ErrLockHeld)
}

func TestDefaultWarehouseLockTTL_Positive(t *testing.T) {
	t.Parallel()

	// The default must be positive and reasonable: a few seconds is
	// wider than the worst-case storage round-trip but short enough
	// that a crashed holder returns the lock promptly.
	ttl := DefaultWarehouseLockTTL
	assert.Greater(t, ttl, time.Duration(0))
	assert.LessOrEqual(t, ttl, 60*time.Second)
}
