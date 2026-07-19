package packetdb

import (
	"fmt"

	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// PacketRegistry holds the version-gated entries parsed from a single
// rAthena clif_packetdb.hpp. The registry is immutable after
// construction: parse once, then call ForPacketVer many times to obtain a
// *packet.DB for a target PACKETVER.
type PacketRegistry struct {
	entries []Entry
}

// NewRegistry builds a PacketRegistry from the entries returned by
// ParseFile. The stats are recorded so callers (and tests) can confirm
// the parse produced the expected counts.
func NewRegistry(entries []Entry, _ ParseStats) *PacketRegistry {
	// Defensive copy: callers may continue to mutate their slice.
	out := make([]Entry, len(entries))
	copy(out, entries)
	return &PacketRegistry{entries: out}
}

// Entries returns the parsed entries in source order. It exposes the
// version-gated internal list for tests and any future debug tooling;
// most callers want ForPacketVer.
func (r *PacketRegistry) Entries() []Entry {
	out := make([]Entry, len(r.entries))
	copy(out, r.entries)
	return out
}

// Size returns the number of version-gated entries held by the
// registry (i.e., the number of direct-numeric definitions parsed; not
// the flattened output for any particular PACKETVER).
func (r *PacketRegistry) Size() int {
	return len(r.entries)
}

// ForPacketVer returns a *packet.DB containing every entry whose
// version gate holds for the supplied PACKETVER.
//
// Entries outside any #if block (Predicate == "") are always included.
// Entries inside a block are included when EvalPredicate returns true
// for that entry's stored Predicate against the supplied version.
// When the same command ID would resolve to multiple entries (e.g.,
// the same packet redefined for newer PACKETVER ranges — the rAthena
// source has many such redefinitions), the LAST entry in source order
// wins, matching rAthena's lenient packetdb_addpacket behavior. The
// registry is built so this ordering is the natural one.
//
// Direction is not derivable from the source at this level (the rAthena
// file is client-bound: every entry is a CZ_* packet from the client's
// perspective). The gateway's login/char server packet DB carries the
// direction for its hand-curated set; the new registry is intended to
// be merged with that set for the directions the gateway supplies. We
// set DirectionClientToServer as the conservative default for the
// clif_packetdb.hpp subset.
func (r *PacketRegistry) ForPacketVer(version int) *packet.DB {
	db := packet.NewDB()
	for _, e := range r.entries {
		if e.Predicate != "" && !EvalPredicate(e.Predicate, version) {
			continue
		}
		db.Register(packet.Definition{
			ID:        e.ID,
			Name:      e.Name,
			Length:    e.Length,
			Direction: packet.DirectionClientToServer,
		})
	}
	return db
}

// String returns a human-readable summary of the registry, suitable for
// logs. It reports the entry count and the flattened size for PACKETVER
// 20250604 (the current rAthena release target).
func (r *PacketRegistry) String() string {
	return fmt.Sprintf("PacketRegistry{entries=%d, ForPacketVer(20250604)=%d}",
		r.Size(), r.ForPacketVer(20250604).Size())
}
