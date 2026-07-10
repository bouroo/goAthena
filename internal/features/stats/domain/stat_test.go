package domain

import "testing"

func TestStatCost(t *testing.T) {
	// Single-step cost = 1 + (val+9)/10 (pre-re formula).
	cases := []struct {
		current uint8
		want1   uint32
	}{
		{0, 1},   // 1 + (0+9)/10 = 1
		{1, 2},   // 1 + (1+9)/10 = 2
		{10, 2},  // 1 + 19/10 = 2
		{11, 3},  // 1 + 20/10 = 3
		{90, 10}, // 1 + 99/10 = 10
		{99, 11}, // 1 + 108/10 = 11
	}
	for _, c := range cases {
		if got := statCostStep(c.current); got != c.want1 {
			t.Errorf("statCostStep(%d) = %d, want %d", c.current, got, c.want1)
		}
	}
}

func TestStatCostMultiStep(t *testing.T) {
	// Raise from 0 by 5: steps 0,1,2,3,4 -> costs 1,2,2,2,2 = 9.
	//   step 0: 1+(0+9)/10 = 1+0 = 1 ; steps 1-4: 1+(n+9)/10 = 1+1 = 2.
	if got := StatCost(0, 5); got != 9 {
		t.Errorf("StatCost(0,5) = %d, want 9", got)
	}
	// Raise from 10 by 3: steps 10,11,12 -> 2+3+3 = 8.
	if got := StatCost(10, 3); got != 8 {
		t.Errorf("StatCost(10,3) = %d, want 8", got)
	}
	// Zero/negative increase is free.
	if got := StatCost(5, 0); got != 0 {
		t.Errorf("StatCost(5,0) = %d, want 0", got)
	}
	if got := StatCost(5, -1); got != 0 {
		t.Errorf("StatCost(5,-1) = %d, want 0", got)
	}
}

func TestStatCostMatchesRathenaNeed(t *testing.T) {
	// pc_need_status_point sums PC_STATUS_POINT_COST over [low, high).
	// Verify a known span: raising STR from 1 to 11 (10 steps).
	// Steps 1..10: costs 2,2,2,2,2,2,2,2,2,2 = 20 (each 1+(n+9)/10 for n=1..9 is 2; n=10 is 2).
	//   n=1..9 -> 1+(n+9)/10, n+9 in 10..18 -> /10 = 1 -> cost 2.
	//   n=10 -> 1+19/10 = 1+1 = 2.
	// Total 10*2 = 20.
	if got := StatCost(1, 10); got != 20 {
		t.Errorf("StatCost(1,10) = %d, want 20", got)
	}
}

func TestStatTypeValid(t *testing.T) {
	for _, s := range StatTypes {
		if !s.Valid() {
			t.Errorf("StatType %d should be valid", s)
		}
	}
	for _, bad := range []StatType{0, 1, 12, 19, 20, 100} {
		if bad.Valid() {
			t.Errorf("StatType %d should be invalid", bad)
		}
	}
}
