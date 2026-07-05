//go:build unit

package repository

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/features/identity/domain"
)

func TestSessionKey_Format(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		accountID uint32
		want      string
	}{
		{"zero", 0, "session:account:0"},
		{"small", 1, "session:account:1"},
		{"max", 1<<32 - 1, fmt.Sprintf("session:account:%d", uint32(1<<32-1))},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, sessionKey(tc.accountID))
		})
	}
}

func TestSessionKey_DeterministicAndUnique(t *testing.T) {
	t.Parallel()

	// Same account_id must always map to the same key (no time/random
	// component leaked into the key path). Cache the key once and
	// compare against repeated calls — proves determinism without
	// relying on the optimizer to fold identical calls.
	first := sessionKey(42)
	for range 3 {
		assert.Equal(t, first, sessionKey(42))
	}
	// Different account_ids must not collide.
	assert.NotEqual(t, sessionKey(1), sessionKey(2))
	// Key prefix must be stable so other tooling can grep it.
	for _, aid := range []uint32{1, 1000, 1 << 20} {
		assert.True(t, strings.HasPrefix(sessionKey(aid), "session:account:"))
	}
}

func TestSession_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	original := &domain.Session{
		AccountID:  12345,
		LoginID1:   0xdeadbeef,
		LoginID2:   0xcafebabe,
		ClientType: 1,
		Sex:        domain.SexMale,
		RemoteIP:   "203.0.113.7:6128",
		CreatedAt:  time.Date(2026, 7, 5, 12, 34, 56, 0, time.UTC),
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	var decoded domain.Session
	require.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, original.AccountID, decoded.AccountID)
	assert.Equal(t, original.LoginID1, decoded.LoginID1)
	assert.Equal(t, original.LoginID2, decoded.LoginID2)
	assert.Equal(t, original.ClientType, decoded.ClientType)
	assert.Equal(t, original.Sex, decoded.Sex)
	assert.Equal(t, original.RemoteIP, decoded.RemoteIP)
	assert.True(t, original.CreatedAt.Equal(decoded.CreatedAt))
}

func TestSession_JSONRoundTrip_AllSexValues(t *testing.T) {
	t.Parallel()

	for _, s := range []domain.Sex{domain.SexMale, domain.SexFemale, domain.SexServer} {
		t.Run(string(s), func(t *testing.T) {
			t.Parallel()
			sess := &domain.Session{AccountID: 1, Sex: s}
			data, err := json.Marshal(sess)
			require.NoError(t, err)
			var decoded domain.Session
			require.NoError(t, json.Unmarshal(data, &decoded))
			assert.Equal(t, s, decoded.Sex)
		})
	}
}

func TestNewSessionRepository_ReturnsNonNil(t *testing.T) {
	t.Parallel()

	// The constructor should never return nil — callers will dereference
	// without a nil check. We pass a nil client because the constructor
	// doesn't touch it; this is purely a wiring smoke test.
	repo := NewSessionRepository(nil)
	assert.NotNil(t, repo)
}
