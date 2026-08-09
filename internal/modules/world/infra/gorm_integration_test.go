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
	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	charinfra "github.com/bouroo/goAthena/internal/modules/character/infra"
	worlddomain "github.com/bouroo/goAthena/internal/modules/world/domain"
	worldinfra "github.com/bouroo/goAthena/internal/modules/world/infra"
)

// dbForTest opens the GORM connection described by the testdb-harness DB_* env,
// so the world GORM repo runs against a real containerized MariaDB or postgres.
// Mirrors character/infra/gorm_integration_test.go's testDB.
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
// harness, migrates the schema, seeds a parent login row, then runs the suite.
// Picks the engine from DB_DRIVER (default mariadb).
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

// seedChar inserts a char row via the proven character repo and returns its
// char_id, so the world repo (which reads/updates the same char table) has a
// real row to exercise. The world repo never owns char creation; it only reads
// map-enter state and writes position/online back.
func seedChar(t *testing.T, gdb *gorm.DB, name string, pos worlddomain.Position) uint32 {
	t.Helper()
	repo := charinfra.NewGORMCharacterRepository(gdb)
	c, err := repo.Create(context.Background(), chardomain.Character{
		AccountID: 2000001,
		CharNum:   0,
		Name:      name,
		BaseLevel: 1, JobLevel: 1,
		MaxHP: 1000, HP: 800, MaxSP: 50, SP: 40,
		Str: 5, Int: 7, Vit: 4, Agi: 3, Dex: 2, Luk: 1, Rename: 1,
		LastMap: "new_1-1", LastX: uint16(pos.X), LastY: uint16(pos.Y),
	})
	if err != nil {
		t.Fatalf("seed char %q: %v", name, err)
	}
	t.Cleanup(func() { _ = repo.Delete(context.Background(), c.ID, 2000001) })
	return uint32(c.ID)
}

// TestWorld_GORMLoadEnterState proves the world repo's map-enter read path
// round-trips against a real DB — the core #12 risk: the char table's `int`
// column is a MariaDB reserved word. A raw SELECT that fails to back-quote it
// errors 1064 (mariadb) / 42703-ish (postgres); only a real container catches it.
// The character repo writes `int`, LoadEnterState reads it back, and the
// assertion confirms GORM's column quoting holds on both engines.
func TestWorld_GORMLoadEnterState(t *testing.T) {
	worldRepo := worldinfra.NewGORMWorldRepository(dbForTest(t))
	ctx := context.Background()

	charID := seedChar(t, dbForTest(t), "WorldEnter", worlddomain.Position{X: 53, Y: 111})

	const wantInt uint16 = 7
	got, err := worldRepo.LoadEnterState(ctx, charID)
	if err != nil {
		t.Fatalf("load enter state: %v", err)
	}
	if got.Account != 2000001 {
		t.Errorf("account: got %d, want 2000001", got.Account)
	}
	if got.Map != "new_1-1" {
		t.Errorf("map: got %q, want new_1-1", got.Map)
	}
	if got.Pos.X != 53 || got.Pos.Y != 111 {
		t.Errorf("pos: got {%d,%d}, want {53,111}", got.Pos.X, got.Pos.Y)
	}
	if got.Int != wantInt {
		t.Errorf("reserved-word `int` round-trip: got %d, want %d (GORM failed to quote the column)", got.Int, wantInt)
	}
	if got.Str != 5 {
		t.Errorf("non-reserved `str` round-trip: got %d, want 5", got.Str)
	}
	if got.Name != "WorldEnter" {
		t.Errorf("name: got %q, want WorldEnter", got.Name)
	}
	if got.HP != 800 || got.MaxHP != 1000 {
		t.Errorf("hp/maxhp: got %d/%d, want 800/1000", got.HP, got.MaxHP)
	}
}

// TestWorld_GORMLoadEnterStateNotFound confirms a missing char surfaces the
// domain sentinel rather than an opaque GORM error.
func TestWorld_GORMLoadEnterStateNotFound(t *testing.T) {
	worldRepo := worldinfra.NewGORMWorldRepository(dbForTest(t))
	ctx := context.Background()

	_, err := worldRepo.LoadEnterState(ctx, 999999999) // nonexistent char_id
	if !errors.Is(err, worlddomain.ErrEntityNotFound) {
		t.Fatalf("got %v, want ErrEntityNotFound", err)
	}
}

// TestWorld_GORMSetPosition proves the warp/transit write path persists the
// destination map + cell into the char table, and the next LoadEnterState
// reflects it.
func TestWorld_GORMSetPosition(t *testing.T) {
	worldRepo := worldinfra.NewGORMWorldRepository(dbForTest(t))
	ctx := context.Background()

	charID := seedChar(t, dbForTest(t), "WorldWarp", worlddomain.Position{X: 53, Y: 111})

	want := worlddomain.Position{X: 100, Y: 200}
	if err := worldRepo.SetPosition(ctx, charID, "prontera", want); err != nil {
		t.Fatalf("set position: %v", err)
	}
	got, err := worldRepo.LoadEnterState(ctx, charID)
	if err != nil {
		t.Fatalf("load enter state: %v", err)
	}
	if got.Map != "prontera" {
		t.Errorf("map after set position: got %q, want prontera", got.Map)
	}
	if got.Pos.X != want.X || got.Pos.Y != want.Y {
		t.Errorf("pos after set position: got {%d,%d}, want {%d,%d}", got.Pos.X, got.Pos.Y, want.X, want.Y)
	}
}

// TestWorld_GORMSetOnline proves the online/last-position write path persists the
// updated cell, and the next LoadEnterState reflects it.
func TestWorld_GORMSetOnline(t *testing.T) {
	worldRepo := worldinfra.NewGORMWorldRepository(dbForTest(t))
	ctx := context.Background()

	charID := seedChar(t, dbForTest(t), "WorldOnline", worlddomain.Position{X: 53, Y: 111})

	want := worlddomain.Position{X: 50, Y: 60}
	if err := worldRepo.SetOnline(ctx, charID, true, want); err != nil {
		t.Fatalf("set online: %v", err)
	}
	got, err := worldRepo.LoadEnterState(ctx, charID)
	if err != nil {
		t.Fatalf("load enter state: %v", err)
	}
	if got.Pos.X != want.X || got.Pos.Y != want.Y {
		t.Errorf("pos after set online: got {%d,%d}, want {%d,%d}", got.Pos.X, got.Pos.Y, want.X, want.Y)
	}
}
