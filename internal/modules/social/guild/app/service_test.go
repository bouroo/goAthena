//go:build unit

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/bouroo/goAthena/internal/modules/social/guild/app"
	"github.com/bouroo/goAthena/internal/modules/social/guild/domain"
	"github.com/bouroo/goAthena/internal/modules/social/guild/infra"
)

// newFixture builds a service over the in-memory repo with three chars
// registered: 1001 (master, account 500) on prontera, 1002 (online) on
// prontera, and 1003 (offline) on geffen.
func newFixture(t *testing.T) (*app.GuildService, *infra.MemoryGuildRepository) {
	t.Helper()
	repo := infra.NewMemoryGuildRepository()
	repo.SetChar(1001, infra.CharInfo{AccountID: 500, Name: "Master", Map: "prontera", Class: 7, BaseLevel: 50, Online: true})
	repo.SetChar(1002, infra.CharInfo{AccountID: 501, Name: "Member", Map: "prontera", Class: 7, BaseLevel: 40, Online: true})
	repo.SetChar(1003, infra.CharInfo{AccountID: 502, Name: "Sleeper", Map: "geffen", Class: 7, BaseLevel: 45, Online: false})
	return app.NewGuildService(repo), repo
}

func TestCreate_RejectsBlankName(t *testing.T) {
	svc, _ := newFixture(t)
	for _, name := range []string{"", "   ", "\t"} {
		if _, err := svc.Create(context.Background(), 500, 1001, name); !errors.Is(err, domain.ErrEmptyGuildName) {
			t.Fatalf("Create(%q) err = %v, want ErrEmptyGuildName", name, err)
		}
	}
}

func TestCreate_TrimsNameAndMarksMaster(t *testing.T) {
	svc, _ := newFixture(t)
	g, err := svc.Create(context.Background(), 500, 1001, "  Emperium  ")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if g.Name != "Emperium" {
		t.Errorf("name = %q, want trimmed", g.Name)
	}
	if g.Master != 1001 {
		t.Errorf("master = %d, want 1001", g.Master)
	}
	if g.MasterNam != "Master" {
		t.Errorf("master name = %q, want Master", g.MasterNam)
	}
	if g.GuildLv != 1 || g.MaxMember != domain.MaxGuildSize {
		t.Errorf("lv/max = %d/%d, want 1/%d", g.GuildLv, g.MaxMember, domain.MaxGuildSize)
	}
	members, err := svc.Members(context.Background(), g.ID)
	if err != nil {
		t.Fatalf("Members: %v", err)
	}
	if len(members) != 1 || !members[0].Master || members[0].Position != 0 {
		t.Fatalf("members = %+v, want one master at position 0", members)
	}
}

func TestCreate_RejectsDuplicateNameAndSecondGuild(t *testing.T) {
	svc, repo := newFixture(t)
	if _, err := svc.Create(context.Background(), 500, 1001, "Emperium"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Name taken by another char.
	repo.SetChar(1004, infra.CharInfo{AccountID: 503, Name: "Rival", Map: "prontera", Class: 7, BaseLevel: 60, Online: true})
	if _, err := svc.Create(context.Background(), 503, 1004, "Emperium"); !errors.Is(err, domain.ErrNameExists) {
		t.Fatalf("dup name err = %v, want ErrNameExists", err)
	}
	// Same char, fresh name: already in a guild.
	if _, err := svc.Create(context.Background(), 500, 1001, "Second"); !errors.Is(err, domain.ErrAlreadyInGuild) {
		t.Fatalf("second guild err = %v, want ErrAlreadyInGuild", err)
	}
}

func TestAccept_JoinsAndEnforcesOneGuild(t *testing.T) {
	svc, _ := newFixture(t)
	g, err := svc.Create(context.Background(), 500, 1001, "Emperium")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Accept(context.Background(), g.ID, 1003); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	joined, err := svc.GetByMember(context.Background(), 1003)
	if err != nil {
		t.Fatalf("GetByMember: %v", err)
	}
	if joined.ID != g.ID {
		t.Fatalf("joined guild = %d, want %d", joined.ID, g.ID)
	}
	// One guild per char.
	if err := svc.Accept(context.Background(), g.ID, 1003); !errors.Is(err, domain.ErrAlreadyInGuild) {
		t.Fatalf("re-join err = %v, want ErrAlreadyInGuild", err)
	}
	// Unknown guild.
	if err := svc.Accept(context.Background(), domain.GuildID(999), 1002); !errors.Is(err, domain.ErrGuildNotFound) {
		t.Fatalf("unknown guild err = %v, want ErrGuildNotFound", err)
	}
}

func TestLeave_GuildSurvivesUntilEmpty(t *testing.T) {
	svc, _ := newFixture(t)
	g, err := svc.Create(context.Background(), 500, 1001, "Emperium")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Accept(context.Background(), g.ID, 1002); err != nil {
		t.Fatalf("Accept 1002: %v", err)
	}
	if err := svc.Accept(context.Background(), g.ID, 1003); err != nil {
		t.Fatalf("Accept 1003: %v", err)
	}
	// Master leave: rAthena lets the master leave like anyone; the guild
	// survives leaderless (int_guild breaks only when EMPTY).
	if err := svc.Leave(context.Background(), g.ID, 1001); err != nil {
		t.Fatalf("master Leave: %v", err)
	}
	if _, err := svc.Get(context.Background(), g.ID); err != nil {
		t.Fatalf("guild should survive master leave: %v", err)
	}
	if err := svc.Leave(context.Background(), g.ID, 1002); err != nil {
		t.Fatalf("member Leave: %v", err)
	}
	if _, err := svc.Get(context.Background(), g.ID); err != nil {
		t.Fatalf("guild should survive with one member: %v", err)
	}
	// Last member out → guild dies (guild_check_empty → BreakGuild,
	// int_guild.cpp:1353).
	if err := svc.Leave(context.Background(), g.ID, 1003); err != nil {
		t.Fatalf("last Leave: %v", err)
	}
	if _, err := svc.Get(context.Background(), g.ID); !errors.Is(err, domain.ErrGuildNotFound) {
		t.Fatalf("empty guild err = %v, want ErrGuildNotFound", err)
	}
}

func TestLeave_NotMemberRejected(t *testing.T) {
	svc, _ := newFixture(t)
	g, err := svc.Create(context.Background(), 500, 1001, "Emperium")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Leave(context.Background(), g.ID, 1002); !errors.Is(err, domain.ErrNotInGuild) {
		t.Fatalf("stranger leave err = %v, want ErrNotInGuild", err)
	}
}

func TestKick_MasterOnly_TargetProtected(t *testing.T) {
	svc, _ := newFixture(t)
	g, err := svc.Create(context.Background(), 500, 1001, "Emperium")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Accept(context.Background(), g.ID, 1002); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	// Non-master cannot kick.
	if err := svc.Kick(context.Background(), g.ID, 1002, 1001); !errors.Is(err, domain.ErrNotMaster) {
		t.Fatalf("member kick err = %v, want ErrNotMaster", err)
	}
	// Master cannot be expelled — including by the master.
	if err := svc.Kick(context.Background(), g.ID, 1001, 1001); !errors.Is(err, domain.ErrCantExpelMaster) {
		t.Fatalf("self kick err = %v, want ErrCantExpelMaster", err)
	}
	// Master kicks member.
	if err := svc.Kick(context.Background(), g.ID, 1001, 1002); err != nil {
		t.Fatalf("Kick: %v", err)
	}
	if _, err := svc.GetByMember(context.Background(), 1002); !errors.Is(err, domain.ErrNotInGuild) {
		t.Fatalf("kicked char err = %v, want ErrNotInGuild", err)
	}
	// Unknown target.
	if err := svc.Kick(context.Background(), g.ID, 1001, 1003); !errors.Is(err, domain.ErrNotInGuild) {
		t.Fatalf("stranger kick err = %v, want ErrNotInGuild", err)
	}
}

func TestBreak_RulesAndTeardown(t *testing.T) {
	svc, _ := newFixture(t)
	g, err := svc.Create(context.Background(), 500, 1001, "Emperium")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Accept(context.Background(), g.ID, 1002); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	// Wrong key.
	if err := svc.Break(context.Background(), g.ID, 1001, "emperium"); !errors.Is(err, domain.ErrBreakNameMismatch) {
		t.Fatalf("bad key err = %v, want ErrBreakNameMismatch", err)
	}
	// Non-master.
	if err := svc.Break(context.Background(), g.ID, 1002, "Emperium"); !errors.Is(err, domain.ErrNotMaster) {
		t.Fatalf("member break err = %v, want ErrNotMaster", err)
	}
	// Other members online (rAthena flag 2).
	if err := svc.Break(context.Background(), g.ID, 1001, "Emperium"); !errors.Is(err, domain.ErrMembersOnline) {
		t.Fatalf("online-member break err = %v, want ErrMembersOnline", err)
	}
	// Member goes offline → break succeeds.
	if err := svc.Leave(context.Background(), g.ID, 1002); err != nil {
		t.Fatalf("member leave: %v", err)
	}
	if err := svc.Break(context.Background(), g.ID, 1001, "Emperium"); err != nil {
		t.Fatalf("Break: %v", err)
	}
	if _, err := svc.Get(context.Background(), g.ID); !errors.Is(err, domain.ErrGuildNotFound) {
		t.Fatalf("broken guild err = %v, want ErrGuildNotFound", err)
	}
	if _, err := svc.GetByMember(context.Background(), 1001); !errors.Is(err, domain.ErrNotInGuild) {
		t.Fatalf("master membership err = %v, want ErrNotInGuild", err)
	}
}
