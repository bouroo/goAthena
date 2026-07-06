//go:build unit

package nats

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSanitizeToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"plain", "abc", "abc"},
		{"uuidFragment", "abc12345", "abc12345"},
		{"dot", "a.b.c", "a_b_c"},
		{"wildcard", "a>b", "a_b"},
		{"mixed", "zone.eu>x", "zone_eu_x"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, sanitizeToken(tc.input))
		})
	}
}
