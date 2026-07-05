// Package packet defines kRO packet structures, the packet database, and
// PACKETVER schema merge logic. This package has zero internal/ dependencies
// and is importable by external tooling (load testers, packet analyzers).
//
// # Packet database
//
// DB is a registry of packet definitions keyed by their 16-bit command ID.
// Each definition records the packet's fixed on-wire length, or signals
// that the on-wire length is variable and must be read from a uint16
// length prefix at byte offset 2 of the packet header.
//
// The variable-length convention matches rathena's PacketDatabase<>::handle
// dynamic branch at rathena/src/common/packets.hpp:720-737 and the
// map-server wire-format reader at rathena/src/map/clif.cpp:25749
// (RFIFOW(fd,2)).
//
// # Login-server packet DB
//
// NewLoginServerDB returns a database pre-populated with all known
// login-server packet definitions (both directions). Inbound (C→S)
// entries mirror rathena/src/login/loginclif.cpp:483-498 verbatim; outbound
// (S→C) entries are added as encoder-side reference.
//
// # Map-server packet codegen (deferred)
//
// Map-server packet DBs are PACKETVER-gated (~2500+ version snapshots,
// rathena/src/map/clif_packetdb.hpp + clif_shuffle.hpp) and must be
// generated, not hand-written. This is the responsibility of unit P1.2b:
// a go:generate program that parses the C++ headers and emits a Go source
// file for a chosen PACKETVER. See .agents/handoff/p1.0-scout-findings.md
// §1.4 for the strategy and §4 for the codegen plan.
package packet
