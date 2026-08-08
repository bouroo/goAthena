package statcalc

// amotion_re is intentionally a placeholder that reuses the Pre-Renewal attack
// delay. The true Renewal ASPD (status.cpp status_base_amotion_pc RENEWAL_ASPD
// branch, lines 2359-2399) is materially different — it depends on the job's
// per-weapon aspd_base table, shield/dual-wield detection, per-skill ASPD
// bonuses (SA_ADVANCEDBOOK, SG_DEVIL, GS_SINGLEACTION, riding), and the
// status-change ASPD modifier (status_calc_aspd) — none of which the kernel
// models yet (equipment lands in M10, skills/status-changes later). A faithful
// subset cannot be computed without those inputs, so Renewal reuses the classic
// percent-reduction delay here. TODO(M12): replace with the real RENEWAL_ASPD
// formula once aspd_base weapon tables and status-change evaluation exist.
func (renewalSet) Amotion(b Base, weaponBaseASPD uint16) int32 {
	return Amotion(b, weaponBaseASPD)
}
