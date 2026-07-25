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

// ItemWeightLookup resolves item_db-derived profile facts for a nameid.
// It is broader than its name suggests: it began as the weight lookup the
// service-layer weight gate consumes, and gained IsStackable when the
// real-time AddItem path needed the rAthena stack-merge predicate. Both
// facts come from the same item_db row, so one lookup type is the right
// seam. Until item_db.yml loading lands, the production implementation
// (ZeroItemWeight) reports 0 weight and "always stackable" for every item,
// which keeps the weight gate trivially satisfied and lets the merge path
// proceed conservatively (a merge only succeeds when a matching plain
// stack already exists).
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

	// IsStackable reports whether nameID's item type stacks in rAthena's
	// inventory model (itemdb.cpp:4932 item_data::isStackable). Returns
	// false for Weapon/Armor/PetEgg/PetArmor/ShadowGear; true otherwise.
	// Unknown nameids return true — the merge only commits when a matching
	// plain stack already exists, so a false positive cannot corrupt a
	// distinct item.
	IsStackable(nameID uint32) bool
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

// IsStackable returns true for every nameID. With no itemdb loaded the
// merge path is permissive: it still only commits when a matching plain
// stack (same nameid + zero bound/expire/unique/cards) already exists, so
// the permissive default cannot merge two distinct items. A real itemdb
// narrows this to the rAthena type set.
func (ZeroItemWeight) IsStackable(_ uint32) bool { return true }
