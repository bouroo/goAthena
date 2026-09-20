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
	"github.com/bouroo/goAthena/internal/modules/content/quest/domain"
	"github.com/bouroo/goAthena/internal/modules/content/quest/infra"
)

// Identifier of the seeded parent row the quest rows reference.
const (
	testQuestAccountID uint32 = 2000001 // seeded login.account_id
	testQuestCharID    uint32 = 1500001 // seeded char.char_id
)

// dbForTest opens the GORM connection described by the testdb-harness DB_* env.
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

// TestMain provisions a real MariaDB or postgres container via the shared
// testdb harness, migrates the schema, seeds the parent login + char rows the
// quest rows reference, then runs the suite.
func TestMain(m *testing.M) {
	driver := os.Getenv("DB_DRIVER")
	if driver == "" {
		driver = "mariadb"
	}
	cfg, err := testdb.Setup(driver)
	if err != nil {
		fmt.Fprintf(os.Stderr, "testdb setup: %v\n", err)
		os.Exit(1)
	}
	if err := seedParentRows(context.Background(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "seed parent rows: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	testdb.Terminate()
	os.Exit(code)
}

// seedParentRows inserts the account and character the quest rows reference.
func seedParentRows(ctx context.Context, cfg config.DBConfig) error {
	gdb, err := db.New(cfg)
	if err != nil {
		return fmt.Errorf("open seed db: %w", err)
	}
	defer func() { _ = db.Close(gdb) }()

	loginMariadb := fmt.Sprintf(`INSERT INTO login (account_id, userid, user_pass, sex, state, unban_time, expiration_time)
		VALUES (%d, 'questacc', MD5('s3cret'), 'M', 0, 0, 0)`, testQuestAccountID)
	loginPostgres := fmt.Sprintf(`INSERT INTO login (account_id, userid, user_pass, sex, state, unban_time, expiration_time)
		OVERRIDING SYSTEM VALUE
		VALUES (%d, 'questacc', md5('s3cret'), 'M', 0, 0, 0)`, testQuestAccountID)

	charMariadb := "INSERT INTO `char` (char_id, account_id, char_num, name, class, base_level, job_level) " +
		fmt.Sprintf("VALUES (%d, %d, 0, 'QuestHero', 0, 1, 1)", testQuestCharID, testQuestAccountID)
	charPostgres := fmt.Sprintf(`INSERT INTO "char" (char_id, account_id, char_num, name, class, base_level, job_level)
		OVERRIDING SYSTEM VALUE
		VALUES (%d, %d, 0, 'QuestHero', 0, 1, 1)`, testQuestCharID, testQuestAccountID)

	stmts := []string{loginMariadb, charMariadb}
	if cfg.Driver == "postgres" {
		stmts = []string{loginPostgres, charPostgres}
	}
	for _, s := range stmts {
		if err := gdb.WithContext(ctx).Exec(s).Error; err != nil {
			return fmt.Errorf("seed parent rows: %w", err)
		}
	}
	return nil
}

// TestQuest_GORMRoundTrip proves the quest repo's write/read path round-trips
// against a real DB — the core #7 risk for the quest bounded context. Set
// upserts, Get returns the persisted value, ListByChar enumerates, and a
// non-existent key returns (zero, false) without error.
func TestQuest_GORMRoundTrip(t *testing.T) {
	repo := infra.NewGORMQuestRepository(dbForTest(t))
	ctx := context.Background()

	// Initial Get on a fresh key returns false.
	_, ok, err := repo.Get(ctx, testQuestCharID, "Healer", "KillCount")
	if err != nil {
		t.Fatalf("get fresh: %v", err)
	}
	if ok {
		t.Errorf("get fresh: ok=true, want false")
	}

	// Set then Get returns the value.
	if err := repo.Set(ctx, testQuestCharID, "Healer", "KillCount", "7"); err != nil {
		t.Fatalf("set: %v", err)
	}
	row, ok, err := repo.Get(ctx, testQuestCharID, "Healer", "KillCount")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !ok {
		t.Fatalf("get: ok=false after set")
	}
	if row.Value != "7" {
		t.Errorf("value = %q, want %q", row.Value, "7")
	}

	// Set upserts over the existing row.
	if err := repo.Set(ctx, testQuestCharID, "Healer", "KillCount", "12"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	row, _, _ = repo.Get(ctx, testQuestCharID, "Healer", "KillCount")
	if row.Value != "12" {
		t.Errorf("value after upsert = %q, want %q", row.Value, "12")
	}

	// A second key on the same char.
	if err := repo.Set(ctx, testQuestCharID, "QuestGiver", "Step", "3"); err != nil {
		t.Fatalf("set 2: %v", err)
	}

	// ListByChar returns both rows.
	rows, err := repo.ListByChar(ctx, testQuestCharID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("list count = %d, want 2", len(rows))
	}

	// Different char sees nothing.
	rows, err = repo.ListByChar(ctx, testQuestCharID+1)
	if err != nil {
		t.Fatalf("list other: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("list other count = %d, want 0", len(rows))
	}
}

// guard against the domain package being unused in this build.
var (
	_ = errors.New
	_ = domain.ErrQuestInvalidName
)
