package statcalc

import "github.com/bouroo/goAthena/pkg/ro/mode"

// FormulaSet is the set of derived-stat formulas for one game-balance mode.
// Bounded contexts receive a FormulaSet (resolved once at startup from a
// Registry) and never branch on the mode themselves; every mode-conditional
// formula lives behind these methods. Each is a pure function over Base,
// mirroring the matching rAthena status.cpp branch.
type FormulaSet interface {
	// BaseATK is the left-side (base) physical attack.
	BaseATK(b Base) int32
	// MatkMin / MatkMax are the soft magic-attack bounds.
	MatkMin(b Base) int32
	MatkMax(b Base) int32
	// Hit / Flee are the level + stat accuracy totals.
	Hit(b Base) int32
	Flee(b Base) int32
	// SoftDEF / SoftMDEF are the right-side (vit/int-derived) defenses.
	SoftDEF(b Base) int32
	SoftMDEF(b Base) int32
	// Critical / PerfectFlee return the stored (pre-/10) crit and perfect-flee.
	Critical(b Base) int32
	PerfectFlee(b Base) int32
	// Amotion is the PC attack delay (ms-scaled) for a single weapon.
	Amotion(b Base, weaponBaseASPD uint16) int32
}

// preRenewalSet implements FormulaSet with the classic (non-RENEWAL) branches.
// Its methods delegate to the package-level free functions, which are the
// pre-Renewal contract the hand-computed test vectors bind to directly.
type preRenewalSet struct{}

func (preRenewalSet) BaseATK(b Base) int32              { return BaseATK(b) }
func (preRenewalSet) MatkMin(b Base) int32              { return MatkMin(b) }
func (preRenewalSet) MatkMax(b Base) int32              { return MatkMax(b) }
func (preRenewalSet) Hit(b Base) int32                  { return Hit(b) }
func (preRenewalSet) Flee(b Base) int32                 { return Flee(b) }
func (preRenewalSet) SoftDEF(b Base) int32              { return SoftDEF(b) }
func (preRenewalSet) SoftMDEF(b Base) int32             { return SoftMDEF(b) }
func (preRenewalSet) Critical(b Base) int32             { return Critical(b) }
func (preRenewalSet) PerfectFlee(b Base) int32          { return PerfectFlee(b) }
func (preRenewalSet) Amotion(b Base, aspd uint16) int32 { return Amotion(b, aspd) }

// renewalSet implements FormulaSet with the #ifdef RENEWAL branches ported from
// rathenaThailand/src/map/status.cpp; see baseatk_re.go, derived_re.go,
// amotion_re.go for the per-formula sources and the trait-modeling caveat.
type renewalSet struct{}

// PreRenewalSet and RenewalSet are the singleton formula sets (immutable,
// stateless values). Tests and single-mode callers may reference them directly
// without building a Registry.
var (
	PreRenewalSet FormulaSet = preRenewalSet{}
	RenewalSet    FormulaSet = renewalSet{}
)

// Registry resolves a FormulaSet by mode. Build once at composition time from
// the zone.renewal flag and hand the resulting FormulaSet to every bounded
// context that derives stats. The two sets are stateless, so a Registry holds
// the same singletons every caller shares.
type Registry struct {
	sets [2]FormulaSet // indexed by mode.Mode
}

// NewRegistry builds a Registry holding both mode formula sets.
func NewRegistry() *Registry {
	return &Registry{sets: [2]FormulaSet{PreRenewalSet, RenewalSet}}
}

// Get returns the FormulaSet for m. An out-of-range mode falls back to
// PreRenewal (the safe default) rather than indexing out of bounds.
func (r *Registry) Get(m mode.Mode) FormulaSet {
	if m < 0 || int(m) >= len(r.sets) {
		return r.sets[mode.PreRenewal]
	}
	return r.sets[m]
}
