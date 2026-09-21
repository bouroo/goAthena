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
	"github.com/bouroo/goAthena/internal/modules/social/friend/domain"
	"github.com/bouroo/goAthena/internal/modules/social/friend/infra"
)

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
func insertChar(t *testing.T, gdb *gorm.DB, name string, online bool) uint32 {
	t.Helper()
	onlineVal := 0
	if online {
		onlineVal = 1
	}
	// GORM's Table("char") quotes the reserved word per dialect (backticks on
	// MariaDB, double quotes on postgres), so the statement stays portable.
	err := gdb.Table("char").Create(map[string]any{
		"account_id": 3000001,
		"char_num":   0,
		"name":       name,
		"class":      1,
		"base_level": 10,
		"last_map":   "prontera",
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
		_ = gdb.Table("friends").Where("char_id = ? OR friend_id = ?", charID, charID).
			Delete(nil).Error
	})
	return charID
}

// TestFriend_GORMRoundTrip proves the friend repository's full lifecycle
// against a real DB: Add inserts BOTH directions, List joins the friend's char
// row (account, name, online), Remove deletes both directions, and the
// sentinels fire (duplicate, missing, both caps).
func TestFriend_GORMRoundTrip(t *testing.T) {
	gdb := dbForTest(t)
	repo := infra.NewGORMFriendRepository(gdb)
	ctx := context.Background()

	hero := insertChar(t, gdb, "FriHero", true)
	partner := insertChar(t, gdb, "FriPartner", true)
	offline := insertChar(t, gdb, "FriOffline", false)

	// Add: both directions readable.
	if err := repo.Add(ctx, hero, partner); err != nil {
		t.Fatalf("add hero-partner: %v", err)
	}
	if err := repo.Add(ctx, partner, offline); err != nil {
		t.Fatalf("add partner-offline: %v", err)
	}
	friends, err := repo.List(ctx, hero)
	if err != nil {
		t.Fatalf("list hero: %v", err)
	}
	if len(friends) != 1 {
		t.Fatalf("hero friends = %d, want 1", len(friends))
	}
	f := friends[0]
	if f.FriendCharID != partner || f.Name != "FriPartner" || !f.Online {
		t.Errorf("hero friend = %+v, want (partner, FriPartner, online)", f)
	}
	friends, err = repo.List(ctx, partner)
	if err != nil {
		t.Fatalf("list partner: %v", err)
	}
	if len(friends) != 2 {
		t.Fatalf("partner friends = %d, want 2 (both directions stored)", len(friends))
	}

	// Duplicate add is rejected.
	if err := repo.Add(ctx, hero, partner); !errors.Is(err, domain.ErrFriendExists) {
		t.Errorf("duplicate add: err = %v, want ErrFriendExists", err)
	}

	// Remove deletes BOTH directions.
	if err := repo.Remove(ctx, hero, partner); err != nil {
		t.Fatalf("remove: %v", err)
	}
	friends, _ = repo.List(ctx, hero)
	if len(friends) != 0 {
		t.Errorf("hero friends after remove = %d, want 0", len(friends))
	}
	friends, _ = repo.List(ctx, partner)
	if len(friends) != 1 {
		t.Errorf("partner friends after remove = %d, want 1 (offline row untouched)", len(friends))
	}
	if err := repo.Remove(ctx, hero, partner); !errors.Is(err, domain.ErrFriendNotFound) {
		t.Errorf("repeat remove: err = %v, want ErrFriendNotFound", err)
	}

	// Fill hero's list to the cap: the next add is the requester-full sentinel.
	fillers := make([]uint32, 0, domain.MaxFriends)
	for i := 0; i < domain.MaxFriends; i++ {
		fillers = append(fillers, insertChar(t, gdb, fmt.Sprintf("FriFiller%02d", i), false))
	}
	for _, filler := range fillers {
		if err := repo.Add(ctx, filler, hero); err != nil {
			t.Fatalf("fill hero list: %v", err)
		}
	}
	if err := repo.Add(ctx, hero, partner); !errors.Is(err, domain.ErrFriendListFull) {
		t.Errorf("full requester: err = %v, want ErrFriendListFull", err)
	}
	// An orphaned pair (char row deleted) is skipped by the read, not surfaced.
	dead := insertChar(t, gdb, "FriDead", false)
	if err := repo.Add(ctx, partner, dead); err != nil {
		t.Fatalf("add dead pair: %v", err)
	}
	deadList, err := repo.List(ctx, dead)
	if err != nil {
		t.Fatalf("list dead: %v", err)
	}
	// Delete the FRIEND's char row, orphaning the pair dead→partner.
	_ = gdb.Table("char").Where("char_id = ?", partner).Delete(nil).Error
	afterList, err := repo.List(ctx, dead)
	if err != nil {
		t.Fatalf("list after char delete: %v", err)
	}
	if len(deadList) != 1 || len(afterList) != 0 {
		t.Errorf("dead-side list before/after char delete = %d/%d, want 1/0 (orphan skipped)",
			len(deadList), len(afterList))
	}
}
