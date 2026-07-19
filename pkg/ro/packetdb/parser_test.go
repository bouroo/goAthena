//go:build unit

package packetdb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvalPredicate_DirectComparison(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		predicate string
		version   int
		want      bool
	}{
		{"gte-equal", "PACKETVER >= 20040705", 20040705, true},
		{"gte-above", "PACKETVER >= 20040705", 20040706, true},
		{"gte-below", "PACKETVER >= 20040705", 20040704, false},
		{"main-num", "PACKETVER_MAIN_NUM >= 20230802", 20230802, true},
		{"main-num-above", "PACKETVER_MAIN_NUM >= 20230802", 20240000, true},
		{"defined-zero", "defined(PACKETVER_ZERO)", 20250604, true},
		{"defined-far-version", "defined(PACKETVER_ZERO)", 19990000, true},
		{"or-disjunction-below-first", "PACKETVER_MAIN_NUM >= 20200916 || PACKETVER_RE_NUM >= 20200724", 20200801, true},
		{"or-disjunction-above-both", "PACKETVER_MAIN_NUM >= 20200916 || PACKETVER_RE_NUM >= 20200724", 20210101, true},
		{"or-disjunction-below-both", "PACKETVER_MAIN_NUM >= 20300101 || PACKETVER_RE_NUM >= 20300101", 20250604, false},
		{"three-way-or-with-defined", "PACKETVER_MAIN_NUM >= 20100817 || PACKETVER_RE_NUM >= 20100706 || defined(PACKETVER_ZERO)", 20250604, true},
		{"line-1303-typo", "PACKETVER >= 2009122", 2009122, true},
		{"line-1303-typo-far-above", "PACKETVER >= 2009122", 20250604, true},
		{"line-1303-typo-far-below", "PACKETVER >= 2009122", 2009121, false},
		{"whitespace-tolerated", "  PACKETVER   >=   20040705  ", 20040705, true},
		{"unknown-form-fails-closed", "PACKETVER == 20040705", 20040705, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := EvalPredicate(tc.predicate, tc.version)
			assert.Equal(t, tc.want, got, "predicate=%q version=%d", tc.predicate, tc.version)
		})
	}
}

func TestValidatePredicate(t *testing.T) {
	t.Parallel()
	require.NoError(t, ValidatePredicate("PACKETVER >= 20040705"))
	require.NoError(t, ValidatePredicate("PACKETVER_MAIN_NUM >= 20230802"))
	require.NoError(t, ValidatePredicate("defined(PACKETVER_ZERO)"))
	require.NoError(t, ValidatePredicate("A >= 1 || B >= 2 || defined(C)"))
	err := ValidatePredicate("PACKETVER == 20040705")
	assert.Error(t, err, "== should be unsupported")
	err = ValidatePredicate("")
	assert.Error(t, err)
}

func TestEvalPredicate_Negation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		predicate string
		version   int
		want      bool
	}{
		{"neg-gte-below", "!(PACKETVER >= 20040705)", 20040704, true},
		{"neg-gte-equal", "!(PACKETVER >= 20040705)", 20040705, false},
		{"neg-gte-above", "!(PACKETVER >= 20040705)", 20040706, false},
		{"neg-defined-zero", "!defined(PACKETVER_ZERO)", 20250604, false},
		{"neg-defined-far", "!defined(PACKETVER_ZERO)", 19990000, false},
		{"double-neg-gte", "!!(PACKETVER >= 20040705)", 20040705, true},
		{"neg-disjunction", "!(PACKETVER >= 20300101 || PACKETVER_RE_NUM >= 20300101)", 20250604, true},
		{"neg-disjunction-met", "!(PACKETVER >= 20300101 || PACKETVER_RE_NUM >= 20300101)", 20990101, false},
		{"neg-paren-no-space", "!(PACKETVER >= 20040705)", 20040101, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := EvalPredicate(tc.predicate, tc.version)
			assert.Equal(t, tc.want, got, "predicate=%q version=%d", tc.predicate, tc.version)
		})
	}
}

func TestValidatePredicate_AcceptsNegationAndParens(t *testing.T) {
	t.Parallel()
	require.NoError(t, ValidatePredicate("!PACKETVER >= 20040705"))
	require.NoError(t, ValidatePredicate("!(PACKETVER >= 20040705)"))
	require.NoError(t, ValidatePredicate("!(PACKETVER_MAIN_NUM >= 20230802 || PACKETVER_RE_NUM >= 20230802)"))
	require.NoError(t, ValidatePredicate("!(defined(PACKETVER_ZERO))"))
	require.NoError(t, ValidatePredicate("!defined(PACKETVER_ZERO)"))
	require.NoError(t, ValidatePredicate("!!PACKETVER >= 20040705"))
	// Still rejects truly unsupported forms even when negated.
	assert.Error(t, ValidatePredicate("!(PACKETVER == 20040705)"), "negation of == is still unsupported")
	assert.Error(t, ValidatePredicate("!!(PACKETVER == 20040705)"))
}

func TestParse_DirectNumericForms(t *testing.T) {
	t.Parallel()
	src := `
		packet(0x0064,55);
		packet(0x0069,-1);
		parseable_packet(0x0072,19,clif_parse_X,2,6,10,14,18);
		parseable_packet(0x008c,-1,clif_parse_GlobalMessage,2,4);
	`
	entries, st, err := parseString(src)
	require.NoError(t, err)
	assert.Equal(t, 4, st.Entries)
	assert.Equal(t, 0, st.Symbolic)
	assert.Equal(t, 0, st.IfBlocks)
	require.Len(t, entries, 4)
	assert.Equal(t, uint16(0x0064), entries[0].ID)
	assert.Equal(t, "0x0064", entries[0].Name)
	assert.Equal(t, 55, entries[0].Length)
	assert.Equal(t, uint16(0x0069), entries[1].ID)
	assert.Equal(t, -1, entries[1].Length)
	assert.Equal(t, uint16(0x0072), entries[2].ID)
	assert.Equal(t, 19, entries[2].Length)
}

func TestParse_SymbolicEntriesSkipped(t *testing.T) {
	t.Parallel()
	src := `
		parseable_packet( HEADER_CZ_CONTACTNPC, sizeof( PACKET_CZ_CONTACTNPC ), clif_parse_NpcClicked, 0 );
		packet( inventorylistnormalType, -1 );
		packet( useItemAckType, sizeof( struct PACKET_ZC_USE_ITEM_ACK ) );
	`
	entries, st, err := parseString(src)
	require.NoError(t, err)
	assert.Equal(t, 0, st.Entries)
	assert.Equal(t, 3, st.Symbolic)
	assert.Empty(t, entries)
}

func TestParse_VersionGateSingleThreshold(t *testing.T) {
	t.Parallel()
	src := `
		packet(0x0064,55);
		#if PACKETVER >= 20040705
			packet(0x020e,24);
		#endif
	`
	entries, st, err := parseString(src)
	require.NoError(t, err)
	assert.Equal(t, 1, st.IfBlocks)
	require.Len(t, entries, 2)
	assert.Equal(t, uint16(0x0064), entries[0].ID)
	assert.Equal(t, "", entries[0].Predicate)
	assert.Equal(t, uint16(0x020e), entries[1].ID)
	assert.Equal(t, "PACKETVER >= 20040705", entries[1].Predicate)
	assert.Equal(t, 20040705, entries[1].MinVersion)
}

func TestParse_ElifSymbolicBranch(t *testing.T) {
	t.Parallel()
	src := `
		#if PACKETVER_MAIN_NUM >= 20120503 || PACKETVER_RE_NUM >= 20120502
			parseable_packet( HEADER_CZ_REQ_RANKING, sizeof( PACKET_CZ_REQ_RANKING ), clif_parse_ranklist, 0 );
		#elif PACKETVER >= 20041108
			parseable_packet(0x0072,26,clif_parse_WantToConnection,3,7,11,15,19,23);
		#endif
	`
	_, st, err := parseString(src)
	require.NoError(t, err)
	assert.Equal(t, 1, st.IfBlocks)
	assert.Equal(t, 1, st.Symbolic, "HEADER_ entry inside #if must be counted symbolic")
}

func TestParse_NestedBlocks(t *testing.T) {
	t.Parallel()
	src := `
		#if PACKETVER >= 20041108
			packet(0x0084,2);
			#if PACKETVER >= 20050328
				packet(0x0085,3);
			#endif
		#endif
	`
	entries, st, err := parseString(src)
	require.NoError(t, err)
	assert.Equal(t, 2, st.IfBlocks)
	require.Len(t, entries, 2)
	// The outer entry's predicate is the outer #if alone.
	assert.Equal(t, "PACKETVER >= 20041108", entries[0].Predicate)
	// The nested entry's predicate is the conjunction of both #if gates.
	assert.Equal(t, "PACKETVER >= 20041108 && PACKETVER >= 20050328", entries[1].Predicate)
}

func TestParse_LineCommentsStripped(t *testing.T) {
	t.Parallel()
	src := `
		// 2004-07-05aSakexe
		#if PACKETVER >= 20040705
			parseable_packet(0x0072,22,clif_parse_WantToConnection,5,9,13,17,21); // comment
		#endif
	`
	entries, _, err := parseString(src)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, uint16(0x0072), entries[0].ID)
	assert.Equal(t, 22, entries[0].Length)
}

func TestParse_MalformedHex(t *testing.T) {
	t.Parallel()
	// Regex requires 0xNNNN. A bare decimal would be classified as a
	// packet-shaped line and reported symbolic with no entry — the
	// parser must not panic.
	_, st, err := parseString("packet(123,4);")
	require.NoError(t, err)
	assert.Equal(t, 1, st.Symbolic)
}

func TestParse_IfndefPreambleIgnored(t *testing.T) {
	t.Parallel()
	// Real rAthena preamble uses #ifndef CLIF_PACKETDB_HPP / #define /
	// #endif at the top. The #ifndef guard must NOT count toward
	// IfBlocks.
	src := `
#ifndef CLIF_PACKETDB_HPP
#define CLIF_PACKETDB_HPP

	packet(0x0064,55);
	packet(0x0065,17);

#endif /* CLIF_PACKETDB_HPP */
	`
	entries, st, err := parseString(src)
	require.NoError(t, err)
	assert.Equal(t, 0, st.IfBlocks, "#ifndef preamble must not count")
	assert.Equal(t, 2, st.Entries)
	require.Len(t, entries, 2)
}

func TestParse_Line1303Typo(t *testing.T) {
	t.Parallel()
	src := `
#if PACKETVER >= 2009122
	packet(0x0802,18);
#endif
	`
	entries, _, err := parseString(src)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "PACKETVER >= 2009122", entries[0].Predicate)
}

func TestParse_UnclosedIfFails(t *testing.T) {
	t.Parallel()
	src := `
#if PACKETVER >= 20040705
	packet(0x020e,24);
	`
	_, _, err := parseString(src)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unclosed")
}

func TestParse_ElifWithoutIfFails(t *testing.T) {
	t.Parallel()
	src := `
		#elif PACKETVER >= 20040705
			packet(0x020e,24);
		#endif
	`
	_, _, err := parseString(src)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "#elif")
}

func TestParse_ElifElseNumericBranches(t *testing.T) {
	t.Parallel()
	// Numeric (non-symbolic) bodies in each branch so we can assert the
	// effective predicate string and the per-branch MinVersion. The
	// effective predicate for the #elif body must be
	// "!(<if-predicate>) && <elif-predicate>"; for the #else body it
	// must be "!(<if-predicate>) && !(<elif-predicate>) && true".
	src := `
		#if PACKETVER >= 20300101
			packet(0xAAAA,1);
		#elif PACKETVER >= 20041108
			packet(0xBBBB,2);
		#else
			packet(0xCCCC,3);
		#endif
	`
	entries, st, err := parseString(src)
	require.NoError(t, err)
	assert.Equal(t, 1, st.IfBlocks, "single #if block regardless of #elif/#else count")
	require.Len(t, entries, 3)

	// #if branch: no history, predicate is the raw #if head.
	assert.Equal(t, "PACKETVER >= 20300101", entries[0].Predicate)
	assert.Equal(t, 20300101, entries[0].MinVersion)

	// #elif branch: history = [raw #if head]; effective predicate is
	// the negated history AND the elif predicate.
	assert.Equal(t,
		"!(PACKETVER >= 20300101) && PACKETVER >= 20041108",
		entries[1].Predicate)
	assert.Equal(t, 20041108, entries[1].MinVersion)

	// #else branch: history = [raw #if head, raw elif head]; effective
	// predicate negates both and ANDs the always-true sentinel.
	assert.Equal(t,
		"!(PACKETVER >= 20300101) && !(PACKETVER >= 20041108) && true /*else*/",
		entries[2].Predicate)
}

func TestRegistry_ForPacketVer_ElifElseNumericSelection(t *testing.T) {
	t.Parallel()
	// Same source as TestParse_ElifElseNumericBranches; this test
	// drives PacketRegistry.ForPacketVer to confirm the flattener picks
	// the right branch in each PACKETVER region.
	src := `
		#if PACKETVER >= 20300101
			packet(0xAAAA,1);
		#elif PACKETVER >= 20041108
			packet(0xBBBB,2);
		#else
			packet(0xCCCC,3);
		#endif
	`
	entries, st, err := parseString(src)
	require.NoError(t, err)
	reg := NewRegistry(entries, st)

	// Far future: #if branch selected.
	db := reg.ForPacketVer(20990101)
	_, ok := db.Lookup(0xAAAA)
	assert.True(t, ok, "future PACKETVER should select the #if branch")
	_, ok = db.Lookup(0xBBBB)
	assert.False(t, ok)
	_, ok = db.Lookup(0xCCCC)
	assert.False(t, ok)

	// Mid range: #elif branch selected (and only that one).
	db = reg.ForPacketVer(20041108)
	_, ok = db.Lookup(0xAAAA)
	assert.False(t, ok, "mid PACKETVER must not select the #if branch")
	_, ok = db.Lookup(0xBBBB)
	assert.True(t, ok, "mid PACKETVER should select the #elif branch")
	_, ok = db.Lookup(0xCCCC)
	assert.False(t, ok, "mid PACKETVER must not select the #else branch")

	// Old range: only #else branch selected.
	db = reg.ForPacketVer(20000101)
	_, ok = db.Lookup(0xAAAA)
	assert.False(t, ok)
	_, ok = db.Lookup(0xBBBB)
	assert.False(t, ok)
	_, ok = db.Lookup(0xCCCC)
	assert.True(t, ok, "old PACKETVER should select the #else branch")
}
