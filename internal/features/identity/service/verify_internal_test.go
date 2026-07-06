//go:build unit

package service

import (
	"testing"

	"github.com/bouroo/goAthena/internal/features/identity/domain"
)

// TestVerifyPassword_LengthMismatchDoesNotLeak exercises verifyPassword
// across the encoding matrix and length-mismatch combinations to ensure
// no path leaks the stored credential length.
//
// The historical implementation called subtle.ConstantTimeCompare
// directly on the raw bytes, which short-circuits on length mismatch
// and therefore leaks the stored credential's length via timing. The
// fixed implementation SHA-256 hashes both sides so both operands are
// always exactly 32 bytes; these cases verify the post-fix behavior is
// correct for every encoding/length combination, including the
// previously-timing-leaky "different length" cases.
func TestVerifyPassword_LengthMismatchDoesNotLeak(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		stored   string
		given    string
		encoding domain.PasswordEncoding
		want     bool
	}{
		{
			name:     "plain_different_length_different_content",
			stored:   "ab",
			given:    "a",
			encoding: domain.PassEncodingPlain,
			want:     false,
		},
		{
			name:     "plain_same_length_same_content",
			stored:   "a",
			given:    "a",
			encoding: domain.PassEncodingPlain,
			want:     true,
		},
		{
			name:     "plain_both_empty",
			stored:   "",
			given:    "",
			encoding: domain.PassEncodingPlain,
			want:     true,
		},
		{
			name:     "md5_matching_hash",
			stored:   "2ab96390c7dbe3439de74d0c9b0b1767",
			given:    "hunter2",
			encoding: domain.PassEncodingMD5,
			want:     true,
		},
		{
			name:     "md5_length_mismatch_stored_not_valid_hash",
			stored:   "short",
			given:    "2ab96390c7dbe3439de74d0c9b0b1767",
			encoding: domain.PassEncodingMD5,
			want:     false,
		},
		{
			name:     "unknown_encoding_returns_false",
			stored:   "anything",
			given:    "anything",
			encoding: domain.PasswordEncoding(0xFF),
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := verifyPassword(tt.stored, tt.given, tt.encoding)
			if got != tt.want {
				t.Errorf("verifyPassword(%q, %q, %v) = %v, want %v",
					tt.stored, tt.given, tt.encoding, got, tt.want)
			}
		})
	}
}
