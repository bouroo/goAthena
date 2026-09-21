//go:build integration

package infra_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"testing"

	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/infra/testdb"
	"github.com/bouroo/goAthena/internal/infrastructure/db"
	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	charinfra "github.com/bouroo/goAthena/internal/modules/character/infra"
	worldapp "github.com/bouroo/goAthena/internal/modules/world/app"
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

// TestWorld_GORMSaveState proves the full state write path — hp/sp plus the
// accumulated base_exp/job_exp — persists into the char table's columns and
// survives a reload via LoadEnterState: the durability guarantee that in-session
// combat/regen/heal and EXP-from-kill changes survive a disconnect/restart.
func TestWorld_GORMSaveState(t *testing.T) {
	worldRepo := worldinfra.NewGORMWorldRepository(dbForTest(t))
	ctx := context.Background()

	// seedChar inserts HP=800/MaxHP=1000, SP=40/MaxSP=50.
	charID := seedChar(t, dbForTest(t), "WorldVitals", worlddomain.Position{X: 53, Y: 111})

	// SaveState with a leveling-recalculated snapshot: level 2, job level 3,
	// maxima grown to 1200/60, current vitals 750/25, EXP totals 1234/5678,
	// 6 status points + 3 skill points.
	if err := worldRepo.SaveState(ctx, charID, 2, 3, 1200, 60, 750, 25, 1234, 5678, 6, 3); err != nil {
		t.Fatalf("save state: %v", err)
	}
	got, err := worldRepo.LoadEnterState(ctx, charID)
	if err != nil {
		t.Fatalf("load enter state: %v", err)
	}
	if got.HP != 750 || got.SP != 25 {
		t.Errorf("vitals after save = hp %d sp %d, want hp 750 sp 25", got.HP, got.SP)
	}
	if got.BaseExp != 1234 || got.JobExp != 5678 {
		t.Errorf("exp after save = base %d job %d, want base 1234 job 5678", got.BaseExp, got.JobExp)
	}
	if got.Level != 2 {
		t.Errorf("level after save = %d, want 2 (base_level persists)", got.Level)
	}
	if got.MaxHP != 1200 || got.MaxSP != 60 {
		t.Errorf("max vitals after save = %d/%d, want 1200/60 (level-up recalc persists)", got.MaxHP, got.MaxSP)
	}
	if got.StatusPoint != 6 {
		t.Errorf("status points after save = %d, want 6 (points persist)", got.StatusPoint)
	}
	if got.JobLevel != 3 {
		t.Errorf("job level after save = %d, want 3 (job_level persists)", got.JobLevel)
	}
	if got.SkillPoint != 3 {
		t.Errorf("skill points after save = %d, want 3 (skill points persist)", got.SkillPoint)
	}
}

// TestWorldService_SaveAll_ReloadRoundtrip is the graceful-shutdown durability
// proof. It drives a real WorldService over a GORM repo, mutates a character's
// in-session vitals (combat damage via AddVitals — the clamp primitive combat's
// applyDamage shares), flushes them with SaveAll, then reloads the same char
// through a brand-new WorldService: the "server restart" scenario. The in-memory
// damage must survive the reload. This crosses the WorldService (in-memory
// authority) <-> GORM (durable char table) boundary that the memory-repo unit
// tests cannot reach; only a real containerized DB catches an hp/sp mapping
// regression (e.g. SaveVitals not auto-quoting the column).
func TestWorldService_SaveAll_ReloadRoundtrip(t *testing.T) {
	ctx := context.Background()
	repo := worldinfra.NewGORMWorldRepository(dbForTest(t))
	// seedChar inserts HP=800/MaxHP=1000, SP=40/MaxSP=50 at {53,111}.
	charID := seedChar(t, dbForTest(t), "WorldSaveAllReload", worlddomain.Position{X: 53, Y: 111})

	svc1 := worldapp.NewWorldService(repo, slog.Default(), 50)
	e1, err := svc1.EnterMap(ctx, charID)
	if err != nil {
		t.Fatalf("enter map: %v", err)
	}
	if e1.HP != 800 || e1.SP != 40 {
		t.Fatalf("enter vitals = hp %d sp %d, want 800/40 (loaded from DB)", e1.HP, e1.SP)
	}

	// In-session combat damage: 800-150=650, 40-10=30.
	hpAfter, spAfter, err := svc1.AddVitals(charID, -150, -10)
	if err != nil {
		t.Fatalf("apply damage: %v", err)
	}
	if hpAfter != 650 || spAfter != 30 {
		t.Fatalf("after damage = hp %d sp %d, want 650/30", hpAfter, spAfter)
	}

	// Graceful-shutdown flush: persist every online PC's vitals + offline flag.
	svc1.SaveAll(ctx)

	// "Restart": a fresh WorldService over the same repo reloads durable state.
	svc2 := worldapp.NewWorldService(repo, slog.Default(), 50)
	e2, err := svc2.EnterMap(ctx, charID)
	if err != nil {
		t.Fatalf("reload enter map: %v", err)
	}
	if e2.HP != 650 || e2.SP != 30 {
		t.Errorf("reload vitals = hp %d sp %d, want 650/30 (in-session damage lost across restart)", e2.HP, e2.SP)
	}
}

// TestWorldService_LeaveMap_ReloadRoundtrip is the disconnect/warp durability
// proof. It exercises gateway OnClose's persist primitive — LeaveMap — after the
// most dramatic in-session vitals change, a respawn (HP/SP revived to max), then
// reloads through a fresh WorldService. The respawn state must survive the
// disconnect + restart. Together with SaveAll above, this proves BOTH persist
// triggers (graceful shutdown + per-conn disconnect) are durable end-to-end over
// a real GORM char table.
func TestWorldService_LeaveMap_ReloadRoundtrip(t *testing.T) {
	ctx := context.Background()
	repo := worldinfra.NewGORMWorldRepository(dbForTest(t))
	// seedChar inserts HP=800/MaxHP=1000, SP=40/MaxSP=50 at {53,111}.
	charID := seedChar(t, dbForTest(t), "WorldLeaveReload", worlddomain.Position{X: 53, Y: 111})

	svc1 := worldapp.NewWorldService(repo, slog.Default(), 50)
	if _, err := svc1.EnterMap(ctx, charID); err != nil {
		t.Fatalf("enter map: %v", err)
	}

	// Respawn revives vitals to max: 800->1000 HP, 40->50 SP. The save point
	// falls back to the enter map/pos (seedChar sets no save_map).
	if err := svc1.RespawnPlayer(charID); err != nil {
		t.Fatalf("respawn: %v", err)
	}

	// Disconnect/warp persist: LeaveMap writes offline flag + last position +
	// vitals and drops the entity from the registry.
	if err := svc1.LeaveMap(ctx, charID); err != nil {
		t.Fatalf("leave map: %v", err)
	}

	// "Restart": a fresh WorldService reloads the respawned vitals.
	svc2 := worldapp.NewWorldService(repo, slog.Default(), 50)
	e2, err := svc2.EnterMap(ctx, charID)
	if err != nil {
		t.Fatalf("reload enter map: %v", err)
	}
	if e2.HP != 1000 || e2.SP != 50 {
		t.Errorf("reload vitals = hp %d sp %d, want 1000/50 (respawn lost across disconnect+restart)", e2.HP, e2.SP)
	}
}
