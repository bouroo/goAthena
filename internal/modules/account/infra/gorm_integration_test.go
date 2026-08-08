//go:build integration

package infra_test

import (
	"context"
	"os"
	"testing"

	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/config"
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
		Port:     13306,
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
