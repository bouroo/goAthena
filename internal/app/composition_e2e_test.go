//go:build e2e

package app_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/app"
	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// seededAccountID is the account_id of the `test`/`test` row inserted by
// migration 000002_identity.up.sql (AUTO_INCREMENT base 2000000). The CA_LOGIN
// for this account must round-trip to an AC_ACCEPT_LOGIN carrying exactly this AID.
const seededAccountID uint32 = 2000000

// TestServe_CALogin_RoundTrip is the M1 end-to-end proof. It boots the real
// modular monolith via app.Serve against the live compose stack (MariaDB +
// Valkey + NATS), drives a byte-exact CA_LOGIN over the TCP gateway for the
// seeded `test`/`test` account, and asserts an AC_ACCEPT_LOGIN (0x0ac4) reply
// carrying the seeded account_id. This exercises every M1 seam together: real
// config load, real GORM AccountRepository against MariaDB, the real Valkey
// SessionStore, account.Register, the composition-root handler threading into
// gateway.Register, the dispatcher, the login codec, and the gnet TCP transport.
//
// Listener addresses are rebound to ephemeral loopback ports so the test is
// hermetic and never clashes with a concurrently-running goathena serve.
func TestServe_CALogin_RoundTrip(t *testing.T) {
	// The whole server only works against the live compose stack; skip with an
	// actionable message when it is not up rather than hanging on a dial.
	requireStack(t)

	cfg := loadConfigForE2E(t)
	tcpAddr := rebindListenersToEphemeralPorts(t, cfg)
	require.NoError(t, cfg.Validate(), "config drift: config.yaml no longer validates")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	serveErr := make(chan error, 1)
	go func() { serveErr <- app.Serve(ctx, cfg) }()

	// Wait for the gateway TCP listener to accept, but surface a fast, clear
	// failure if Serve died during startup (usually a down dependency).
	waitGatewayOrFatal(t, tcpAddr, serveErr)

	// --- Drive the real CA_LOGIN over TCP ---
	conn, err := net.Dial("tcp", tcpAddr)
	require.NoError(t, err)
	defer conn.Close()

	var req bytes.Buffer
	require.NoError(t, packet.CALoginRequest{
		Version: uint32(cfg.Gateway.Packetver), Username: "test", Password: "test",
	}.Encode(&req))
	_, err = conn.Write(req.Bytes())
	require.NoError(t, err)

	resp := make([]byte, packet.AcceptLoginResponse{}.Size())
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err = io.ReadFull(conn, resp)
	require.NoError(t, err, "no AC_ACCEPT_LOGIN received; login likely failed against the DB")

	// [0:2] cmd 0x0ac4, [2:4] length, [8:12] AID — per pkg/ro/packet/encode.go.
	assert.Equal(t, packet.HeaderACACCEPTLOGIN, binary.LittleEndian.Uint16(resp[0:2]),
		"expected AC_ACCEPT_LOGIN header")
	assert.Equal(t, uint16(packet.AcceptLoginResponse{}.Size()), binary.LittleEndian.Uint16(resp[2:4]),
		"AC_ACCEPT_LOGIN length")
	assert.Equal(t, seededAccountID, binary.LittleEndian.Uint32(resp[8:12]),
		"AID must match the seeded `test` account")

	// Cancel the run context and confirm Serve drains and exits cleanly.
	cancel()
	select {
	case err := <-serveErr:
		assert.NoError(t, err, "app.Serve returned an error on shutdown")
	case <-time.After(15 * time.Second):
		t.Fatal("app.Serve did not shut down within 15s")
	}
}

// requireStack skips the test unless the compose stack's service ports accept a
// TCP connection. e2e is opt-in (-tags=e2e) and assumes an operator has already
// run `docker compose up -d mariadb valkey nats` and `task migrate-up`; this
// guard turns a silent hang into an actionable skip.
func requireStack(t *testing.T) {
	t.Helper()
	for _, addr := range []string{"127.0.0.1:3306", "127.0.0.1:6379", "127.0.0.1:4222"} {
		c, err := net.DialTimeout("tcp", addr, time.Second)
		if err != nil {
			t.Skipf("compose stack not reachable (%s down): %v\n"+
				"run: docker compose up -d mariadb valkey nats && task migrate-up", addr, err)
		}
		_ = c.Close()
	}
}

// loadConfigForE2E loads the repo's config.yaml by pointing CONFIG_FILE at it,
// so the test uses the real, validated configuration regardless of the test
// process's working directory.
func loadConfigForE2E(t *testing.T) *config.Config {
	t.Helper()
	t.Setenv("CONFIG_FILE", filepath.Join(repoRoot(t), "config.yaml"))
	cfg, err := config.Load()
	require.NoError(t, err, "load config.yaml")
	return cfg
}

// rebindListenersToEphemeralPorts moves HTTP, gRPC, and both gateway listeners
// onto free loopback ports so the test never collides with a running goathena
// serve, and returns the gateway TCP address the test dials. All Port fields
// validate min=1, so 0 is unusable; freePort yields valid, non-conflicting ports.
func rebindListenersToEphemeralPorts(t *testing.T, cfg *config.Config) string {
	t.Helper()
	cfg.HTTP.Host, cfg.HTTP.Port = "127.0.0.1", freePort(t)
	cfg.GRPC.Host, cfg.GRPC.Port = "127.0.0.1", freePort(t)
	cfg.App.Host, cfg.App.Port = "127.0.0.1", freePort(t)
	tcpAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(freePort(t)))
	cfg.Gateway.TCP.Addr = tcpAddr
	cfg.Gateway.WS.Addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(freePort(t)))
	return tcpAddr
}

// waitGatewayOrFatal polls the gateway TCP address until it accepts or serveErr
// delivers a startup error, failing the test fast in the latter case.
func waitGatewayOrFatal(t *testing.T, addr string, serveErr <-chan error) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-serveErr:
			require.FailNow(t, "app.Serve exited during startup", "error: %v", err)
		default:
		}
		if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("gateway listener %s never accepted within 20s", addr)
}

// repoRoot resolves the repository root from this test file's location
// (internal/app/ -> ../..).
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}

// freePort returns an OS-assigned free TCP port. The brief close-before-bind
// window is an acceptable TOCTOU for loopback tests.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}
