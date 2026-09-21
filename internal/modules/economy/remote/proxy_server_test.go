package remote

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	natsserver "github.com/nats-io/nats-server/v2/server"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	charinfra "github.com/bouroo/goAthena/internal/modules/character/infra"
	"github.com/bouroo/goAthena/internal/modules/economy/app"
	economydomain "github.com/bouroo/goAthena/internal/modules/economy/domain"
	economyinfra "github.com/bouroo/goAthena/internal/modules/economy/infra"
)

// startBus boots an in-process NATS server and returns a connected client —
// no Docker, the same wire path production uses.
func startBus(t *testing.T) *nats.Conn {
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
	nc, err := nats.Connect(srv.ClientURL())
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	t.Cleanup(nc.Close)
	return nc
}

// startFixture wires proxy → bus → server → the real EconomyService over
// memory repos, on separate connections exactly like a zone process and an
// economy host in production.
func startFixture(t *testing.T) (*Proxy, *charinfra.MemoryCharacterRepository, *economyinfra.MemoryLedger) {
	t.Helper()
	bus := startBus(t)
	hostConn, err := nats.Connect(bus.ConnectedUrl())
	if err != nil {
		t.Fatalf("host connect: %v", err)
	}
	t.Cleanup(hostConn.Close)
	proxyConn, err := nats.Connect(bus.ConnectedUrl())
	if err != nil {
		t.Fatalf("proxy connect: %v", err)
	}
	t.Cleanup(proxyConn.Close)

	repo := charinfra.NewMemoryCharacterRepository()
	ledger := economyinfra.NewMemoryLedger()
	svc := app.NewEconomyService(repo, ledger)
	if _, err := NewServer(hostConn, svc, discardLogger()); err != nil {
		t.Fatalf("server: %v", err)
	}
	// Production default: a 1s fixture timeout flaked under the full -race
	// coverage suite (parallel package contention), turning fast local
	// round-trips into timeout errors. Only a genuinely absent host waits.
	return NewProxy(proxyConn, DefaultTimeout), repo, ledger
}

func seedChar(t *testing.T, repo *charinfra.MemoryCharacterRepository, zeny uint32) uint32 {
	t.Helper()
	c, err := repo.CreateWithID(context.Background(), chardomain.Character{
		ID: 777, AccountID: 2001, Name: "Trader", Zeny: zeny,
	})
	if err != nil {
		t.Fatalf("seed char: %v", err)
	}
	return uint32(c.ID)
}

func TestProxyZenyRoundTrip(t *testing.T) {
	proxy, repo, ledger := startFixture(t)
	ctx := context.Background()
	charID := seedChar(t, repo, 1000)

	got, err := proxy.GetZeny(ctx, charID)
	if err != nil || got != 1000 {
		t.Fatalf("GetZeny = %d, %v; want 1000, nil", got, err)
	}

	if err := proxy.DeductZenyFor(ctx, charID, 300, app.LedgerEntry{Reason: economydomain.ReasonScript}); err != nil {
		t.Fatalf("DeductZenyFor: %v", err)
	}
	if got, _ := proxy.GetZeny(ctx, charID); got != 700 {
		t.Fatalf("balance after deduct = %d, want 700", got)
	}
	if err := proxy.CreditZenyWithPeer(ctx, charID, 100, 42); err != nil {
		t.Fatalf("CreditZenyWithPeer: %v", err)
	}
	if got, _ := proxy.GetZeny(ctx, charID); got != 800 {
		t.Fatalf("balance after credit = %d, want 800", got)
	}

	rows, err := ledger.ListByChar(ctx, charID, 10)
	if err != nil {
		t.Fatalf("ledger: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("ledger rows = %d, want 2", len(rows))
	}
	credit := rows[0] // newest first
	if credit.Amount != 100 || credit.Reason != economydomain.ReasonTrade || credit.PeerCharID != 42 {
		t.Errorf("credit row = %+v, want +100 trade peer=42", credit)
	}
	deduct := rows[1]
	if deduct.Amount != -300 || deduct.Reason != economydomain.ReasonScript {
		t.Errorf("deduct row = %+v, want -300 script", deduct)
	}
}

func TestProxyPlainVerbsRecordUnknown(t *testing.T) {
	proxy, repo, ledger := startFixture(t)
	ctx := context.Background()
	charID := seedChar(t, repo, 500)

	if err := proxy.DeductZeny(ctx, charID, 100); err != nil {
		t.Fatalf("DeductZeny: %v", err)
	}
	if err := proxy.CreditZeny(ctx, charID, 50); err != nil {
		t.Fatalf("CreditZeny: %v", err)
	}
	rows, _ := ledger.ListByChar(ctx, charID, 10)
	if len(rows) != 2 {
		t.Fatalf("ledger rows = %d, want 2", len(rows))
	}
	for _, row := range rows {
		if row.Reason != economydomain.ReasonUnknown {
			t.Errorf("plain verb row reason = %q, want unknown", row.Reason)
		}
	}
}

func TestProxyWithPeerZeroDegradesToUnknown(t *testing.T) {
	// The WithPeer proxy methods must write the identical audit row the
	// in-process service writes — app.TradeEntry(0) degrades to ReasonUnknown.
	proxy, repo, ledger := startFixture(t)
	ctx := context.Background()
	charID := seedChar(t, repo, 500)

	if err := proxy.DeductZenyWithPeer(ctx, charID, 100, 0); err != nil {
		t.Fatalf("DeductZenyWithPeer(0): %v", err)
	}
	rows, _ := ledger.ListByChar(ctx, charID, 10)
	if len(rows) != 1 || rows[0].Reason != economydomain.ReasonUnknown {
		t.Fatalf("rows = %+v, want one unknown-reason row", rows)
	}
}

func TestProxySurfacesSentinels(t *testing.T) {
	proxy, repo, _ := startFixture(t)
	ctx := context.Background()
	charID := seedChar(t, repo, 100)

	if err := proxy.DeductZeny(ctx, charID, 999); !errors.Is(err, economydomain.ErrInsufficientFunds) {
		t.Fatalf("overdraw err = %v, want ErrInsufficientFunds", err)
	}
	if _, err := proxy.GetZeny(ctx, 404040); !errors.Is(err, chardomain.ErrCharacterNotFound) {
		t.Fatalf("unknown char err = %v, want ErrCharacterNotFound", err)
	}
	if err := proxy.CreditZeny(ctx, charID, 0); err == nil {
		t.Fatal("credit 0 must be rejected")
	}
}

func TestProxyTimesOutWithoutHost(t *testing.T) {
	bus := startBus(t)
	proxyConn, err := nats.Connect(bus.ConnectedUrl())
	if err != nil {
		t.Fatalf("proxy connect: %v", err)
	}
	t.Cleanup(proxyConn.Close)
	proxy := NewProxy(proxyConn, 200*time.Millisecond)

	done := make(chan error, 1)
	go func() {
		_, err := proxy.GetZeny(context.Background(), 1)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("call with no host must fail")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("proxy call hung past its timeout")
	}
}

func TestServerSubscriptionsCount(t *testing.T) {
	bus := startBus(t)
	hostConn, err := nats.Connect(bus.ConnectedUrl())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(hostConn.Close)
	svc := app.NewEconomyService(charinfra.NewMemoryCharacterRepository(), economyinfra.NewMemoryLedger())
	srv, err := NewServer(hostConn, svc, discardLogger())
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	if got := srv.Subscriptions(); got != 3 {
		t.Errorf("subscriptions = %d, want 3 (get, credit, deduct)", got)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
