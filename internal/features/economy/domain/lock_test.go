//go:build unit

package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewLockToken(t *testing.T) {
	tok, err := NewLockToken()
	require.NoError(t, err)
	// 16 random bytes hex-encoded = 32 chars.
	assert.Len(t, tok, 32)

	// Two tokens must differ (128 bits of entropy).
	other, err := NewLockToken()
	require.NoError(t, err)
	assert.NotEqual(t, tok, other, "tokens must be unique")
}

func TestCharLockKey(t *testing.T) {
	assert.Equal(t, "economy:char:42", CharLockKey(42))
	assert.Equal(t, "economy:char:0", CharLockKey(0))
}
