// Package crypto implements RO packet obfuscation used by the map server.
//
// The kRO map server obfuscates packet IDs using a 32-bit linear congruential
// generator (LCG) with per-PACKETVER keys. Login and char server traffic is
// NOT obfuscated. This package implements the deobfuscation primitives used
// by the gateway to decode incoming map-server packets.
//
// Key triplets are generated from rAthena's clif_obfuscation.hpp at build
// time (see cmd/genpacket). PACKETVER > 20180307 does not obfuscate.
//
// Reference: rathena/src/map/clif.cpp (clif_parse, ~line 25700-25780),
// rathena/src/map/clif_obfuscation.hpp.
//
//go:generate go run ../../../cmd/genpacket -o obfuscation_keys.go
package crypto
