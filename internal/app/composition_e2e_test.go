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
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

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
	root := repoRoot(t)
	t.Setenv("CONFIG_FILE", filepath.Join(root, "config.yaml"))
	cfg, err := config.Load()
	require.NoError(t, err, "load config.yaml")
	// zone.map_dir is a repo-root-relative path in config.yaml ("./data/maps"),
	// resolved against the server's CWD at runtime. A test process runs with its
	// own CWD (the package dir), so absolutize it against the repo root or the
	// FileMapStore would look for maps under the test's CWD and fail to load.
	cfg.Zone.MapDir = filepath.Join(root, filepath.Clean(cfg.Zone.MapDir))
	return cfg
}

// rebindListenersToEphemeralPorts moves HTTP, gRPC, and all three gateway
// listeners (login/char TCP, WS, and map TCP) onto free loopback ports so the
// test never collides with a running goathena serve, and returns the gateway
// TCP address the test dials. All Port fields validate min=1, so 0 is unusable;
// freePort yields valid, non-conflicting ports. The map listener's address is
// left on cfg.Gateway.MapAddr for the map-enter test to wait on and dial.
func rebindListenersToEphemeralPorts(t *testing.T, cfg *config.Config) string {
	t.Helper()
	cfg.HTTP.Host, cfg.HTTP.Port = "127.0.0.1", freePort(t)
	cfg.GRPC.Host, cfg.GRPC.Port = "127.0.0.1", freePort(t)
	cfg.App.Host, cfg.App.Port = "127.0.0.1", freePort(t)
	tcpAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(freePort(t)))
	cfg.Gateway.TCP.Addr = tcpAddr
	cfg.Gateway.WS.Addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(freePort(t)))
	cfg.Gateway.MapAddr = net.JoinHostPort("127.0.0.1", strconv.Itoa(freePort(t)))
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

// TestServe_CharListAndSelect_RoundTrip is the M2a end-to-end proof. On one TCP
// connection it replays the real multiplexed login→char flow the gateway serves:
// CA_LOGIN → AC_ACCEPT_LOGIN (capture the per-conn session token), CH_ENTER →
// HC_ACCEPT_ENTER (the char list for the seeded account), CH_SELECT_CHAR →
// HC_NOTIFY_ZONESVR (the zone redirect carrying the chosen char's GID and last
// map). It exercises the M2a seams end to end: the codec DB merge (one login
// decoder framing both CA_* and CH_* on the same stream), the per-conn auth
// cache as the CH_ENTER trust anchor, the GORM CharacterRepository against a
// real MariaDB char row, the MapCharacterInfo builder, and the zone-redirect
// handler reading cfg.Gateway.MapAddr.
func TestServe_CharListAndSelect_RoundTrip(t *testing.T) {
	requireStack(t)

	cfg := loadConfigForE2E(t)
	tcpAddr := rebindListenersToEphemeralPorts(t, cfg)
	// Pin the advertised zone address to a numeric loopback so the redirect's
	// IP/port wire bytes are deterministic (no localhost IPv4/IPv6 resolution
	// skew). ParseZoneAddr encodes 127.0.0.1 as 0x0100007F (inet_order).
	cfg.Gateway.MapAddr = "127.0.0.1:5121"
	require.NoError(t, cfg.Validate(), "config drift: config.yaml no longer validates")

	// Seed a char row for the migration-seeded `test` account so the list is
	// non-empty and the select resolves a known GID. account 2000000 is the
	// dedicated test account; clearing its chars at setup keeps the slot-0
	// pick deterministic across runs.
	seedCharID := seedCharForAccount(t, cfg, seededAccountID, "E2eHero", "prontera")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	serveErr := make(chan error, 1)
	go func() { serveErr <- app.Serve(ctx, cfg) }()
	waitGatewayOrFatal(t, tcpAddr, serveErr)

	conn, err := net.Dial("tcp", tcpAddr)
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))

	// --- CA_LOGIN → AC_ACCEPT_LOGIN; capture the session token the gateway
	// caches on this connection (the CH_ENTER trust anchor). ---
	var login bytes.Buffer
	require.NoError(t, packet.CALoginRequest{
		Version: uint32(cfg.Gateway.Packetver), Username: "test", Password: "test",
	}.Encode(&login))
	_, err = conn.Write(login.Bytes())
	require.NoError(t, err)

	accept := make([]byte, packet.AcceptLoginResponse{}.Size())
	_, err = io.ReadFull(conn, accept)
	require.NoError(t, err, "no AC_ACCEPT_LOGIN received; login likely failed against the DB")
	require.Equal(t, packet.HeaderACACCEPTLOGIN, binary.LittleEndian.Uint16(accept[0:2]),
		"expected AC_ACCEPT_LOGIN header")
	loginID1 := binary.LittleEndian.Uint32(accept[4:8])
	loginID2 := binary.LittleEndian.Uint32(accept[12:16])
	sexByte := accept[46]
	require.NotZero(t, loginID2, "AC_ACCEPT_LOGIN carried no session token (login_id2)")

	// --- CH_ENTER → HC_ACCEPT_ENTER. The gateway's single login decoder must
	// frame this char-role opcode on the same stream (the DB-merge fix); the
	// handler verifies the echoed token against the per-conn auth cache. ---
	var enter bytes.Buffer
	require.NoError(t, packet.CHEnterRequest{
		AccountID: seededAccountID, LoginID1: loginID1, LoginID2: loginID2, Sex: sexByte,
	}.Encode(&enter))
	_, err = conn.Write(enter.Bytes())
	require.NoError(t, err)

	charList := readLengthPrefixedPacket(t, conn)
	require.Equal(t, packet.HeaderHCACCEPTENTER, binary.LittleEndian.Uint16(charList[0:2]),
		"expected HC_ACCEPT_ENTER header")
	require.Len(t, charList, 27+175, "HC_ACCEPT_ENTER with exactly one CHARACTER_INFO")
	// The char-list header defaults: total=premiumStart=premiumEnd=15.
	assert.Equal(t, uint8(15), charList[4], "total slots")
	info := charList[27:]
	assert.Equal(t, seedCharID, binary.LittleEndian.Uint32(info[0:4]), "CHARACTER_INFO GID = seeded char_id")
	assert.Equal(t, "E2eHero", string(bytes.TrimRight(info[108:108+24], "\x00")), "char name")

	// --- CH_SELECT_CHAR(slot 0) → HC_NOTIFY_ZONESVR. The redirect carries the
	// chosen char's GID and last_map, and the configured zone address. ---
	var sel bytes.Buffer
	require.NoError(t, packet.CHSelectCharRequest{Slot: 0}.Encode(&sel))
	_, err = conn.Write(sel.Bytes())
	require.NoError(t, err)

	redirect := make([]byte, 156)
	_, err = io.ReadFull(conn, redirect)
	require.NoError(t, err, "no HC_NOTIFY_ZONESVR received")
	require.Equal(t, packet.HeaderHCNOTIFYZONESVR, binary.LittleEndian.Uint16(redirect[0:2]),
		"expected HC_NOTIFY_ZONESVR header")
	assert.Equal(t, seedCharID, binary.LittleEndian.Uint32(redirect[2:6]), "redirect CID = char GID")
	assert.Equal(t, "prontera", string(bytes.TrimRight(redirect[6:6+16], "\x00")), "redirect map name")
	// The redirect carries the configured zone address as inet_order bytes:
	// 127.0.0.1 → 0x0100007F at [22:26], port 5121 at [26:28].
	assert.Equal(t, uint32(0x0100007F), binary.LittleEndian.Uint32(redirect[22:26]), "zone IP encodes 127.0.0.1")
	assert.Equal(t, uint16(5121), binary.LittleEndian.Uint16(redirect[26:28]), "zone port")

	cancel()
	select {
	case err := <-serveErr:
		assert.NoError(t, err, "app.Serve returned an error on shutdown")
	case <-time.After(15 * time.Second):
		t.Fatal("app.Serve did not shut down within 15s")
	}
}

// TestServe_MakeChar_RoundTrip is the M2b end-to-end proof. On one TCP
// connection it replays login → CH_ENTER (empty char list) → CH_MAKE_CHAR →
// HC_ACCEPT_MAKECHAR, then re-enters (CH_ENTER) to confirm the freshly created
// novice now appears in the char list read back from the real MariaDB. It
// exercises the M2b seams together: the CH_MAKE_CHAR handler sourcing the owning
// account from the per-conn auth cache (CH_MAKE_CHAR carries no account_id, so a
// stray/default owner would be an impersonation bug), the GORM
// CharacterRepository.Create writing a real char row with novice defaults, the
// name/slot guards, and the HC_ACCEPT_MAKECHAR encoder.
func TestServe_MakeChar_RoundTrip(t *testing.T) {
	requireStack(t)

	cfg := loadConfigForE2E(t)
	tcpAddr := rebindListenersToEphemeralPorts(t, cfg)
	cfg.Gateway.MapAddr = "127.0.0.1:5121"
	require.NoError(t, cfg.Validate(), "config drift: config.yaml no longer validates")

	// Start from an empty char list so slot 0 is free and the create is the only
	// row for the account. The returned session cleans up the created row.
	gdb := resetCharsForAccount(t, cfg, seededAccountID)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	serveErr := make(chan error, 1)
	go func() { serveErr <- app.Serve(ctx, cfg) }()
	waitGatewayOrFatal(t, tcpAddr, serveErr)

	conn, err := net.Dial("tcp", tcpAddr)
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))

	// --- CA_LOGIN → AC_ACCEPT_LOGIN; capture the per-conn session token. ---
	var login bytes.Buffer
	require.NoError(t, packet.CALoginRequest{
		Version: uint32(cfg.Gateway.Packetver), Username: "test", Password: "test",
	}.Encode(&login))
	_, err = conn.Write(login.Bytes())
	require.NoError(t, err)

	accept := make([]byte, packet.AcceptLoginResponse{}.Size())
	_, err = io.ReadFull(conn, accept)
	require.NoError(t, err, "no AC_ACCEPT_LOGIN received; login likely failed against the DB")
	require.Equal(t, packet.HeaderACACCEPTLOGIN, binary.LittleEndian.Uint16(accept[0:2]),
		"expected AC_ACCEPT_LOGIN header")
	loginID1 := binary.LittleEndian.Uint32(accept[4:8])
	loginID2 := binary.LittleEndian.Uint32(accept[12:16])
	sexByte := accept[46]

	// --- CH_ENTER → HC_ACCEPT_ENTER with zero characters (slot 0 is free). ---
	var enter bytes.Buffer
	require.NoError(t, packet.CHEnterRequest{
		AccountID: seededAccountID, LoginID1: loginID1, LoginID2: loginID2, Sex: sexByte,
	}.Encode(&enter))
	_, err = conn.Write(enter.Bytes())
	require.NoError(t, err)

	emptyList := readLengthPrefixedPacket(t, conn)
	require.Equal(t, packet.HeaderHCACCEPTENTER, binary.LittleEndian.Uint16(emptyList[0:2]),
		"expected HC_ACCEPT_ENTER header")
	require.Len(t, emptyList, 27, "HC_ACCEPT_ENTER with zero CHARACTER_INFO blocks")

	// --- CH_MAKE_CHAR → HC_ACCEPT_MAKECHAR (fixed 177B, no length prefix). ---
	const makeName = "E2eMake"
	var mk bytes.Buffer
	require.NoError(t, packet.CHMakeCharRequest{
		Name: makeName, Slot: 0, HairColor: 1, HairStyle: 2, Job: 0, Sex: 1,
	}.Encode(&mk))
	_, err = conn.Write(mk.Bytes())
	require.NoError(t, err)

	acceptMake := make([]byte, packet.AcceptMakeCharResponse{}.Size())
	_, err = io.ReadFull(conn, acceptMake)
	require.NoError(t, err, "no HC_ACCEPT_MAKECHAR received; make-char was refused")
	require.Equal(t, packet.HeaderHCACCEPTMAKECHAR, binary.LittleEndian.Uint16(acceptMake[0:2]),
		"expected HC_ACCEPT_MAKECHAR header")
	createdGID := binary.LittleEndian.Uint32(acceptMake[2:6])
	require.NotZero(t, createdGID, "HC_ACCEPT_MAKECHAR carried no char_id")
	assert.Equal(t, makeName, string(bytes.TrimRight(acceptMake[110:134], "\x00")),
		"CHARACTER_INFO name at offset 110")

	// --- Persistence + trust anchor: the row landed under the conn-auth account
	// with exactly the assigned char_id. ---
	var dbCharID uint32
	require.NoError(t, gdb.Raw(
		"SELECT char_id FROM `char` WHERE account_id = ? AND char_num = 0", seededAccountID).
		Scan(&dbCharID).Error, "read back created char_id from MariaDB")
	assert.Equal(t, createdGID, dbCharID, "wire GID must equal the persisted char_id")

	// --- Re-enter: the created novice round-trips through the read path, now as
	// the single CHARACTER_INFO in the list. ---
	_, err = conn.Write(enter.Bytes()) // CH_ENTER again on the same connection
	require.NoError(t, err)
	oneList := readLengthPrefixedPacket(t, conn)
	require.Equal(t, packet.HeaderHCACCEPTENTER, binary.LittleEndian.Uint16(oneList[0:2]))
	require.Len(t, oneList, 27+175, "HC_ACCEPT_ENTER with exactly one CHARACTER_INFO")
	assert.Equal(t, createdGID, binary.LittleEndian.Uint32(oneList[27:27+4]),
		"re-entered char list carries the created char_id")
	assert.Equal(t, makeName, string(bytes.TrimRight(oneList[27+108:27+108+24], "\x00")),
		"re-entered char list carries the created name")

	cancel()
	select {
	case err := <-serveErr:
		assert.NoError(t, err, "app.Serve returned an error on shutdown")
	case <-time.After(15 * time.Second):
		t.Fatal("app.Serve did not shut down within 15s")
	}
}

// TestServe_MapEnter_RoundTrip is the M3+M4b end-to-end proof of the secure
// map enter followed by spawn-on-enter. It boots the real modular monolith
// (with the dedicated map listener), seeds a real char row for the test account
// in MariaDB, logs in on the login/char TCP listener to mint a session in
// Valkey, then opens a FRESH connection to the separate map listener and sends
// CZ_ENTER echoing the login's login_id1 as the AuthCode and the seeded char_id
// as the CharID. The map connection starts at the map role, so CZ_ENTER routes
// to the map dispatch table, the CZ_ENTER handler re-verifies login_id1 against
// the Valkey SessionStore (constant-time), replies ZC_ACCEPT_ENTER (0x02eb), and
// then the M4b SpawnService loads the seeded char, loads the Prontera map data,
// builds the player, and emits a ZC_SPAWN_UNIT (0x09fe, 107B) self-spawn. This
// is the first cross-listener boundary AND the first world-state boundary: it
// proves the gateway-map-tcp runnable binds, NewMapTCPHandler seeds the map role
// + map decoder, the reconnect trust gate admits a verified session, the
// character repository resolves via DI, FileMapStore loads the Prontera gat/rsw,
// and the SpawnService writes the spawn frame through the live TCP conn.
func TestServe_MapEnter_RoundTrip(t *testing.T) {
	requireStack(t)

	cfg := loadConfigForE2E(t)
	tcpAddr := rebindListenersToEphemeralPorts(t, cfg)
	mapAddr := cfg.Gateway.MapAddr // ephemeral map listener from the rebind above
	require.NoError(t, cfg.Validate(), "config drift: config.yaml no longer validates")

	// Seed a real char the SpawnService can load. last_map/last_x/last_y match
	// the seedCharForAccount defaults ("prontera", 53, 111) so the self-spawn
	// PosX/PosY are predictable; the cleanup inside seedCharForAccount removes
	// the row after the test.
	seedCharID := seedCharForAccount(t, cfg, seededAccountID, "E2eHero", "prontera")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	serveErr := make(chan error, 1)
	go func() { serveErr <- app.Serve(ctx, cfg) }()
	defer func() {
		cancel()
		select {
		case err := <-serveErr:
			assert.NoError(t, err, "app.Serve returned an error on shutdown")
		case <-time.After(15 * time.Second):
			t.Fatal("app.Serve did not shut down within 15s")
		}
	}()
	waitGatewayOrFatal(t, tcpAddr, serveErr)
	waitGatewayOrFatal(t, mapAddr, serveErr) // dedicated map listener must accept too

	// --- Login on the login/char listener; capture the session token (login_id1)
	// the CZ_ENTER trust gate will verify on the separate map connection. ---
	loginConn, err := net.Dial("tcp", tcpAddr)
	require.NoError(t, err)
	require.NoError(t, loginConn.SetReadDeadline(time.Now().Add(5*time.Second)))
	var login bytes.Buffer
	require.NoError(t, packet.CALoginRequest{
		Version: uint32(cfg.Gateway.Packetver), Username: "test", Password: "test",
	}.Encode(&login))
	_, err = loginConn.Write(login.Bytes())
	require.NoError(t, err)

	accept := make([]byte, packet.AcceptLoginResponse{}.Size())
	_, err = io.ReadFull(loginConn, accept)
	require.NoError(t, err, "no AC_ACCEPT_LOGIN received; login likely failed against the DB")
	require.Equal(t, packet.HeaderACACCEPTLOGIN, binary.LittleEndian.Uint16(accept[0:2]),
		"expected AC_ACCEPT_LOGIN header")
	loginID1 := binary.LittleEndian.Uint32(accept[4:8])
	sexByte := accept[46]
	require.NotZero(t, loginID1, "AC_ACCEPT_LOGIN carried no login_id1 to echo in CZ_ENTER")
	require.NoError(t, loginConn.Close(), "close the login/char connection (map is a reconnect)")

	// --- Fresh reconnect to the dedicated map listener. This connection never
	// touched CA_LOGIN, so the only proof that login was accepted is the Valkey
	// session — exactly the threat model CZ_ENTER's trust gate exists for. ---
	mapConn, err := net.Dial("tcp", mapAddr)
	require.NoError(t, err)
	defer mapConn.Close()
	require.NoError(t, mapConn.SetReadDeadline(time.Now().Add(5*time.Second)))

	var enter bytes.Buffer
	require.NoError(t, packet.CZEnterRequest{
		AccountID: seededAccountID, CharID: seedCharID, AuthCode: loginID1, ClientTime: 0, Sex: sexByte,
	}.Encode(&enter))
	_, err = mapConn.Write(enter.Bytes())
	require.NoError(t, err)

	// --- ZC_ACCEPT_ENTER (0x02eb, 13B): the trust gate admitted the session. ---
	resp := make([]byte, packet.MapAcceptEnterResponse{}.Size())
	_, err = io.ReadFull(mapConn, resp)
	require.NoError(t, err, "no ZC_ACCEPT_ENTER received; CZ_ENTER verification likely failed")
	require.Equal(t, packet.HeaderZCACCEPTENTER, binary.LittleEndian.Uint16(resp[0:2]),
		"expected ZC_ACCEPT_ENTER header")
	require.Equal(t, uint32(0), binary.LittleEndian.Uint32(resp[2:6]), "StartTime")
	assert.Equal(t, uint8(5), resp[9], "xSize (rAthena hardcode)")
	assert.Equal(t, uint8(5), resp[10], "ySize (rAthena hardcode)")

	// --- ZC_SPAWN_UNIT (0x09fe, 107B): the M4b SpawnService loaded the seeded
	// char, loaded Prontera, and self-spawned the entering player. A lone
	// enterer emits exactly one spawn frame (its own); there are no neighbors on
	// a freshly booted map. ---
	spawn := make([]byte, packet.SpawnUnitResponse{}.Size())
	_, err = io.ReadFull(mapConn, spawn)
	require.NoError(t, err, "no ZC_SPAWN_UNIT received; spawn-on-enter likely failed to load the char/map")
	require.Equal(t, packet.HeaderZCSPAWNUNIT, binary.LittleEndian.Uint16(spawn[0:2]),
		"expected ZC_SPAWN_UNIT header")
	require.Equal(t, uint16(packet.SpawnUnitResponse{}.Size()), binary.LittleEndian.Uint16(spawn[2:4]),
		"ZC_SPAWN_UNIT length")
	// AID = account_id (the verified session's, not the packet's), GID = char_id.
	assert.Equal(t, seededAccountID, binary.LittleEndian.Uint32(spawn[5:9]),
		"spawn AID is the seeded account_id")
	assert.Equal(t, seedCharID, binary.LittleEndian.Uint32(spawn[9:13]),
		"spawn GID is the seeded char_id")
	// ObjectType [4] = 0 (PC); PC self-spawn advertises no HP bar (MaxHP=HP=-1).
	assert.Equal(t, uint8(0), spawn[4], "ObjectType = PC")
	assert.Equal(t, int32(-1), int32(binary.LittleEndian.Uint32(spawn[72:76])), "MaxHP = -1 for PC")
	assert.Equal(t, int32(-1), int32(binary.LittleEndian.Uint32(spawn[76:80])), "HP = -1 for PC")
	// Name [83:107] is "E2eHero" null-padded to 24 bytes.
	nameField := make([]byte, 24)
	copy(nameField, []byte("E2eHero"))
	assert.Equal(t, nameField, spawn[83:107], "spawn name field")

	// --- M4c: CZ_REQUEST_MOVE (0x0085, 5B) → ZC_NOTIFY_PLAYERMOVE (0x0087, 12B).
	// This crosses the move handler → MoveService queue → worker goroutine → real
	// Prontera pathfinder (the single-goroutine contract the world-move Runnable
	// owns) and back out the live TCP conn. The destination (60,115) is walkable
	// and reachable from the spawn cell (53,111) on prontera.gat, so the worker
	// resolves a path and emits the self-ack. ---
	var moveReqBuf bytes.Buffer
	require.NoError(t, packet.CZRequestMoveRequest{DestX: 60, DestY: 115}.Encode(&moveReqBuf))
	_, err = mapConn.Write(moveReqBuf.Bytes())
	require.NoError(t, err, "send CZ_REQUEST_MOVE")

	notifyMove := make([]byte, packet.MapNotifyPlayerMoveResponse{}.Size()) // 12
	require.NoError(t, mapConn.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err = io.ReadFull(mapConn, notifyMove)
	require.NoError(t, err, "no ZC_NOTIFY_PLAYERMOVE received; move worker likely did not resolve the path")
	require.Equal(t, packet.HeaderZCNOTIFYPLAYERMOVE, binary.LittleEndian.Uint16(notifyMove[0:2]),
		"expected ZC_NOTIFY_PLAYERMOVE header")

	// Build the expected frame with a DUMMY clock and compare only the
	// deterministic slices: header [0:2] and the 3-byte packed src/dest [6:12].
	// [2:6] is moveStartTime, the live systemClock's UnixMilli tick — it is
	// correct (non-zero) but non-deterministic across runs, so it is not
	// asserted byte-exact.
	var expectMove bytes.Buffer
	require.NoError(t, (packet.MapNotifyPlayerMoveResponse{
		MoveStartTime: 0, SrcX: 53, SrcY: 111, DestX: 60, DestY: 115,
	}).Encode(&expectMove))
	want := expectMove.Bytes()
	assert.Equal(t, want[0:2], notifyMove[0:2], "ZC_NOTIFY_PLAYERMOVE header")
	assert.Equal(t, want[6:9], notifyMove[6:9], "self-ack packed src (53,111)")
	assert.Equal(t, want[9:12], notifyMove[9:12], "self-ack packed dest (60,115)")
}

// readLengthPrefixedPacket reads one rAthena packet whose uint16 length at byte
// offset 2 is the total wire length (header included) — the framing of
// HC_ACCEPT_ENTER. It peeks the 4-byte header, then reads the remainder.
func readLengthPrefixedPacket(t *testing.T, r io.Reader) []byte {
	t.Helper()
	hdr := make([]byte, 4)
	_, err := io.ReadFull(r, hdr)
	require.NoError(t, err, "short read on packet header")
	total := int(binary.LittleEndian.Uint16(hdr[2:4]))
	require.GreaterOrEqual(t, total, 4, "packet length field < header size")
	body := make([]byte, total-4)
	_, err = io.ReadFull(r, body)
	require.NoError(t, err, "short read on packet body")
	return append(hdr, body...)
}

// seedCharForAccount inserts a single char row for accountID at slot 0 with the
// given name and last_map, returns its auto-increment char_id, and registers a
// cleanup that removes every char row it created. It connects to MariaDB with
// the same DSN app.Serve uses, and clears any pre-existing chars for the account
// first so the slot-0 selection in the test is deterministic.
func seedCharForAccount(t *testing.T, cfg *config.Config, accountID uint32, name, lastMap string) uint32 {
	t.Helper()
	gdb, err := gorm.Open(mysql.Open(cfg.DBConnString()), &gorm.Config{})
	require.NoError(t, err, "open gorm to seed char")
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	// t.Cleanup is LIFO: register the pool close FIRST so it runs LAST, after
	// the row-delete cleanup below, which must execute on an open connection.
	t.Cleanup(func() { _ = sqlDB.Close() })

	// accountID is the migration-seeded `test` account (test infra, no real
	// chars); clearing its rows keeps slot 0 deterministic across runs.
	require.NoError(t, gdb.Exec("DELETE FROM `char` WHERE account_id = ?", accountID).Error,
		"clear existing chars for test account")

	require.NoError(t, gdb.Exec(
		`INSERT INTO `+"`char`"+` (account_id, char_num, name, class, base_level, job_level,
		   base_exp, job_exp, zeny, str, agi, vit, `+"`int`"+`, dex, luk,
		   max_hp, hp, max_sp, sp, status_point, skill_point,
		   hair, hair_color, clothes_color, body, weapon, shield,
		   head_top, head_mid, head_bottom, robe,
		   last_map, last_x, last_y, sex, option, karma, manner)
		 VALUES (?, 0, ?, 0, 12, 10, 500, 200, 3000, 9,1,1,1,1,1, 200,100,50,50,0,0, 2,1,0,0,0,0, 0,0,0,0, ?, 53,111, 1, 0, 0, 0)`,
		accountID, name, lastMap).Error, "insert seed char row")

	var charID uint32
	require.NoError(t, gdb.Raw(
		"SELECT char_id FROM `char` WHERE account_id = ? AND char_num = 0", accountID).
		Scan(&charID).Error, "read back seeded char_id")
	require.NotZero(t, charID, "seeded char_id is 0")

	// Remove the row we inserted; runs before the pool close above.
	t.Cleanup(func() {
		_ = gdb.Exec("DELETE FROM `char` WHERE char_id = ?", charID).Error
	})
	return charID
}

// resetCharsForAccount opens a GORM session against the same DSN app.Serve uses,
// deletes every existing char row for accountID so the make-char test starts from
// an empty slate (slot 0 free, no name collisions), and registers a cleanup
// (LIFO: the row-delete registers second so it runs before the pool close) that
// removes any chars created during the test. It returns the session so the test
// can read back the created char_id.
func resetCharsForAccount(t *testing.T, cfg *config.Config, accountID uint32) *gorm.DB {
	t.Helper()
	gdb, err := gorm.Open(mysql.Open(cfg.DBConnString()), &gorm.Config{})
	require.NoError(t, err, "open gorm to reset chars")
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	// t.Cleanup is LIFO: register the pool close first so it runs last.
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, gdb.Exec("DELETE FROM `char` WHERE account_id = ?", accountID).Error,
		"clear existing chars for test account")
	t.Cleanup(func() {
		_ = gdb.Exec("DELETE FROM `char` WHERE account_id = ?", accountID).Error
	})
	return gdb
}
