package domain

// BaseATK calculates the player's base attack based on pre-Renewal rules.
// Formula: str + (str/10)*(str/10) + dex/5 + luk/5.
// Source: rAthena src/map/status.cpp status_base_atk (pre-Renewal branch).
func BaseATK(str, dex, luk uint8) int32 {
	s := int32(str)
	d := int32(dex)
	l := int32(luk)

	// integer division as per rAthena
	return s + (s/10)*(s/10) + d/5 + l/5
}

// DamageBand represents the inclusive damage range [Min, Max]
// after defense reduction.
type DamageBand struct {
	Min, Max int32
}

// MeleeDamage calculates the melee damage band against a target with given DEF and VIT.
// Steps follow rAthena battle.cpp pre-Renewal defense reduction formulas.
func MeleeDamage(str, dex, luk uint8, mobDef, mobVit int) DamageBand {
	base := BaseATK(str, dex, luk)

	// Hard DEF reduction
	def := mobDef
	if def < 0 {
		def = 0
	} else if def > 100 {
		def = 100
	}
	afterHard := base * int32(100-def) / 100

	// VIT soft-DEF band
	vit := max(0, int32(mobVit)) //nolint:gosec // VIT value is within safe range for int32
	vfloor := (3*vit)/10 + vit/2
	spread := max(0, (vit*vit)/150-(3*vit)/10-1)

	minDmg := afterHard - (vfloor + spread)
	maxDmg := afterHard - vfloor

	// Floor each at 1
	if minDmg < 1 {
		minDmg = 1
	}
	if maxDmg < 1 {
		maxDmg = 1
	}
	if minDmg > maxDmg {
		minDmg = maxDmg
	}

	return DamageBand{Min: minDmg, Max: maxDmg}
}
