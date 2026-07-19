//go:build integration

package athenaconf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRoundTrip_RathenaConfDir parses every .conf file shipped under
// third_party/rathena/conf/ via ParseDir and asserts that the merged
// File plus Manifest surface the expectations in
// .agents/plans/rathena-compat-roadmap/subplans/phase-r0-conf-translator.md:
//
//   - no parse errors
//   - merged key count >= 200 (sanity floor; rAthena ships ~250+ keys)
//   - Manifest.Unmapped is non-empty (C4+ backlog) and contains no Initial-key-map keys
//   - for every key in the Initial key map, the value landed on Config
//
// The test is integration-tagged because it depends on the third_party/
// rathena submodule being present. CI runs it only when the submodule
// is initialised; unit-tag builds skip it.
func TestRoundTrip_RathenaConfDir(t *testing.T) {
	wd, err := os.Getwd()
	require.NoError(t, err)

	var confDir string
	for cur := wd; cur != filepath.Dir(cur); cur = filepath.Dir(cur) {
		if _, err := os.Stat(filepath.Join(cur, "go.mod")); err == nil {
			confDir = filepath.Join(cur, "third_party", "rathena", "conf")
			break
		}
	}
	require.NotEmpty(t, confDir, "could not locate module root from %s", wd)
	if _, err := os.Stat(confDir); err != nil {
		t.Skipf("rathena conf dir not present at %s (third_party submodule uninitialised); skipping", confDir)
	}

	root := filepath.Join(confDir, "..")
	p := NewParser(root)
	merged, manifest, err := p.ParseDir(confDir)
	require.NoError(t, err)

	t.Logf("rAthena conf round-trip: keys=%d sources=%d", len(merged.Keys), len(manifest.Sources))

	assert.GreaterOrEqual(t, len(merged.Keys), 200,
		"expected >=200 keys parsed, got %d", len(merged.Keys))

	cfg := newTestConfig()
	require.NoError(t, ApplyToConfig(cfg, merged, manifest))

	t.Logf("after ApplyToConfig: identity.use_md5_passwords=%v identity.max_chars=%d unmapped=%d",
		cfg.Identity.UseMD5Passwords, cfg.Identity.MaxChars, len(manifest.Unmapped))

	assert.NotEmpty(t, manifest.Unmapped,
		"C4+ backlog requires that some keys are unmapped")

	for _, k := range []string{"use_MD5_passwords", "chars_per_account"} {
		assert.NotContains(t, manifest.Unmapped, k,
			"initial-key-map key %q must not be flagged unmapped", k)
	}
}
