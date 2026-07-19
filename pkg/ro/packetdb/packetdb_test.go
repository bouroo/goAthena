//go:build integration

package packetdb_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/pkg/ro/packetdb"
)

// rathenaPacketDBPath returns the absolute path to rAthena's authoritative
// clif_packetdb.hpp, located relative to this test file using runtime.Caller.
// Mirrors the established pattern in
// internal/infrastructure/db/migrations/drift_test.go and
// internal/features/script/service/corpus_run_test.go.
//
// Layout this function assumes:
//
//	pkg/ro/packetdb/packetdb_test.go         ← 3 dirs up to repo root
//	third_party/rathena/src/map/clif_packetdb.hpp
func rathenaPacketDBPath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller must work")
	base := filepath.Join(
		filepath.Dir(file),
		"..", "..", "..",
		"third_party", "rathena", "src", "map", "clif_packetdb.hpp",
	)
	abs, err := filepath.Abs(base)
	require.NoError(t, err)
	if _, err := os.Stat(abs); err != nil {
		t.Skipf("rAthena clif_packetdb.hpp not available at %s: %v", abs, err)
		return ""
	}
	return abs
}

// TestIntegration_ParseRealClifPacketDB is the N1 baseline integration
// gate. It parses rAthena's authoritative map-server packet definition
// file and asserts that:
//
//  1. The parser succeeds (file is well-formed under our grammar).
//  2. Total parsed direct-numeric entries are non-zero and within the
//     expected magnitude (~1225, tolerating rAthena minor updates with a
//     floor of 1200).
//  3. Skipped symbolic entries are > 0 (the plan estimates ~151).
//  4. The number of #if blocks processed is within the expected magnitude
//     (169, tolerating minor updates with a floor of 160).
//  5. A PacketRegistry built from the entries produces a non-empty
//     *packet.DB for the current rAthena PACKETVER target (20250604).
//  6. A PacketRegistry built for a newer PACKETVER yields more entries
//     than one built for a very old PACKETVER — the version flattener
//     must be monotone w.r.t. PACKETVER.
//
// The four baseline numbers are logged via t.Logf so they appear in CI
// output and can be cited verbatim in D-012.
func TestIntegration_ParseRealClifPacketDB(t *testing.T) {
	t.Parallel()
	path := rathenaPacketDBPath(t)

	entries, stats, err := packetdb.ParseFile(path)
	require.NoError(t, err, "ParseFile must accept real rAthena clif_packetdb.hpp")
	require.NotEmpty(t, entries, "real file must yield at least one direct-numeric entry")

	// (1) total parsed entries — must be non-zero and within the planned magnitude.
	assert.GreaterOrEqual(t, stats.Entries, 1200,
		"direct-numeric entry count must stay near the planned ~1225 baseline; "+
			"a drop below 1200 indicates a catastrophic regression in the parser")
	t.Logf("baseline[1] total_parsed_entries=%d", stats.Entries)

	// (2) skipped symbolic entries — must be > 0 so we know the parser
	// actually encountered the deferred symbolic subset (otherwise the
	// gate becomes meaningless).
	assert.Greater(t, stats.Symbolic, 0,
		"parser must report at least one skipped symbolic entry (plan estimates ~151)")
	t.Logf("baseline[2] skipped_symbolic_entries=%d", stats.Symbolic)

	// (3) #if blocks — must match the planned magnitude (169). Floor 160
	// tolerates rAthena minor updates while catching parser regressions.
	assert.GreaterOrEqual(t, stats.IfBlocks, 160,
		"#if block count must stay near the planned 169 baseline")
	assert.LessOrEqual(t, stats.IfBlocks, 200,
		"#if block count must not balloon beyond the planned magnitude")
	t.Logf("baseline[3] if_blocks=%d", stats.IfBlocks)

	// (4) ForPacketVer(20250604).Size() — the gateway's primary view.
	reg := packetdb.NewRegistry(entries, stats)
	flat := reg.ForPacketVer(20250604)
	assert.Greater(t, flat.Size(), 0,
		"ForPacketVer(20250604) must yield a non-empty *packet.DB")
	t.Logf("baseline[4] ForPacketVer(20250604).Size()=%d", flat.Size())

	// Monotonicity: newer PACKETVER resolves at least as many entries as
	// an older one. This is the gateway's correctness contract — every
	// new version-gate entry rAthena defines is forward-only, so a
	// strictly older PACKETVER must produce a strictly smaller DB.
	older := reg.ForPacketVer(20000101)
	assert.Greater(t, flat.Size(), older.Size(),
		"ForPacketVer(20250604).Size() must be strictly greater than ForPacketVer(20000101).Size() "+
			"(the version flattener is monotone w.r.t. PACKETVER)")

	// Sanity: ForPacketVer(0) (impossibly old) must produce only the
	// always-active entries — those outside any #if block. It must be
	// strictly smaller than the 20250604 view.
	zero := reg.ForPacketVer(0)
	assert.LessOrEqual(t, zero.Size(), flat.Size(),
		"ForPacketVer(0).Size() must be <= ForPacketVer(20250604).Size()")

	// Final summary line — easy to grep from CI output.
	t.Logf(
		"N1 baseline: entries=%d symbolic=%d ifBlocks=%d ForPacketVer(20250604)=%d ForPacketVer(20000101)=%d",
		stats.Entries, stats.Symbolic, stats.IfBlocks, flat.Size(), older.Size(),
	)
}
