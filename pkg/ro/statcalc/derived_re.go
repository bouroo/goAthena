package statcalc

// All formulas below are the Renewal branches ported from status.cpp
// status_calc_misc / status_base_matk_* #ifdef RENEWAL (BL_PC). The post-2020
// traits (spl, con) are not modeled in Base yet — they are 0 for a naked
// character, so their terms are omitted until the trait lands (M9+ combat).

// matkMin_re / matkMax_re are the Renewal soft magic-attack bounds. The two are
// equal for a PC (status_base_matk_min/max share one BL_PC expression, line
// ~2571/2590):
//
//	int + int/2 + dex/5 + luk/3 + level/4 + 5*spl
func (renewalSet) MatkMin(b Base) int32 {
	return matkRenewal(b)
}

func (renewalSet) MatkMax(b Base) int32 {
	return matkRenewal(b)
}

func matkRenewal(b Base) int32 {
	return int32(b.Int) + int32(b.Int)/2 + int32(b.Dex)/5 + int32(b.Luk)/3 + int32(b.Level)/4
}

// hit_re is the Renewal hit (status_calc_misc line ~2641):
//
//	level + dex + luk/3 + 175 + 2*con
func (renewalSet) Hit(b Base) int32 {
	return int32(b.Level) + int32(b.Dex) + int32(b.Luk)/3 + 175
}

// flee_re is the Renewal flee (line ~2646):
//
//	level + agi + luk/5 + 100 + 2*con
func (renewalSet) Flee(b Base) int32 {
	return int32(b.Level) + int32(b.Agi) + int32(b.Luk)/5 + 100
}

// softDEF_re is the Renewal right-side defense (line ~2654, BL_PC):
//
//	(level + vit)/2 + agi/5
func (renewalSet) SoftDEF(b Base) int32 {
	return (int32(b.Level)+int32(b.Vit))/2 + int32(b.Agi)/5
}

// softMDEF_re is the Renewal right-side soft magic defense (line ~2662, BL_PC):
//
//	int + level/4 + (dex + vit)/5
func (renewalSet) SoftMDEF(b Base) int32 {
	return int32(b.Int) + int32(b.Level)/4 + (int32(b.Dex)+int32(b.Vit))/5
}

// critical_re is the Renewal stored critical (line ~2726):
//
//	level/10 + 10 + luk*3
func (renewalSet) Critical(b Base) int32 {
	return int32(b.Level)/10 + 10 + int32(b.Luk)*3
}

// perfectFlee_re is the Renewal stored perfect flee (line ~2737): identical to
// pre-Renewal — luk + 10.
func (renewalSet) PerfectFlee(b Base) int32 {
	return int32(b.Luk) + 10
}
