//go:build unit

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bouroo/goAthena/internal/modules/social/party/app"
	"github.com/bouroo/goAthena/internal/modules/social/party/domain"
	"github.com/bouroo/goAthena/internal/modules/social/party/infra"
)

// newFixture builds a service over the in-memory repo with three chars
// registered: 1001 (leader, account 500) on prontera, 1002 on prontera, and 1003
// on geffen.
func newFixture(t *testing.T) (*app.PartyService, *infra.MemoryPartyRepository) {
	t.Helper()
	repo := infra.NewMemoryPartyRepository()
	repo.SetChar(1001, infra.CharInfo{AccountID: 500, Name: "Leader", Map: "prontera", Class: 1, BaseLevel: 10, Online: true})
	repo.SetChar(1002, infra.CharInfo{AccountID: 501, Name: "Friend", Map: "prontera", Class: 1, BaseLevel: 8, Online: true})
	repo.SetChar(1003, infra.CharInfo{AccountID: 502, Name: "Faraway", Map: "geffen", Class: 1, BaseLevel: 9, Online: true})
	return app.NewPartyService(repo), repo
}

func TestCreate_RejectsBlankName(t *testing.T) {
	svc, _ := newFixture(t)
	for _, name := range []string{"", "   ", "\t"} {
		if _, err := svc.Create(context.Background(), 500, 1001, name); !errors.Is(err, domain.ErrEmptyPartyName) {
			t.Fatalf("Create(%q) err = %v, want ErrEmptyPartyName", name, err)
		}
	}
}

func TestCreate_TrimsNameAndMarksLeader(t *testing.T) {
	svc, _ := newFixture(t)
	p, err := svc.Create(context.Background(), 500, 1001, "  Athena  ")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.Name != "Athena" {
		t.Errorf("name = %q, want trimmed %q", p.Name, "Athena")
	}
	if p.LeaderID != 500 || p.LeaderChar != 1001 {
		t.Errorf("leader = (%d,%d), want (500,1001)", p.LeaderID, p.LeaderChar)
	}
	members, err := svc.Members(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("Members: %v", err)
	}
	if len(members) != 1 || !members[0].Leader {
		t.Fatalf("members = %+v, want one leader entry", members)
	}
}

func TestCreate_AlreadyInParty(t *testing.T) {
	svc, _ := newFixture(t)
	if _, err := svc.Create(context.Background(), 500, 1001, "First"); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := svc.Create(context.Background(), 500, 1001, "Second"); !errors.Is(err, domain.ErrAlreadyInParty) {
		t.Fatalf("second create err = %v, want ErrAlreadyInParty", err)
	}
}

func TestAccept_AddsMemberAndCapsAtMaxPartySize(t *testing.T) {
	svc, repo := newFixture(t)
	ctx := context.Background()
	p, err := svc.Create(ctx, 500, 1001, "Full")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Accept(ctx, p.ID, 1002); err != nil {
		t.Fatalf("accept 1002: %v", err)
	}
	// Fill the roster to the cap: 1001 + 1002 already, so register the rest.
	for charID := uint32(2000); charID < 2000+domain.MaxPartySize-2; charID++ {
		repo.SetChar(charID, infra.CharInfo{AccountID: charID, Name: "Filler", Map: "prontera", Online: true})
		if err := svc.Accept(ctx, p.ID, charID); err != nil {
			t.Fatalf("accept %d: %v", charID, err)
		}
	}
	repo.SetChar(3000, infra.CharInfo{AccountID: 3000, Name: "Overflow", Map: "prontera", Online: true})
	if err := svc.Accept(ctx, p.ID, 3000); !errors.Is(err, domain.ErrPartyFull) {
		t.Fatalf("accept past cap err = %v, want ErrPartyFull", err)
	}
}

func TestAccept_RejectsSecondParty(t *testing.T) {
	svc, _ := newFixture(t)
	ctx := context.Background()
	a, _ := svc.Create(ctx, 500, 1001, "A")
	b, _ := svc.Create(ctx, 501, 1002, "B")
	if err := svc.Accept(ctx, b.ID, 1001); !errors.Is(err, domain.ErrAlreadyInParty) {
		t.Fatalf("accept already-partied char err = %v, want ErrAlreadyInParty", err)
	}
	if _, err := svc.Get(ctx, a.ID); err != nil {
		t.Fatalf("party A vanished: %v", err)
	}
}

func TestLeave_NonLeaderOnlyClearsMembership(t *testing.T) {
	svc, _ := newFixture(t)
	ctx := context.Background()
	p, _ := svc.Create(ctx, 500, 1001, "Stable")
	if err := svc.Accept(ctx, p.ID, 1002); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if err := svc.Leave(ctx, p.ID, 1002); err != nil {
		t.Fatalf("leave: %v", err)
	}
	members, err := svc.Members(ctx, p.ID)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	if len(members) != 1 || members[0].CharID != 1001 {
		t.Fatalf("members after leave = %+v, want only the leader", members)
	}
}

func TestLeave_LeaderDisbandsEntireParty(t *testing.T) {
	svc, _ := newFixture(t)
	ctx := context.Background()
	p, _ := svc.Create(ctx, 500, 1001, "Doomed")
	if err := svc.Accept(ctx, p.ID, 1002); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if err := svc.Leave(ctx, p.ID, 1001); err != nil {
		t.Fatalf("leader leave: %v", err)
	}
	// rAthena deletes the party and withdraws everyone (int_party.cpp:651-676).
	if _, err := svc.Get(ctx, p.ID); !errors.Is(err, domain.ErrPartyNotFound) {
		t.Errorf("party after leader leave err = %v, want ErrPartyNotFound", err)
	}
	if _, err := svc.GetByMember(ctx, 1002); !errors.Is(err, domain.ErrNotInParty) {
		t.Errorf("remaining member err = %v, want ErrNotInParty", err)
	}
}

func TestKick_OnlyLeaderMayAndNeverDisbands(t *testing.T) {
	svc, _ := newFixture(t)
	ctx := context.Background()
	p, _ := svc.Create(ctx, 500, 1001, "Kickable")
	if err := svc.Accept(ctx, p.ID, 1002); err != nil {
		t.Fatalf("accept: %v", err)
	}
	// A non-leader kick is refused.
	if err := svc.Kick(ctx, p.ID, 1002, 1001); !errors.Is(err, domain.ErrNotLeader) {
		t.Fatalf("non-leader kick err = %v, want ErrNotLeader", err)
	}
	// A leader kick removes only the target.
	if err := svc.Kick(ctx, p.ID, 1001, 1002); err != nil {
		t.Fatalf("leader kick: %v", err)
	}
	members, _ := svc.Members(ctx, p.ID)
	if len(members) != 1 || members[0].CharID != 1001 {
		t.Fatalf("members after kick = %+v, want only the leader", members)
	}
}

func TestKick_LeaderKickingSelfDisbands(t *testing.T) {
	// rAthena's party_removemember has no self-kick guard: a leader naming
	// themselves falls through to the withdraw path and disbands the party.
	svc, _ := newFixture(t)
	ctx := context.Background()
	p, _ := svc.Create(ctx, 500, 1001, "SelfKick")
	if err := svc.Accept(ctx, p.ID, 1002); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if err := svc.Kick(ctx, p.ID, 1001, 1001); err != nil {
		t.Fatalf("self kick: %v", err)
	}
	if _, err := svc.Get(ctx, p.ID); !errors.Is(err, domain.ErrPartyNotFound) {
		t.Errorf("party after self-kick err = %v, want ErrPartyNotFound", err)
	}
}

func TestSetOptions_LeaderOnlyAndClampsFlags(t *testing.T) {
	svc, _ := newFixture(t)
	ctx := context.Background()
	p, _ := svc.Create(ctx, 500, 1001, "Options")
	if err := svc.Accept(ctx, p.ID, 1002); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if err := svc.SetOptions(ctx, p.ID, 1002, 1, 1); !errors.Is(err, domain.ErrNotLeader) {
		t.Fatalf("non-leader SetOptions err = %v, want ErrNotLeader", err)
	}
	// Out-of-range bits are masked: exp keeps bit 0, item keeps bits 0-1.
	if err := svc.SetOptions(ctx, p.ID, 1001, 0xff, 0xff); err != nil {
		t.Fatalf("SetOptions: %v", err)
	}
	got, err := svc.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Exp != 1 || got.Item != 3 {
		t.Errorf("options = (exp %d, item %d), want (1, 3)", got.Exp, got.Item)
	}
}

func TestSplitExp_OnlyOnlineSameMapMembersShare(t *testing.T) {
	svc, _ := newFixture(t)
	ctx := context.Background()
	p, _ := svc.Create(ctx, 500, 1001, "Share")
	if err := svc.Accept(ctx, p.ID, 1002); err != nil { // prontera, online
		t.Fatalf("accept 1002: %v", err)
	}
	if err := svc.Accept(ctx, p.ID, 1003); err != nil { // geffen — different map
		t.Fatalf("accept 1003: %v", err)
	}
	awards, err := svc.SplitExp(ctx, p.ID, "prontera", 100, 40)
	if err != nil {
		t.Fatalf("SplitExp: %v", err)
	}
	if len(awards) != 2 {
		t.Fatalf("awards = %v, want 2 recipients (geffen member excluded)", awards)
	}
	// rAthena divides with truncation and drops the remainder (party.cpp:1263).
	for charID, gain := range awards {
		if gain != [2]uint64{50, 20} {
			t.Errorf("award[%d] = %v, want [50 20]", charID, gain)
		}
	}
}

func TestSplitExp_DropsRemainder(t *testing.T) {
	svc, repo := newFixture(t)
	ctx := context.Background()
	p, _ := svc.Create(ctx, 500, 1001, "Remainder")
	if err := svc.Accept(ctx, p.ID, 1002); err != nil {
		t.Fatalf("accept: %v", err)
	}
	repo.SetChar(1004, infra.CharInfo{AccountID: 503, Name: "Third", Map: "prontera", Online: true})
	if err := svc.Accept(ctx, p.ID, 1004); err != nil {
		t.Fatalf("accept 1004: %v", err)
	}
	// 100/3 = 33 per member; the leftover 1 is NOT folded into the leader.
	awards, err := svc.SplitExp(ctx, p.ID, "prontera", 100, 0)
	if err != nil {
		t.Fatalf("SplitExp: %v", err)
	}
	var total uint64
	for _, gain := range awards {
		if gain[0] != 33 {
			t.Errorf("award = %v, want base 33", gain)
		}
		total += gain[0]
	}
	if total != 99 {
		t.Errorf("total awarded = %d, want 99 (3x33, remainder dropped)", total)
	}
}

func TestSplitExp_OfflineMembersExcluded(t *testing.T) {
	svc, repo := newFixture(t)
	ctx := context.Background()
	p, _ := svc.Create(ctx, 500, 1001, "Offline")
	if err := svc.Accept(ctx, p.ID, 1002); err != nil {
		t.Fatalf("accept: %v", err)
	}
	repo.SetChar(1002, infra.CharInfo{AccountID: 501, Name: "Friend", Map: "prontera", Online: false})
	awards, err := svc.SplitExp(ctx, p.ID, "prontera", 100, 0)
	if err != nil {
		t.Fatalf("SplitExp: %v", err)
	}
	if len(awards) != 1 {
		t.Fatalf("awards = %v, want 1 (offline member excluded)", awards)
	}
	if gain := awards[1001]; gain != [2]uint64{100, 0} {
		t.Errorf("solo award = %v, want [100 0]", gain)
	}
}

func TestGetByMember_NotInParty(t *testing.T) {
	svc, _ := newFixture(t)
	if _, err := svc.GetByMember(context.Background(), 1001); !errors.Is(err, domain.ErrNotInParty) {
		t.Fatalf("GetByMember err = %v, want ErrNotInParty", err)
	}
}
