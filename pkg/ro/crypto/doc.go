// Package crypto implements RO packet obfuscation used by the map server.
//
// The kRO map server obfuscates packet IDs using a 32-bit linear congruential
// generator (LCG) with per-PACKETVER keys. Login and char server traffic is
// NOT obfuscated. This package implements the deobfuscation primitives used
// by the gateway to decode incoming map-server packets.
//
// Key triplets are generated from rAthena's clif_obfuscation.hpp at build
// time (see cmd/genpacket). PACKETVER > 20180307 does not obfuscate. The
// header is sourced from the third_party/rathena git submodule (uninitialized
// by default; the committed obfuscation_keys.go is authoritative and go
// generate is a no-op when the submodule is absent).
//
// Reference: rathena/src/map/clif.cpp (clif_parse, ~line 25700-25780),
// rathena/src/map/clif_obfuscation.hpp.
//
//go:generate sh ../../../scripts/gen-obfuscation-keys.sh
package crypto
