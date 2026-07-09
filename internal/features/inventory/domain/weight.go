package domain

// Max-weight math for the pre-renewal (pre-re) weight capacity formula
// (rAthena src/map/status.cpp:3663).
//
//	goAthena does not yet load job_db, so every character is treated as
//	a Novice for the duration of Phase 2A. Once job_db lands, swap the
//	constant for a per-character lookup keyed off the rAthena job id.

// NoviceMaxWeightBase is rAthena's job_db max_weight_base for Novice
// and any other class that has not yet been registered in job_db
// (rAthena src/map/pc.cpp:13864). Pre-renewal default = 20000.
const NoviceMaxWeightBase uint32 = 20000

// StrWeightStep is the per-STR carry-weight increment in pre-re:
// base_weight + str*StrWeightStep (rAthena status.cpp:3663).
const StrWeightStep uint32 = 300

// MaxWeight returns the pre-renewal max carry weight for a character
// with the given job base weight and STR: base + str*300. Wrapping is
// not possible for the legitimate input range: even the largest legal
// STR (uint16 max = 65535) yields 65535*300 = ~19.6M, which still fits
// in uint32 alongside the 20000 base.
func MaxWeight(jobMaxWeightBase uint32, str uint16) uint32 {
	return jobMaxWeightBase + uint32(str)*StrWeightStep
}

// ItemWeightLookup resolves the item_db weight for a nameid. Until
// item_db.yml loading lands, the production implementation
// (ZeroItemWeight) returns 0 for every item, which keeps the weight
// gate trivially satisfied and lets acquire paths land without a hard
// dependency on the itemdb parser.
//
// Once the asset loader is wired, swap ZeroItemWeight for a YAML-backed
// implementation — the interface stays the same and the service-layer
// checkWeight keeps working unchanged.
type ItemWeightLookup interface {
	// Weight returns the per-unit weight for nameID in rAthena
	// `item_db.yml` `Weight` units. Zero is a valid value (weightless
	// items such as consumable drops). Unknown nameids return 0 by
	// convention — callers MUST treat 0 as "no data" and not as
	// "weightless", since the gate is short-circuited when every item
	// reports 0 weight.
	Weight(nameID uint32) uint32
}

// ZeroItemWeight is the production-default ItemWeightLookup. It reports
// 0 for every item until item_db.yml loading lands, which makes every
// acquisition pass the capacity gate. Documented as a deliberate
// placeholder, not a bug: the gate is real and tested, and the lookup
// seam is the only contract acquisition code depends on.
type ZeroItemWeight struct{}

// Weight returns 0 for every nameID. Implementation cost is zero; the
// method exists to satisfy ItemWeightLookup so it can be wired into DI
// without nil checks downstream.
func (ZeroItemWeight) Weight(_ uint32) uint32 { return 0 }
