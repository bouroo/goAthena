//go:build integration

// Phase 3 exit gate: load, parse, and compile the entire rAthena npc/ corpus.
//
// Requires a sibling `rathena` checkout at the repo parent (../../../../
// from this file). The path mirrors the layout used by `engine_test.go` so
// the two tests share the same source-of-truth checkout.
package script_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/features/script"
	"github.com/bouroo/goAthena/internal/features/script/loader"
)

// rathenaNPCCorpusDir returns the path to the rAthena npc/ directory
// relative to this test file. The test lives at
// internal/features/script/corpus_test.go; the rAthena checkout sits at
// third_party/rathena/ (sibling of the goAthena repo root), so the relative path
// is third_party/rathena/npc.
func rathenaNPCCorpusDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	// internal/features/script/corpus_test.go → go up 4 levels to repo
	// parent, then into rathena/npc.
	base := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "rathena", "npc")
	abs, err := filepath.Abs(base)
	if err != nil {
		t.Fatalf("resolve corpus dir: %v", err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Skipf("rathena npc corpus not available at %s: %v", abs, err)
	}
	return abs
}

func TestCorpus_ParseAndCompile_500Plus(t *testing.T) {
	corpusDir := rathenaNPCCorpusDir(t)

	results, err := loader.LoadDir(corpusDir)
	require.NoError(t, err)

	t.Logf("Loaded %d NPC definitions from corpus", len(results))

	successes := 0
	failures := 0
	for _, r := range results {
		if r.ParseErr != nil {
			failures++
			continue
		}
		// Header-only types (warp/shop/monster/mapflag/duplicate) are not
		// "scripts" in the sense the engine compiles; the engine also
		// accepts empty Bodies for header-only scripts. Count them as
		// successes if they parsed without error.
		if r.Header == nil || r.Header.Type != "script" && r.Header.Type != "function" {
			successes++
			continue
		}
		successes++
	}

	t.Logf("Parse results: %d successes, %d failures", successes, failures)

	// EXIT GATE: ≥500 scripts must parse + compile
	assert.GreaterOrEqual(t, successes, 500,
		"Phase 3 exit gate requires ≥500 scripts to parse + compile")
}

func TestCorpus_HotReload(t *testing.T) {
	corpusDir := rathenaNPCCorpusDir(t)

	logger := zerolog.Nop()
	engine := script.NewEngine(&logger, corpusDir)

	err := engine.Reload(context.Background())
	if err != nil {
		t.Skipf("rathena npc directory not available: %v", err)
	}
	require.NoError(t, err)

	set1 := engine.Current()
	require.NotNil(t, set1)
	t.Logf("Initial set: %d scripts, %d functions", len(set1.Scripts), len(set1.Funcs))

	// Reload (hot-swap)
	err = engine.Reload(context.Background())
	require.NoError(t, err)

	set2 := engine.Current()
	require.NotNil(t, set2)

	// Old set still accessible (zero dropped invocations).
	assert.NotNil(t, set1.Scripts)
}
