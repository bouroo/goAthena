//go:build integration

// Phase R0 S5 corpus conformance gate: run the real rAthena npc/ tree
// through parse + compile + VM execution and assert the harness
// classifies every script. The assertion is completeness (every script
// reaches a terminal state) rather than a fixed success-rate floor —
// today's success rate is the baseline that S6+ cites as a delta
// (decision D-009).
//
// Requires a sibling `rathena` checkout at the repo parent (../../../../
// from this file), same convention as internal/features/script/corpus_test.go.
package service_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/features/script"
	"github.com/bouroo/goAthena/internal/features/script/service"
)

// rathenaNPCCorpusDir mirrors corpus_test.go's path resolver so this
// test and the parse+compile test share the same source-of-truth
// checkout.
func rathenaNPCCorpusDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	base := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "..", "rathena", "npc")
	abs, err := filepath.Abs(base)
	if err != nil {
		t.Fatalf("resolve corpus dir: %v", err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Skipf("rathena npc corpus not available at %s: %v", abs, err)
	}
	return abs
}

type gapEntry struct {
	Name  string
	Count int
}

func topGaps(m map[string]int, n int) []gapEntry {
	out := make([]gapEntry, 0, len(m))
	for k, v := range m {
		out = append(out, gapEntry{Name: k, Count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func TestCorpus_ExecuteOnInit_Gate(t *testing.T) {
	corpusDir := rathenaNPCCorpusDir(t)

	logger := zerolog.Nop()
	engine := script.NewEngine(&logger, corpusDir)

	ctx := context.Background()
	set, err := engine.LoadAndCompile(ctx)
	require.NoError(t, err)
	require.NotNil(t, set)

	t.Logf("Compiled set: %d scripts, %d funcs", len(set.Scripts), len(set.Funcs))

	report := service.RunCorpus(ctx, set)
	require.NotNil(t, report)

	t.Logf("Total=%d Ran=%d Succeeded=%d Failed=%d Skipped=%d DurationMS=%d",
		report.Total, report.Ran, report.Succeeded, report.Failed, report.Skipped, report.DurationMS)

	// S5 exit gate: every script reaches a terminal state.
	assert.Greater(t, report.Total, 0, "corpus must contain at least one script")
	assert.Greater(t, report.Ran, 0, "at least one script must declare OnInit")
	assert.Equal(t, report.Succeeded+report.Failed+report.Skipped, report.Total,
		"S5 invariant: every script classified into exactly one bucket")
	assert.GreaterOrEqual(t, report.Succeeded, 1,
		"regression guard: at least one script must run cleanly")

	// Informational: top-N gap lists. These are the input for S6+
	// builtin work. Long today (engine has ~23/672 builtins); expected.
	for i, g := range topGaps(report.BuiltinGaps, 10) {
		t.Logf("builtin gap[%d]: %s = %d", i+1, g.Name, g.Count)
	}
	for i, g := range topGaps(report.FuncGaps, 5) {
		t.Logf("func gap[%d]: %s = %d", i+1, g.Name, g.Count)
	}
}
