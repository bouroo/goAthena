package app

// Rates are the server-side EXP and drop multipliers (percent; 100 = 1x), the
// goAthena equivalents of rAthena battle.conf base_exp_rate / item_rate_*. A
// zero field means "none" (no EXP gain / no drops) — a deliberate operator
// setting, not the default; config defaults both to 100.
type Rates struct {
	BaseExp int // applied to mob base EXP on kill (pc_gainexp * base_exp_rate/100)
	Drop    int // applied to every mob_db drop roll (item_rate_*/100), clamped at 100%
}

// DefaultRates returns the 1x multipliers (base EXP and drops at 100%): the
// rAthena battle.conf default and the goAthena config default. Tests and fixtures
// that do not exercise rate scaling pass this so a baseline kill awards full EXP
// and rolls drops at the raw mob_db Rate.
func DefaultRates() Rates { return Rates{BaseExp: 100, Drop: 100} }

// baseExpAward returns the base EXP a kill grants after the server multiplier.
// mobBaseExp is non-negative: CombatService.awardKill guards entry.BaseExp <= 0
// before calling, so the int32→uint64 conversion cannot sign-extend.
func (r Rates) baseExpAward(mobBaseExp int32) uint64 {
	return uint64(mobBaseExp) * uint64(r.BaseExp) / 100 //nolint:gosec // G115: caller guards BaseExp>0
}

// dropThreshold returns the per-myriad (1/10000) probability a drop entry with
// the raw mob_db Rate wins after the server drop multiplier, clamped at the
// denominator so a high rate guarantees the drop (rAthena's cap_value). A zero
// rate yields 0, which dropLoot treats as "never drops". Negative cannot occur
// (config validates min=0) but is clamped defensively.
func (r Rates) dropThreshold(rawRate int) int {
	threshold := int64(rawRate) * int64(r.Drop) / 100
	return int(max(0, min(threshold, dropRateDenominator))) //nolint:gosec // G115: clamped to [0,denominator]
}
