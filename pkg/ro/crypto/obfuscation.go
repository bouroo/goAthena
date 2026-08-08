package crypto

// Obfuscator is the per-connection deobfuscation state for kRO map-server
// packet IDs. The cipher is a 32-bit linear congruential generator (LCG) used
// to produce a 15-bit XOR mask over each packet's 16-bit command ID; the XOR
// is symmetric so the same primitive encodes outbound IDs (although rAthena's
// reference server only obfuscates inbound traffic).
//
// State semantics — verified against rathena/src/map/clif.cpp:
//
//   - Initial: after parsing the WantToConnection packet, two LCG steps are
//     applied starting from key0 (clif.cpp:10723).
//   - Per-packet decode: cmd ^= (state >> 16) & 0x7FFF using the CURRENT
//     state, then the state is advanced by one LCG step (clif.cpp:25707 +
//     25775).
//   - First packet (no session yet): the wantToConnection cmd is decoded
//     using key0 as the seed and exactly ONE LCG step (clif.cpp:25713).
//   - Off-mode: when (key0,key1,key2) == (0,0,0) (PACKETVER > 20180307),
//     no XOR is applied and no state advance happens; the obfuscator is the
//     identity transform.
type Obfuscator struct {
	state      uint32
	multiplier uint32 // key1
	increment  uint32 // key2
	disabled   bool   // true when all three keys are zero
}

// NewObfuscator creates an Obfuscator with the session key initialized
// from the per-PACKETVER triplet (key0, key1, key2). The session state is
// the result of two LCG steps starting from key0 (clif.cpp:10723). When all
// three keys are zero, NewObfuscator returns a disabled Obfuscator whose
// Decode/Encode are the identity transform and never advance state.
func NewObfuscator(key0, key1, key2 uint32) *Obfuscator {
	if key0 == 0 && key1 == 0 && key2 == 0 {
		return &Obfuscator{disabled: true}
	}

	o := &Obfuscator{
		multiplier: key1,
		increment:  key2,
	}
	o.state = lcgStep(key0, o.multiplier, o.increment)
	o.state = lcgStep(o.state, o.multiplier, o.increment)
	return o
}

// Disabled reports whether the obfuscator is the identity transform. This is
// true when the constructor was called with (0,0,0) — i.e. PACKETVER after
// 20180307.
func (o *Obfuscator) Disabled() bool {
	return o.disabled
}

// Decode deobfuscates a raw 16-bit packet command using the current state
// then advances the LCG by one step. When the obfuscator is disabled,
// Decode returns raw unchanged and does not advance state.
func (o *Obfuscator) Decode(raw uint16) uint16 {
	return o.transform(raw)
}

// Encode obfuscates a 16-bit packet command (XOR is symmetric with Decode).
// It uses the same mask and advances state identically to Decode. Although
// rAthena does not obfuscate outbound traffic, Encode is provided so callers
// that want symmetric behavior — for example test fixtures or future
// gateway→zone relaying — can produce the matching ciphertext.
func (o *Obfuscator) Encode(raw uint16) uint16 {
	return o.transform(raw)
}

func (o *Obfuscator) transform(raw uint16) uint16 {
	if o.disabled {
		return raw
	}

	masked := raw ^ uint16((o.state>>16)&0x7FFF)
	o.state = lcgStep(o.state, o.multiplier, o.increment)
	return masked
}

// FirstPacketDecode deobfuscates the first map-server packet
// (WantToConnection) before a session exists. Per clif.cpp:25713, the mask
// is derived from exactly one LCG step seeded with key0:
//
//	cmd = raw ^ (((key0*key1 + key2) >> 16) & 0x7FFF)
//
// When all three keys are zero (off-mode), the call returns raw unchanged.
func FirstPacketDecode(key0, key1, key2 uint32, raw uint16) uint16 {
	if key0 == 0 && key1 == 0 && key2 == 0 {
		return raw
	}

	state := lcgStep(key0, key1, key2)
	return raw ^ uint16((state>>16)&0x7FFF)
}

// lcgStep performs one iteration of the 32-bit LCG used for packet ID
// obfuscation: state' = state*multiplier + increment (mod 2^32). Unsigned
// 32-bit arithmetic in Go naturally wraps modulo 2^32, so no explicit mask
// is required; the & 0xFFFFFFFF steps in rAthena are redundant on a system
// where uint32 has well-defined wrap-around semantics.
func lcgStep(state, multiplier, increment uint32) uint32 {
	return state*multiplier + increment
}

// KeysForVersion returns the (key0, key1, key2) triplet for a given PACKETVER
// date (e.g. 20130807). The table is generated at build time from
// rathena/src/map/clif_obfuscation.hpp; a version exactly matching the
// PACKETVER macro value emits its triplet, anything strictly greater than
// obfuscationCutoff returns (0,0,0) (kRO dropped obfuscation after this
// date), and unknown versions fail-closed to (0,0,0).
func KeysForVersion(packetver int) (key0, key1, key2 uint32) {
	if packetver > obfuscationCutoff {
		return 0, 0, 0
	}

	keys, ok := obfuscationKeys[packetver]
	if !ok {
		return 0, 0, 0
	}
	return keys[0], keys[1], keys[2]
}
