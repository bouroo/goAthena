//go:build integration

package infra_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/infrastructure/db"
	"github.com/bouroo/goAthena/internal/infra/testdb"
	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	"github.com/bouroo/goAthena/internal/modules/character/infra"
)

// dbForTest opens the GORM connection described by the testdb-harness DB_* env,
// so the character GORM repo runs against a real containerized MariaDB or
// postgres. Mirrors account/infra/gorm_integration_test.go's testDB.
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
// testdb harness, migrates the schema, seeds a parent login row, then runs the
// suite. Picks the engine from DB_DRIVER (default mariadb).
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
	if err := seedParentLogin(context.Background(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "seed login: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	testdb.Terminate()
	os.Exit(code)
}

// seedParentLogin inserts the account the characters below reference. Mirrors
// account/infra's seed (engine-specific MD5 + postgres OVERRIDING SYSTEM VALUE).
func seedParentLogin(ctx context.Context, cfg config.DBConfig) error {
	gdb, err := db.New(cfg)
	if err != nil {
		return fmt.Errorf("open seed db: %w", err)
	}
	defer func() { _ = db.Close(gdb) }()

	const mariadbSeed = `INSERT INTO login (account_id, userid, user_pass, sex, state, unban_time, expiration_time)
		VALUES (2000001, 'testacc', MD5('s3cret'), 'M', 0, 0, 0)`
	const postgresSeed = `INSERT INTO login (account_id, userid, user_pass, sex, state, unban_time, expiration_time)
		OVERRIDING SYSTEM VALUE
		VALUES (2000001, 'testacc', md5('s3cret'), 'M', 0, 0, 0)`

	seed := mariadbSeed
	if cfg.Driver == "postgres" {
		seed = postgresSeed
	}
	if err := gdb.WithContext(ctx).Exec(seed).Error; err != nil {
		return fmt.Errorf("seed login: %w", err)
	}
	return nil
}

// TestChar_GORMReservedWordRoundTrip proves the character repo's reserved-word
// read path round-trips against a real DB — the core #12 risk: the `char`
// table has MariaDB-reserved column names `int` and `rename`. A raw SELECT that
// fails to back-quote them errors with 1064 (mariadb) / 42703-ish (postgres);
// only a real container catches it. Create writes them, FindByID reads them
// back, and the assertions confirm GORM's column quoting holds on both engines.
func TestChar_GORMReservedWordRoundTrip(t *testing.T) {
	repo := infra.NewGORMCharacterRepository(dbForTest(t))
	ctx := context.Background()

	const (
		wantInt    uint16 = 9
		wantRename uint16 = 1
		wantStr    uint16 = 5
	)
	created, err := repo.Create(ctx, chardomain.Character{
		AccountID: 2000001,
		CharNum:   0,
		Name:      "HeroInt",
		BaseLevel: 1, JobLevel: 1,
		MaxHP: 1000, HP: 1000, MaxSP: 50, SP: 50,
		Str: wantStr, Int: wantInt, Rename: wantRename,
		LastMap: "new_1-1", SaveMap: "new_1-1",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.FindByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("find by id: %v", err)
	}
	if got.Int != wantInt {
		t.Errorf("reserved-word `int` round-trip: got %d, want %d (GORM failed to quote the column)", got.Int, wantInt)
	}
	if got.Rename != wantRename {
		t.Errorf("reserved-word `rename` round-trip: got %d, want %d (GORM failed to quote the column)", got.Rename, wantRename)
	}
	if got.Str != wantStr {
		t.Errorf("non-reserved `str` round-trip: got %d, want %d", got.Str, wantStr)
	}

	chars, err := repo.ListByAccount(ctx, 2000001)
	if err != nil {
		t.Fatalf("list by account: %v", err)
	}
	if len(chars) != 1 || chars[0].ID != created.ID {
		t.Fatalf("list by account: got %d chars, want 1 with id %d", len(chars), created.ID)
	}
}
