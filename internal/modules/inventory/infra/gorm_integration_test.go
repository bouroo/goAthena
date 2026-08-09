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
	"github.com/bouroo/goAthena/internal/modules/inventory/domain"
	"github.com/bouroo/goAthena/internal/modules/inventory/infra"
)

// Identifiers of the seeded parent rows. inventory.char_id references the
// `char` row below (an index, not a hard FK, but seeded for consistency), which
// in turn references the `login` account.
const (
	testAccountID uint32 = 2000001 // seeded login.account_id
	testCharID    uint32 = 1500001 // seeded char.char_id
	testNameID    uint32 = 501     // rAthena Red Potion nameid
)

// dbForTest opens the GORM connection described by the testdb-harness DB_* env,
// so the inventory GORM repo runs against a real containerized MariaDB or
// postgres. Mirrors character/infra/gorm_integration_test.go's dbForTest.
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
// inventory rows reference, then runs the suite. Picks the engine from
// DB_DRIVER (default mariadb).
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

// seedParentRows inserts the account and character the inventory rows reference.
// Mirrors account/infra's engine-specific seed: mariadb MD5(...) + postgres
// md5(...)/OVERRIDING SYSTEM VALUE for the identity columns.
func seedParentRows(ctx context.Context, cfg config.DBConfig) error {
	gdb, err := db.New(cfg)
	if err != nil {
		return fmt.Errorf("open seed db: %w", err)
	}
	defer func() { _ = db.Close(gdb) }()

	// login row.
	loginMariadb := fmt.Sprintf(`INSERT INTO login (account_id, userid, user_pass, sex, state, unban_time, expiration_time)
		VALUES (%d, 'testacc', MD5('s3cret'), 'M', 0, 0, 0)`, testAccountID)
	loginPostgres := fmt.Sprintf(`INSERT INTO login (account_id, userid, user_pass, sex, state, unban_time, expiration_time)
		OVERRIDING SYSTEM VALUE
		VALUES (%d, 'testacc', md5('s3cret'), 'M', 0, 0, 0)`, testAccountID)

	// char row. `char` is a MariaDB reserved word (the CHAR data type), so the
	// table name is back-quoted; postgres double-quotes it and overrides the
	// GENERATED ALWAYS AS IDENTITY column.
	charMariadb := "INSERT INTO `char` (char_id, account_id, char_num, name, class, base_level, job_level) " +
		fmt.Sprintf("VALUES (%d, %d, 0, 'TestHero', 0, 1, 1)", testCharID, testAccountID)
	charPostgres := fmt.Sprintf(`INSERT INTO "char" (char_id, account_id, char_num, name, class, base_level, job_level)
		OVERRIDING SYSTEM VALUE
		VALUES (%d, %d, 0, 'TestHero', 0, 1, 1)`, testCharID, testAccountID)

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

// TestInventory_GORMRoundTrip proves the inventory repo's write/read path
// round-trips against a real DB — the core #12 risk for the inventory bounded
// context. Add inserts a stacked item, LoadByChar reads it back with the right
// amount, Remove decrements a partial stack and finally deletes the row at
// zero, and the error paths (over-remove / missing-id) return the domain
// sentinels. Only a real containerized engine catches a GORM column/tag mismatch
// or a reserved-word quoting regression on both MariaDB and postgres.
func TestInventory_GORMRoundTrip(t *testing.T) {
	repo := infra.NewGORMItemRepository(dbForTest(t))
	ctx := context.Background()

	// Add a stackable item.
	item, err := repo.Add(ctx, testCharID, testNameID, 10)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if item.ID == 0 {
		t.Fatalf("add: id not populated (auto-increment failed)")
	}
	if item.CharID != testCharID {
		t.Errorf("add char_id: got %d, want %d", item.CharID, testCharID)
	}
	if item.NameID != testNameID {
		t.Errorf("add nameid: got %d, want %d", item.NameID, testNameID)
	}
	if item.Amount != 10 {
		t.Errorf("add amount: got %d, want 10", item.Amount)
	}

	// LoadByChar returns exactly that row.
	items, err := repo.LoadByChar(ctx, testAccountID, testCharID)
	if err != nil {
		t.Fatalf("load by char: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("load by char: got %d items, want 1", len(items))
	}
	if items[0].ID != item.ID || items[0].NameID != testNameID || items[0].Amount != 10 {
		t.Errorf("load by char: got id=%d nameid=%d amount=%d, want id=%d nameid=%d amount=10",
			items[0].ID, items[0].NameID, items[0].Amount, item.ID, testNameID)
	}

	// Remove part of the stack; remaining amount reflects the decrement.
	if err := repo.Remove(ctx, item.ID, 4); err != nil {
		t.Fatalf("remove partial: %v", err)
	}
	items, err = repo.LoadByChar(ctx, testAccountID, testCharID)
	if err != nil {
		t.Fatalf("load after partial remove: %v", err)
	}
	if len(items) != 1 || items[0].Amount != 6 {
		t.Fatalf("after partial remove: got %d items, amount=%d, want 1 item amount=6", len(items), itemOrAmount(items))
	}

	// Remove the rest -> row deleted, LoadByChar sees none.
	if err := repo.Remove(ctx, item.ID, 6); err != nil {
		t.Fatalf("remove rest: %v", err)
	}
	items, err = repo.LoadByChar(ctx, testAccountID, testCharID)
	if err != nil {
		t.Fatalf("load after final remove: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("after final remove: got %d items, want 0", len(items))
	}

	// Error paths against a fresh item: over-remove then not-found.
	item2, err := repo.Add(ctx, testCharID, testNameID, 3)
	if err != nil {
		t.Fatalf("add second: %v", err)
	}
	if err := repo.Remove(ctx, item2.ID, 5); !errors.Is(err, domain.ErrInsufficientAmount) {
		t.Fatalf("remove over amount: got %v, want ErrInsufficientAmount", err)
	}
	if err := repo.Remove(ctx, item2.ID, 3); err != nil {
		t.Fatalf("remove exact: %v", err)
	}
	if err := repo.Remove(ctx, item2.ID, 1); !errors.Is(err, domain.ErrItemNotFound) {
		t.Fatalf("remove missing id: got %v, want ErrItemNotFound", err)
	}
}

// itemOrAmount is a tiny helper so the "after partial remove" diagnostic prints
// the remaining amount when a row exists and a clear marker otherwise.
func itemOrAmount(items []domain.Item) uint32 {
	if len(items) == 1 {
		return items[0].Amount
	}
	return 0
}
