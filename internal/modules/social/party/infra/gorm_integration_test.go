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
	"github.com/bouroo/goAthena/internal/modules/social/party/domain"
	"github.com/bouroo/goAthena/internal/modules/social/party/infra"
)

const testPartyAccountID uint32 = 3000001

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

// insertChar inserts a `char` row for the seeded account and returns its id.
// The id is DB-assigned (postgres IDENTITY GENERATED ALWAYS rejects an explicit
// value), so it is read back through the unique `name`.
func insertChar(t *testing.T, gdb *gorm.DB, name, lastMap string, online bool) uint32 {
	t.Helper()
	onlineVal := 0
	if online {
		onlineVal = 1
	}
	// GORM's Table("char") quotes the reserved word per dialect (backticks on
	// MariaDB, double quotes on postgres), so the statement stays portable.
	err := gdb.Table("char").Create(map[string]any{
		"account_id": testPartyAccountID,
		"char_num":   0,
		"name":       name,
		"class":      1,
		"base_level": 10,
		"last_map":   lastMap,
		"online":     onlineVal,
		"party_id":   0,
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

// TestParty_GORMRoundTrip proves the party repository's full lifecycle against a
// real DB: create assigns the leader's char.party_id, the roster reads back the
// member fields, joining/leaving moves char.party_id, the party fills to the cap
// and stops, and a leader leave disbands every remaining member.
func TestParty_GORMRoundTrip(t *testing.T) {
	gdb := dbForTest(t)
	repo := infra.NewGORMPartyRepository(gdb)
	ctx := context.Background()

	leader := insertChar(t, gdb, "PtyLeader", "prontera", true)
	friend := insertChar(t, gdb, "PtyFriend", "prontera", true)
	faraway := insertChar(t, gdb, "PtyFaraway", "geffen", true)

	p, err := repo.Create(ctx, testPartyAccountID, leader, "Athena")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.ID == 0 {
		t.Fatalf("create: party id not populated (auto-increment failed)")
	}
	if p.LeaderID != testPartyAccountID || p.LeaderChar != leader {
		t.Errorf("create leader = (%d,%d), want (%d,%d)", p.LeaderID, p.LeaderChar, testPartyAccountID, leader)
	}

	// The leader's char row must now point at the party.
	got, err := repo.GetByMember(ctx, leader)
	if err != nil {
		t.Fatalf("get by member: %v", err)
	}
	if got.ID != p.ID {
		t.Errorf("get by member: got party %d, want %d", got.ID, p.ID)
	}

	// A second create for the same char is rejected.
	if _, err := repo.Create(ctx, testPartyAccountID, leader, "Dup"); !errors.Is(err, domain.ErrAlreadyInParty) {
		t.Fatalf("second create: got %v, want ErrAlreadyInParty", err)
	}

	// Join friend and faraway.
	for _, charID := range []uint32{friend, faraway} {
		if err := repo.AddMember(ctx, p.ID, charID); err != nil {
			t.Fatalf("add member %d: %v", charID, err)
		}
	}
	members, err := repo.Members(ctx, p.ID)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	if len(members) != 3 {
		t.Fatalf("members: got %d, want 3", len(members))
	}
	var leaderSeen bool
	for _, m := range members {
		if m.CharID == leader {
			leaderSeen = true
			if !m.Leader {
				t.Errorf("leader flag missing on char %d", leader)
			}
			if m.Map != "prontera" || m.BaseLevel != 10 {
				t.Errorf("leader row = %+v, want map=prontera base_level=10", m)
			}
		}
	}
	if !leaderSeen {
		t.Fatalf("roster missing leader char %d", leader)
	}

	// A char already in a party cannot join another (or the same) one.
	if err := repo.AddMember(ctx, p.ID, friend); !errors.Is(err, domain.ErrAlreadyInParty) {
		t.Fatalf("re-add member: got %v, want ErrAlreadyInParty", err)
	}

	// Fill to the cap, then the next add is rejected as full.
	for i := len(members); i < domain.MaxPartySize; i++ {
		extra := insertChar(t, gdb, fmt.Sprintf("PtyFill%d", i), "prontera", true)
		if err := repo.AddMember(ctx, p.ID, extra); err != nil {
			t.Fatalf("fill member %d: %v", i, err)
		}
	}
	overflow := insertChar(t, gdb, "PtyOverflow", "prontera", true)
	if err := repo.AddMember(ctx, p.ID, overflow); !errors.Is(err, domain.ErrPartyFull) {
		t.Fatalf("add past cap: got %v, want ErrPartyFull", err)
	}

	// Options round-trip.
	if err := repo.SetOptions(ctx, p.ID, 1, 2); err != nil {
		t.Fatalf("set options: %v", err)
	}
	if err := repo.SetOptions(ctx, domain.PartyID(999999), 1, 0); !errors.Is(err, domain.ErrPartyNotFound) {
		t.Fatalf("set options on missing party: got %v, want ErrPartyNotFound", err)
	}

	// A non-leader leave clears only that char's party_id.
	if err := repo.RemoveMember(ctx, p.ID, friend); err != nil {
		t.Fatalf("remove friend: %v", err)
	}
	if _, err := repo.GetByMember(ctx, friend); !errors.Is(err, domain.ErrNotInParty) {
		t.Fatalf("friend after leave: got %v, want ErrNotInParty", err)
	}
	if _, err := repo.Get(ctx, p.ID); err != nil {
		t.Fatalf("party should survive a non-leader leave: %v", err)
	}

	// The leader leaving disbands: party row gone, every member unassigned.
	if err := repo.RemoveMember(ctx, p.ID, leader); err != nil {
		t.Fatalf("leader leave: %v", err)
	}
	if _, err := repo.Get(ctx, p.ID); !errors.Is(err, domain.ErrPartyNotFound) {
		t.Fatalf("party after leader leave: got %v, want ErrPartyNotFound", err)
	}
	if _, err := repo.GetByMember(ctx, faraway); !errors.Is(err, domain.ErrNotInParty) {
		t.Fatalf("faraway after disband: got %v, want ErrNotInParty", err)
	}
}

// TestParty_GetMissingReturnsSentinel pins the not-found mapping on the read
// path so callers can branch on the sentinel rather than on a GORM error.
func TestParty_GetMissingReturnsSentinel(t *testing.T) {
	repo := infra.NewGORMPartyRepository(dbForTest(t))
	if _, err := repo.Get(context.Background(), domain.PartyID(999999)); !errors.Is(err, domain.ErrPartyNotFound) {
		t.Fatalf("Get(missing) = %v, want ErrPartyNotFound", err)
	}
}
