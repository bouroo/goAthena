//go:build unit

package packetdb

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/pkg/ro/packet"
)

func TestRegistry_FromTestdata_BaselineShape(t *testing.T) {
	t.Parallel()
	path := filepath.Join("testdata", "clif_packetdb.hpp")
	entries, st, err := ParseFile(path)
	require.NoError(t, err)
	require.NotEmpty(t, entries, "fixture must yield at least one entry")

	reg := NewRegistry(entries, st)
	require.Equal(t, len(entries), reg.Size())

	// Verify the curated fixture exercises every grammar form. Numbers
	// below are derived from the eight entry lines we deliberately
	// included outside any #if.
	for _, id := range []uint16{0x0064, 0x0065, 0x0069, 0x006b, 0x0072, 0x008c} {
		assert.True(t, reg.Size() >= 1, "registry should carry entries")
		def, ok := reg.ForPacketVer(20040705).Lookup(id)
		require.True(t, ok, "fixture must include %04x", id)
		assert.NotZero(t, def.Length, "length must be non-zero")
	}
}

func TestRegistry_ForPacketVer_PredicateGating(t *testing.T) {
	t.Parallel()
	src := `
		packet(0x0064,55); // always
		#if PACKETVER >= 20040705
			packet(0x020e,24);
		#endif
	`
	entries, st, err := parseString(src)
	require.NoError(t, err)
	reg := NewRegistry(entries, st)

	// Below the gate: only the always-active entry.
	db := reg.ForPacketVer(20040101)
	_, ok := db.Lookup(0x0064)
	assert.True(t, ok, "always-active entry must be present")
	_, ok = db.Lookup(0x020e)
	assert.False(t, ok, "gated entry must be filtered out")

	// At and above the gate: both.
	db = reg.ForPacketVer(20040705)
	_, ok = db.Lookup(0x0064)
	assert.True(t, ok)
	_, ok = db.Lookup(0x020e)
	assert.True(t, ok, "entry at the exact PACKETVER threshold must be included")
}

func TestRegistry_ForPacketVer_ElifApplied(t *testing.T) {
	t.Parallel()
	// The flattener should treat #elif predicates as the active
	// branch when the #if head is false. With version=19900101, the
	// #if head is false and the #elif branch (>= 20041108) is also
	// false; with version=20041108, only the #elif branch is true.
	src := `
		#if PACKETVER >= 20300101
			packet(0xAAAA,1);
		#elif PACKETVER >= 20041108
			packet(0xBBBB,2);
		#endif
	`
	entries, st, err := parseString(src)
	require.NoError(t, err)
	reg := NewRegistry(entries, st)

	db := reg.ForPacketVer(20041108)
	_, ok := db.Lookup(0xAAAA)
	assert.False(t, ok)
	_, ok = db.Lookup(0xBBBB)
	assert.True(t, ok)
}

func TestRegistry_ForPacketVer_LaterOverride(t *testing.T) {
	t.Parallel()
	// rAthena redefines packets for newer PACKETVER ranges. The
	// gateway relies on the LAST entry in source order winning.
	src := `
		packet(0x0064,55);
		#if PACKETVER >= 20040705
			packet(0x0064,80);
		#endif
	`
	entries, st, err := parseString(src)
	require.NoError(t, err)
	reg := NewRegistry(entries, st)

	def, ok := reg.ForPacketVer(20040705).Lookup(0x0064)
	require.True(t, ok)
	assert.Equal(t, 80, def.Length, "later source-order definition should win")

	def, ok = reg.ForPacketVer(20040101).Lookup(0x0064)
	require.True(t, ok)
	assert.Equal(t, 55, def.Length, "earlier definition remains when gate is closed")
}

func TestRegistry_ForPacketVer_EmptyForVeryOldVersion(t *testing.T) {
	t.Parallel()
	src := `
		#if PACKETVER >= 20040705
			packet(0x020e,24);
		#endif
	`
	entries, st, err := parseString(src)
	require.NoError(t, err)
	reg := NewRegistry(entries, st)

	db := reg.ForPacketVer(20000101)
	assert.Equal(t, 0, db.Size())
}

func TestRegistry_VariableLengthPreserved(t *testing.T) {
	t.Parallel()
	src := `packet(0x0069,-1);`
	entries, st, err := parseString(src)
	require.NoError(t, err)
	reg := NewRegistry(entries, st)
	db := reg.ForPacketVer(20040101)
	length, ok := db.Length(0x0069)
	require.True(t, ok)
	assert.Equal(t, packet.VariableLength, length)
}

func TestRegistry_StringSummary(t *testing.T) {
	t.Parallel()
	src := `packet(0x0064,55);`
	entries, st, err := parseString(src)
	require.NoError(t, err)
	reg := NewRegistry(entries, st)
	s := reg.String()
	assert.Contains(t, s, "PacketRegistry")
	assert.Contains(t, s, "entries=1")
}
