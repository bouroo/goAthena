//go:build unit

package assets

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEUCKRKnownString(t *testing.T) {
	eucKR := []byte{0xbe, 0xc8, 0xb3, 0xe7} // "안녕" in EUC-KR
	got, err := DecodeEUCKR(eucKR)
	require.NoError(t, err)
	assert.Equal(t, "안녕", got)
}

func TestEUCKRRoundTrip(t *testing.T) {
	original := "안녕하세요, World! 123"
	encoded, err := EncodeEUCKR(original)
	require.NoError(t, err)
	decoded, err := DecodeEUCKR(encoded)
	require.NoError(t, err)
	assert.Equal(t, original, decoded)
}

func TestEUCKRASCII(t *testing.T) {
	got, err := DecodeEUCKR([]byte("hello"))
	require.NoError(t, err)
	assert.Equal(t, "hello", got)
}
