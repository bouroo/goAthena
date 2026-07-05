// Package net provides the kRO packet stream codec shared between the
// gateway and zone services. It frames a raw TCP byte stream into discrete
// packets, applying packet-ID deobfuscation for map-server traffic and
// returning each packet's frame with the decoded command ID patched into
// the leading two bytes (matching rathena/src/map/clif.cpp:25773).
//
// The codec has two modes:
//
//   - Login mode (NewLoginDecoder): plaintext pass-through. No deobfuscation.
//     Used for login-server and char-server traffic — rAthena does not
//     obfuscate packets on those servers (rathena/src/login/loginclif.cpp).
//
//   - Map mode (NewMapDecoder): LCG packet-ID deobfuscation. The first
//     packet (WantToConnection) is decoded via crypto.FirstPacketDecode
//     with no session state (rathena/src/map/clif.cpp:25713). Subsequent
//     packets are decoded via a per-connection crypto.Obfuscator whose
//     state is initialized from two LCG steps after the first packet
//     (clif.cpp:10723) and advanced after every decode (clif.cpp:25775).
//     When the per-PACKETVER key triplet is (0,0,0) (post-2018-03-07
//     clients) the obfuscator is the identity transform.
//
// Buffer management: the decoder owns a single append-grow slice. Each
// Next shifts the consumed prefix off; the remaining bytes stay in place.
// No ring buffer is used — for Phase 1 throughput this avoids the complexity
// of a ring at the cost of an O(n) copy per packet. The transport layer
// must bound total buffered bytes (see conf/packet_athena.conf stall time
// in rAthena) to avoid unbounded growth from malicious peers.
package net
