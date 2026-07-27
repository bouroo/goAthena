// Package packet defines kRO packet structures and the packet database used
// by the gateway codec to split a raw TCP byte stream into discrete packets.
//
// The database is a registry of packet definitions keyed by their 16-bit
// command ID. Each definition records the packet's fixed on-wire length, or
// signals that the on-wire length is variable and must be read from a
// uint16 length prefix at byte offset 2 of the packet header.
//
// Login-server packet definitions are hand-curated from rAthena's
// src/login/loginclif.cpp LoginPacketDatabase (P1.2a). Map-server packet
// codegen from rathena/src/map/clif_packetdb.hpp + clif_shuffle.hpp is
// deferred to P1.2b.
package packet

import "maps"

// Direction indicates whether a packet is client→server or server→client.
type Direction int

const (
	// DirectionClientToServer marks packets sent by the kRO client.
	DirectionClientToServer Direction = iota
	// DirectionServerToClient marks packets sent by a kRO server
	// (login, char, or map server).
	DirectionServerToClient
)

// VariableLength indicates a packet whose on-wire size is not fixed.
// The actual length is read from the uint16 at byte offset 2 of the packet
// header (the [cmd:2][length:2][payload...] layout used by modern kRO
// variable-length packets, e.g. AC_ACCEPT_LOGIN, AC_ACK_HASH, HC_ACCEPT_ENTER).
//
// See rathena/src/map/clif.cpp:25749 (RFIFOW(fd,2)) and
// rathena/src/common/packets.hpp (structs with int16 packetType +
// int16 packetLength + trailing flexible array).
const VariableLength = -1

// Definition describes a single kRO packet type.
//
// All login-server definitions are stable across PACKETVER values: the
// fixed-length payload fields do not change with PACKETVER (login-server
// is a thin layer). Map-server definitions are version-gated and will be
// added by the P1.2b codegen.
type Definition struct {
	// ID is the packet command ID as it appears on the wire
	// (e.g. 0x0064 for CA_LOGIN).
	ID uint16
	// Name is the rAthena PACKET_* name without the "PACKET_" prefix
	// (e.g. "CA_LOGIN").
	Name string
	// Length is the fixed on-wire byte length, or VariableLength (-1)
	// when the packet length is read from the uint16 at offset 2.
	Length int
	// Direction is C→S (client → server) or S→C (server → client).
	Direction Direction
}

// DB is a registry of packet definitions keyed by command ID.
//
// The zero value is not usable; construct one with NewDB. DB is safe for
// concurrent reads after construction; mutating a DB after sharing it with
// other goroutines is not supported.
type DB struct {
	entries map[uint16]Definition
}

// NewDB creates an empty packet database.
func NewDB() *DB {
	return &DB{entries: make(map[uint16]Definition)}
}

// Register adds a packet definition to the database.
//
// Re-registering the same command ID overwrites the previous definition
// silently (matches rAthena's lenient PacketDatabase::add behavior at
// rathena/src/common/packets.hpp:664, which logs an error and returns).
// Use Size/Has to inspect state before relying on a registration.
func (db *DB) Register(d Definition) {
	db.entries[d.ID] = d
}

// Lookup returns the definition for a command ID. The second return value
// is false when the command is unknown.
func (db *DB) Lookup(cmd uint16) (Definition, bool) {
	d, ok := db.entries[cmd]
	return d, ok
}

// Length returns the fixed on-wire length for a command, or VariableLength
// (-1) when the packet is variable-length. Returns (0, false) for unknown
// commands — callers must check the bool before using the length.
func (db *DB) Length(cmd uint16) (int, bool) {
	d, ok := db.entries[cmd]
	if !ok {
		return 0, false
	}
	return d.Length, true
}

// Has reports whether a command ID is registered.
func (db *DB) Has(cmd uint16) bool {
	_, ok := db.entries[cmd]
	return ok
}

// Size returns the number of registered packet definitions.
func (db *DB) Size() int {
	return len(db.entries)
}

// Merge copies every definition from other into db. Already-present IDs are
// overwritten with the same leniency as Register (no panic, no duplicate
// insertion). Merge does not clear other, so callers may continue to use it
// after merging.
//
// This is how the gateway combines the login-server and char-server packet
// sets into one DB without forcing each subsystem to know the other's IDs.
//
// Merge is nil-safe: a nil other is a no-op, and a zero-value receiver is
// lazily initialized so external callers of the public package cannot panic
// the database with `&DB{}` or `db.Merge(nil)`.
func (db *DB) Merge(other *DB) {
	if other == nil {
		return
	}
	if db.entries == nil {
		db.entries = make(map[uint16]Definition, len(other.entries))
	}
	maps.Copy(db.entries, other.entries)
}
