// Package mode names the two Ragnarok Online game-balance modes a server runs.
//
// In rAthena, Renewal is a compile-time axis (#ifdef RENEWAL) that is INDEPENDENT
// of PACKETVER: a server may be Renewal at any packet date, and pre-Renewal at
// any packet date. The goAthena Thai fork (rathenaThailand) is Renewal-ON at
// PACKETVER 20250604; goAthena is pre-Renewal only because zone.renewal is false,
// not because of the packet version. Carrying the mode as its own type through
// the kernel lets formula registries and data loaders select the correct branch
// without re-deriving it from the packetver (the two must never be conflated).
package mode

// Mode selects a game-balance mode.
type Mode int8

const (
	// PreRenewal is the classic (pre-2010) balance mode: the #else branches of
	// the rAthena status formulas.
	PreRenewal Mode = iota
	// Renewal is the post-2010 balance mode: the #ifdef RENEWAL branches.
	Renewal
)

// String reports the mode name for logs and config dumps.
func (m Mode) String() string {
	switch m {
	case PreRenewal:
		return "pre-renewal"
	case Renewal:
		return "renewal"
	default:
		return "unknown"
	}
}
