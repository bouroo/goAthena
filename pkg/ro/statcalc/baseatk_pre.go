package statcalc

// BaseATK is the Pre-Renewal left-side (base) physical attack for a non-bow PC:
//
//	str + (str/10)² + dex/5 + luk/5
//
// Source: status.cpp status_base_atk, BL_PC non-RENEWAL branch (the bow path is
// not used — the slice has no bows). This free function is the Pre-Renewal
// contract the hand-computed test vectors bind to directly; preRenewalSet.BaseATK
// delegates to it.
func BaseATK(b Base) int32 {
	s := int32(b.Str)
	dstr := s / 10
	return s + dstr*dstr + int32(b.Dex)/5 + int32(b.Luk)/5
}
