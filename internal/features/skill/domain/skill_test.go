//go:build unit

package domain

import (
	"strings"
	"testing"

	"github.com/bouroo/goAthena/pkg/ro/skilldb"
	"github.com/stretchr/testify/require"
)

func TestInfFromTargetType(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want uint16
	}{
		{name: "Attack", in: "Attack", want: InfAttack},
		{name: "Ground", in: "Ground", want: InfGround},
		{name: "Self", in: "Self", want: InfSelf},
		{name: "Support", in: "Support", want: InfSupport},
		{name: "Trap", in: "Trap", want: InfTrap},
		{name: "unknown", in: "Other", want: InfNone},
		{name: "empty", in: "", want: InfNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := InfFromTargetType(tt.in); got != tt.want {
				t.Errorf("InfFromTargetType(%q) = %#x, want %#x", tt.in, got, tt.want)
			}
		})
	}
}

func TestNewRegistry_FromSkillDB(t *testing.T) {
	db, err := skilldb.Load(strings.NewReader(`Header:
  Type: SKILL_DB
  Version: 4
Body:
  - Id: 5
    Name: SM_BASH
    MaxLevel: 10
    TargetType: Attack
    Range: 3
    Requires:
      SpCost:
        - {Level: 1, Amount: 8}
        - {Level: 10, Amount: 15}
  - Id: 1
    Name: NV_BASIC
    MaxLevel: 9
`))
	require.NoError(t, err)

	reg := NewRegistry(db)
	require.NotNil(t, reg)
	bash, ok := reg.entries[5]
	require.True(t, ok)
	if bash.Inf != InfAttack || bash.Range != 3 || bash.SpAt(1) != 8 || bash.SpAt(10) != 15 {
		t.Fatalf("converted SM_BASH = %+v, want attack/range/SP conversion", bash)
	}
	basic, ok := reg.entries[1]
	require.True(t, ok)
	if basic.Inf != InfNone || basic.spCost != nil {
		t.Fatalf("converted NV_BASIC = %+v, want passive with nil SP costs", basic)
	}
}

func TestSetRegistryLookupFallback(t *testing.T) {
	db, err := skilldb.Load(strings.NewReader(`Header:
  Type: SKILL_DB
  Version: 4
Body:
  - Id: 999
    Name: TEST_SKILL
    MaxLevel: 1
    TargetType: Support
`))
	require.NoError(t, err)

	SetRegistry(NewRegistry(db))
	t.Cleanup(func() { SetRegistry(nil) })
	got, ok := Lookup(999)
	if !ok || got.Name != "TEST_SKILL" || got.Inf != InfSupport {
		t.Fatalf("Lookup(999) = %+v, %t, want DB-backed skill", got, ok)
	}

	SetRegistry(nil)
	if _, ok := Lookup(999); ok {
		t.Fatal("Lookup(999) found a skill after resetting to fallback")
	}
	if _, ok := Lookup(SM_BASH); !ok {
		t.Fatal("Lookup(SM_BASH) missed hardcoded fallback")
	}
}

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
		if s.Name != "NV_FIRSTAID" || s.MaxLevel != 1 || s.Inf != InfSelf || len(s.spCost) != 1 || s.spCost[0] != 3 {
			t.Errorf("got %+v", s)
		}
	})

	t.Run("hit NV_TRICKDEAD SP=5", func(t *testing.T) {
		s, ok := Lookup(143)
		if !ok {
			t.Fatal("Lookup(143) miss")
		}
		if s.spCost[0] != 5 {
			t.Errorf("NV_TRICKDEAD SP = %d, want 5 (per skill_db.yml:5090)", s.spCost[0])
		}
	})

	t.Run("miss", func(t *testing.T) {
		if _, ok := Lookup(uint16(60000)); ok {
			t.Error("Lookup(99999) should miss")
		}
	})

	t.Run("hit SM_BASH", func(t *testing.T) {
		s, ok := Lookup(SM_BASH)
		if !ok {
			t.Fatal("Lookup(SM_BASH) miss")
		}
		if s.ID != SM_BASH || s.Name != "SM_BASH" || s.MaxLevel != 10 || s.Inf != InfAttack || s.Range != 0 {
			t.Errorf("got %+v", s)
		}
		if got, want := len(s.spCost), 10; got != want {
			t.Errorf("len(spCost) = %d, want %d", got, want)
		}
		wantSP := []uint16{8, 8, 8, 8, 8, 15, 15, 15, 15, 15}
		for i, w := range wantSP {
			if s.spCost[i] != w {
				t.Errorf("spCost[%d] = %d, want %d (per skill_db.yml:164-200)", i, s.spCost[i], w)
			}
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

func TestSkillSpAt_SMBash(t *testing.T) {
	s, ok := Lookup(SM_BASH)
	if !ok {
		t.Fatal("missing SM_BASH")
	}
	cases := []struct {
		level uint8
		want  uint16
		note  string
	}{
		{0, 0, "under"},
		{1, 8, "L1 floor"},
		{5, 8, "L5 last 8-cost tier"},
		{6, 15, "L6 first 15-cost tier"},
		{10, 15, "L10 cap"},
		{11, 0, "over MaxLevel=10"},
		{255, 0, "far over"},
	}
	for _, c := range cases {
		if got := s.SpAt(c.level); got != c.want {
			t.Errorf("SM_BASH SpAt(%d) = %d, want %d (%s)", c.level, got, c.want, c.note)
		}
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
