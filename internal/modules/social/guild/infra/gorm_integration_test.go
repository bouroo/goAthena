//go:build integration

package infra_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/infra/testdb"
	"github.com/bouroo/goAthena/internal/infrastructure/db"
	"github.com/bouroo/goAthena/internal/modules/social/guild/domain"
	"github.com/bouroo/goAthena/internal/modules/social/guild/infra"
)

const testGuildAccountID uint32 = 4000001

func dbForTest(t *testing.T) *gorm.DB {
	t.Helper()
	cfg := config.DBConfig{
		Driver:   envOr("DB_DRIVER", "mariadb"),
		Host:     envOr("DB_HOST", "127.0.0.1"),
		Port:     envInt("DB_PORT", 13306),
		Name:     envOr("DB_NAME", "n"),
		User:     envOr("DB_USER", "r"),
		Password: envOr("DB_PASSWORD", "r"),
		SSLMode:  "disable",
	}
	gdb, err := db.New(cfg)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(gdb) })
	return gdb
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return fallback
}

// TestMain provisions a real MariaDB or postgres container via the shared testdb
// harness and migrates the schema.
func TestMain(m *testing.M) {
	driver := os.Getenv("DB_DRIVER")
	if driver == "" {
		driver = "mariadb"
	}
	if _, err := testdb.Setup(driver); err != nil {
		fmt.Fprintf(os.Stderr, "testdb setup: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	testdb.Terminate()
	os.Exit(code)
}

// insertChar inserts a `char` row and returns its DB-assigned id.
func insertChar(t *testing.T, gdb *gorm.DB, name, lastMap string, online bool) uint32 {
	t.Helper()
	onlineVal := 0
	if online {
		onlineVal = 1
	}
	err := gdb.Table("char").Create(map[string]any{
		"account_id": testGuildAccountID,
		"char_num":   0,
		"name":       name,
		"class":      1,
		"base_level": 10,
		"last_map":   lastMap,
		"online":     onlineVal,
		"party_id":   0,
		"guild_id":   0,
	}).Error
	if err != nil {
		t.Fatalf("insert char %s: %v", name, err)
	}
	var charID uint32
	if err := gdb.Table("char").Select("char_id").Where("name = ?", name).
		Scan(&charID).Error; err != nil {
		t.Fatalf("read back char %s: %v", name, err)
	}
	t.Cleanup(func() {
		_ = gdb.Table("char").Where("char_id = ?", charID).Delete(nil).Error
	})
	return charID
}

// TestGuild_GORMRoundTrip proves the guild repository's full lifecycle against
// a real DB: create assigns the master's char.guild_id and rejects duplicates,
// the roster reads back member fields with the master flag, joining/leaving
// moves char.guild_id, the guild fills to the cap and stops, a master leaving
// keeps the guild, and the last member out kills it.
func TestGuild_GORMRoundTrip(t *testing.T) {
	gdb := dbForTest(t)
	repo := infra.NewGORMGuildRepository(gdb)
	ctx := context.Background()

	master := insertChar(t, gdb, "GldMaster", "prontera", true)
	friend := insertChar(t, gdb, "GldFriend", "prontera", true)
	faraway := insertChar(t, gdb, "GldFaraway", "geffen", true)

	g, err := repo.Create(ctx, testGuildAccountID, master, "Emperium Crew")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if g.ID == 0 {
		t.Fatalf("create: guild id not populated (auto-increment failed)")
	}
	if g.Master != master || g.MasterNam != "GldMaster" {
		t.Errorf("create master = (%d,%q), want (%d,%q)", g.Master, g.MasterNam, master, "GldMaster")
	}

	// The master's char row must now point at the guild.
	got, err := repo.GetByMember(ctx, master)
	if err != nil {
		t.Fatalf("get by member: %v", err)
	}
	if got.ID != g.ID {
		t.Errorf("get by member: got guild %d, want %d", got.ID, g.ID)
	}

	// Duplicate name and second-guild rejections.
	if _, err := repo.Create(ctx, testGuildAccountID, master, "Other"); !errors.Is(err, domain.ErrAlreadyInGuild) {
		t.Fatalf("second create: got %v, want ErrAlreadyInGuild", err)
	}
	rival := insertChar(t, gdb, "GldRival", "prontera", true)
	if _, err := repo.Create(ctx, testGuildAccountID, rival, "Emperium Crew"); !errors.Is(err, domain.ErrNameExists) {
		t.Fatalf("dup name create: got %v, want ErrNameExists", err)
	}

	// Join friend and faraway.
	for _, charID := range []uint32{friend, faraway} {
		if err := repo.AddMember(ctx, g.ID, charID); err != nil {
			t.Fatalf("add member %d: %v", charID, err)
		}
	}
	members, err := repo.Members(ctx, g.ID)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	if len(members) != 3 {
		t.Fatalf("members: got %d, want 3", len(members))
	}
	var masterSeen bool
	for _, m := range members {
		if m.CharID == master {
			masterSeen = true
			if !m.Master || m.Position != 0 {
				t.Errorf("master flag missing on char %d (master=%v pos=%d)", master, m.Master, m.Position)
			}
			if m.Map != "prontera" || m.BaseLevel != 10 {
				t.Errorf("master row = %+v, want map=prontera base_level=10", m)
			}
		}
	}
	if !masterSeen {
		t.Fatalf("roster missing master char %d", master)
	}

	// A char already in a guild cannot join another (or the same) one.
	if err := repo.AddMember(ctx, g.ID, friend); !errors.Is(err, domain.ErrAlreadyInGuild) {
		t.Fatalf("re-add member: got %v, want ErrAlreadyInGuild", err)
	}

	// Fill to the cap, then the next add is rejected as full.
	for i := len(members); i < domain.MaxGuildSize; i++ {
		extra := insertChar(t, gdb, fmt.Sprintf("GldFill%d", i), "prontera", true)
		if err := repo.AddMember(ctx, g.ID, extra); err != nil {
			t.Fatalf("fill member %d: %v", i, err)
		}
	}
	overflow := insertChar(t, gdb, "GldOverflow", "prontera", true)
	if err := repo.AddMember(ctx, g.ID, overflow); !errors.Is(err, domain.ErrGuildFull) {
		t.Fatalf("add past cap: got %v, want ErrGuildFull", err)
	}

	// A member leave clears only that char's guild_id.
	if err := repo.RemoveMember(ctx, g.ID, friend); err != nil {
		t.Fatalf("remove friend: %v", err)
	}
	if _, err := repo.GetByMember(ctx, friend); !errors.Is(err, domain.ErrNotInGuild) {
		t.Fatalf("friend after leave: got %v, want ErrNotInGuild", err)
	}
	if _, err := repo.Get(ctx, g.ID); err != nil {
		t.Fatalf("guild should survive a member leave: %v", err)
	}

	// Master leaving does NOT disband (rAthena breaks only when empty), but
	// the LAST member out does.
	if err := repo.RemoveMember(ctx, g.ID, master); err != nil {
		t.Fatalf("master leave: %v", err)
	}
	if _, err := repo.Get(ctx, g.ID); err != nil {
		t.Fatalf("guild should survive master leave: %v", err)
	}
	// Drain the remaining roster — the guild dies with its LAST member out,
	// not merely with the founding three.
	remaining, err := repo.Members(ctx, g.ID)
	if err != nil {
		t.Fatalf("remaining roster: %v", err)
	}
	for _, m := range remaining {
		if err := repo.RemoveMember(ctx, g.ID, m.CharID); err != nil {
			t.Fatalf("drain member %d: %v", m.CharID, err)
		}
	}
	if _, err := repo.Get(ctx, g.ID); !errors.Is(err, domain.ErrGuildNotFound) {
		t.Fatalf("guild after last leave: got %v, want ErrGuildNotFound", err)
	}
}

// TestGuild_BreakTeardown pins the disband path: every member unassigned, row
// gone.
func TestGuild_BreakTeardown(t *testing.T) {
	gdb := dbForTest(t)
	repo := infra.NewGORMGuildRepository(gdb)
	ctx := context.Background()

	master := insertChar(t, gdb, "GldBreakM", "prontera", true)
	member := insertChar(t, gdb, "GldBreakF", "prontera", true)
	g, err := repo.Create(ctx, testGuildAccountID, master, "Doomed")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.AddMember(ctx, g.ID, member); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := repo.Break(ctx, g.ID); err != nil {
		t.Fatalf("break: %v", err)
	}
	if _, err := repo.Get(ctx, g.ID); !errors.Is(err, domain.ErrGuildNotFound) {
		t.Fatalf("guild after break: got %v, want ErrGuildNotFound", err)
	}
	for _, charID := range []uint32{master, member} {
		if _, err := repo.GetByMember(ctx, charID); !errors.Is(err, domain.ErrNotInGuild) {
			t.Fatalf("char %d after break: got %v, want ErrNotInGuild", charID, err)
		}
	}
}

// TestGuild_GetMissingReturnsSentinel pins the not-found mapping on the read
// path so callers can branch on the sentinel rather than on a GORM error.
func TestGuild_GetMissingReturnsSentinel(t *testing.T) {
	repo := infra.NewGORMGuildRepository(dbForTest(t))
	if _, err := repo.Get(context.Background(), domain.GuildID(999999)); !errors.Is(err, domain.ErrGuildNotFound) {
		t.Fatalf("Get(missing) = %v, want ErrGuildNotFound", err)
	}
}
