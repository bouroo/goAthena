//go:build unit

package domain

import (
	"testing"
)

func TestBaseATK(t *testing.T) {
	cases := []struct {
		str, dex, luk uint8
		want          int32
	}{
		{0, 0, 0, 0},
		{10, 0, 0, 11},    // 10 + (10/10)^2 + 0 + 0 = 10 + 1 = 11
		{50, 0, 0, 75},    // 50 + (50/10)^2 + 0 + 0 = 50 + 25 = 75
		{99, 50, 20, 194}, // 99 + (99/10)^2 + 50/5 + 20/5 = 99 + 81 + 10 + 4 = 194
	}

	for _, c := range cases {
		if got := BaseATK(c.str, c.dex, c.luk); got != c.want {
			t.Errorf("BaseATK(%d, %d, %d) = %d, want %d", c.str, c.dex, c.luk, got, c.want)
		}
	}
}

func TestMeleeDamage(t *testing.T) {
	// Attacker: str=10, dex=5, luk=1 -> BaseATK: 10 + (10/10)^2 + 5/5 + 1/5 = 10 + 1 + 1 + 0 = 12
	// Target: mobDef=0, mobVit=0 -> afterHard = 12*(100-0)/100 = 12.
	// VIT formula: vit=0. vfloor=0, spread=0. minDmg=12-0=12, maxDmg=12-0=12.

	t.Run("Poring Def0 Vit0", func(t *testing.T) {
		got := MeleeDamage(10, 5, 1, 0, 0)
		want := DamageBand{12, 12}
		if got != want {
			t.Errorf("Poring Damage = %+v, want %+v", got, want)
		}
	})

	// Target: Lunatic Def0, Vit3.
	// afterHard = 12.
	// vit=3. vfloor=(3*3)/10 + 3/2 = 0 + 1 = 1.
	// spread=(3*3)/150 - (3*3)/10 - 1 = 0 - 0 - 1 = -1 -> 0.
	// minDmg = 12 - (1+0) = 11. maxDmg = 12 - 1 = 11.
	t.Run("Lunatic Def0 Vit3", func(t *testing.T) {
		got := MeleeDamage(10, 5, 1, 0, 3)
		want := DamageBand{11, 11}
		if got != want {
			t.Errorf("Lunatic Damage = %+v, want %+v", got, want)
		}
	})

	// Target: Drops Def0, Vit2.
	// afterHard = 12.
	// vit=2. vfloor=(3*2)/10 + 2/2 = 0 + 1 = 1.
	// spread=(2*2)/150 - (3*2)/10 - 1 = 0 - 0 - 1 = -1 -> 0.
	// minDmg = 12 - (1+0) = 11. maxDmg = 12 - 1 = 11.
	t.Run("Drops Def0 Vit2", func(t *testing.T) {
		got := MeleeDamage(10, 5, 1, 0, 2)
		want := DamageBand{11, 11}
		if got != want {
			t.Errorf("Drops Damage = %+v, want %+v", got, want)
		}
	})

	// Target: Spore Def0, Vit5.
	// afterHard = 12.
	// vit=5. vfloor=(3*5)/10 + 5/2 = 1 + 2 = 3.
	// spread=(5*5)/150 - (3*5)/10 - 1 = 25/150 - 1 - 1 = 0 - 1 - 1 = -2 -> 0.
	// minDmg = 12 - (3+0) = 9. maxDmg = 12 - 3 = 9.
	t.Run("Spore Def0 Vit5", func(t *testing.T) {
		got := MeleeDamage(10, 5, 1, 0, 5)
		want := DamageBand{9, 9}
		if got != want {
			t.Errorf("Spore Damage = %+v, want %+v", got, want)
		}
	})

	// Test case: Def > 0
	// Attacker: str=10, dex=5, luk=1 -> BaseATK=12.
	// Target: Def=50, Vit=0.
	// afterHard = 12*(100-50)/100 = 6.
	// vit=0. minDmg=6, maxDmg=6.
	t.Run("Def50 Vit0", func(t *testing.T) {
		got := MeleeDamage(10, 5, 1, 50, 0)
		want := DamageBand{6, 6}
		if got != want {
			t.Errorf("Def50 Vit0 Damage = %+v, want %+v", got, want)
		}
	})

	// Test case: Vit > 0 causing band
	// Attacker: str=50, dex=0, luk=0 -> BaseATK=75.
	// Target: Def=0, Vit=20.
	// afterHard = 75.
	// vit=20. vfloor=(3*20)/10 + 20/2 = 6 + 10 = 16.
	// spread=(20*20)/150 - (3*20)/10 - 1 = 400/150 - 6 - 1 = 2 - 7 = -5 -> 0.
	// vit=20.
	// Actually: 20^2=400. 400/150 = 2.
	// spread = 2 - 6 - 1 = -5 -> 0.
	// VIT formula check again:
	// spread = [vit^2/150] - [vit*0.3] - 1
	// [20*20/150] = 400/150 = 2.
	// [20*0.3] = 6.
	// spread = 2 - 6 - 1 = -5 -> 0.
	// minDmg = 75 - (16+0) = 59. maxDmg = 75 - 16 = 59.
	t.Run("Def0 Vit20", func(t *testing.T) {
		got := MeleeDamage(50, 0, 0, 0, 20)
		want := DamageBand{59, 59}
		if got != want {
			t.Errorf("Def0 Vit20 Damage = %+v, want %+v", got, want)
		}
	})

	// Test case: VIT producing spread
	// Need vit large enough.
	// spread = [vit^2/150] - [vit*0.3] - 1 > 0
	// e.g. vit=40.
	// [40^2/150] = [1600/150] = 10.
	// [40*0.3] = 12.
	// spread = 10 - 12 - 1 = -3 -> 0. Still 0?
	// The VIT formula has a randomness part.
	// [vit*0.3] + rnd(0, max(0, [vit^2/150] - [vit*0.3] - 1)) + [vit*0.5]
	// Maybe my manual calculation of spread is wrong.
	// Let's trust the formula I implemented.
	// If spread = 0, min=max.
	// Let's try larger VIT. vit=100.
	// vfloor = 30 + 50 = 80.
	// [10000/150] = 66.
	// spread = 66 - 30 - 1 = 35.
	// vfloor = 80.
	// minDmg = afterHard - (80+35) = afterHard - 115.
	// maxDmg = afterHard - 80.
	// For BaseATK 75, this would be negative, so floored to 1.

	// Let's use a very strong attacker for Vit 100. BaseATK > 115.
	// Attacker: str=99, dex=0, luk=0 -> BaseATK=99+(9)^2=180.
	// afterHard = 180.
	// vit=100.
	// vfloor = 80.
	// spread = 35.
	// min = 180 - 115 = 65.
	// max = 180 - 80 = 100.
	t.Run("Def0 Vit100", func(t *testing.T) {
		got := MeleeDamage(99, 0, 0, 0, 100)
		want := DamageBand{65, 100}
		if got != want {
			t.Errorf("Def0 Vit100 Damage = %+v, want %+v", got, want)
		}
	})

	t.Run("Def100", func(t *testing.T) {
		got := MeleeDamage(99, 0, 0, 100, 0)
		// afterHard = 180 * (100-100)/100 = 0.
		// floored to 1.
		want := DamageBand{1, 1}
		if got != want {
			t.Errorf("Def100 Damage = %+v, want %+v", got, want)
		}
	})
}
