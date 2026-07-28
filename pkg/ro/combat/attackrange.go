package combat

// MeleeRange is the maximum Chebyshev distance at which a bare-handed PC may
// strike a mob. The client pathfinds adjacent to the target before issuing
// CZ_ACTION_REQUEST, so this is a guard against out-of-range attacks rather than
// the authoritative reach — rAthena derives reach from weapon and size, which
// the bare-handed slice does not model.
const MeleeRange = 1

// InMeleeRange reports whether cell (ax, ay) is within MeleeRange (Chebyshev
// distance) of (bx, by). It is a pure geometry check; the caller owns the
// (locked) position reads it feeds in.
func InMeleeRange(ax, ay, bx, by int) bool {
	dx := abs(ax - bx)
	dy := abs(ay - by)
	if dx > dy {
		return dx <= MeleeRange
	}
	return dy <= MeleeRange
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
