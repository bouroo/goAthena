package statcalc

// MatkMin / MatkMax are the Pre-Renewal soft magic-attack bounds:
//
//	min = int + (int/7)²
//	max = int + (int/5)²
//
// Source: status.cpp status_base_matk_min/max (no RENEWAL guard — same both).
func MatkMin(b Base) int32 {
	i := int32(b.Int)
	return i + (i/7)*(i/7)
}

// MatkMax is the high MATK bound. See MatkMin.
func MatkMax(b Base) int32 {
	i := int32(b.Int)
	return i + (i/5)*(i/5)
}

// Hit and Flee are the level + stat totals (non-RENEWAL adds nothing else):
//
//	Hit  = level + dex
//	Flee = level + agi
//
// Source: status.cpp status_calc_misc #else (HIT/FLEE), lines ~2704-2711.
func Hit(b Base) int32 { return int32(b.Level) + int32(b.Dex) }

// Flee is level + agi (non-RENEWAL). See Hit for the source.
func Flee(b Base) int32 { return int32(b.Level) + int32(b.Agi) }

// SoftDEF is the right-side (vit) defense; SoftMDEF is the right-side soft
// magic defense:
//
//	softDEF  = vit
//	softMDEF = int + vit/2
//
// Source: status.cpp status_calc_misc #else (Def2/Mdef2), lines ~2712-2719.
// The left-side (item) DEF/MDEF are equipment contributions (StatusInputs).
func SoftDEF(b Base) int32 { return int32(b.Vit) }

// SoftMDEF is the right-side soft magic defense, int + vit/2. See SoftDEF.
func SoftMDEF(b Base) int32 { return int32(b.Int) + int32(b.Vit)/2 }

// Critical and PerfectFlee return the *stored* values (before the /10 the wire
// applies). status_calc_misc computes (enable_critical / enable_perfect_flee
// both include BL_PC by default):
//
//	cri   = 10 + luk*10/3   (integer division; "every 1 luk = +0.3 crit")
//	flee2 = luk + 10        ("every 10 luk = +1 perfect flee")
//
// The ZC_STATUS wire fields carry these /10; see ZCStatus. Source: status.cpp
// status_calc_misc #else, lines ~2722-2740.
func Critical(b Base) int32 { return 10 + int32(b.Luk)*10/3 }

// PerfectFlee is the stored perfect-flee total, luk + 10. See Critical.
func PerfectFlee(b Base) int32 { return int32(b.Luk) + 10 }
