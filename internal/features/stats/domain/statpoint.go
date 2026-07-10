package domain

// statPoints[i] is the cumulative total status points a character has
// accumulated by reaching base level i+1, assuming none were spent. It is
// the pre-Renewal statpoint table for levels 1..99.
//
// Source: rathena/db/pre-re/statpoint.yml (Points = "Total status points
// given from BaseLevel 1 to 'Level'"). The level-up grant for advancing
// from level L to L+1 is the difference statPoints[L]-statPoints[L-1],
// matching rAthena PlayerStatPointDatabase::pc_gets_status_point
// (rathena/src/map/pc.cpp:8755).
var statPoints = [...]uint32{
	48, 51, 54, 57, 60, 64, 68, 72, 76, 80,
	85, 90, 95, 100, 105, 111, 117, 123, 129, 135,
	142, 149, 156, 163, 170, 178, 186, 194, 202, 210,
	219, 228, 237, 246, 255, 265, 275, 285, 295, 305,
	316, 327, 338, 349, 360, 372, 384, 396, 408, 420,
	433, 446, 459, 472, 485, 499, 513, 527, 541, 555,
	570, 585, 600, 615, 630, 646, 662, 678, 694, 710,
	727, 744, 761, 778, 795, 813, 831, 849, 867, 885,
	904, 923, 942, 961, 980, 1000, 1020, 1040, 1060, 1080,
	1101, 1122, 1143, 1164, 1185, 1207, 1229, 1251, 1273,
}

// StatPointsTotal returns the cumulative status points granted by reaching
// the given base level (i.e. the value of statPoints[level-1]). Returns 0
// for level < 1 and the max-level total for level above the table.
func StatPointsTotal(level uint32) uint32 {
	if level < 1 {
		return 0
	}
	if int(level) > len(statPoints) {
		return statPoints[len(statPoints)-1]
	}
	return statPoints[level-1]
}

// StatusPointGrant returns the status points granted when a character
// advances from level to level+1. It is the table delta
// StatPointsTotal(level+1) - StatPointsTotal(level), matching rAthena's
// pc_gets_status_point(level) = table[level+1] - table[level]
// (rathena/src/map/pc.cpp:8755-8763).
func StatusPointGrant(level uint32) uint32 {
	return StatPointsTotal(level+1) - StatPointsTotal(level)
}
