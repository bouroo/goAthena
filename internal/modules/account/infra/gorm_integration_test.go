//go:build integration

package infra_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"

	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/infra/testdb"
	"github.com/bouroo/goAthena/internal/infrastructure/db"
	"github.com/bouroo/goAthena/internal/modules/account/app"
	"github.com/bouroo/goAthena/internal/modules/account/infra"
)

// testDB opens the GORM connection described by the integration env (or the
// smoke defaults) so the account GORM repo + auth service run against a real
// MariaDB. Requires the `login` table (goathena migrate up) and a seeded row.
func testDB(t *testing.T) *gorm.DB {
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
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// TestMain provisions one DB container for the whole package (default MariaDB;
// set DB_DRIVER=postgres to exercise PostgreSQL) and seeds the row the tests
// below authenticate against. Without it the live tests would skip/fail on a
// missing connection; with it they run against a real engine.
func TestMain(m *testing.M) {
	driver := os.Getenv("DB_DRIVER")
	if driver == "" {
		driver = "mariadb"
	}
	cfg, err := testdb.Setup(driver)
	if err != nil {
		fmt.Println("testdb setup:", err)
		os.Exit(1)
	}
	if err := seedLogin(context.Background(), cfg); err != nil {
		fmt.Println("testdb seed:", err)
		testdb.Terminate()
		os.Exit(1)
	}
	code := m.Run()
	testdb.Terminate()
	os.Exit(code)
}

// seedLogin inserts the account the integration tests authenticate against: a
// fixed account_id 2000001 with user_pass set to the engine's MD5 of "s3cret",
// matching rAthena's use_MD5_passwords storage (lowercase hex) so the MD5 auth
// path accepts it. PostgreSQL's GENERATED ALWAYS identity needs
// OVERRIDING SYSTEM VALUE to accept an explicit account_id.
func seedLogin(ctx context.Context, cfg config.DBConfig) error {
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

func TestGORMRepo_FindByUserID_Live(t *testing.T) {
	repo := infra.NewGORMAccountRepository(testDB(t))
	acc, err := repo.FindByUserID(context.Background(), "testacc")
	if err != nil {
		t.Fatalf("FindByUserID: %v", err)
	}
	if acc.UserID != "testacc" {
		t.Errorf("userid = %q", acc.UserID)
	}
	if acc.ID != 2000001 {
		t.Errorf("account_id = %d, want 2000001", acc.ID)
	}
}

func TestAuthService_Authenticate_LiveMD5(t *testing.T) {
	repo := infra.NewGORMAccountRepository(testDB(t))
	svc := app.NewAuthService(repo, true) // use_MD5_passwords

	acc, id1, id2, err := svc.Authenticate(context.Background(), "testacc", "s3cret", "10.0.0.1")
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if acc.ID != 2000001 {
		t.Errorf("account_id = %d", acc.ID)
	}
	if id1 == 0 && id2 == 0 {
		t.Error("expected non-zero session tokens")
	}
}
