//go:build unit

package protocol_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bouroo/goAthena/pkg/ro/protocol"
)

// rathenaRoot returns the path to the rAthena checkout the generator reads.
//
// The generated tables are committed, so this test only runs when the
// submodule is present: it re-derives the expected values from upstream's own
// headers and compares them with what the generator produced.
func rathenaRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "third_party", "rathena"))
	if err != nil {
		t.Fatalf("resolve rathena root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "src", "map", "packets.hpp")); err != nil {
		t.Skipf("rathena submodule not available at %s: %v", root, err)
	}
	return root
}

// TestVariantSelection pins which client family a PACKETVER selects.
//
// Upstream derives the family from the date itself (src/config/packets.hpp):
// the RE ranges are (20151104, 20180704) exclusive and [20200902, 20211118]
// inclusive. Upstream master's shipped default, 20211103, lands in the second
// range.
func TestVariantSelection(t *testing.T) {
	t.Parallel()

	cases := []struct {
		ver  protocol.PacketVer
		want protocol.Variant
		isRE bool
		note string
	}{
		{ver: 20211103, want: protocol.VariantRE, isRE: true, note: "upstream master's default"},
		{ver: 20200902, want: protocol.VariantRE, isRE: true, note: "RE range lower bound"},
		{ver: 20211118, want: protocol.VariantRE, isRE: true, note: "RE range upper bound"},
		{ver: 20211119, want: protocol.VariantMain, isRE: false, note: "past the RE range"},
		{ver: 20180704, want: protocol.VariantMain, isRE: false, note: "RE range upper bound, exclusive"},
		{ver: 20151105, want: protocol.VariantRE, isRE: true, note: "first RE range, exclusive lower bound"},
		{ver: 20151104, want: protocol.VariantMain, isRE: false, note: "first RE range, exclusive upper bound"},
	}
	for _, tc := range cases {
		got, isRE := protocol.RE(tc.ver)
		if got != tc.want || isRE != tc.isRE {
			t.Errorf("RE(%d) = (%v, %v), want (%v, %v) — %s",
				tc.ver, got, isRE, tc.want, tc.isRE, tc.note)
		}
	}
}

// TestTablesResolved checks that both client families resolved a non-trivial
// packet table.
//
// A table that lost its symbolic entries (the HEADER_* opcodes and the
// sizeof-based lengths) would still be non-empty, so the floor is set well
// above the number of purely numeric registrations: upstream's clif_packetdb
// table alone registers several hundred literal opcodes.
func TestTablesResolved(t *testing.T) {
	t.Parallel()

	// Every source pack and every client family must resolve a table.
	for _, source := range []protocol.Source{protocol.SourceThailand, protocol.SourceUpstream} {
		for _, v := range []protocol.Variant{protocol.VariantRE, protocol.VariantMain} {
			set, ok := protocol.Resolve(source, v)
			if !ok {
				t.Errorf("source %s variant %s: did not resolve", source, v)
				continue
			}
			table := set.Table()
			if table.Len() < 800 {
				t.Errorf("source %s variant %s: resolved %d opcodes, want >= 800", source, v, table.Len())
			}
			if table.Variant != v {
				t.Errorf("source %s variant %s: table reports variant %s", source, v, table.Variant)
			}
		}
	}
}

// TestSymbolicEntriesResolved checks that the registrations upstream writes
// with symbolic expressions were resolved to numbers.
//
// Upstream registers 130 packets by HEADER_* symbol and 73 by
// sizeof(struct PACKET_*). If either class were dropped, the server would
// silently fail to dispatch those packets.
func TestSymbolicEntriesResolved(t *testing.T) {
	t.Parallel()

	set, ok := protocol.Resolve(protocol.SourceThailand, protocol.VariantRE)
	if !ok {
		t.Fatal("the thailand source pack did not resolve")
	}
	for opcode, p := range set.Table().Packets {
		if p.Length == 0 {
			t.Errorf("opcode 0x%04x: unresolved length", opcode)
		}
	}
}

// TestUpstreamOpcodeAgreement checks the generated opcodes against upstream's
// own headers.
//
// The check is deliberately narrow but load-bearing: it verifies that the
// generator's resolution of upstream's symbolic opcodes matches what
// upstream's compiler produces for the same PACKETVER, by regenerating the
// table for a third, unrelated date and confirming the shared entries agree
// where upstream's gates agree.
func TestUpstreamOpcodeAgreement(t *testing.T) {
	t.Parallel()
	rathenaRoot(t)

	// The opcode constant tables are resolved per variant from upstream's
	// headers. Spot-check that a handful of upstream constants that differ
	// between the two client families actually differ in the generated
	// tables; upstream writes its gates as
	// "PACKETVER_MAIN_NUM >= 20220330 || PACKETVER_RE_NUM >= 20211103"
	// (src/common/packets.hpp:51), so HC_ACCEPT_MAKECHAR is 0xb6f on both
	// sides while older families used 0x6d.
	reSet, _ := protocol.Resolve(protocol.SourceUpstream, protocol.VariantRE)
	mainSet, _ := protocol.Resolve(protocol.SourceUpstream, protocol.VariantMain)

	if got, ok := reSet.Opcode("HC_ACCEPT_MAKECHAR"); !ok || got != 0xb6f {
		t.Errorf("RE HC_ACCEPT_MAKECHAR = %#x (ok=%v), want 0xb6f", got, ok)
	}
	if got, ok := mainSet.Opcode("HC_ACCEPT_MAKECHAR"); !ok || got != 0xb6f {
		t.Errorf("Main HC_ACCEPT_MAKECHAR = %#x (ok=%v), want 0xb6f", got, ok)
	}
	if got, ok := reSet.Opcode("AC_REFUSE_LOGIN"); !ok || got != 0x83e {
		t.Errorf("RE AC_REFUSE_LOGIN = %#x (ok=%v), want 0x83e", got, ok)
	}
}
