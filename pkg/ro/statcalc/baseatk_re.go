package statcalc

// baseATK_re is the Renewal left-side (base) physical attack for a PC, ported
// from status.cpp status_base_atk, BL_PC #ifdef RENEWAL branch (line ~2424):
//
//	(dstr*10 + dex*10/5 + luk*10/3 + level*10/4)/10 + 5*pow
//
// dstr is the melee strength (str for a non-bow PC; the bow path is unused).
// pow is a post-2020 Renewal trait not modeled in Base yet — it is 0 for a
// naked character, so the +5*pow term is omitted until the trait lands (M9+
// combat damage). Integer division and left-to-right precedence mirror C.
func (renewalSet) BaseATK(b Base) int32 {
	s := int32(b.Str)
	return (s*10 + int32(b.Dex)*10/5 + int32(b.Luk)*10/3 + int32(b.Level)*10/4) / 10
}
