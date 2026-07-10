//go:build unit

package domain

import (
	"testing"
)

func TestInfConstants(t *testing.T) {
	cases := []struct {
		name string
		got  uint16
		want uint16
	}{
		{"InfNone", InfNone, 0x00},
		{"InfAttack", InfAttack, 0x01},
		{"InfGround", InfGround, 0x02},
		{"InfSelf", InfSelf, 0x04},
		{"InfSupport", InfSupport, 0x10},
		{"InfTrap", InfTrap, 0x20},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %#x, want %#x", c.name, c.got, c.want)
		}
	}
}

func TestLookup(t *testing.T) {
	t.Run("hit NV_BASIC", func(t *testing.T) {
		s, ok := Lookup(1)
		if !ok {
			t.Fatal("Lookup(1) miss")
		}
		if s.Name != "NV_BASIC" || s.MaxLevel != 9 || s.Inf != InfNone {
			t.Errorf("got %+v", s)
		}
	})

	t.Run("hit NV_FIRSTAID", func(t *testing.T) {
		s, ok := Lookup(142)
		if !ok {
			t.Fatal("Lookup(142) miss")
		}
		if s.Name != "NV_FIRSTAID" || s.MaxLevel != 1 || s.Inf != InfSelf || len(s.SpCost) != 1 || s.SpCost[0] != 3 {
			t.Errorf("got %+v", s)
		}
	})

	t.Run("hit NV_TRICKDEAD SP=5", func(t *testing.T) {
		s, ok := Lookup(143)
		if !ok {
			t.Fatal("Lookup(143) miss")
		}
		if s.SpCost[0] != 5 {
			t.Errorf("NV_TRICKDEAD SP = %d, want 5 (per skill_db.yml:5090)", s.SpCost[0])
		}
	})

	t.Run("miss", func(t *testing.T) {
		if _, ok := Lookup(uint16(60000)); ok {
			t.Error("Lookup(99999) should miss")
		}
	})
}

func TestSkillSpAt(t *testing.T) {
	s, ok := Lookup(142)
	if !ok {
		t.Fatal("missing NV_FIRSTAID")
	}
	cases := []struct {
		level uint8
		want  uint16
	}{
		{0, 0},   // under
		{1, 3},   // in range
		{2, 0},   // over
		{255, 0}, // far over
	}
	for _, c := range cases {
		if got := s.SpAt(c.level); got != c.want {
			t.Errorf("SpAt(%d) = %d, want %d", c.level, got, c.want)
		}
	}

	passive, ok := Lookup(1)
	if !ok {
		t.Fatal("missing NV_BASIC")
	}
	if got := passive.SpAt(9); got != 0 {
		t.Errorf("passive SpAt(9) = %d, want 0", got)
	}
}

func TestBuildSkillList(t *testing.T) {
	t.Run("empty input -> empty non-nil", func(t *testing.T) {
		got := BuildSkillList(nil)
		if got == nil {
			t.Fatal("nil slice, want empty non-nil")
		}
		if len(got) != 0 {
			t.Errorf("len = %d, want 0", len(got))
		}
	})

	t.Run("single known", func(t *testing.T) {
		got := BuildSkillList([]LearnedSkill{{ID: 142, Level: 1}})
		if len(got) != 1 {
			t.Fatalf("len = %d, want 1", len(got))
		}
		e := got[0]
		if e.ID != 142 || e.Inf != InfSelf || e.Level != 1 || e.SP != 3 || e.Name != "NV_FIRSTAID" || e.UpFlag != 0 {
			t.Errorf("got %+v", e)
		}
	})

	t.Run("unknown skipped, preserves order", func(t *testing.T) {
		got := BuildSkillList([]LearnedSkill{
			{ID: 1, Level: 1},
			{ID: uint16(60000), Level: 1},
			{ID: 143, Level: 1},
		})
		if len(got) != 2 {
			t.Fatalf("len = %d, want 2", len(got))
		}
		if got[0].ID != 1 || got[1].ID != 143 {
			t.Errorf("order broken: %+v", got)
		}
	})

	t.Run("level clamp high", func(t *testing.T) {
		// NV_BASIC MaxLevel=9, request 250 -> clamp to 9.
		got := BuildSkillList([]LearnedSkill{{ID: 1, Level: 250}})
		if len(got) != 1 || got[0].Level != 9 {
			t.Errorf("got %+v, want Level=9", got)
		}
	})

	t.Run("level clamp low", func(t *testing.T) {
		// Level=0 should be normalized to 1.
		got := BuildSkillList([]LearnedSkill{{ID: 142, Level: 0}})
		if len(got) != 1 || got[0].Level != 1 || got[0].SP != 3 {
			t.Errorf("got %+v, want Level=1 SP=3", got)
		}
	})

	t.Run("NV_TRICKDEAD SP=5 preserved", func(t *testing.T) {
		got := BuildSkillList([]LearnedSkill{{ID: 143, Level: 1}})
		if len(got) != 1 || got[0].SP != 5 {
			t.Errorf("got %+v, want SP=5", got)
		}
	})

	t.Run("UpFlag is 0 (deferred)", func(t *testing.T) {
		got := BuildSkillList([]LearnedSkill{{ID: 1, Level: 9}})
		if got[0].UpFlag != 0 {
			t.Errorf("UpFlag = %d, want 0", got[0].UpFlag)
		}
	})

	t.Run("Range2 propagation", func(t *testing.T) {
		// Inject a synthetic registry entry via a non-registered ID to
		// test the range2 default path is unreachable. Instead verify
		// current registry all have Range=0 -> Range2=0.
		got := BuildSkillList([]LearnedSkill{
			{ID: 1, Level: 1},
			{ID: 142, Level: 1},
			{ID: 143, Level: 1},
		})
		for _, e := range got {
			if e.Range2 != 0 {
				t.Errorf("%s Range2 = %d, want 0", e.Name, e.Range2)
			}
		}
	})
}
