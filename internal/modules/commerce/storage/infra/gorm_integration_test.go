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
	"github.com/bouroo/goAthena/internal/modules/commerce/storage/domain"
	"github.com/bouroo/goAthena/internal/modules/commerce/storage/infra"
)

// Identifiers of the seeded parent rows. storage.account_id references the
// `login` row below.
const (
	testStorageAccountID uint32 = 2000001 // seeded login.account_id
	testStorageNameID    uint32 = 501     // rAthena Red Potion nameid
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
// testdb harness, migrates the schema, seeds the parent login row the storage
// rows reference, then runs the suite.
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

// seedParentRows inserts the account the storage rows reference.
func seedParentRows(ctx context.Context, cfg config.DBConfig) error {
	gdb, err := db.New(cfg)
	if err != nil {
		return fmt.Errorf("open seed db: %w", err)
	}
	defer func() { _ = db.Close(gdb) }()

	loginMariadb := fmt.Sprintf(`INSERT INTO login (account_id, userid, user_pass, sex, state, unban_time, expiration_time)
		VALUES (%d, 'stortestacc', MD5('s3cret'), 'M', 0, 0, 0)`, testStorageAccountID)
	loginPostgres := fmt.Sprintf(`INSERT INTO login (account_id, userid, user_pass, sex, state, unban_time, expiration_time)
		OVERRIDING SYSTEM VALUE
		VALUES (%d, 'stortestacc', md5('s3cret'), 'M', 0, 0, 0)`, testStorageAccountID)

	stmts := []string{loginMariadb}
	if cfg.Driver == "postgres" {
		stmts = []string{loginPostgres}
	}
	for _, s := range stmts {
		if err := gdb.WithContext(ctx).Exec(s).Error; err != nil {
			return fmt.Errorf("seed parent rows: %w", err)
		}
	}
	return nil
}

// TestStorage_GORMRoundTrip proves the storage repo's write/read path
// round-trips against a real DB. Add inserts a stackable item, LoadByAccount
// reads it back with the right amount, Remove decrements a partial stack and
// finally deletes the row at zero. Stackable items merge into the existing row
// on a second Add; equipment inserts a separate row. Error paths return the
// domain sentinels.
func TestStorage_GORMRoundTrip(t *testing.T) {
	repo := infra.NewGORMStorageRepository(dbForTest(t))
	ctx := context.Background()

	// Add a stackable item.
	item, err := repo.Add(ctx, testStorageAccountID, testStorageNameID, 10)
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if item.ID == 0 {
		t.Fatalf("add: id not populated (auto-increment failed)")
	}
	if item.AccountID != testStorageAccountID {
		t.Errorf("add account_id: got %d, want %d", item.AccountID, testStorageAccountID)
	}
	if item.NameID != testStorageNameID {
		t.Errorf("add nameid: got %d, want %d", item.NameID, testStorageNameID)
	}
	if item.Amount != 10 {
		t.Errorf("add amount: got %d, want 10", item.Amount)
	}

	// Stackable second Add merges into the same row.
	stacked, err := repo.Add(ctx, testStorageAccountID, testStorageNameID, 5)
	if err != nil {
		t.Fatalf("add stacked: %v", err)
	}
	if stacked.ID != item.ID {
		t.Errorf("stackable add: got id=%d, want %d (merge)", stacked.ID, item.ID)
	}
	if stacked.Amount != 15 {
		t.Errorf("stackable add: got amount=%d, want 15", stacked.Amount)
	}

	// LoadByAccount returns exactly that row.
	items, err := repo.LoadByAccount(ctx, testStorageAccountID)
	if err != nil {
		t.Fatalf("load by account: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("load by account: got %d items, want 1", len(items))
	}
	if items[0].ID != item.ID || items[0].NameID != testStorageNameID || items[0].Amount != 15 {
		t.Errorf("load by account: got id=%d nameid=%d amount=%d, want id=%d nameid=%d amount=15",
			items[0].ID, items[0].NameID, items[0].Amount, item.ID, testStorageNameID)
	}

	// Remove part of the stack; remaining amount reflects the decrement.
	if err := repo.Remove(ctx, item.ID, 4); err != nil {
		t.Fatalf("remove partial: %v", err)
	}
	items, err = repo.LoadByAccount(ctx, testStorageAccountID)
	if err != nil {
		t.Fatalf("load after partial remove: %v", err)
	}
	if len(items) != 1 || items[0].Amount != 11 {
		t.Fatalf("after partial remove: got %d items, amount=%d, want 1 item amount=11", len(items), amountOr(items))
	}

	// Remove the rest -> row deleted, LoadByAccount sees none.
	if err := repo.Remove(ctx, item.ID, 11); err != nil {
		t.Fatalf("remove rest: %v", err)
	}
	items, err = repo.LoadByAccount(ctx, testStorageAccountID)
	if err != nil {
		t.Fatalf("load after final remove: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("after final remove: got %d items, want 0", len(items))
	}

	// Equipment never stacks: same NameID with Equip!=0 inserts a new row.
	eqRow, err := repo.Add(ctx, testStorageAccountID, testStorageNameID, 1)
	if err != nil {
		t.Fatalf("add equipment: %v", err)
	}
	if err := repo.SetEquip(ctx, eqRow.ID, 0x0001); err != nil {
		t.Fatalf("set equip: %v", err)
	}
	eqRow2, err := repo.Add(ctx, testStorageAccountID, testStorageNameID, 1)
	if err != nil {
		t.Fatalf("add equipment 2: %v", err)
	}
	if eqRow.ID == eqRow2.ID {
		t.Errorf("equipment should not stack: got same id %d", eqRow.ID)
	}

	// Error paths against a fresh item: over-remove then not-found.
	item2, err := repo.Add(ctx, testStorageAccountID, testStorageNameID, 3)
	if err != nil {
		t.Fatalf("add second: %v", err)
	}
	if err := repo.Remove(ctx, item2.ID, 5); !errors.Is(err, domain.ErrStorageInsufficientAmount) {
		t.Fatalf("remove over amount: got %v, want ErrStorageInsufficientAmount", err)
	}
	if err := repo.Remove(ctx, item2.ID, 3); err != nil {
		t.Fatalf("remove exact: %v", err)
	}
	if err := repo.Remove(ctx, item2.ID, 1); !errors.Is(err, domain.ErrStorageItemNotFound) {
		t.Fatalf("remove missing id: got %v, want ErrStorageItemNotFound", err)
	}
}

func amountOr(items []domain.StorageItem) uint32 {
	if len(items) == 1 {
		return items[0].Amount
	}
	return 0
}
