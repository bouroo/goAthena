package sizefix

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSubmodulePreRenewalSizeFix proves the loader parses the real
// rathenaThailand pre-re size_fix.yml. The YAML lists only weapons that deviate
// from the 100% default (pre-re: Knuckle and Whip); every other weapon/size
// resolves to 100. Skipped when the submodule is absent so it never breaks CI
// without the submodule.
func TestSubmodulePreRenewalSizeFix(t *testing.T) {
	path := filepath.Join("..", "..", "..", "third_party", "rathenaThailand", "db", "pre-re", "size_fix.yml")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("rathenaThailand submodule not available at %s: %v", path, err)
	}

	tbl, err := LoadFile(path)
	require.NoError(t, err)
	// Pre-re size_fix lists exactly the two weapons that deviate from 100%.
	require.Equal(t, 2, tbl.Len())

	// Knuckle: Small 100 (default), Medium 75, Large 50.
	assert.Equal(t, 100, tbl.Rate("Knuckle", "Small"))
	assert.Equal(t, 75, tbl.Rate("Knuckle", "Medium"))
	assert.Equal(t, 50, tbl.Rate("Knuckle", "Large"))
	// Whip: only Large deviates (50).
	assert.Equal(t, 100, tbl.Rate("Whip", "Small"))
	assert.Equal(t, 100, tbl.Rate("Whip", "Medium"))
	assert.Equal(t, 50, tbl.Rate("Whip", "Large"))
	// A weapon absent from the file defaults to 100 across every size.
	assert.Equal(t, 100, tbl.Rate("Dagger", "Small"))
	assert.Equal(t, 100, tbl.Rate("Dagger", "Medium"))
	assert.Equal(t, 100, tbl.Rate("Dagger", "Large"))
}
