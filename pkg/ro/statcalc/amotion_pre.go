package statcalc

// Amotion is the Pre-Renewal PC attack delay (ms-scaled) for a single equipped
// weapon:
//
//	amotion = weaponBaseASPD
//	amotion -= amotion * (4*agi + dex) / 1000
//
// (bAspd/shield adjustments are 0 for a naked character and omitted.) The wire
// ASPD field carries amotion directly; the client derives its display ASPD from
// it. weaponBaseASPD is the job's aspd_base[weapon] — Novice fist is 500 (db/
// pre-re/job_aspd.yml:95, BaseASPD.Fist). Source: status.cpp
// status_base_amotion_pc #else, lines 2404-2414.
func Amotion(b Base, weaponBaseASPD uint16) int32 {
	a := int32(weaponBaseASPD)
	return a - a*(4*int32(b.Agi)+int32(b.Dex))/1000
}
