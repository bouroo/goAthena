package aoi

// Squeeze tiers: local 9-grid density → broadcast radius (in cells).
// The thresholds come from the project plan §5.2 and are intentionally
// aggressive — under War-of-Emperium-class congestion we shrink the
// viewport to keep the per-tick broadcast cost bounded.
const (
	// SqueezeNormalMax is the highest density that still uses the full
	// broadcast radius.
	SqueezeNormalMax = 100
	// SqueezeTier1Max caps the middle squeeze tier. Above this we drop
	// to the smallest tier.
	SqueezeTier1Max = 500

	// SqueezeRadiusNormal is the normal broadcast radius in cells.
	SqueezeRadiusNormal = 15
	// SqueezeRadiusTier1 is the radius used once the neighborhood exceeds
	// SqueezeNormalMax entities.
	SqueezeRadiusTier1 = 8
	// SqueezeRadiusTier2 is the radius used under extreme congestion.
	SqueezeRadiusTier2 = 5
)

// SqueezeRadius returns the effective broadcast radius for an AOI query
// given the local 9-grid entity count.
//
//	Tier   Density range            Radius
//	----   ---------------------    ------
//	0      N <= SqueezeNormalMax    15 cells (normal)
//	1      SqueezeNormalMax < N <=  SqueezeTier1Max  8 cells
//	2      N > SqueezeTier1Max      5 cells
//
// Negative counts are treated as zero density (normal radius).
func SqueezeRadius(entityCount9Grid int) int {
	switch {
	case entityCount9Grid <= SqueezeNormalMax:
		return SqueezeRadiusNormal
	case entityCount9Grid <= SqueezeTier1Max:
		return SqueezeRadiusTier1
	default:
		return SqueezeRadiusTier2
	}
}

// SqueezeTier classifies the density into one of the three named tiers.
// Useful for telemetry and debugging — game logic should branch on the
// returned radius directly.
type SqueezeTier int

const (
	// SqueezeTierNormal corresponds to N <= SqueezeNormalMax.
	SqueezeTierNormal SqueezeTier = iota
	// SqueezeTierReduced corresponds to SqueezeNormalMax < N <= SqueezeTier1Max.
	SqueezeTierReduced
	// SqueezeTierMinimal corresponds to N > SqueezeTier1Max.
	SqueezeTierMinimal
)

// String renders the tier for logging.
func (t SqueezeTier) String() string {
	switch t {
	case SqueezeTierNormal:
		return "normal"
	case SqueezeTierReduced:
		return "reduced"
	case SqueezeTierMinimal:
		return "minimal"
	default:
		return "unknown"
	}
}

// SqueezeTierOf returns the tier that SqueezeRadius would choose for the
// given local density.
func SqueezeTierOf(entityCount9Grid int) SqueezeTier {
	switch {
	case entityCount9Grid <= SqueezeNormalMax:
		return SqueezeTierNormal
	case entityCount9Grid <= SqueezeTier1Max:
		return SqueezeTierReduced
	default:
		return SqueezeTierMinimal
	}
}
