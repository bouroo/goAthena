//go:build unit

package net

import "github.com/bouroo/goAthena/pkg/ro/crypto"

// firstPacketRawFromPlain returns the on-wire raw cmd for a kRO map-server
// first packet (WantToConnection) given the desired plaintext cmd.
//
// rathena/src/map/clif.cpp:25713: cmd_wire = cmd_plain ^ ((key0*key1 + key2) >> 16) & 0x7FFF
//
// Inverse of crypto.FirstPacketDecode. XOR is symmetric so we just run the
// same XOR — the operation has no plaintext/ciphertext direction.
func firstPacketRawFromPlain(key0, key1, key2 uint32, plain uint16) uint16 {
	return crypto.FirstPacketDecode(key0, key1, key2, plain)
}
