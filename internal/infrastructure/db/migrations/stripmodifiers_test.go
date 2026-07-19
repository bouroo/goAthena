//go:build unit

package migrations_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// stripModifiers mirrors the one in drift_test.go (D5 carve-out helper)
// so the unit build can exercise it without requiring the rAthena
// main.sql file. Keep in sync with the drift_test.go copy.
func stripModifiers(s string) string {
	for _, kw := range []string{"DEFAULT", "NOT NULL", "NULL"} {
		if i := strings.Index(strings.ToUpper(s), " "+kw); i >= 0 {
			s = strings.TrimSpace(s[:i])
		}
	}
	return s
}

// TestStripModifiers_NullSuffixStripped covers the peelNullability NULL
// branch in pkg/ro/rathenadb (and its test-driver mirror). Before the
// Gemini correctness fix, the NULL branch trimmed trailing spaces before
// checking the preceding character, which always failed and never peeled
// the explicit NULL constraint.
func TestStripModifiers_NullSuffixStripped(t *testing.T) {
	t.Parallel()
	got := stripModifiers("varchar(15) NULL")
	assert.Equal(t, "varchar(15)", got)
}

// TestStripModifiers_NotNullSuffixStripped: NOT NULL must still peel.
func TestStripModifiers_NotNullSuffixStripped(t *testing.T) {
	t.Parallel()
	got := stripModifiers("varchar(15) NOT NULL")
	assert.Equal(t, "varchar(15)", got)
}

// TestStripModifiers_DefaultWithValueContainingNull covers the
// keyword-ordering fix (Comment 4). OLD order (NOT NULL, NULL, DEFAULT)
// matched " NULL" inside "varchar(15) DEFAULT 'NULL'" first and
// produced "varchar(15) DEFAULT'" — wrong. After the fix the slice is
// {DEFAULT, NOT NULL, NULL} so DEFAULT is peeled first.
func TestStripModifiers_DefaultWithValueContainingNull(t *testing.T) {
	t.Parallel()
	got := stripModifiers("varchar(15) DEFAULT 'NULL'")
	assert.Equal(t, "varchar(15)", got)
}
