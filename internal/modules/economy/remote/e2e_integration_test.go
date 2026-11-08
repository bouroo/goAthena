//go:build integration

package remote_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"gorm.io/gorm"

	natsserver "github.com/nats-io/nats-server/v2/server"

	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/infra/testdb"
	"github.com/bouroo/goAthena/internal/infrastructure/db"
	charinfra "github.com/bouroo/goAthena/internal/modules/character/infra"
	"github.com/bouroo/goAthena/internal/modules/economy/app"
	economydomain "github.com/bouroo/goAthena/internal/modules/economy/domain"
	economyinfra "github.com/bouroo/goAthena/internal/modules/economy/infra"
	"github.com/bouroo/goAthena/internal/modules/economy/remote"
)

func econDBForTest(t *testing.T) *gorm.DB {
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

// startRealStack wires proxy → in-process broker → server → real EconomyService
// over the containerized database. The broker is the same nats-server code the
// production container runs; the boundary under test is the database.
func startRealStack(t *testing.T, gdb *gorm.DB) *remote.Proxy {
	t.Helper()
	srv, err := natsserver.NewServer(&natsserver.Options{Port: natsserver.RANDOM_PORT, NoLog: true, NoSigs: true})
	if err != nil {
		t.Fatalf("nats server: %v", err)
	}
	go srv.Start()
	t.Cleanup(srv.Shutdown)
	if !srv.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats server not ready in 5s")
	}
	hostConn, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatalf("host connect: %v", err)
	}
	t.Cleanup(hostConn.Close)
	proxyConn, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatalf("proxy connect: %v", err)
	}
	t.Cleanup(proxyConn.Close)

	svc := app.NewEconomyService(
		charinfra.NewGORMCharacterRepository(gdb),
		economyinfra.NewGORMLedger(gdb),
	)
	if _, err := remote.NewServer(hostConn, svc, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("server: %v", err)
	}
	return remote.NewProxy(proxyConn, 2*time.Second)
}

// insertEconChar seeds a `char` row carrying zeny and returns its id.
func insertEconChar(t *testing.T, gdb *gorm.DB, name string, zeny uint32) uint32 {
	t.Helper()
	err := gdb.Table("char").Create(map[string]any{
		"account_id": 3000042,
		"char_num":   0,
		"name":       name,
		"class":      1,
		"base_level": 10,
		"zeny":       zeny,
	}).Error
	if err != nil {
		t.Fatalf("seed char: %v", err)
	}
	var id uint32
	if err := gdb.Table("char").Select("char_id").Where("name = ?", name).Scan(&id).Error; err != nil || id == 0 {
		t.Fatalf("read back char id: %v (id=%d)", err, id)
	}
	return id
}

func TestE2E_ProxyMovesRealRows(t *testing.T) {
	gdb := econDBForTest(t)
	proxy := startRealStack(t, gdb)
	ctx := context.Background()

	charID := insertEconChar(t, gdb, "EconRemote", 5000)

	got, err := proxy.GetZeny(ctx, charID)
	if err != nil || got != 5000 {
		t.Fatalf("GetZeny through stack = %d, %v; want 5000", got, err)
	}

	if err := proxy.DeductZenyFor(ctx, charID, 1200, app.LedgerEntry{Reason: economydomain.ReasonScript, MapName: "prontera"}); err != nil {
		t.Fatalf("DeductZenyFor: %v", err)
	}

	// The char row itself moved — the full proxy→broker→host→DB path wrote it.
	var zeny uint32
	if err := gdb.Table("char").Select("zeny").Where("char_id = ?", charID).Scan(&zeny).Error; err != nil {
		t.Fatalf("read zeny: %v", err)
	}
	if zeny != 3800 {
		t.Fatalf("char row zeny = %d, want 3800", zeny)
	}

	// And the ledger audit row landed with the wire-supplied reason.
	var row struct {
		Reason string
		Amount int32
	}
	if err := gdb.Table("zeny_ledger").
		Select("reason, amount").
		Where("char_id = ?", charID).
		Order("id DESC").Limit(1).
		Scan(&row).Error; err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if row.Reason != "script" || row.Amount != -1200 {
		t.Fatalf("ledger row = (%s, %d), want (script, -1200)", row.Reason, row.Amount)
	}
}

func TestE2E_ProxyOverdrawRefuses(t *testing.T) {
	gdb := econDBForTest(t)
	proxy := startRealStack(t, gdb)
	ctx := context.Background()

	charID := insertEconChar(t, gdb, "EconPoor", 100)
	if err := proxy.DeductZeny(ctx, charID, 101); !errors.Is(err, economydomain.ErrInsufficientFunds) {
		t.Fatalf("overdraw err = %v, want ErrInsufficientFunds", err)
	}
	var zeny uint32
	_ = gdb.Table("char").Select("zeny").Where("char_id = ?", charID).Scan(&zeny).Error
	if zeny != 100 {
		t.Fatalf("refused overdraw still moved zeny to %d", zeny)
	}
}
