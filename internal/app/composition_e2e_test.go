//go:build e2e

package app_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"github.com/bouroo/goAthena/internal/app"
	"github.com/bouroo/goAthena/internal/config"
	worlddomain "github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/equip"
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
	// zone.map_dir / mob_db_path / mob_spawns_path are repo-root-relative paths
	// in config.yaml, resolved against the server's CWD at runtime. A test
	// process runs with its own CWD (the package dir), so absolutize each against
	// the repo root or the FileMapStore / mobdb loaders would look for the files
	// under the test's CWD and find nothing. The *_path fields are omitempty, so
	// only rebase the ones actually set (Clean("")=="." would otherwise turn an
	// unset path into the repo root).
	cfg.Zone.MapDir = filepath.Join(root, filepath.Clean(cfg.Zone.MapDir))
	// ScriptDir is relative in config.yaml (e.g. ./data/npc); the shop data dir
	// is derived from it (filepath.Dir(scriptDir) + "/shop"), so both must resolve
	// from the repo root, not the test's package CWD.
	if cfg.Zone.ScriptDir != "" {
		cfg.Zone.ScriptDir = filepath.Join(root, filepath.Clean(cfg.Zone.ScriptDir))
	}
	if cfg.Zone.MobDBPath != "" {
		cfg.Zone.MobDBPath = filepath.Join(root, filepath.Clean(cfg.Zone.MobDBPath))
	}
	if cfg.Zone.MobSpawnsPath != "" {
		cfg.Zone.MobSpawnsPath = filepath.Join(root, filepath.Clean(cfg.Zone.MobSpawnsPath))
	}
	if cfg.Zone.DBPath != "" {
		// M9c-1: item_db resolves through DBRoot() (<db_path>/{re,pre-re}). The
		// fork ships item_db as three sub-files there; without re-basing db_path to
		// the repo root the item_db loader (and drop resolution) would look under
		// the test CWD and find nothing.
		cfg.Zone.DBPath = filepath.Join(root, filepath.Clean(cfg.Zone.DBPath))
	}
	if cfg.Zone.SkillDBPath != "" {
		cfg.Zone.SkillDBPath = filepath.Join(root, filepath.Clean(cfg.Zone.SkillDBPath))
	}
	return cfg
}

// rebindListenersToEphemeralPorts moves HTTP, gRPC, and every gateway listener
// (login/char TCP + WS, map TCP + WS) onto free loopback ports so the test never
// collides with a running goathena serve, and returns the gateway TCP address the
// test dials. All Port fields validate min=1, so 0 is unusable; freePort yields
// valid, non-conflicting ports. The map listener addresses are left on
// cfg.Gateway.MapAddr / MapWSAddr for the map-enter tests to wait on and dial.
func rebindListenersToEphemeralPorts(t *testing.T, cfg *config.Config) string {
	t.Helper()
	cfg.HTTP.Host, cfg.HTTP.Port = "127.0.0.1", freePort(t)
	cfg.GRPC.Host, cfg.GRPC.Port = "127.0.0.1", freePort(t)
	cfg.App.Host, cfg.App.Port = "127.0.0.1", freePort(t)
	tcpAddr := net.JoinHostPort("127.0.0.1", strconv.Itoa(freePort(t)))
	cfg.Gateway.TCP.Addr = tcpAddr
	cfg.Gateway.WS.Addr = net.JoinHostPort("127.0.0.1", strconv.Itoa(freePort(t)))
	cfg.Gateway.MapAddr = net.JoinHostPort("127.0.0.1", strconv.Itoa(freePort(t)))
	cfg.Gateway.MapWSAddr = net.JoinHostPort("127.0.0.1", strconv.Itoa(freePort(t)))
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
	seedCharID := seedCharForAccount(t, cfg, seededAccountID, "E2eHero", "prontera", 53, 111)

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

	// --- start_items: the CH_MAKE_CHAR handler seeded the default rAthena
	// starting gear into the created novice's bag, one inventory row per
	// configured triple with equip=location (char.cpp:1518-1519). Knife in the
	// right hand (EQP_HAND_R=2), Cotton Shirt on the body (EQP_ARMOR=16), and the
	// bonus usable loose (location 0). Proves the seeder ran through the real
	// handler into the real GORM inventory table. ---
	type startInvRow struct {
		NameID uint32 `gorm:"column:nameid"`
		Amount uint32 `gorm:"column:amount"`
		Equip  uint32 `gorm:"column:equip"`
	}
	var seeded []startInvRow
	require.NoError(t, gdb.Raw(
		"SELECT nameid, amount, equip FROM `inventory` WHERE char_id = ? ORDER BY nameid", dbCharID).
		Scan(&seeded).Error, "read back start_items rows from MariaDB")
	require.Len(t, seeded, 3, "default start_items = 3 rows")
	assert.Equal(t, []startInvRow{{1201, 1, 2}, {2301, 1, 16}, {23484, 1, 0}}, seeded,
		"Knife equipped right-hand, Cotton Shirt equipped body, usable loose in bag")

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
	seedCharID := seedCharForAccount(t, cfg, seededAccountID, "E2eHero", "prontera", 53, 111)

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

	// --- M7c: enter status burst. EnterWorld writes the self-spawn, then the
	// ZC_STATUS + par-change family, before the spawn exchange or any later
	// frame. Drain it so the move-ack read below lands on ZC_NOTIFY_PLAYERMOVE,
	// not the burst's tail. ---
	drainEnterStatusBurst(t, mapConn)

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

	// --- LoadEndAck (CZ_NOTIFY_ACTORINIT 0x007d): the client signals
	// map-load-complete right after the enter burst, and rAthena replies with
	// the inventory/skill/hotkey init burst (clif_parse_LoadEndAck). Send the
	// 2-byte cmd-only frame and assert the first reply is ZC_INVENTORY_START
	// (0x0b08) — proof the opcode is handled (not ErrNoHandler) and the init
	// handshake fires. The full 6-frame burst is byte-asserted in the unit test. ---
	var loadEndAck [2]byte
	binary.LittleEndian.PutUint16(loadEndAck[:], packet.HeaderCZNOTIFYACTORINIT)
	_, err = mapConn.Write(loadEndAck[:])
	require.NoError(t, err, "send CZ_NOTIFY_ACTORINIT (0x007d)")
	wantStart := packet.EncodeInventoryStart()
	gotStart := make([]byte, len(wantStart))
	require.NoError(t, mapConn.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err = io.ReadFull(mapConn, gotStart)
	require.NoError(t, err, "no ZC_INVENTORY_START reply after 0x007d; LoadEndAck handler not wired")
	assert.Equal(t, wantStart, gotStart, "LoadEndAck first reply is ZC_INVENTORY_START (0x0b08)")
}

// TestServe_MapEnter_ShowsSpawnedMobs is the M5 end-to-end proof. The real
// monolith eagerly runs SpawnAll during world.Register (before any listener
// accepts), placing the four data/mob_spawns/prontera.yml mobs onto Prontera.
// This test seeds a char co-located with the Poring spawn cell (155,165) so all
// four mobs fall inside the entering player's 15-cell AOI viewport, then asserts
// the live map TCP conn delivers the PC self-spawn followed by four BL_MOB
// ZC_SPAWN_UNIT frames — one per mob_db sprite (Poring/Lunatic/Drops/Spore),
// each carrying its mob_db id in the job slot and an EntityID in the mob partition
// (>= MobIDBase, disjoint from player account_ids). It crosses every M5 seam
// together: mob_db load → spawn-file parse → AOI placement → enter-world spawn
// exchange → live TCP write.
func TestServe_MapEnter_ShowsSpawnedMobs(t *testing.T) {
	requireStack(t)

	cfg := loadConfigForE2E(t)
	tcpAddr := rebindListenersToEphemeralPorts(t, cfg)
	mapAddr := cfg.Gateway.MapAddr
	require.NoError(t, cfg.Validate(), "config drift: config.yaml no longer validates")

	// Co-locate the char with the Poring spawn cell (155,165). All four
	// prontera.yml mobs — Poring 155,165; Lunatic 165,165; Drops 155,175; Spore
	// 165,175 — sit within a 15-cell Chebyshev box of this point, so the
	// entering player's spawn exchange surfaces every one of them.
	seedCharID := seedCharForAccount(t, cfg, seededAccountID, "E2eHero", "prontera", 155, 165)

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
	waitGatewayOrFatal(t, mapAddr, serveErr)

	// Login on the login/char listener to mint the session the map trust gate
	// verifies on the dedicated map connection.
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

	// Reconnect to the dedicated map listener and CZ_ENTER.
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

	// ZC_ACCEPT_ENTER (0x02eb, 13B): the trust gate admitted the session.
	resp := make([]byte, packet.MapAcceptEnterResponse{}.Size())
	_, err = io.ReadFull(mapConn, resp)
	require.NoError(t, err, "no ZC_ACCEPT_ENTER received; CZ_ENTER verification likely failed")
	require.Equal(t, packet.HeaderZCACCEPTENTER, binary.LittleEndian.Uint16(resp[0:2]),
		"expected ZC_ACCEPT_ENTER header")

	// --- ZC_SPAWN_UNIT frames. EnterWorld writes the PC self-spawn, then the
	// enter status burst (ZC_STATUS + par-change family), then the exchange frame
	// for every mob in AOI range. The self-spawn and the burst are fixed in
	// width, so the test reads the self-spawn, drains the burst, then reads the
	// mob spawns. Mobs do not move on their first tick sighting (they settle one
	// WalkSpeed interval before ambling), so no ZC_UNIT_WALKING interleaves the
	// mob burst in the enter window: the next bytes after the burst are exactly
	// the four mob spawns. Their order over the AOI towers is not stable, so they
	// are collected into a set, not sequenced. ---
	const (
		spawnSize = 107 // packet.SpawnUnitResponse{}.Size()
		mobCount  = 4
	)

	// Frame 0: the PC self-spawn.
	self := make([]byte, spawnSize)
	require.NoError(t, mapConn.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err = io.ReadFull(mapConn, self)
	require.NoError(t, err, "no PC self-spawn received; spawn-on-enter likely failed to load the char/map")
	require.Equal(t, packet.HeaderZCSPAWNUNIT, binary.LittleEndian.Uint16(self[0:2]),
		"frame 0 header is ZC_SPAWN_UNIT")
	require.Equal(t, uint8(0), self[4], "frame 0 ObjectType is PC")
	require.Equal(t, seededAccountID, binary.LittleEndian.Uint32(self[5:9]),
		"frame 0 AID is the seeded account_id")
	require.Equal(t, seedCharID, binary.LittleEndian.Uint32(self[9:13]),
		"frame 0 GID is the seeded char_id")

	// Enter status burst: ZC_STATUS + the par-change family, between the
	// self-spawn and the mob spawns.
	drainEnterStatusBurst(t, mapConn)

	// Scan spawn frames collecting the 4 mobs. With scripts loaded (U3+), NPC
	// spawn frames interleave with mob frames in the enter burst; filter by
	// ObjectType == BL_MOB (5) and skip NPC spawns.
	seen := make(map[int16]string, mobCount)
	mobDeadline := time.Now().Add(5 * time.Second)
	for len(seen) < mobCount && time.Now().Before(mobDeadline) {
		require.NoError(t, mapConn.SetReadDeadline(time.Now().Add(500*time.Millisecond)))
		frame, ok := readMapFrame(t, mapConn)
		if !ok {
			continue
		}
		if binary.LittleEndian.Uint16(frame) != packet.HeaderZCSPAWNUNIT || frame[4] != 5 {
			continue // PC self-spawn, NPC spawn, or non-mob frame.
		}
		assert.GreaterOrEqual(t, binary.LittleEndian.Uint32(frame[5:9]), worlddomain.MobIDBase,
			"mob AID is in the mob partition (>= START_NPC_NUM), disjoint from account_ids")
		assert.Equal(t, uint32(0), binary.LittleEndian.Uint32(frame[9:13]),
			"mob GID is 0 (no map_session_data)")
		assert.Equal(t, int32(-1), int32(binary.LittleEndian.Uint32(frame[72:76])), "mob MaxHP = -1 at full HP")
		assert.Equal(t, int32(-1), int32(binary.LittleEndian.Uint32(frame[76:80])), "mob HP = -1 at full HP")
		seen[int16(binary.LittleEndian.Uint16(frame[23:25]))] = string(bytes.TrimRight(frame[83:107], "\x00"))
	}
	require.Len(t, seen, mobCount, "did not see all %d mob spawns in the enter burst", mobCount)

	assert.Equal(t, map[int16]string{
		1002: "Poring", 1063: "Lunatic", 1113: "Drops", 1014: "Spore",
	}, seen, "the entering player sees all four spawned mobs by sprite + name")
}

// TestServe_Combat_KillAwardsExpAndRespawns is the M6 end-to-end proof. It boots
// the real modular monolith, logs in, enters Prontera co-located with a Poring,
// and plays the full combat loop across every real boundary the milestone wires:
//
//   - CZ_ACTION_REQUEST over TCP → CombatService → ZC_NOTIFY_ACT broadcast back to
//     the attacker, carrying the pre-re naked-novice damage (BaseATK 1, reduced by
//     Poring's vit-softDEF 1, floored at 1).
//   - the killing blow → ZC_NOTIFY_VANISH, then the M9c-1 ground-drop broadcast
//     ZC_ITEM_FALL_ENTRY (0x0ADD) for the guaranteed Jellopy (rate 10000 ⇒ always
//     lands; NameID 909, IT_ETC=3 from the fork item_db), then the killer-only
//     EXP/level/status broadcasts: a level-1 novice at 8 EXP kills a Poring worth
//     2 BaseExp (8→10), crossing the level-2 threshold (10) → SPBaseExp,
//     SPBaseLevel, SPStatusPoint.
//   - MobRespawnDelay (5s) on the real time.AfterFunc → a fresh ZC_SPAWN_UNIT for
//     sprite 1002 with a NEW EntityID (a new lifetime, never the dead mob's).
//   - SaveProgression → committed to MariaDB; read back through a separate GORM
//     session confirms base_exp/base_level/status_point persisted.
//
// The Poring wanders one cell per 400ms, so the test cannot hardcode the mob's
// cell. It reads the mob's current position from the enter spawn frame (and from
// each ZC_UNIT_WALKING thereafter), steps onto that cell, and bursts attacks. A
// burst resolves in a few ms, so every in-range burst lands all its hits before
// the wander tick can move the mob; extra attacks past the kill are silently
// dropped (the torn-down mob no longer resolves ByEntity).
// Combat e2e expectations, derived from the mob_db Poring (HP 50, BaseExp 2,
// Defense 0, Vit 1, sprite 1002) and the pre-re L1 naked-novice formula (BaseATK
// 1 − vit-softDEF 1, floored at 1 = 1 dmg/hit): ~50 one-dmg hits kill, so one
// 64-attack burst (≥50 dmg) kills in the first cycle before the Poring can
// wander out of melee range; and 8 seed EXP + 2 kill EXP = 10 crosses the
// level-2 threshold, granting one level (status_point 0 → 3).
const (
	poringSprite      uint16 = 1002
	attacksPerBurst          = 64 // M9b floors naked-novice dmg at 1/hit; 64 ≥ 50 HP kills in ONE in-range burst, before the wandering Poring (1 cell/400ms) can slip out of melee range. Surplus hits a torn-down mob (silent drop). Calibrated for a single-cycle kill to mirror the pre-real-damage instant-kill behavior.
	expectedHitDamage int32  = 1
	expectedExp       int32  = 10 // 8 seed + 2 Poring
	expectedLevel     int32  = 2
	expectedStatus    int32  = 3 // statusPointsPerBaseLevel (3) × 1 level
	// M9c-1: the guaranteed Poring drop (Jellopy, rate 10000) resolved through the
	// fork's item_db_etc.yml (Id 909, Type Etc → itemdb.WireType = IT_ETC = 3).
	jellopyNameID uint32 = 909
	jellopyType   uint16 = 3
)

func TestServe_Combat_KillAwardsExpAndRespawns(t *testing.T) {
	requireStack(t)

	cfg := loadConfigForE2E(t)
	tcpAddr := rebindListenersToEphemeralPorts(t, cfg)
	mapAddr := cfg.Gateway.MapAddr
	require.NoError(t, cfg.Validate(), "config drift: config.yaml no longer validates")

	// A level-1 naked novice (all stats 1) at base_exp 8 on the Poring's home cell
	// (155,165). A Poring kill grants 2 BaseExp (8→10), crossing the level-2
	// threshold and so driving the full EXP + level-up + status-point broadcast.
	// The pre-re damage is BaseATK 1 − Poring vit-softDEF 1, floored at 1 = 1
	// dmg/hit; 50 HP needs ~50 hits (a few 12-attack bursts).
	seedCharID := seedCombatChar(t, cfg, seededAccountID, "E2eSlayer", "prontera", 155, 165)

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
	waitGatewayOrFatal(t, mapAddr, serveErr)

	// --- login → CZ_ENTER → ZC_ACCEPT_ENTER (same gate path as M3/M5) ---
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
	loginID1 := binary.LittleEndian.Uint32(accept[4:8])
	sexByte := accept[46]
	require.NotZero(t, loginID1, "AC_ACCEPT_LOGIN carried no login_id1 to echo in CZ_ENTER")
	require.NoError(t, loginConn.Close(), "close the login/char connection (map is a reconnect)")

	mapConn, err := net.Dial("tcp", mapAddr)
	require.NoError(t, err)
	defer mapConn.Close()
	var enter bytes.Buffer
	require.NoError(t, packet.CZEnterRequest{
		AccountID: seededAccountID, CharID: seedCharID, AuthCode: loginID1, ClientTime: 0, Sex: sexByte,
	}.Encode(&enter))
	_, err = mapConn.Write(enter.Bytes())
	require.NoError(t, err)
	require.NoError(t, mapConn.SetReadDeadline(time.Now().Add(5*time.Second)))
	enterResp := make([]byte, packet.MapAcceptEnterResponse{}.Size())
	_, err = io.ReadFull(mapConn, enterResp)
	require.NoError(t, err, "no ZC_ACCEPT_ENTER received; CZ_ENTER verification likely failed")
	require.Equal(t, packet.HeaderZCACCEPTENTER, binary.LittleEndian.Uint16(enterResp[0:2]),
		"expected ZC_ACCEPT_ENTER header")

	// --- locate the Poring in the enter spawn burst. EnterWorld writes the PC
	// self-spawn then every AOI mob back-to-back; the frame-dispatching reader
	// tolerates any interleaving. The Poring's EntityID is its spawn-frame AID
	// (CombatService resolves the target by EntityID), its cell the packed pos. ---
	var (
		poringID     uint32
		mobX, mobY   int16
		findDeadline = 5 * time.Second
	)
	findEnd := time.Now().Add(findDeadline)
	for poringID == 0 && time.Now().Before(findEnd) {
		require.NoError(t, mapConn.SetReadDeadline(time.Now().Add(500*time.Millisecond)))
		frame, ok := readMapFrame(t, mapConn)
		if !ok {
			continue
		}
		if binary.LittleEndian.Uint16(frame) != packet.HeaderZCSPAWNUNIT || frame[4] != 5 {
			continue // PC self-spawn (ObjectType 0) or a non-mob frame.
		}
		if binary.LittleEndian.Uint16(frame[23:25]) == poringSprite {
			poringID = binary.LittleEndian.Uint32(frame[5:9])
			mobX, mobY = decodeCell(frame[63:66])
		}
	}
	require.NotZero(t, poringID, "no Poring (sprite 1002) in the enter spawn burst within %v", findDeadline)

	// --- chase + attack until the Poring dies ---
	var (
		notifyActs [][]byte
		vanishSeen bool
		expVal     int32
		expSeen    bool
		levelVal   int32
		levelSeen  bool
		statusVal  int32
		statusSeen bool
		// capturedRespawn holds the respawned Poring's ZC_SPAWN_UNIT frame. The 5s
		// respawn timer (armed at the kill) may fire during the chase tail, the
		// reposition awaitFrame, or the dedicated drain below; handleCombat captures
		// it at whichever read site delivers it, so the assertion does not depend on
		// which one ran first.
		capturedRespawn []byte
		// drops holds the ZC_ITEM_FALL_ENTRY (0x0ADD) frames the M9c-1 drop path
		// broadcasts to the attacker on the kill. CombatService emits them between
		// NotifyVanish and the EXP/level/status updates, so they arrive in the same
		// post-burst drain as those frames; handleCombat captures them at whichever
		// read site delivers the kill window.
		drops [][]byte
		// M10a: pickupAck/pickupGone capture the loot-loop frames CZ_ITEM_PICKUP
		// yields — the success ack (ZC_ITEM_PICKUP_ACK result=0) and the floor
		// vanish (ZC_ITEM_DISAPPEAR). They may land in the same drain as the late
		// kill/EXP frames or in the dedicated pickup drain, so capture at whichever
		// read site delivers them.
		pickupAck  []byte
		pickupGone []byte
	)
	handleCombat := func(op uint16, frame []byte) {
		switch op {
		case packet.HeaderZCUNITWALKING:
			// Track the Poring's latest cell as it wanders (packed destXY).
			if binary.LittleEndian.Uint32(frame[5:9]) == poringID {
				mobX, mobY = decodeCell(frame[70:73])
			}
		case packet.HeaderZCSPAWNUNIT:
			// Capture the respawned Poring (BL_MOB, sprite 1002, fresh EntityID ≠
			// the killed poringID). The original spawn is read by the earlier
			// burst-finding loop, not here, so the only Poring spawn this handler
			// ever sees is the post-kill respawn.
			if capturedRespawn == nil && frame[4] == 5 &&
				binary.LittleEndian.Uint16(frame[23:25]) == poringSprite &&
				binary.LittleEndian.Uint32(frame[5:9]) != poringID {
				capturedRespawn = frame
			}
		case packet.HeaderZCNOTIFYACT:
			notifyActs = append(notifyActs, frame)
		case packet.HeaderZCNOTIFYVANISH:
			if binary.LittleEndian.Uint32(frame[2:6]) == poringID {
				vanishSeen = true
			}
		case packet.HeaderZCItemFallEntry:
			// Capture every floor-item drop from the kill. The Poring's Jellopy
			// (rate 10000) always lands; the four probabilistic drops (Knife_,
			// Sticky_Mucus, Apple, Empty_Bottle) may or may not, so capture them
			// all and assert only the guaranteed Jellopy below.
			drops = append(drops, frame)
		case packet.HeaderZCItemPickupAck:
			pickupAck = frame
		case packet.HeaderZCItemDisappear:
			pickupGone = frame
		case packet.HeaderZCLONGLONGPARCHANGE:
			// At PACKETVER >= 20170830 clif_updatestatus routes SP_BASEEXP through
			// the 64-bit ZC_LONGLONGPAR_CHANGE (0x0acb), so EXP rides int64 (offset
			// 4:12), not the 32-bit ZC_LONGPAR_CHANGE. The award's value (10) is far
			// under int32, so it narrows cleanly for the assertion.
			if binary.LittleEndian.Uint16(frame[2:4]) == packet.SPBaseExp {
				expVal = int32(binary.LittleEndian.Uint64(frame[4:12])) //nolint:gosec // G115: exp (10) narrows losslessly
				expSeen = true
			}
		case packet.HeaderZCPARCHANGE:
			switch binary.LittleEndian.Uint16(frame[2:4]) {
			case packet.SPBaseLevel:
				levelVal = int32(binary.LittleEndian.Uint32(frame[4:8]))
				levelSeen = true
			case packet.SPStatusPoint:
				statusVal = int32(binary.LittleEndian.Uint32(frame[4:8]))
				statusSeen = true
			}
		}
	}
	sendMove := func(x, y int16) {
		var b bytes.Buffer
		require.NoError(t, packet.CZRequestMoveRequest{DestX: x, DestY: y}.Encode(&b))
		_, err := mapConn.Write(b.Bytes())
		require.NoError(t, err)
	}
	sendAttack := func() {
		var b bytes.Buffer
		// Action is ignored by CombatService (only TargetGID is read); DMG_NORMAL
		// is the honest attack selector.
		require.NoError(t, packet.CZActionRequestRequest{TargetGID: poringID, Action: packet.DMGNormal}.Encode(&b))
		_, err := mapConn.Write(b.Bytes())
		require.NoError(t, err)
	}

	chaseEnd := time.Now().Add(30 * time.Second)
	for time.Now().Before(chaseEnd) && !vanishSeen {
		// Drain buffered frames first (late responses, mob-position updates, the
		// remaining spawn-burst frames after the Poring).
		drainFrames(t, mapConn, 150*time.Millisecond, handleCombat)
		if vanishSeen {
			break
		}
		// Step onto the Poring's current cell, then wait for the move ack so the
		// server has committed our position before the attack burst.
		sendMove(mobX, mobY)
		awaitFrame(t, mapConn, packet.HeaderZCNOTIFYPLAYERMOVE, 500*time.Millisecond, handleCombat)
		// 64 attacks per burst; at 1 dmg/hit that is ≥ 50 HP in a single in-range
		// burst, killing the Poring before its 400ms wander tick can move it out of
		// melee range. Any past the kill hit a torn-down mob and are silently
		// dropped, so the surplus is harmless insurance.
		for i := 0; i < attacksPerBurst; i++ {
			sendAttack()
		}
		drainFrames(t, mapConn, 400*time.Millisecond, handleCombat)
	}
	require.Truef(t, vanishSeen, "Poring did not die within %v (NotifyAct frames seen: %d)",
		30*time.Second, len(notifyActs))

	// --- assertions on the combat response stream ---
	require.NotEmpty(t, notifyActs, "no ZC_NOTIFY_ACT observed for the kill")
	hit := notifyActs[0]
	require.Equal(t, seededAccountID, binary.LittleEndian.Uint32(hit[2:6]),
		"ZC_NOTIFY_ACT SrcID is the attacker account")
	require.Equal(t, poringID, binary.LittleEndian.Uint32(hit[6:10]),
		"ZC_NOTIFY_ACT TargetID is the Poring's EntityID")
	require.Equal(t, expectedHitDamage, int32(binary.LittleEndian.Uint32(hit[22:26])),
		"ZC_NOTIFY_ACT Damage matches pre-re naked-novice BaseATK reduced by vit-softDEF, floored at 1")
	require.True(t, expSeen, "no SPBaseExp (ZC_LONGPAR_CHANGE) update observed")
	require.Equal(t, expectedExp, expVal, "SPBaseExp = seed 8 + Poring 2 = 10")
	require.True(t, levelSeen, "no SPBaseLevel (ZC_PAR_CHANGE) update observed — kill did not level the novice")
	require.Equal(t, expectedLevel, levelVal, "SPBaseLevel = 2")
	require.True(t, statusSeen, "no SPStatusPoint (ZC_PAR_CHANGE) update observed")
	require.Equal(t, expectedStatus, statusVal, "SPStatusPoint = 0 seed + 3/level = 3")

	// --- M9c-1 ground-drop proof: the kill must broadcast at least the guaranteed
	// Jellopy drop. Jellopy is rate 10000 (per-myriad ⇒ always), so its absence
	// means the drop path or item_db resolution failed; the probabilistic drops are
	// intentionally not asserted. NameID 909 + IT_ETC (3) come from the fork's
	// item_db_etc.yml (Id 909, Type Etc) via itemdb.WireType. ---
	require.NotEmpty(t, drops,
		"no ZC_ITEM_FALL_ENTRY observed for the kill — the guaranteed Jellopy (rate 10000) must always drop")
	var jellopy *[]byte
	for i := range drops {
		if binary.LittleEndian.Uint32(drops[i][6:10]) == jellopyNameID &&
			binary.LittleEndian.Uint16(drops[i][10:12]) == jellopyType {
			jellopy = &drops[i]
			break
		}
	}
	require.NotNil(t, jellopy,
		"no Jellopy drop (NameID %d, IT_ETC %d) among %d floor-item frames — item_db AegisName resolution or drop encoding failed",
		jellopyNameID, jellopyType, len(drops))

	// --- M10a: the loot loop. The player killed the Poring while standing on its
	// death cell (the chase steps onto mobX/mobY then bursts), and the Jellopy
	// drops onto that same cell, so the player is within pickup range (Chebyshev
	// ≤ 2). Read the ground id + drop cell from the captured ZC_ITEM_FALL_ENTRY
	// (ID at [2:6], X at [13:15], Y at [15:17]); reposition onto the cell to be
	// range-safe even if a wander tick shifted the death cell; send CZ_ITEM_PICKUP;
	// then drain for the success ack (ZC_ITEM_PICKUP_ACK result=0, nameid 909) and
	// the floor vanish (ZC_ITEM_DISAPPEAR AID=groundID). The drain is bounded so
	// the 5s respawn timer (armed at the kill) cannot starve it. ---
	groundID := binary.LittleEndian.Uint32((*jellopy)[2:6])
	dropX := int16(binary.LittleEndian.Uint16((*jellopy)[13:15]))
	dropY := int16(binary.LittleEndian.Uint16((*jellopy)[15:17]))
	sendMove(dropX, dropY)
	drainFrames(t, mapConn, 150*time.Millisecond, handleCombat)
	var pickup bytes.Buffer
	require.NoError(t, packet.CZItemPickupRequest{GroundID: groundID}.Encode(&pickup))
	_, err = mapConn.Write(pickup.Bytes())
	require.NoError(t, err, "send CZ_ITEM_PICKUP for the Jellopy drop")
	pickupEnd := time.Now().Add(3 * time.Second)
	for (pickupAck == nil || pickupGone == nil) && time.Now().Before(pickupEnd) {
		drainFrames(t, mapConn, 250*time.Millisecond, handleCombat)
	}
	require.NotNil(t, pickupAck, "no ZC_ITEM_PICKUP_ACK observed after CZ_ITEM_PICKUP — pickup handler not wired or request rejected without a fail ack")
	require.Equal(t, uint8(0), pickupAck[33], "ZC_ITEM_PICKUP_ACK result=0 (success)")
	require.Equal(t, jellopyNameID, binary.LittleEndian.Uint32(pickupAck[6:10]),
		"ZC_ITEM_PICKUP_ACK nameid is the Jellopy (909) the player picked up")
	require.NotNil(t, pickupGone, "no ZC_ITEM_DISAPPEAR observed after a successful pickup — floor vanish not broadcast")
	require.Equal(t, groundID, binary.LittleEndian.Uint32(pickupGone[2:6]),
		"ZC_ITEM_DISAPPEAR AID is the picked-up ground id")

	// --- respawn: reposition on the home cell, then await the respawned Poring.
	// di.go wires SystemRespawnScheduler (a real time.AfterFunc); the timer is
	// armed at the kill (onMobDeath.scheduleRespawn), so it fires 5s later — which
	// may land during the chase tail, the reposition awaitFrame, or the drain
	// below. handleCombat captures the spawn at whichever read site delivers it;
	// this drain loops until capture (or the 8s bound) so the assertion holds
	// regardless of which site ran first. ---
	sendMove(155, 165)
	awaitFrame(t, mapConn, packet.HeaderZCNOTIFYPLAYERMOVE, 500*time.Millisecond, handleCombat)
	respawnEnd := time.Now().Add(8 * time.Second)
	for capturedRespawn == nil && time.Now().Before(respawnEnd) {
		require.NoError(t, mapConn.SetReadDeadline(time.Now().Add(500*time.Millisecond)))
		frame, ok := readMapFrame(t, mapConn)
		if !ok {
			continue
		}
		handleCombat(binary.LittleEndian.Uint16(frame), frame)
	}
	require.NotNil(t, capturedRespawn,
		"no Poring respawn (ZC_SPAWN_UNIT sprite 1002, fresh ID) within 8s; respawn timer did not fire")
	require.Equal(t, uint8(5), capturedRespawn[4], "respawned unit ObjectType is BL_MOB")
	require.Equal(t, poringSprite, binary.LittleEndian.Uint16(capturedRespawn[23:25]),
		"respawned unit sprite is the Poring (1002)")
	require.NotEqual(t, poringID, binary.LittleEndian.Uint32(capturedRespawn[5:9]),
		"respawned Poring has a fresh EntityID, not the dead mob's")

	// --- persistence: read the committed progression back from MariaDB via a
	// separate session. awardKill.SaveProgression commits synchronously before
	// the handler returns, so by the time the wire packets above are drained the
	// row is long since committed. ---
	gdb, err := gorm.Open(mysql.Open(cfg.DBConnString()), &gorm.Config{})
	require.NoError(t, err, "open gorm to read back progression")
	t.Cleanup(func() { sqlDB, _ := gdb.DB(); _ = sqlDB.Close() })
	var prog struct {
		BaseExp     uint64
		BaseLevel   uint16
		StatusPoint uint32
	}
	require.NoError(t, gdb.Raw(
		"SELECT base_exp, base_level, status_point FROM `char` WHERE char_id = ?", seedCharID).
		Scan(&prog).Error, "read back persisted progression")
	assert.Equal(t, uint64(expectedExp), prog.BaseExp, "persisted base_exp = 8 seed + 2 Poring")
	assert.Equal(t, uint16(expectedLevel), prog.BaseLevel, "persisted base_level = 2")
	assert.Equal(t, uint32(expectedStatus), prog.StatusPoint, "persisted status_point = 3")
}

// TestServe_Chat_BroadcastsToAOI is the M11 end-to-end: a player enters the world,
// sends CZ_GLOBAL_MESSAGE ("hello"), and the server replies ZC_NOTIFY_CHAT carrying
// the player's account_id as GID and the message verbatim. This exercises every M11
// seam together: the chat handler resolving the sender from the player registry,
// loading the sender's map, and broadcasting ZC_NOTIFY_CHAT through the map AOI —
// all on the real map-role dispatch path.
func TestServe_Chat_BroadcastsToAOI(t *testing.T) {
	requireStack(t)

	cfg := loadConfigForE2E(t)
	tcpAddr := rebindListenersToEphemeralPorts(t, cfg)
	mapAddr := cfg.Gateway.MapAddr
	require.NoError(t, cfg.Validate(), "config drift: config.yaml no longer validates")

	seedCharID := seedCharForAccount(t, cfg, seededAccountID, "E2eChat", "prontera", 53, 111)

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
	waitGatewayOrFatal(t, mapAddr, serveErr)

	// --- Login to capture the session token the CZ_ENTER gate verifies. ---
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
	require.NoError(t, err, "no AC_ACCEPT_LOGIN; login likely failed against the DB")
	loginID1 := binary.LittleEndian.Uint32(accept[4:8])
	sexByte := accept[46]
	require.NotZero(t, loginID1)
	require.NoError(t, loginConn.Close())

	mapConn := connectAndEnterMap(t, mapAddr, seedCharID, loginID1, sexByte, "E2eChat")
	defer mapConn.Close()

	// --- Send CZ_GLOBAL_MESSAGE (0x008c) with "hello". ---
	const chatMsg = "hello"
	var chat bytes.Buffer
	require.NoError(t, packet.CZGlobalMessageRequest{Message: chatMsg}.Encode(&chat))
	_, err = mapConn.Write(chat.Bytes())
	require.NoError(t, err, "send CZ_GLOBAL_MESSAGE")

	// --- Drain for ZC_NOTIFY_CHAT (0x008d). The sender receives the echo.
	var chatFrame []byte
	chatFrame, ok := awaitFrame(t, mapConn, packet.HeaderZCNOTIFYCHAT, 5*time.Second, nil)
	require.True(t, ok, "no ZC_NOTIFY_CHAT observed after CZ_GLOBAL_MESSAGE — chat handler not wired")

	// [0:2] cmd = 0x008d, [2:4] length, [4:8] GID, [8:] message.
	require.Equal(t, packet.HeaderZCNOTIFYCHAT, binary.LittleEndian.Uint16(chatFrame[0:2]),
		"ZC_NOTIFY_CHAT header")
	packetLen := binary.LittleEndian.Uint16(chatFrame[2:4])
	assert.Greater(t, packetLen, uint16(8), "ZC_NOTIFY_CHAT must carry a non-empty message")
	assert.Equal(t, seededAccountID, binary.LittleEndian.Uint32(chatFrame[4:8]),
		"ZC_NOTIFY_CHAT GID is the sender's account_id")
	gotMsg := string(bytes.TrimRight(chatFrame[8:], "\x00"))
	assert.Equal(t, chatMsg, gotMsg, "ZC_NOTIFY_CHAT message matches the sent chat text")
}

// TestServe_Inventory_EquipWeaponRaisesATK is the M10b end-to-end: a Knife
// (nameid 1201 — the fork's right-hand weapon, Attack 17, EquipLevelMin 1,
// Right_Hand only, no View column) is seeded in the char's bag, the player
// enters the world, sends CZ_REQ_WEAR_EQUIP for the Knife's grid slot, and the
// server answers ZC_REQ_WEAR_EQUIP_ACK result=1 (right-hand bitmask, ItemSprite
// 0 — weapons carry no View) plus a fresh ZC_STATUS whose right-side ATK rose to
// the Knife's 17. This exercises every M10b seam together: the seeded bag row →
// inventory port load → item_db resolution → level + location gate → equip
// bitmask persist → wear ack encode → character reload + equipment fold into the
// stat recompute → ZC_STATUS emit, all on the real map-role dispatch path.
func TestServe_Inventory_EquipWeaponRaisesATK(t *testing.T) {
	requireStack(t)

	cfg := loadConfigForE2E(t)
	tcpAddr := rebindListenersToEphemeralPorts(t, cfg)
	mapAddr := cfg.Gateway.MapAddr
	require.NoError(t, cfg.Validate(), "config drift: config.yaml no longer validates")

	// A level-1 naked novice (all stats 1) — the Knife's EquipLevelMin is 1, so the
	// level gate passes. The Knife is seeded as the char's single bag row (grid slot 0).
	seedCharID := seedCombatChar(t, cfg, seededAccountID, "E2eEquip", "prontera", 155, 165)
	seedKnifeInBag(t, cfg, seedCharID)

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
	waitGatewayOrFatal(t, mapAddr, serveErr)

	// --- login to capture the session token the CZ_ENTER gate verifies. ---
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
	require.NoError(t, err, "no AC_ACCEPT_LOGIN; login likely failed against the DB")
	loginID1 := binary.LittleEndian.Uint32(accept[4:8])
	sexByte := accept[46]
	require.NotZero(t, loginID1)
	require.NoError(t, loginConn.Close())

	mapConn := connectAndEnterMap(t, mapAddr, seedCharID, loginID1, sexByte, "E2eEquip")
	defer mapConn.Close()

	// --- LoadEndAck init burst: the client sends 0x007d after map load and the
	// server replies with the populated inventory list. The seeded Knife (nameid
	// 1201, a weapon) must appear in ZC_INVENTORY_ITEMLIST_EQUIP — proof the
	// LoadEndAck handler loads the persisted bag and splits it by item_db type
	// (weapon → equip list), instead of the empty init it emitted before this. ---
	var loadEndAckCmd [2]byte
	binary.LittleEndian.PutUint16(loadEndAckCmd[:], packet.HeaderCZNOTIFYACTORINIT)
	_, err = mapConn.Write(loadEndAckCmd[:])
	require.NoError(t, err, "send CZ_NOTIFY_ACTORINIT (0x007d)")
	equipList, ok := awaitFrame(t, mapConn, packet.HeaderZCINVENTORYITEMLISTEQUIP, 3*time.Second, nil)
	require.True(t, ok, "no ZC_INVENTORY_ITEMLIST_EQUIP in the LoadEndAck burst; populated inventory list not emitted")
	require.GreaterOrEqual(t, len(equipList), 9, "equip list frame too short for one item entry")
	assert.Equal(t, uint16(1201), binary.LittleEndian.Uint16(equipList[7:9]),
		"equip list carries the seeded Knife (nameid 1201) at item slot 0")

	// --- equip the Knife (grid slot 0) in the right hand, then drain for the
	// wear ack and the refreshed ZC_STATUS. handle captures both at whichever read
	// site delivers them; the bounded loop keeps the 5s respawn timer from starving
	// it (no mob is engaged here, but the drain contract is the same as the loot
	// loop). ---
	var (
		wearAck      []byte
		eqStatus     []byte
		spriteChange []byte
	)
	handle := func(op uint16, frame []byte) {
		switch op {
		case packet.HeaderZCREQWEAREQUIPACKV5:
			wearAck = frame
		case packet.HeaderZCSTATUS:
			eqStatus = frame
		case packet.HeaderZCSPRITECHANGE:
			spriteChange = frame
		}
	}
	var req bytes.Buffer
	require.NoError(t, packet.CZReqWearEquipRequest{Index: 0, Position: equip.HandRight}.Encode(&req))
	_, err = mapConn.Write(req.Bytes())
	require.NoError(t, err, "send CZ_REQ_WEAR_EQUIP for the Knife")
	equipEnd := time.Now().Add(3 * time.Second)
	for (wearAck == nil || eqStatus == nil || spriteChange == nil) && time.Now().Before(equipEnd) {
		drainFrames(t, mapConn, 250*time.Millisecond, handle)
	}

	// --- assertions on the equip response stream ---
	require.NotNil(t, wearAck,
		"no ZC_REQ_WEAR_EQUIP_ACK observed — equip handler not wired or request rejected without a fail ack")
	require.Equal(t, uint8(1), wearAck[10],
		"ZC_REQ_WEAR_EQUIP_ACK result=1 (success)")
	require.Equal(t, uint32(equip.HandRight), binary.LittleEndian.Uint32(wearAck[4:8]),
		"ZC_REQ_WEAR_EQUIP_ACK wearLocation = right-hand bitmask")
	require.Equal(t, uint16(0), binary.LittleEndian.Uint16(wearAck[8:10]),
		"ZC_REQ_WEAR_EQUIP_ACK ItemSpriteNumber = 0 (the Knife carries no View column)")

	require.NotNil(t, eqStatus,
		"no ZC_STATUS observed after a successful equip — stat recompute not emitted")
	require.Equal(t, int16(17), int16(binary.LittleEndian.Uint16(eqStatus[18:20])), //nolint:gosec // G115: ATK 17 fits int16
		"ZC_STATUS Atk2 = Knife ATK 17 (weapon contribution folded into the recompute)")

	// P2A: the equip also broadcasts a ZC_SPRITE_CHANGE (0x01d7) so the actor +
	// AOI neighbors see the weapon sprite update. AREA includes the originator, so
	// the equipping connection receives its own frame. The Knife's item_db SubType
	// is "Dagger" → LOOK_WEAPON val=1; the LOOK_WEAPON type byte combines weapon+
	// shield (clif_changelook PACKETVER>=4 path); no shield is worn (val2=0).
	require.NotNil(t, spriteChange,
		"no ZC_SPRITE_CHANGE observed after equip — look-sprite broadcast not emitted")
	require.Equal(t, uint16(packet.HeaderZCSPRITECHANGE), binary.LittleEndian.Uint16(spriteChange[0:2]),
		"ZC_SPRITE_CHANGE opcode = 0x01d7")
	require.Equal(t, seededAccountID, binary.LittleEndian.Uint32(spriteChange[2:6]),
		"ZC_SPRITE_CHANGE GID = the equipping player's account_id (bl.id)")
	require.Equal(t, packet.LookWeapon, spriteChange[6],
		"ZC_SPRITE_CHANGE type = LOOK_WEAPON(2)")
	require.Equal(t, uint32(1), binary.LittleEndian.Uint32(spriteChange[7:11]),
		"ZC_SPRITE_CHANGE val = 1 (Knife SubType Dagger → W_DAGGER)")
	require.Equal(t, uint32(0), binary.LittleEndian.Uint32(spriteChange[11:15]),
		"ZC_SPRITE_CHANGE val2 = 0 (no shield worn)")

	// P2b: the worn weapon look is persisted to the char DB so a logout/login
	// re-spawns with the same sprite (rAthena persists via chrif_save; goAthena has
	// no logout save, so SaveLook commits it at equip time). The Knife → W_DAGGER(1);
	// the seeded default was 0.
	require.Equal(t, uint16(1), readCharWeapon(t, cfg, seedCharID),
		"char.weapon persisted = W_DAGGER(1) after equip (not reverted to 0 on relog)")
}

// TestServe_UseItem_HealsHPAndConsumesCount is the M12 end-to-end: a player
// picks a Healing item from inventory (CZ_USE_ITEM2 0x0439), the server consumes
// one unit, applies the item's itemheal HP, replies ZC_USE_ITEM_ACK2, and
// broadcasts the new HP via ZC_PAR_CHANGE. The Red Potion (id 503, itemheal
// 100,50) loaded from the forked item_db_usable.yml restores HP directly, so the
// test seeds the char's HP below MaxHP and asserts the post-use HP is greater
// than the pre-use value. The bag decrement is observed by replaying the
// inventory load through the same GORM session the test already opens for
// seeding, so the M10a success-only invariant (one unit consumed) is verified
// without re-driving CZ_USE_ITEM2.
func TestServe_UseItem_HealsHPAndConsumesCount(t *testing.T) {
	requireStack(t)

	cfg := loadConfigForE2E(t)
	tcpAddr := rebindListenersToEphemeralPorts(t, cfg)
	mapAddr := cfg.Gateway.MapAddr
	require.NoError(t, cfg.Validate(), "config drift: config.yaml no longer validates")

	// Seed a char with HP=40, MaxHP=40 and immediately drop HP to 10 so the
	// post-use ZC_PAR_CHANGE SPHP must rise above 10. Use seedCombatChar (HP=40)
	// for the char shape, then UPDATE the row to lower HP.
	seedCharID := seedCombatChar(t, cfg, seededAccountID, "E2eHealer", "prontera", 53, 111)
	seedRedPotionInBag(t, cfg, seedCharID)
	lowerCharHP(t, cfg, seedCharID, 10)

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
	waitGatewayOrFatal(t, mapAddr, serveErr)

	// --- login → CZ_ENTER → ZC_ACCEPT_ENTER ---
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
	loginID1 := binary.LittleEndian.Uint32(accept[4:8])
	sexByte := accept[46]
	require.NotZero(t, loginID1)
	require.NoError(t, loginConn.Close())

	mapConn := connectAndEnterMap(t, mapAddr, seedCharID, loginID1, sexByte, "E2eHealer")
	defer mapConn.Close()

	// --- capture pre-use HP from the bag so the assertion can require a strict
	// increase. The bag row landed in the seed (amount=2) and the char's HP=10
	// after the lowerCharHP update, so the post-use ZC_PAR_CHANGE SPHP must be
	// strictly greater than 10. ---
	preUseHP := uint32(10)
	require.Equal(t, preUseHP, readCharHP(t, cfg, seedCharID), "seed HP must match the pre-use baseline")

	// --- send CZ_USE_ITEM2 (0x0439, 8B) for grid slot 0 (the Red Potion). The
	// AID field is informational; the dispatcher's per-conn auth is the trust
	// anchor the handler uses. ---
	var use bytes.Buffer
	require.NoError(t, packet.CZUseItemRequest{Index: 0, AID: seededAccountID}.Encode(&use))
	_, err = mapConn.Write(use.Bytes())
	require.NoError(t, err, "send CZ_USE_ITEM2")

	// --- drain for the ZC_USE_ITEM_ACK2 + ZC_PAR_CHANGE reply set. The handler
	// emits the ack immediately, then the two par-change frames (SPHP, SPSP);
	// no other opcode sits between them. ---
	var useAck []byte
	var hpPar []byte
	useEnd := time.Now().Add(3 * time.Second)
	for (useAck == nil || hpPar == nil) && time.Now().Before(useEnd) {
		drainFrames(t, mapConn, 250*time.Millisecond, func(op uint16, frame []byte) {
			switch op {
			case packet.HeaderZCUSEITEMACK2:
				useAck = frame
			case packet.HeaderZCPARCHANGE:
				if binary.LittleEndian.Uint16(frame[2:4]) == packet.SPHP {
					hpPar = frame
				}
			}
		})
	}

	require.NotNil(t, useAck, "no ZC_USE_ITEM_ACK2 observed after CZ_USE_ITEM2 — use-item handler not wired or rejected without an ack")
	// Header [0:2] = 0x01c8; Index [2:4] == 0; ItemID [4:6] == 503; AID [6:10] ==
	// seededAccountID; Amount [10:12] == 1 (the post-decrement stack); Result [12] == 1.
	require.Equal(t, packet.HeaderZCUSEITEMACK2, binary.LittleEndian.Uint16(useAck[0:2]),
		"ZC_USE_ITEM_ACK2 header")
	assert.Equal(t, uint16(0), binary.LittleEndian.Uint16(useAck[2:4]),
		"ZC_USE_ITEM_ACK2 index = 0 (the Red Potion's grid slot)")
	assert.Equal(t, uint16(503), binary.LittleEndian.Uint16(useAck[4:6]),
		"ZC_USE_ITEM_ACK2 itemID = 503 (Red_Potion)")
	assert.Equal(t, seededAccountID, binary.LittleEndian.Uint32(useAck[6:10]),
		"ZC_USE_ITEM_ACK2 AID = the verified account")
	assert.Equal(t, uint16(1), binary.LittleEndian.Uint16(useAck[10:12]),
		"ZC_USE_ITEM_ACK2 amount = 1 (the post-decrement stack)")
	assert.Equal(t, uint8(1), useAck[12], "ZC_USE_ITEM_ACK2 Result = 1 (success)")

	require.NotNil(t, hpPar, "no ZC_PAR_CHANGE SPHP observed after a successful use — HP broadcast missing")
	hpAfter := int32(binary.LittleEndian.Uint32(hpPar[4:8])) //nolint:gosec // G115: HP well under int32 max
	assert.Greater(t, hpAfter, int32(preUseHP),
		"ZC_PAR_CHANGE SPHP must be greater than the pre-use HP (10): got %d", hpAfter)

	// --- the bag was consumed by one unit. Read back through GORM: the seed
	// inserted amount=2, so the post-use row should carry amount=1. ---
	gdb, err := gorm.Open(mysql.Open(cfg.DBConnString()), &gorm.Config{})
	require.NoError(t, err, "open gorm to read back bag")
	t.Cleanup(func() { sqlDB, _ := gdb.DB(); _ = sqlDB.Close() })
	var postAmount uint32
	require.NoError(t, gdb.Raw(
		"SELECT amount FROM inventory WHERE char_id = ? AND nameid = 503 ORDER BY id", seedCharID).
		Scan(&postAmount).Error, "read back post-use Red Potion amount")
	assert.Equal(t, uint32(1), postAmount, "Red Potion stack decremented from 2 to 1")
}

// connectAndEnterMap opens a fresh connection to the dedicated map listener,
// sends CZ_ENTER, and asserts the full enter exchange — ZC_ACCEPT_ENTER (trust
// gate admitted the session) then ZC_SPAWN_UNIT (SpawnService loaded the char and
// self-spawned) — draining the enter status burst afterward so the caller's next
// read lands on a clean stream. It is the reusable enter primitive for the
// restart e2e, which must enter, return to char select, and re-enter.
func connectAndEnterMap(t *testing.T, mapAddr string, charID uint32, loginID1 uint32, sexByte uint8, name string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", mapAddr)
	require.NoError(t, err)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))

	var enter bytes.Buffer
	require.NoError(t, packet.CZEnterRequest{
		AccountID: seededAccountID, CharID: charID, AuthCode: loginID1, ClientTime: 0, Sex: sexByte,
	}.Encode(&enter))
	_, err = conn.Write(enter.Bytes())
	require.NoError(t, err)

	resp := make([]byte, packet.MapAcceptEnterResponse{}.Size())
	_, err = io.ReadFull(conn, resp)
	require.NoError(t, err, "no ZC_ACCEPT_ENTER; CZ_ENTER verification likely failed")
	require.Equal(t, packet.HeaderZCACCEPTENTER, binary.LittleEndian.Uint16(resp[0:2]), "expected ZC_ACCEPT_ENTER")

	spawn := make([]byte, packet.SpawnUnitResponse{}.Size())
	_, err = io.ReadFull(conn, spawn)
	require.NoError(t, err, "no ZC_SPAWN_UNIT; spawn-on-enter likely failed (ErrPlayerAlreadyRegistered?)")
	require.Equal(t, packet.HeaderZCSPAWNUNIT, binary.LittleEndian.Uint16(spawn[0:2]), "expected ZC_SPAWN_UNIT")
	assert.Equal(t, seededAccountID, binary.LittleEndian.Uint32(spawn[5:9]), "spawn AID = account_id")
	assert.Equal(t, charID, binary.LittleEndian.Uint32(spawn[9:13]), "spawn GID = char_id")

	drainEnterStatusBurst(t, conn)
	return conn
}

// TestServe_Restart_ReturnsToCharSelectAndReenters is the M7d end-to-end: a
// player enters the world, sends CZ_RESTART (type 1 = return to char select), and
// the server replies ZC_RESTART_ACK(type=1) then closes the map connection. The
// client then reconnects and re-enters CZ_ENTER — which must succeed, proving the
// char-select teardown unregistered the player (a lingering entry would make the
// re-enter's SpawnService.Register hit ErrPlayerAlreadyRegistered and drop the
// spawn frame).
func TestServe_Restart_ReturnsToCharSelectAndReenters(t *testing.T) {
	requireStack(t)

	cfg := loadConfigForE2E(t)
	tcpAddr := rebindListenersToEphemeralPorts(t, cfg)
	mapAddr := cfg.Gateway.MapAddr
	require.NoError(t, cfg.Validate(), "config drift: config.yaml no longer validates")

	seedCharID := seedCharForAccount(t, cfg, seededAccountID, "E2eReturn", "prontera", 53, 111)

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
	waitGatewayOrFatal(t, mapAddr, serveErr)

	// --- Login to capture the session token the CZ_ENTER gate verifies. ---
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
	require.NoError(t, err, "no AC_ACCEPT_LOGIN; login likely failed against the DB")
	require.Equal(t, packet.HeaderACACCEPTLOGIN, binary.LittleEndian.Uint16(accept[0:2]))
	loginID1 := binary.LittleEndian.Uint32(accept[4:8])
	sexByte := accept[46]
	require.NotZero(t, loginID1)
	require.NoError(t, loginConn.Close())

	// --- First enter. ---
	mapConn := connectAndEnterMap(t, mapAddr, seedCharID, loginID1, sexByte, "E2eReturn")
	defer mapConn.Close()

	// --- CZ_RESTART (0x00b2, 3B, type=1) → ZC_RESTART_ACK (0x00b3, 3B, type=1). ---
	var restart bytes.Buffer
	require.NoError(t, packet.CZRestartRequest{Type: 1}.Encode(&restart))
	_, err = mapConn.Write(restart.Bytes())
	require.NoError(t, err)

	require.NoError(t, mapConn.SetReadDeadline(time.Now().Add(5*time.Second)))
	ack := make([]byte, packet.RestartAckResponse{}.Size())
	_, err = io.ReadFull(mapConn, ack)
	require.NoError(t, err, "no ZC_RESTART_ACK received")
	require.Equal(t, packet.HeaderZCRESTARTACK, binary.LittleEndian.Uint16(ack[0:2]), "expected ZC_RESTART_ACK")
	assert.Equal(t, uint8(1), ack[2], "type=1 char-select allowed")

	// --- The server closes the map connection after the ack; the client's next
	// read hits EOF (not a timeout). A polling deadline bounds the wait. ---
	require.NoError(t, mapConn.SetReadDeadline(time.Now().Add(3*time.Second)))
	_, err = io.ReadFull(mapConn, make([]byte, 1))
	assert.ErrorIs(t, err, io.EOF, "map connection closed by the server after ZC_RESTART_ACK")

	// --- Reconnect and re-enter on a fresh map connection. This is the proof the
	// teardown ran: a lingering registry entry would make SpawnService.Register
	// reject the duplicate account and the spawn frame would never arrive. ---
	reenterConn := connectAndEnterMap(t, mapAddr, seedCharID, loginID1, sexByte, "E2eReturn")
	defer reenterConn.Close()
}

// --- WebSocket dual-client helpers (roBrowser transport) ---
//
// roBrowser reaches the server over WebSocket, not raw TCP. Unlike the TCP
// listeners, a WS connection is message-framed: every server-side Encode→Write
// is one discrete WS message, so a client Read returns one whole packet rather
// than a partial byte stream. The TCP-oriented readMapFrame/awaitFrame/drain
// helpers therefore do not apply here; these helpers read whole messages.

// wsDial opens a coder/websocket client to the WS upgrade path on addr and
// registers a CloseNow cleanup. The path is the "/ws/" constant both WS
// listeners serve (the config's ws.path is informational; the handler binds the
// fixed route).
func wsDial(t *testing.T, addr string) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.Dial(context.Background(), "ws://"+addr+"/ws/", nil)
	require.NoError(t, err, "dial WS %s", addr)
	t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

// wsWrite sends one binary WS message. A RO client frames one packet per message.
func wsWrite(t *testing.T, c *websocket.Conn, p []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, c.Write(ctx, websocket.MessageBinary, p), "ws write")
}

// wsRead reads one binary WS message within timeout, returning (msg, false) on
// any error/timeout. Each server packet is its own WS message, so the returned
// slice is exactly one packet (with its 2-byte header at [0:2]).
func wsRead(t *testing.T, c *websocket.Conn, timeout time.Duration) ([]byte, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		return nil, false
	}
	return data, true
}

// wsReadUntil reads whole-packet WS messages until one whose little-endian
// header == want arrives, or timeout elapses (then (nil, false)). Intervening
// frames — the enter status burst queued ahead of a request's reply — are
// discarded. In the happy path ZC_NOTIFY_TIME always arrives before the budget
// is spent; the timeout is only a bound for failure diagnostics.
func wsReadUntil(t *testing.T, c *websocket.Conn, want uint16, timeout time.Duration) ([]byte, bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, false
		}
		msg, ok := wsRead(t, c, remaining)
		if !ok {
			return nil, false
		}
		if binary.LittleEndian.Uint16(msg) == want {
			return msg, true
		}
	}
}

// TestServe_WS_DualClient_LoginEnterRoundTrip is the M7e dual-client proof: the
// same combat slice the TCP e2e drives is reachable over WebSocket, the transport
// roBrowser must use (a browser cannot open the raw TCP map socket). It exercises
// both WS listeners the M7e wiring added — the login/char WS listener (CA_LOGIN →
// AC_ACCEPT_LOGIN) and the dedicated map-role WS listener (CZ_ENTER → ZC_ACCEPT_ENTER
// → ZC_SPAWN_UNIT, then a CZ_REQUEST_TIME → ZC_NOTIFY_TIME round-trip) — proving
// the map decoder, dispatch table, and response encode all work over WS messages,
// not just the login framing the infra-level ws_test already covers. The combat
// loop itself is transport-agnostic and TCP-proven; this test covers the
// transport-specific risk surface (message framing through the world core).
func TestServe_WS_DualClient_LoginEnterRoundTrip(t *testing.T) {
	requireStack(t)

	cfg := loadConfigForE2E(t)
	tcpAddr := rebindListenersToEphemeralPorts(t, cfg)
	wsLoginAddr := cfg.Gateway.WS.Addr
	mapWSAddr := cfg.Gateway.MapWSAddr
	require.NoError(t, cfg.Validate(), "config drift: config.yaml no longer validates")

	seedCharID := seedCharForAccount(t, cfg, seededAccountID, "E2eBrowser", "prontera", 53, 111)

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
	waitGatewayOrFatal(t, wsLoginAddr, serveErr) // login/char WS listener
	waitGatewayOrFatal(t, mapWSAddr, serveErr)   // map-role WS listener (M7e)

	// --- Login over the login/char WS listener: CA_LOGIN → AC_ACCEPT_LOGIN. ---
	loginWS := wsDial(t, wsLoginAddr)
	var login bytes.Buffer
	require.NoError(t, packet.CALoginRequest{
		Version: uint32(cfg.Gateway.Packetver), Username: "test", Password: "test",
	}.Encode(&login))
	wsWrite(t, loginWS, login.Bytes())

	accept, ok := wsRead(t, loginWS, 5*time.Second)
	require.True(t, ok, "no AC_ACCEPT_LOGIN over WS; login likely failed against the DB")
	require.Equal(t, packet.HeaderACACCEPTLOGIN, binary.LittleEndian.Uint16(accept[0:2]),
		"expected AC_ACCEPT_LOGIN over WS")
	loginID1 := binary.LittleEndian.Uint32(accept[4:8])
	sexByte := accept[46]
	require.NotZero(t, loginID1, "AC_ACCEPT_LOGIN over WS carried no login_id1")
	// The map conn is a separate listener; close the login-WS conn (cleanup also
	// CloseNows it, idempotently).
	require.NoError(t, loginWS.Close(websocket.StatusNormalClosure, ""), "close login WS conn")

	// --- Enter the map over the dedicated map-role WS listener. ---
	mapWS := wsDial(t, mapWSAddr)
	var enter bytes.Buffer
	require.NoError(t, packet.CZEnterRequest{
		AccountID: seededAccountID, CharID: seedCharID, AuthCode: loginID1, ClientTime: 0, Sex: sexByte,
	}.Encode(&enter))
	wsWrite(t, mapWS, enter.Bytes())

	enterResp, ok := wsRead(t, mapWS, 5*time.Second)
	require.True(t, ok, "no ZC_ACCEPT_ENTER over WS; CZ_ENTER verification likely failed")
	require.Equal(t, packet.HeaderZCACCEPTENTER, binary.LittleEndian.Uint16(enterResp[0:2]),
		"expected ZC_ACCEPT_ENTER over WS")

	// The self-spawn is its own WS message right after the enter ack.
	spawn, ok := wsRead(t, mapWS, 5*time.Second)
	require.True(t, ok, "no ZC_SPAWN_UNIT over WS; spawn-on-enter likely failed (ErrPlayerAlreadyRegistered?)")
	require.Equal(t, packet.HeaderZCSPAWNUNIT, binary.LittleEndian.Uint16(spawn[0:2]),
		"expected ZC_SPAWN_UNIT over WS")
	assert.Equal(t, seededAccountID, binary.LittleEndian.Uint32(spawn[5:9]),
		"spawn AID = account_id over WS")
	assert.Equal(t, seedCharID, binary.LittleEndian.Uint32(spawn[9:13]),
		"spawn GID = char_id over WS")

	// --- A map-role request/response round-trip proves bidirectional framing
	// over WS (request decode → dispatch → TimeHandler → ZC_NOTIFY_TIME encode →
	// WS write). CZ_REQUEST_TIME → ZC_NOTIFY_TIME is a clean 1:1 reply; the enter
	// status burst frames queued ahead of it are scanned past by wsReadUntil. ---
	var reqTime bytes.Buffer
	require.NoError(t, packet.CZRequestTimeRequest{ClientTick: 0}.Encode(&reqTime))
	wsWrite(t, mapWS, reqTime.Bytes())
	timeResp, ok := wsReadUntil(t, mapWS, packet.HeaderZCNOTIFYTIME, 5*time.Second)
	require.True(t, ok, "no ZC_NOTIFY_TIME over WS; the map-role WS round-trip failed")
	assert.Len(t, timeResp, packet.NotifyTimeResponse{}.Size(), "ZC_NOTIFY_TIME is 6 bytes")
}

// readMapFrame reads exactly one framed map-role packet from conn, dispatching on
// the 2-byte opcode: variable-length packets (spawn-unit, unit-walking) carry
// their total wire length at [2:4]; fixed-length packets are read to their known
// size (via the response type's Size()). Returns (frame, true) on success and
// (nil, false) on a read deadline/EOF — the latter ends a drain rather than
// failing. An unsupported opcode fails the test loudly: the server sent a frame
// type the test must be taught to handle.
func readMapFrame(t *testing.T, conn net.Conn) ([]byte, bool) {
	t.Helper()
	opBuf := make([]byte, 2)
	// The opcode read is the drain's idle sentinel: a read deadline or close
	// here ends the drain, not the test. Once an opcode is read the frame is in
	// flight and MUST be fully consumed — aborting mid-frame (returning false
	// after the opcode) would leave the stream desynced. The caller's polling
	// deadline is short (idle detection), so it can fire between the opcode read
	// and the body read when the writer is briefly scheduled out; re-arm a
	// generous deadline for the length-prefix + body so a polling-deadline split
	// is not mistaken for a genuine desync. A real desync still fails loudly once
	// this window elapses.
	if _, err := io.ReadFull(conn, opBuf); err != nil {
		return nil, false
	}
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	op := binary.LittleEndian.Uint16(opBuf)
	if op == packet.HeaderZCSPAWNUNIT || op == packet.HeaderZCUNITWALKING ||
		op == packet.HeaderZCSETUNITIDLE ||
		op == packet.HeaderZCPCPURCHASEITEMLIST ||
		op == packet.HeaderZCPCSELLITEMLIST ||
		op == packet.HeaderZCINVENTORYITEMLISTNORMAL ||
		op == packet.HeaderZCINVENTORYITEMLISTEQUIP ||
		op == packet.HeaderZCINVENTORYSTART ||
		op == packet.HeaderZCSKILLINFOLIST ||
		op == packet.HeaderZCSAYDIALOG2 ||
		op == packet.HeaderZCNOTIFYCHAT {
		lenBuf := make([]byte, 2)
		_, err := io.ReadFull(conn, lenBuf)
		require.NoErrorf(t, err, "read length prefix of opcode 0x%04x", op)
		total := int(binary.LittleEndian.Uint16(lenBuf))
		require.GreaterOrEqualf(t, total, 4, "variable packet length %d < 4 for opcode 0x%04x", total, op)
		frame := make([]byte, total)
		copy(frame[0:2], opBuf)
		copy(frame[2:4], lenBuf)
		if total > 4 {
			_, err = io.ReadFull(conn, frame[4:])
			require.NoErrorf(t, err, "read %d-byte body of opcode 0x%04x", total-4, op)
		}
		return frame, true
	}
	total := mapFrameSize(op)
	require.NotZero(t, total, "readMapFrame: unsupported opcode 0x%04x (add its size to mapFrameSize)", op)
	frame := make([]byte, total)
	copy(frame[0:2], opBuf)
	_, err := io.ReadFull(conn, frame[2:])
	require.NoErrorf(t, err, "read %d-byte fixed body of opcode 0x%04x", total-2, op)
	return frame, true
}

// mapFrameSize returns the fixed wire length for a map-role opcode, sourced from
// each response type's Size() method (no magic numbers). Variable-length opcodes
// are handled by readMapFrame's length-prefix branch, not here. Zero ⇒ unknown.
func mapFrameSize(op uint16) int {
	switch op {
	case packet.HeaderZCACCEPTENTER:
		return packet.MapAcceptEnterResponse{}.Size()
	case packet.HeaderZCNOTIFYPLAYERMOVE:
		return packet.MapNotifyPlayerMoveResponse{}.Size()
	case packet.HeaderZCACTIONRESPONSE:
		return packet.ActionResponse{}.Size()
	case packet.HeaderZCNOTIFYACT:
		return packet.NotifyActResponse{}.Size()
	case packet.HeaderZCNOTIFYVANISH:
		return packet.NotifyVanishResponse{}.Size()
	// ZC_INVENTORY_END (0x0b0b) is a fixed 4-byte packet (cmd + invType + flag)
	// with no length prefix — unlike ZC_INVENTORY_START/ITEMLIST_* which carry one
	// — so it is read as fixed-size, not the variable-length path above.
	case packet.HeaderZCINVENTORYEND:
		return len(packet.EncodeInventoryEnd())
	// ZC_SHORTCUT_KEY_LIST (0x0b20) is fixed at 271 bytes (cmd + rotate + tab + 38
	// hotkey slots) — no length prefix.
	case packet.HeaderZCSHORTCUTKEYLIST:
		return len(packet.EncodeEmptyHotkeyList(0))
	case packet.HeaderZCSTATUS:
		return packet.StatusResponse{}.Size()
	case packet.HeaderZCPARCHANGE:
		return packet.ParChangeResponse{}.Size()
	case packet.HeaderZCLONGPARCHANGE:
		return packet.LongParChangeResponse{}.Size()
	case packet.HeaderZCLONGLONGPARCHANGE:
		return packet.LongLongParChangeResponse{}.Size()
	// ZC_ACK_REQNAMEALL2 (0x0a30, 106B) and ZC_ACK_REQNAMEALL_NPC (0x0adf, 58B)
	// are fixed-size name replies with no length prefix — the PC and mob/NPC
	// variants clif_name emits at PACKETVER 20250604.
	case packet.HeaderZCACKREQNAMEALL2:
		return packet.ReqNameAll2Response{}.Size()
	case packet.HeaderZCACKREQNAMEALLNPC:
		return packet.ReqNameAllNPCResponse{}.Size()
	case packet.HeaderZCItemFallEntry:
		// ItemFallEntryResponse.Size/Encode carry pointer receivers, so the zero
		// struct is addressed before the call — a value receiver call won't compile.
		return (&packet.ItemFallEntryResponse{}).Size()
	case packet.HeaderZCItemThrowAck:
		// A4: the drop ack (0x00af, 6 bytes fixed: index.W + count.W). Pointer
		// receiver — the zero value Size() is exact.
		return (&packet.ItemThrowAckResponse{}).Size()
	case packet.HeaderZCItemPickupAck:
		// M10a: the pickup success/fail ack (result=0 / result=6). Pointer receiver.
		return (&packet.ItemPickupAckResponse{}).Size()
	case packet.HeaderZCItemDisappear:
		// M10a: the post-pickup floor-vanish broadcast (0x00a1). Pointer receiver.
		return (&packet.ItemDisappearResponse{}).Size()
	case packet.HeaderZCREQWEAREQUIPACKV5:
		// M10b: the equip ack (0x0999, result 0=fail/1=ok/2=low-level). Value receiver.
		return packet.ReqWearEquipAckResponse{}.Size()
	case packet.HeaderZCREQTAKEOFFEQUIPACK:
		// M10b: the takeoff ack (0x099a, flag wire-inverted 0=success). Value receiver.
		return packet.ReqTakeoffEquipAckResponse{}.Size()
	case packet.HeaderZCSPRITECHANGE:
		// P2A: the look-sprite broadcast (0x01d7, 15B PACKETVER>=20181121 variant).
		return packet.SpriteChangeResponse{}.Size()
	case packet.HeaderZCUSEITEMACK2:
		// M12: the use-item ack (0x01c8, Result 0=fail/1=success). Value receiver.
		return packet.UseItemAck2Response{}.Size()
	case packet.HeaderZCSELECTDEALTYPE:
		// M13: NPC shop deal-type selector (0x00c4, 6 bytes fixed).
		return packet.SelectDealtypeResponse{}.Size()
	case packet.HeaderZCPCPURCHASERESULT:
		// M13: shop purchase result ack (0x00ca, 3 bytes fixed).
		return packet.PurchaseResultResponse{}.Size()
	case packet.HeaderZCPCSELLRESULT:
		return packet.SellResultResponse{}.Size()
	case packet.HeaderZCWAITDIALOG2:
		// M14a: the NPC "Next" prompt (0x0973, 7 bytes fixed). Value receiver.
		return packet.WaitDialog2Response{}.Size()
	case packet.HeaderZCCLOSEDIALOG:
		// M14a: the NPC "Close" frame (0x00b6, 6 bytes fixed). Value receiver.
		return packet.CloseDialogResponse{}.Size()
	case packet.HeaderZCNPCACKMAPMOVE:
		// M14a: the warp/change-map ack (0x0091, 22 bytes fixed). Value receiver.
		return packet.MapMoveResponse{}.Size()
	case packet.HeaderZCNOTIFYSKILL:
		// M14b: the skill-hit broadcast (0x01de, 33 bytes fixed). Value receiver.
		return packet.NotifySkillResponse{}.Size()
	case packet.HeaderZCACKTOUSESKILL:
		// M14b: the skill cast result ack (0x0110, 14 bytes fixed; cause 12 =
		// SP insufficient). Value receiver.
		return packet.AckUseSkillResponse{}.Size()
	case packet.HeaderZCNOTIFYCHAT:
		// M11: variable-length chat broadcast (0x008d). Handled by the
		// length-prefix branch in readMapFrame.
		return 0
	case packet.HeaderZCSTATUSCHANGEACK:
		// M15b: the stat-change ack (0x00bc, 6 bytes fixed). No Size() method.
		return 6
	default:
		return 0
	}
}

// drainEnterStatusBurst consumes the enter status burst SpawnService emits right
// after the PC self-spawn — ZC_STATUS, then the 16 ZC_PAR_CHANGE frames, the
// ZC_LONGPAR_CHANGE zeny, and the two ZC_LONGLONGPAR_CHANGE exp frames — so the
// caller's next raw read lands on the frame that follows it (a mob spawn or the
// move ack). The burst is fixed in width regardless of stat values, so its size is
// the sum of the encoders it is built from; the leading ZC_STATUS header pins that
// the consumed bytes are indeed the burst, not a desynced stream.
func drainEnterStatusBurst(t *testing.T, conn net.Conn) {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	size := packet.StatusResponse{}.Size() +
		16*packet.ParChangeResponse{}.Size() +
		packet.LongParChangeResponse{}.Size() +
		2*packet.LongLongParChangeResponse{}.Size()
	buf := make([]byte, size)
	_, err := io.ReadFull(conn, buf)
	require.NoError(t, err, "no enter status burst received after the PC self-spawn")
	require.Equal(t, packet.HeaderZCSTATUS, binary.LittleEndian.Uint16(buf[0:2]),
		"status burst must begin with ZC_STATUS (0x00bd)")
}

// drainFrames reads framed packets from conn, dispatching each to handle, until
// no full frame arrives within idle (a read deadline ends the drain rather than
// blocking). Used to collect late responses and mob-position updates between
// chase cycles.
func drainFrames(t *testing.T, conn net.Conn, idle time.Duration, handle func(op uint16, frame []byte)) {
	t.Helper()
	for {
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(idle)))
		frame, ok := readMapFrame(t, conn)
		if !ok {
			return
		}
		handle(binary.LittleEndian.Uint16(frame), frame)
	}
}

// awaitFrame reads framed packets until one with opcode want arrives (or timeout
// elapses), dispatching every intervening frame to handle. Returns the matched
// frame, or (nil, false) on timeout.
func awaitFrame(t *testing.T, conn net.Conn, want uint16, timeout time.Duration, handle func(op uint16, frame []byte)) ([]byte, bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		require.NoError(t, conn.SetReadDeadline(deadline))
		frame, ok := readMapFrame(t, conn)
		if !ok {
			return nil, false
		}
		op := binary.LittleEndian.Uint16(frame)
		if handle != nil {
			handle(op, frame)
		}
		if op == want {
			return frame, true
		}
	}
}

// decodeCell unpacks a 3-byte rAthena packed position (RBUFPOS) to (x, y),
// mirroring pkg/ro/packet/coords.go decodePos (dir is dropped — combat only
// needs the cell).
func decodeCell(p []byte) (x, y int16) {
	x = int16((uint16(p[0]) << 2) | (uint16(p[1]) >> 6))
	y = int16((uint16(p[1]&0x3f) << 4) | (uint16(p[2]) >> 4))
	return x, y
}

// seedCombatChar inserts a level-1 novice (base_exp 8, just under the level-2
// threshold of 10) at slot 0 on the given cell, so a single Poring kill
// (BaseExp 2: 8→10) drives the full EXP + level-up + status-point broadcast the
// combat e2e asserts. It mirrors seedCharForAccount's clear/insert/readback/
// LIFO-cleanup structure; only base_level/base_exp/base_job values differ.
func seedCombatChar(t *testing.T, cfg *config.Config, accountID uint32, name, lastMap string, lastX, lastY int) uint32 {
	t.Helper()
	gdb, err := gorm.Open(mysql.Open(cfg.DBConnString()), &gorm.Config{})
	require.NoError(t, err, "open gorm to seed combat char")
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, gdb.Exec("DELETE FROM `char` WHERE account_id = ?", accountID).Error,
		"clear existing chars for test account")
	require.NoError(t, gdb.Exec(
		`INSERT INTO `+"`char`"+` (account_id, char_num, name, class, base_level, job_level,
		   base_exp, job_exp, zeny, str, agi, vit, `+"`int`"+`, dex, luk,
		   max_hp, hp, max_sp, sp, status_point, skill_point,
		   hair, hair_color, clothes_color, body, weapon, shield,
		   head_top, head_mid, head_bottom, robe,
		   last_map, last_x, last_y, sex, option, karma, manner)
		 VALUES (?, 0, ?, 0, 1, 1, 8, 1, 0, 1,1,1,1,1,1, 40,40,11,11,0,0, 2,1,0,0,0,0, 0,0,0,0, ?, ?, ?, 1, 0, 0, 0)`,
		accountID, name, lastMap, lastX, lastY).Error, "insert level-1 combat seed char row")

	var charID uint32
	require.NoError(t, gdb.Raw(
		"SELECT char_id FROM `char` WHERE account_id = ? AND char_num = 0", accountID).
		Scan(&charID).Error, "read back seeded combat char_id")
	require.NotZero(t, charID, "seeded combat char_id is 0")
	t.Cleanup(func() { _ = gdb.Exec("DELETE FROM `char` WHERE char_id = ?", charID).Error })
	return charID
}

// seedSkillChar inserts a level-1 naked novice at (lastX, lastY) with SP and
// max_sp set to 2000 — enough margin for 100+ SM_BASH lv10 casts (15 SP each)
// so the M14b skill-cast e2e never trips the SP-insufficient negative path on
// the way to a kill. Everything else (stats 1, HP 40, base_exp 8) matches
// seedCombatChar so the kill path can be reasoned about identically; only the
// SP/max_sp column pair differs. Mirrors seedCombatChar's
// clear/insert/readback/LIFO-cleanup structure.
func seedSkillChar(t *testing.T, cfg *config.Config, accountID uint32, name, lastMap string, lastX, lastY int) uint32 {
	t.Helper()
	gdb, err := gorm.Open(mysql.Open(cfg.DBConnString()), &gorm.Config{})
	require.NoError(t, err, "open gorm to seed skill char")
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, gdb.Exec("DELETE FROM `char` WHERE account_id = ?", accountID).Error,
		"clear existing chars for test account")
	require.NoError(t, gdb.Exec(
		`INSERT INTO `+"`char`"+` (account_id, char_num, name, class, base_level, job_level,
		   base_exp, job_exp, zeny, str, agi, vit, `+"`int`"+`, dex, luk,
		   max_hp, hp, max_sp, sp, status_point, skill_point,
		   hair, hair_color, clothes_color, body, weapon, shield,
		   head_top, head_mid, head_bottom, robe,
		   last_map, last_x, last_y, sex, option, karma, manner)
		 VALUES (?, 0, ?, 0, 1, 1, 8, 1, 0, 1,1,1,1,1,1, 40,40,2000,2000,0,0, 2,1,0,0,0,0, 0,0,0,0, ?, ?, ?, 1, 0, 0, 0)`,
		accountID, name, lastMap, lastX, lastY).Error, "insert level-1 high-SP skill seed char row")

	var charID uint32
	require.NoError(t, gdb.Raw(
		"SELECT char_id FROM `char` WHERE account_id = ? AND char_num = 0", accountID).
		Scan(&charID).Error, "read back seeded skill char_id")
	require.NotZero(t, charID, "seeded skill char_id is 0")
	t.Cleanup(func() { _ = gdb.Exec("DELETE FROM `char` WHERE char_id = ?", charID).Error })
	return charID
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
// given name, last_map, and last cell (last_x/last_y), returns its auto-increment
// char_id, and registers a cleanup that removes every char row it created. It
// connects to MariaDB with the same DSN app.Serve uses, and clears any
// pre-existing chars for the account first so the slot-0 selection in the test
// is deterministic. lastX/lastY are the persisted spawn cell: SpawnService uses
// the char's last position when the caller passes a zero SpawnPoint, so a test
// that wants to enter near a known world entity (e.g. a mob spawn cell) seeds
// the char there.
func seedCharForAccount(t *testing.T, cfg *config.Config, accountID uint32, name, lastMap string, lastX, lastY int) uint32 {
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
		 VALUES (?, 0, ?, 0, 12, 10, 500, 200, 3000, 9,1,1,1,1,1, 200,100,50,50,0,0, 2,1,0,0,0,0, 0,0,0,0, ?, ?, ?, 1, 0, 0, 0)`,
		accountID, name, lastMap, lastX, lastY).Error, "insert seed char row")

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

// seedKnifeInBag inserts one Knife (nameid 1201, amount 1, equip 0) into the
// char's `inventory` row set so the equip e2e has a wearable weapon at grid slot
// 0 (the single row's id-ascending index). Every other inventory column has a
// migration default, so the INSERT names only the columns the equip path reads.
// It registers a cleanup (LIFO: row-delete before pool close) that removes the
// seeded row.
func seedKnifeInBag(t *testing.T, cfg *config.Config, charID uint32) {
	t.Helper()
	gdb, err := gorm.Open(mysql.Open(cfg.DBConnString()), &gorm.Config{})
	require.NoError(t, err, "open gorm to seed bag")
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, gdb.Exec(
		`INSERT INTO inventory (char_id, nameid, amount, equip) VALUES (?, 1201, 1, 0)`,
		charID).Error, "insert Knife (nameid 1201) into the char's bag")
	t.Cleanup(func() { _ = gdb.Exec("DELETE FROM inventory WHERE char_id = ?", charID).Error })
}

// seedRedPotionInBag inserts one Red Potion (nameid 503, amount 2) for the use-item
// e2e. Amount=2 lets the test fire a single CZ_USE_ITEM2 and still observe a non-zero
// stack after the consume (the post-use ZC_USE_ITEM_ACK2 amount field = 1).
func seedRedPotionInBag(t *testing.T, cfg *config.Config, charID uint32) {
	t.Helper()
	gdb, err := gorm.Open(mysql.Open(cfg.DBConnString()), &gorm.Config{})
	require.NoError(t, err, "open gorm to seed Red Potion")
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, gdb.Exec(
		`INSERT INTO inventory (char_id, nameid, amount, equip) VALUES (?, 503, 2, 0)`,
		charID).Error, "insert Red Potion (nameid 503) into the char's bag")
	t.Cleanup(func() { _ = gdb.Exec("DELETE FROM inventory WHERE char_id = ?", charID).Error })
}

// seedShopRedPotion inserts five Red Potions (nameid 501) at inventory index 0.
func seedShopRedPotion(t *testing.T, cfg *config.Config, charID uint32) {
	t.Helper()
	gdb, err := gorm.Open(mysql.Open(cfg.DBConnString()), &gorm.Config{})
	require.NoError(t, err, "open gorm to seed shop inventory")
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, gdb.Exec(
		`INSERT INTO inventory (char_id, nameid, amount, equip) VALUES (?, 501, 5, 0)`,
		charID).Error, "insert Red Potion (nameid 501) into the char's bag")
	t.Cleanup(func() { _ = gdb.Exec("DELETE FROM inventory WHERE char_id = ?", charID).Error })
}

// readInventoryAmount returns the persisted stack amount for a character item.
func readInventoryAmount(t *testing.T, cfg *config.Config, charID, nameID uint32) uint32 {
	t.Helper()
	gdb, err := gorm.Open(mysql.Open(cfg.DBConnString()), &gorm.Config{})
	require.NoError(t, err, "open gorm to read inventory")
	t.Cleanup(func() { sqlDB, _ := gdb.DB(); _ = sqlDB.Close() })
	var amount uint32
	require.NoError(t, gdb.Raw(
		`SELECT amount FROM inventory WHERE char_id = ? AND nameid = ? ORDER BY id`, charID, nameID).
		Scan(&amount).Error, "read persisted inventory amount")
	return amount
}

// lowerCharHP drops the seeded char's HP to hp via UPDATE so the post-use
// ZC_PAR_CHANGE SPHP must strictly exceed hp. The char is reloaded by SpawnService
// on the next CZ_ENTER, so the seed-then-lower ordering is the only requirement.
func lowerCharHP(t *testing.T, cfg *config.Config, charID uint32, hp uint32) {
	t.Helper()
	gdb, err := gorm.Open(mysql.Open(cfg.DBConnString()), &gorm.Config{})
	require.NoError(t, err, "open gorm to lower HP")
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, gdb.Exec(
		`UPDATE `+"`char`"+` SET hp = ? WHERE char_id = ?`, hp, charID).Error,
		"lower char HP to expected pre-use value")
}

// readCharHP queries the persisted HP the SpawnService will load on the next
// CZ_ENTER. Useful for asserting the pre-use baseline drifts the test expects.
func readCharHP(t *testing.T, cfg *config.Config, charID uint32) uint32 {
	t.Helper()
	gdb, err := gorm.Open(mysql.Open(cfg.DBConnString()), &gorm.Config{})
	require.NoError(t, err, "open gorm to read HP")
	t.Cleanup(func() { sqlDB, _ := gdb.DB(); _ = sqlDB.Close() })
	var hp uint32
	require.NoError(t, gdb.Raw(
		`SELECT hp FROM `+"`char`"+` WHERE char_id = ?`, charID).Scan(&hp).Error,
		"read persisted HP")
	return hp
}

// readCharWeapon queries the persisted weapon look column the SpawnService loads
// on the next CZ_ENTER. Used by the equip e2e to assert the worn weapon look is
// persisted (not reverted to 0 on relog) — the equip-look-not-persisted gap.
func readCharWeapon(t *testing.T, cfg *config.Config, charID uint32) uint16 {
	t.Helper()
	gdb, err := gorm.Open(mysql.Open(cfg.DBConnString()), &gorm.Config{})
	require.NoError(t, err, "open gorm to read weapon look")
	t.Cleanup(func() { sqlDB, _ := gdb.DB(); _ = sqlDB.Close() })
	var weapon uint16
	require.NoError(t, gdb.Raw(
		`SELECT weapon FROM `+"`char`"+` WHERE char_id = ?`, charID).Scan(&weapon).Error,
		"read persisted weapon look")
	return weapon
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

// setCharZeny updates the zeny column for a char row so the e2e test can
// seed a known balance before the player enters the map.
func setCharZeny(t *testing.T, cfg *config.Config, charID uint32, zeny uint32) {
	t.Helper()
	gdb, err := gorm.Open(mysql.Open(cfg.DBConnString()), &gorm.Config{})
	require.NoError(t, err, "open gorm to set zeny")
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, gdb.Exec(
		`UPDATE `+"`char`"+` SET zeny = ? WHERE char_id = ?`, zeny, charID).Error,
		"set char zeny for shop test")
}

// readCharZeny queries the persisted zeny for post-purchase verification.
func readCharZeny(t *testing.T, cfg *config.Config, charID uint32) uint32 {
	t.Helper()
	gdb, err := gorm.Open(mysql.Open(cfg.DBConnString()), &gorm.Config{})
	require.NoError(t, err, "open gorm to read zeny")
	t.Cleanup(func() { sqlDB, _ := gdb.DB(); _ = sqlDB.Close() })
	var zeny uint32
	require.NoError(t, gdb.Raw(
		`SELECT zeny FROM `+"`char`"+` WHERE char_id = ?`, charID).Scan(&zeny).Error,
		"read persisted zeny")
	return zeny
}

// readUntilCmd reads frames via readMapFrame until it finds one whose leading
// opcode matches wantCmd, or findTimeout elapses. It returns the full frame.
// Frames with other opcodes are silently discarded (drained).
func readUntilCmd(t *testing.T, conn net.Conn, wantCmd uint16, findTimeout time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(findTimeout)
	for time.Now().Before(deadline) {
		require.NoError(t, conn.SetReadDeadline(time.Now().Add(500*time.Millisecond)))
		frame, ok := readMapFrame(t, conn)
		if !ok {
			continue
		}
		if binary.LittleEndian.Uint16(frame) == wantCmd {
			require.NoError(t, conn.SetReadDeadline(time.Time{}))
			return frame
		}
	}
	t.Fatalf("readUntilCmd: opcode 0x%04x not seen within %v", wantCmd, findTimeout)
	return nil
}

func writeSellItemDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "item_db.yml")
	const contents = `Header:
  Type: ITEM_DB
  Version: 3
Body:
  - Id: 501
    AegisName: Red_Potion
    Name: Red Potion
    Type: Healing
    Buy: 10
    Sell: 100
`
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o600), "write sell item_db fixture")
	return path
}

func TestServe_ShopSell(t *testing.T) {
	requireStack(t)

	cfg := loadConfigForE2E(t)
	cfg.Zone.ItemDBPath = writeSellItemDB(t)
	tcpAddr := rebindListenersToEphemeralPorts(t, cfg)
	mapAddr := cfg.Gateway.MapAddr
	require.NoError(t, cfg.Validate())

	const startZeny uint32 = 0
	seedCharID := seedCombatChar(t, cfg, seededAccountID, "E2eSeller", "prontera", 155, 150)
	setCharZeny(t, cfg, seedCharID, startZeny)
	seedShopRedPotion(t, cfg, seedCharID)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	serveErr := make(chan error, 1)
	go func() { serveErr <- app.Serve(ctx, cfg) }()
	defer func() {
		cancel()
		select {
		case err := <-serveErr:
			assert.NoError(t, err)
		case <-time.After(15 * time.Second):
			t.Fatal("app.Serve did not shut down within 15s")
		}
	}()
	waitGatewayOrFatal(t, tcpAddr, serveErr)
	waitGatewayOrFatal(t, mapAddr, serveErr)

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
	require.NoError(t, err, "no AC_ACCEPT_LOGIN")
	loginID1 := binary.LittleEndian.Uint32(accept[4:8])
	sexByte := accept[46]
	require.NotZero(t, loginID1)
	require.NoError(t, loginConn.Close())

	mapConn := connectAndEnterMap(t, mapAddr, seedCharID, loginID1, sexByte, "E2eSeller")
	defer mapConn.Close()

	shopNpcID := worlddomain.NPCIDBase + 3
	var contact bytes.Buffer
	require.NoError(t, (packet.CZContactNPCRequest{AID: shopNpcID, Type: 1}).Encode(&contact))
	_, err = mapConn.Write(contact.Bytes())
	require.NoError(t, err, "send CZ_CONTACTNPC")
	resp := readUntilCmd(t, mapConn, packet.HeaderZCSELECTDEALTYPE, 5*time.Second)
	require.Equal(t, shopNpcID, binary.LittleEndian.Uint32(resp[2:6]))

	var dealtype bytes.Buffer
	require.NoError(t, (packet.CZAckSelectDealTypeRequest{NpcID: shopNpcID, Type: 1}).Encode(&dealtype))
	_, err = mapConn.Write(dealtype.Bytes())
	require.NoError(t, err, "send CZ_ACK_SELECT_DEALTYPE Sell")

	listFrame := readUntilCmd(t, mapConn, packet.HeaderZCPCSELLITEMLIST, 5*time.Second)
	require.Greater(t, binary.LittleEndian.Uint16(listFrame[2:4]), uint16(4), "sell list must contain Red Potion")
	var redPotionIndex uint16
	var redPotionPrice uint32
	for off := 4; off+10 <= len(listFrame); off += 10 {
		index := binary.LittleEndian.Uint16(listFrame[off : off+2])
		if index == 0 {
			redPotionIndex = index
			redPotionPrice = binary.LittleEndian.Uint32(listFrame[off+2 : off+6])
			break
		}
	}
	require.Equal(t, uint16(0), redPotionIndex)
	require.NotZero(t, redPotionPrice, "Red Potion must be sellable")

	var sell bytes.Buffer
	require.NoError(t, (packet.CZPCSellItemListRequest{
		Entries: []packet.CZPCSellItemListEntry{{Index: redPotionIndex, Amount: 2}},
	}).Encode(&sell))
	_, err = mapConn.Write(sell.Bytes())
	require.NoError(t, err, "send CZ_PC_SELL_ITEMLIST")

	resultFrame := readUntilCmd(t, mapConn, packet.HeaderZCPCSELLRESULT, 5*time.Second)
	assert.Equal(t, uint8(0), resultFrame[2], "result=0 means sale succeeded")
	assert.Equal(t, startZeny+2*redPotionPrice, readCharZeny(t, cfg, seedCharID))
	assert.Equal(t, uint32(3), readInventoryAmount(t, cfg, seedCharID, 501))
}

// TestServe_ShopBuy exercises the full NPC shop buy loop: contact a shop NPC,
// select Buy, purchase an item, and verify zeny is deducted and the purchase
// succeeds. The shop NPC "Tool Shop" is loaded from data/shop/prontera.yml at
// prontera(155,153). Script NPCs from data/npc/*.txt are allocated first
// (alphabetical: healer.txt → sample.txt → warper.txt), then shop NPCs; the
// Tool Shop is the 4th NPC so its EntityID = NPCIDBase + 3.
func TestServe_ShopBuy(t *testing.T) {
	requireStack(t)

	cfg := loadConfigForE2E(t)
	tcpAddr := rebindListenersToEphemeralPorts(t, cfg)
	mapAddr := cfg.Gateway.MapAddr
	require.NoError(t, cfg.Validate())

	const startZeny uint32 = 100000
	seedCharID := seedCombatChar(t, cfg, seededAccountID, "E2eBuyer", "prontera", 155, 150)
	setCharZeny(t, cfg, seedCharID, startZeny)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	serveErr := make(chan error, 1)
	go func() { serveErr <- app.Serve(ctx, cfg) }()
	defer func() {
		cancel()
		select {
		case err := <-serveErr:
			assert.NoError(t, err)
		case <-time.After(15 * time.Second):
			t.Fatal("app.Serve did not shut down within 15s")
		}
	}()
	waitGatewayOrFatal(t, tcpAddr, serveErr)
	waitGatewayOrFatal(t, mapAddr, serveErr)

	// Login → CZ_ENTER → map enter
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
	require.NoError(t, err, "no AC_ACCEPT_LOGIN")
	loginID1 := binary.LittleEndian.Uint32(accept[4:8])
	sexByte := accept[46]
	require.NotZero(t, loginID1)
	require.NoError(t, loginConn.Close())

	mapConn := connectAndEnterMap(t, mapAddr, seedCharID, loginID1, sexByte, "E2eBuyer")
	defer mapConn.Close()

	// --- Step 1: CZ_CONTACTNPC → ZC_SELECT_DEALTYPE ---
	// Script NPCs (healer.txt, sample.txt, warper.txt) are allocated first, then shop NPCs.
	shopNpcID := worlddomain.NPCIDBase + 3
	var contact bytes.Buffer
	require.NoError(t, (packet.CZContactNPCRequest{AID: shopNpcID, Type: 1}).Encode(&contact))
	_, err = mapConn.Write(contact.Bytes())
	require.NoError(t, err, "send CZ_CONTACTNPC")

	resp := readUntilCmd(t, mapConn, packet.HeaderZCSELECTDEALTYPE, 5*time.Second)
	require.Equal(t, shopNpcID, binary.LittleEndian.Uint32(resp[2:6]),
		"ZC_SELECT_DEALTYPE NpcID must match the shop NPC EntityID")

	// --- Step 2: CZ_ACK_SELECT_DEALTYPE(type=0=Buy) → ZC_PC_PURCHASE_ITEMLIST ---
	var dealtype bytes.Buffer
	require.NoError(t, (packet.CZAckSelectDealTypeRequest{NpcID: shopNpcID, Type: 0}).Encode(&dealtype))
	_, err = mapConn.Write(dealtype.Bytes())
	require.NoError(t, err, "send CZ_ACK_SELECT_DEALTYPE")

	listFrame := readUntilCmd(t, mapConn, packet.HeaderZCPCPURCHASEITEMLIST, 5*time.Second)
	totalLen := binary.LittleEndian.Uint16(listFrame[2:4])
	require.Greater(t, totalLen, uint16(4), "purchase list must have at least one item")
	// First item starts at offset 4. ShopBuyItem: [4:itemId][8:price][12:discountPrice]...
	firstItemID := binary.LittleEndian.Uint32(listFrame[4:8])
	firstPrice := binary.LittleEndian.Uint32(listFrame[8:12])
	assert.Equal(t, uint32(501), firstItemID, "first shop item should be Red Potion (501)")
	assert.Equal(t, uint32(50), firstPrice, "Red Potion price should be 50z")

	// --- Step 3: CZ_PC_PURCHASE_ITEMLIST → ZC_PC_PURCHASE_RESULT ---
	var purchase bytes.Buffer
	require.NoError(t, (packet.CZPCPurchaseItemListRequest{
		Entries: []packet.CZPCPurchaseItemListEntry{
			{ItemID: 501, Amount: 2},
		},
	}).Encode(&purchase))
	_, err = mapConn.Write(purchase.Bytes())
	require.NoError(t, err, "send CZ_PC_PURCHASE_ITEMLIST")

	resultFrame := readUntilCmd(t, mapConn, packet.HeaderZCPCPURCHASERESULT, 5*time.Second)
	assert.Equal(t, uint8(0), resultFrame[2], "result=0 means purchase succeeded")

	// --- Verify: zeny was deducted ---
	finalZeny := readCharZeny(t, cfg, seedCharID)
	expectedZeny := startZeny - 2*50 // 100000 - 100 = 99900
	assert.Equal(t, expectedZeny, finalZeny, "zeny should be deducted by 2 Red Potions at 50z each")
}

// readCharPosition reads last_map/last_x/last_y back from the char table. Used
// by the warp e2e to assert SavePosition persisted the destination cell.
func readCharPosition(t *testing.T, cfg *config.Config, charID uint32) (string, uint16, uint16) {
	t.Helper()
	gdb, err := gorm.Open(mysql.Open(cfg.DBConnString()), &gorm.Config{})
	require.NoError(t, err, "open gorm to read position")
	t.Cleanup(func() {
		sqlDB, dbErr := gdb.DB()
		_ = sqlDB.Close()
		_ = dbErr
	})
	type posRow struct {
		LastMap string
		LastX   uint16
		LastY   uint16
	}
	var row posRow
	require.NoError(t, gdb.Raw(
		`SELECT last_map, last_x, last_y FROM `+"`char`"+` WHERE char_id = ?`, charID).
		Scan(&row).Error, "read persisted position")
	return row.LastMap, row.LastX, row.LastY
}

// TestServe_Warp_MovesPlayerToNewMap is the M14a end-to-end: a player on
// Prontera contacts the Warper NPC, advances the dialog past `next`, and the
// warp statement migrates the player to Izlude. The proof is two-fold:
//   - ZC_NPCACK_MAPMOVE (0x0091) is emitted with map="izlude" and the warp cell.
//     This frame is the LAST step of scriptWorld.Warp, after the AOI relocate +
//     registry re-key + SavePosition already succeeded — observing it means the
//     whole server-side transit ran.
//   - last_map/last_x/last_y are persisted, so a fresh CZ_ENTER re-enter (whose
//     mechanics the Restart test already proves) would spawn the player on
//     Izlude rather than the source map.
//
// Script NPCs are allocated alphabetically (healer.txt → sample.txt →
// warper.txt), so the Warper is the 3rd script NPC: EntityID = NPCIDBase + 2.
func TestServe_Warp_MovesPlayerToNewMap(t *testing.T) {
	requireStack(t)

	cfg := loadConfigForE2E(t)
	tcpAddr := rebindListenersToEphemeralPorts(t, cfg)
	mapAddr := cfg.Gateway.MapAddr
	require.NoError(t, cfg.Validate(), "config drift: config.yaml no longer validates")

	seedCharID := seedCharForAccount(t, cfg, seededAccountID, "E2eWarp", "prontera", 150, 155)

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
	waitGatewayOrFatal(t, mapAddr, serveErr)

	// --- Login to capture the session token the CZ_ENTER gate verifies. ---
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
	require.NoError(t, err, "no AC_ACCEPT_LOGIN; login likely failed against the DB")
	loginID1 := binary.LittleEndian.Uint32(accept[4:8])
	sexByte := accept[46]
	require.NotZero(t, loginID1)
	require.NoError(t, loginConn.Close())

	// --- Enter on Prontera. ---
	mapConn := connectAndEnterMap(t, mapAddr, seedCharID, loginID1, sexByte, "E2eWarp")
	defer mapConn.Close()

	// --- Contact the Warper (Type 0 = script dialog) and advance past `next`. ---
	// Script NPCs allocate alphabetically: healer(0) → sample(1) → warper(2).
	warperID := worlddomain.NPCIDBase + 2
	var contact bytes.Buffer
	require.NoError(t, (packet.CZContactNPCRequest{AID: warperID, Type: 0}).Encode(&contact))
	_, err = mapConn.Write(contact.Bytes())
	require.NoError(t, err, "send CZ_CONTACTNPC")

	// The Warper script runs mes; mes; next; — the "Next" prompt (ZC_WAIT_DIALOG2)
	// arrives after the mes lines. readUntilCmd skips the variable-length mes
	// frames to find it.
	waitFrame := readUntilCmd(t, mapConn, packet.HeaderZCWAITDIALOG2, 5*time.Second)
	assert.Equal(t, warperID, binary.LittleEndian.Uint32(waitFrame[2:6]),
		"ZC_WAIT_DIALOG2 NpcID must match the Warper")

	// CZ_REQ_NEXT_SCRIPT advances past `next`; the VM then runs
	// `warp "izlude",128,128` → scriptWorld.Warp emits ZC_NPCACK_MAPMOVE (0x0091).
	var advance bytes.Buffer
	require.NoError(t, (packet.CZReqNextScriptRequest{NpcID: warperID}).Encode(&advance))
	_, err = mapConn.Write(advance.Bytes())
	require.NoError(t, err, "send CZ_REQ_NEXT_SCRIPT")

	moveFrame := readUntilCmd(t, mapConn, packet.HeaderZCNPCACKMAPMOVE, 5*time.Second)
	// ZC_NPCACK_MAPMOVE layout: [0:2]=0x0091 [2:18]=mapName[16] [18:20]=x [20:22]=y.
	gotMap := string(bytes.TrimRight(moveFrame[2:18], "\x00"))
	assert.Equal(t, "izlude", gotMap, "ZC_NPCACK_MAPMOVE map name = izlude")
	assert.Equal(t, uint16(128), binary.LittleEndian.Uint16(moveFrame[18:20]), "warp X = 128")
	assert.Equal(t, uint16(128), binary.LittleEndian.Uint16(moveFrame[20:22]), "warp Y = 128")

	// --- Persistence: last_map/last_x/last_y were written so a fresh CZ_ENTER
	// re-enter spawns on Izlude. (Re-enter mechanics are covered by the Restart
	// test; here we assert the warp persisted the destination.) ---
	pMap, pX, pY := readCharPosition(t, cfg, seedCharID)
	assert.Equal(t, "izlude", pMap, "persisted last_map = izlude")
	assert.Equal(t, uint16(128), pX, "persisted last_x = 128")
	assert.Equal(t, uint16(128), pY, "persisted last_y = 128")
}

// TestServe_UseSkill_Bash is the M14b end-to-end: a player on the Poring's
// spawn cell casts SM_BASH (skillID 5) via CZ_USE_SKILL2 and the server
// broadcasts ZC_NOTIFY_SKILL + applies damage. The test seeds a high-SP char
// at the Poring's home cell (155,165) so the first cast is in melee range. It
// asserts the ZC_NOTIFY_SKILL frame carries the correct skill ID (5 = SM_BASH)
// and a non-zero damage value. Repeated casts drive the Poring to death
// (ZC_NOTIFY_VANISH) proving damage was applied through the existing combat
// pipeline.
func TestServe_UseSkill_Bash(t *testing.T) {
	requireStack(t)

	cfg := loadConfigForE2E(t)
	tcpAddr := rebindListenersToEphemeralPorts(t, cfg)
	mapAddr := cfg.Gateway.MapAddr
	require.NoError(t, cfg.Validate())

	seedCharID := seedSkillChar(t, cfg, seededAccountID, "E2eBash", "prontera", 155, 165)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	serveErr := make(chan error, 1)
	go func() { serveErr <- app.Serve(ctx, cfg) }()
	defer func() {
		cancel()
		select {
		case err := <-serveErr:
			assert.NoError(t, err)
		case <-time.After(15 * time.Second):
			t.Fatal("app.Serve did not shut down within 15s")
		}
	}()
	waitGatewayOrFatal(t, tcpAddr, serveErr)
	waitGatewayOrFatal(t, mapAddr, serveErr)

	// Login → CZ_ENTER → map enter
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
	require.NoError(t, err, "no AC_ACCEPT_LOGIN")
	loginID1 := binary.LittleEndian.Uint32(accept[4:8])
	sexByte := accept[46]
	require.NotZero(t, loginID1)
	require.NoError(t, loginConn.Close())

	mapConn := connectAndEnterMap(t, mapAddr, seedCharID, loginID1, sexByte, "E2eBash")
	defer mapConn.Close()

	// --- locate the Poring in the enter spawn burst (same pattern as Combat test) ---
	var poringID uint32
	findEnd := time.Now().Add(5 * time.Second)
	for poringID == 0 && time.Now().Before(findEnd) {
		require.NoError(t, mapConn.SetReadDeadline(time.Now().Add(500*time.Millisecond)))
		frame, ok := readMapFrame(t, mapConn)
		if !ok {
			continue
		}
		if binary.LittleEndian.Uint16(frame) != packet.HeaderZCSPAWNUNIT || frame[4] != 5 {
			continue
		}
		if binary.LittleEndian.Uint16(frame[23:25]) == poringSprite {
			poringID = binary.LittleEndian.Uint32(frame[5:9])
		}
	}
	require.NotZero(t, poringID, "no Poring in the enter spawn burst")

	// --- Cast SM_BASH (skillID=5, level=5) on the Poring. The char spawns on
	// the Poring's cell so melee range is satisfied. Scan frames for
	// ZC_NOTIFY_SKILL to verify the skill broadcast was emitted. ---
	var skillNotify []byte
	var spUpdate []byte
	castEnd := time.Now().Add(5 * time.Second)
	for skillNotify == nil && time.Now().Before(castEnd) {
		var skill bytes.Buffer
		require.NoError(t, (packet.CZUseSkill{SkillLv: 5, SkillID: 5, TargetID: poringID}).Encode(&skill))
		_, err = mapConn.Write(skill.Bytes())
		require.NoError(t, err, "send CZ_USE_SKILL2")

		// Read frames for up to 1s looking for ZC_NOTIFY_SKILL and ZC_PAR_CHANGE SP_SP.
		readEnd := time.Now().Add(1 * time.Second)
		for time.Now().Before(readEnd) && skillNotify == nil {
			require.NoError(t, mapConn.SetReadDeadline(time.Now().Add(500*time.Millisecond)))
			frame, ok := readMapFrame(t, mapConn)
			if !ok {
				break // deadline; retry the cast
			}
			op := binary.LittleEndian.Uint16(frame)
			switch op {
			case packet.HeaderZCNOTIFYSKILL:
				skillNotify = frame
			case packet.HeaderZCPARCHANGE:
				if binary.LittleEndian.Uint16(frame[2:4]) == packet.SPSP {
					spUpdate = frame
				}
			}
		}
	}
	require.NotNil(t, skillNotify, "no ZC_NOTIFY_SKILL received within %v", 5*time.Second)

	// Verify the ZC_NOTIFY_SKILL carries SM_BASH's skill ID (5) and targets the Poring.
	assert.Equal(t, uint16(5), binary.LittleEndian.Uint16(skillNotify[2:4]),
		"ZC_NOTIFY_SKILL SKID = 5 (SM_BASH)")
	assert.Equal(t, poringID, binary.LittleEndian.Uint32(skillNotify[8:12]),
		"ZC_NOTIFY_SKILL targetID = Poring EntityID")
	skillDmg := int32(binary.LittleEndian.Uint32(skillNotify[24:28])) //nolint:gosec // G115: test wire decode
	assert.Greater(t, skillDmg, int32(0), "BASH damage > 0")

	// Verify SP was spent (ZC_PAR_CHANGE SP_SP was broadcast).
	require.NotNil(t, spUpdate, "no ZC_PAR_CHANGE SP_SP received — SP was not broadcast")
	newSP := int32(binary.LittleEndian.Uint32(spUpdate[4:8])) //nolint:gosec // G115: test wire decode
	assert.Less(t, newSP, int32(2000), "SP should have decreased from the cast cost")
}

// TestServe_StatChange is the M15 end-to-end proof. It boots the real modular monolith,
// logs in, enters Prontera, sends CZ_STATUS_CHANGE to raise STR, and verifies the
// server responds with ZC_STATUS_CHANGE_ACK (success) plus ZC_PAR_CHANGE frames for
// the updated STR and StatusPoint. It then verifies persistence by reading the char row.
func TestServe_StatChange(t *testing.T) {
	requireStack(t)

	cfg := loadConfigForE2E(t)
	tcpAddr := rebindListenersToEphemeralPorts(t, cfg)
	mapAddr := cfg.Gateway.MapAddr
	require.NoError(t, cfg.Validate())

	// Seed a character with known status points. STR starts at 1 (cost = 2).
	const startStatusPoint uint32 = 48
	seedCharID := seedCombatChar(t, cfg, seededAccountID, "E2eStatChg", "prontera", 155, 150)
	setCharStatusPoint(t, cfg, seedCharID, startStatusPoint)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	serveErr := make(chan error, 1)
	go func() { serveErr <- app.Serve(ctx, cfg) }()
	defer func() {
		cancel()
		select {
		case err := <-serveErr:
			assert.NoError(t, err)
		case <-time.After(15 * time.Second):
			t.Fatal("app.Serve did not shut down within 15s")
		}
	}()
	waitGatewayOrFatal(t, tcpAddr, serveErr)
	waitGatewayOrFatal(t, mapAddr, serveErr)

	// --- login → CZ_ENTER → map enter ---
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
	require.NoError(t, err, "no AC_ACCEPT_LOGIN")
	loginID1 := binary.LittleEndian.Uint32(accept[4:8])
	sexByte := accept[46]
	require.NotZero(t, loginID1)
	require.NoError(t, loginConn.Close())

	mapConn, err := net.Dial("tcp", mapAddr)
	require.NoError(t, err)
	defer mapConn.Close()
	var enter bytes.Buffer
	require.NoError(t, packet.CZEnterRequest{
		AccountID: seededAccountID, CharID: seedCharID, AuthCode: loginID1, ClientTime: 0, Sex: sexByte,
	}.Encode(&enter))
	_, err = mapConn.Write(enter.Bytes())
	require.NoError(t, err)
	require.NoError(t, mapConn.SetReadDeadline(time.Now().Add(5*time.Second)))
	enterResp := make([]byte, packet.MapAcceptEnterResponse{}.Size())
	_, err = io.ReadFull(mapConn, enterResp)
	require.NoError(t, err)
	require.Equal(t, packet.HeaderZCACCEPTENTER, binary.LittleEndian.Uint16(enterResp[0:2]))

	// Drain all post-enter frames (NPC/mob spawns + status burst) via frame scanning
	// so the next read lands on the stat-change response. A fixed-size read fails
	// when spawn packets interleave with the status burst (D8).
	require.NoError(t, mapConn.SetReadDeadline(time.Now().Add(800*time.Millisecond)))
	for {
		_, ok := readMapFrame(t, mapConn)
		if !ok {
			break
		}
	}

	// --- CZ_STATUS_CHANGE (0x00bb): raise STR (statID=13, amount=1) ---
	var statReq bytes.Buffer
	require.NoError(t, (packet.CZStatusChangeRequest{StatusID: 13, Amount: 1}).Encode(&statReq))
	_, err = mapConn.Write(statReq.Bytes())
	require.NoError(t, err)

	// Read ZC_STATUS_CHANGE_ACK (0x00bc, 6 bytes)
	require.NoError(t, mapConn.SetReadDeadline(time.Now().Add(5*time.Second)))
	ackFrame, ok := readMapFrame(t, mapConn)
	require.True(t, ok, "no ZC_STATUS_CHANGE_ACK received")
	assert.Equal(t, packet.HeaderZCSTATUSCHANGEACK, binary.LittleEndian.Uint16(ackFrame),
		"first response must be ZC_STATUS_CHANGE_ACK")
	assert.Equal(t, uint8(0), ackFrame[4], "result must be 0 (success)")
	assert.Equal(t, uint8(2), ackFrame[5], "STR must be 2 after the allocation")

	// Read ZC_PAR_CHANGE for SP_STR
	strPar, ok := readMapFrame(t, mapConn)
	require.True(t, ok, "no ZC_PAR_CHANGE for STR")
	assert.Equal(t, packet.HeaderZCPARCHANGE, binary.LittleEndian.Uint16(strPar))
	assert.Equal(t, packet.SPStr, binary.LittleEndian.Uint16(strPar[2:4]))
	strVal := int32(binary.LittleEndian.Uint32(strPar[4:8])) //nolint:gosec // G115: test wire decode
	assert.Equal(t, int32(2), strVal, "STR should be 2 after allocation")

	// Read ZC_PAR_CHANGE for SP_STATUSPOINT
	spPar, ok := readMapFrame(t, mapConn)
	require.True(t, ok, "no ZC_PAR_CHANGE for STATUSPOINT")
	assert.Equal(t, packet.HeaderZCPARCHANGE, binary.LittleEndian.Uint16(spPar))
	assert.Equal(t, packet.SPStatusPoint, binary.LittleEndian.Uint16(spPar[2:4]))
	spVal := int32(binary.LittleEndian.Uint32(spPar[4:8])) //nolint:gosec // G115: test wire decode
	assert.Equal(t, int32(startStatusPoint-2), spVal, "StatusPoint should be %d", startStatusPoint-2)

	// --- Verify persistence: STR and StatusPoint were saved ---
	finalSTR := readCharStat(t, cfg, seedCharID, "str")
	finalSP := readCharStatusPoint(t, cfg, seedCharID)
	assert.Equal(t, uint16(2), finalSTR, "STR should be persisted as 2")
	assert.Equal(t, startStatusPoint-2, finalSP, "StatusPoint should be persisted as %d", startStatusPoint-2)
}

// setCharStatusPoint updates the status_point column for a char row.
func setCharStatusPoint(t *testing.T, cfg *config.Config, charID uint32, sp uint32) {
	t.Helper()
	gdb, err := gorm.Open(mysql.Open(cfg.DBConnString()), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, gdb.Exec(
		`UPDATE `+"`char`"+` SET status_point = ? WHERE char_id = ?`, sp, charID).Error,
		"set char status_point")
}

// readCharStatusPoint reads the status_point column for a char row.
func readCharStatusPoint(t *testing.T, cfg *config.Config, charID uint32) uint32 {
	t.Helper()
	gdb, err := gorm.Open(mysql.Open(cfg.DBConnString()), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	var sp uint32
	require.NoError(t, gdb.Raw(
		`SELECT status_point FROM `+"`char`"+` WHERE char_id = ?`, charID).Scan(&sp).Error,
		"read persisted status_point")
	return sp
}

// readCharStat reads a named stat column (str/agi/vit/int/dex/luk) for a char row.
func readCharStat(t *testing.T, cfg *config.Config, charID uint32, stat string) uint16 {
	t.Helper()
	gdb, err := gorm.Open(mysql.Open(cfg.DBConnString()), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	var val uint16
	require.NoError(t, gdb.Raw(
		`SELECT `+stat+` FROM `+"`char`"+` WHERE char_id = ?`, charID).Scan(&val).Error,
		"read persisted stat "+stat)
	return val
}

// TestServe_GetCharName_RoundTrip is the L3 proof for CZ_GETCHARNAMEREQUEST
// (0x0094) name resolution. It drives the real map dispatch table: login →
// map-enter (which registers the player + loads the char name), then sends 0x0094
// for the player's own GID and asserts the server replies 0x0a30
// (ZC_ACK_REQNAMEALL2) carrying the char name — proving the opcode routes through
// BuildDispatcher → NameHandler → SpawnService.SendNameReply end to end.
func TestServe_GetCharName_RoundTrip(t *testing.T) {
	requireStack(t)
	cfg := loadConfigForE2E(t)
	tcpAddr := rebindListenersToEphemeralPorts(t, cfg)
	mapAddr := cfg.Gateway.MapAddr
	require.NoError(t, cfg.Validate(), "config drift: config.yaml no longer validates")

	seedCharID := seedCharForAccount(t, cfg, seededAccountID, "E2eHero", "prontera", 53, 111)

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
	waitGatewayOrFatal(t, mapAddr, serveErr)

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
	require.NoError(t, err, "no AC_ACCEPT_LOGIN received")
	loginID1 := binary.LittleEndian.Uint32(accept[4:8])
	sexByte := accept[46]
	require.NoError(t, loginConn.Close())

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

	// ZC_ACCEPT_ENTER: the trust gate admitted the session (the player is now
	// registered in the PlayerRegistry with CharID = seedCharID).
	enterResp := make([]byte, packet.MapAcceptEnterResponse{}.Size())
	_, err = io.ReadFull(mapConn, enterResp)
	require.NoError(t, err, "no ZC_ACCEPT_ENTER received")
	require.Equal(t, packet.HeaderZCACCEPTENTER, binary.LittleEndian.Uint16(enterResp[0:2]))

	// --- Send 0x0094 requesting the name for our own GID. ---
	var nameReq bytes.Buffer
	require.NoError(t, (packet.CZGetCharNameRequestRequest{GID: seedCharID}).Encode(&nameReq))
	_, err = mapConn.Write(nameReq.Bytes())
	require.NoError(t, err)

	// awaitFrame skips the self-spawn (and any neighbor spawns) until the 0x0a30
	// reply. A 5s timeout is generous; the handler replies synchronously.
	reply, ok := awaitFrame(t, mapConn, packet.HeaderZCACKREQNAMEALL2, 5*time.Second, nil)
	require.True(t, ok, "no ZC_ACK_REQNAMEALL2 (0x0a30) reply to CZ_GETCHARNAMEREQUEST")
	require.Equal(t, (packet.ReqNameAll2Response{}).Size(), len(reply), "REQNAMEALL2 length")
	assert.Equal(t, seedCharID, binary.LittleEndian.Uint32(reply[2:6]), "reply GID = requested char_id")
	assert.Equal(t, "E2eHero", string(bytes.TrimRight(reply[6:6+24], "\x00")), "reply carries the char name")
}

// TestServe_ItemDrop_RoundTrip is the A4 end-to-end: a player enters the world,
// drops one Jellopy (nameid 909) from bag slot 0 via CZ_ITEM_DROP (0x0363), and
// the server replies with the SELF ZC_ITEM_THROW_ACK (count=1, index echo) plus
// the AREA ZC_ITEM_FALL_ENTRY broadcast (ground id + cell + amount), then the
// bag row is gone from MariaDB. This proves the drop handler wires through
// BuildDispatcher → DropHandler → DropService and feeds the M10a pickup loop
// (the dropped floor item is then picked up, closing the loot loop).
func TestServe_ItemDrop_RoundTrip(t *testing.T) {
	requireStack(t)

	cfg := loadConfigForE2E(t)
	tcpAddr := rebindListenersToEphemeralPorts(t, cfg)
	mapAddr := cfg.Gateway.MapAddr
	require.NoError(t, cfg.Validate(), "config drift: config.yaml no longer validates")

	seedCharID := seedCharForAccount(t, cfg, seededAccountID, "E2eDropper", "prontera", 53, 111)
	// seed one Jellopy at bag slot 0 (the single row's id-ascending index).
	seedJellopyInBag(t, cfg, seedCharID)

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
	waitGatewayOrFatal(t, mapAddr, serveErr)

	loginConn, err := net.DialTimeout("tcp", tcpAddr, 3*time.Second)
	require.NoError(t, err)
	defer loginConn.Close()
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

	mapConn, err := net.DialTimeout("tcp", mapAddr, 3*time.Second)
	require.NoError(t, err)
	defer mapConn.Close()
	require.NoError(t, mapConn.SetReadDeadline(time.Now().Add(5*time.Second)))
	loginID1 := binary.LittleEndian.Uint32(accept[4:8])
	sexByte := accept[46]
	require.NoError(t, loginConn.Close())
	require.NoError(t, packet.CZEnterRequest{
		AccountID: seededAccountID, CharID: seedCharID, AuthCode: loginID1, ClientTime: 0, Sex: sexByte,
	}.Encode(mapConn))
	resp, ok := awaitFrame(t, mapConn, packet.HeaderZCACCEPTENTER, 5*time.Second, nil)
	require.True(t, ok, "no ZC_ACCEPT_ENTER; CZ_ENTER verification likely failed")
	require.Equal(t, packet.HeaderZCACCEPTENTER, binary.LittleEndian.Uint16(resp[0:2]), "expected ZC_ACCEPT_ENTER")
	spawn := make([]byte, packet.SpawnUnitResponse{}.Size())
	_, err = io.ReadFull(mapConn, spawn)
	require.NoError(t, err, "no ZC_SPAWN_UNIT; spawn-on-enter likely failed")
	require.Equal(t, packet.HeaderZCSPAWNUNIT, binary.LittleEndian.Uint16(spawn[0:2]), "expected ZC_SPAWN_UNIT")
	drainEnterStatusBurst(t, mapConn)

	// --- Drop the Jellopy from slot 0 (raw index; goAthena is 0-based). ---
	var drop bytes.Buffer
	require.NoError(t, (packet.CZDropItemRequest{InventoryIndex: 0, Amount: 1}).Encode(&drop))
	_, err = mapConn.Write(drop.Bytes())
	require.NoError(t, err, "send CZ_ITEM_DROP (0x0363)")

	var throwAck []byte
	var fallEntry []byte
	dropEnd := time.Now().Add(3 * time.Second)
	for (throwAck == nil || fallEntry == nil) && time.Now().Before(dropEnd) {
		require.NoError(t, mapConn.SetReadDeadline(time.Now().Add(500*time.Millisecond)))
		frame, ok := readMapFrame(t, mapConn)
		if !ok {
			continue
		}
		switch binary.LittleEndian.Uint16(frame) {
		case packet.HeaderZCItemThrowAck:
			throwAck = frame
		case packet.HeaderZCItemFallEntry:
			fallEntry = frame
		}
	}
	require.NotNil(t, throwAck, "no ZC_ITEM_THROW_ACK observed after CZ_ITEM_DROP — drop handler not wired or request rejected")
	assert.Equal(t, uint16(0), binary.LittleEndian.Uint16(throwAck[2:4]), "throw ack index echoes the raw slot 0")
	assert.Equal(t, uint16(1), binary.LittleEndian.Uint16(throwAck[4:6]), "throw ack count = dropped amount")

	require.NotNil(t, fallEntry, "no ZC_ITEM_FALL_ENTRY broadcast for the drop")
	assert.Equal(t, jellopyNameID, binary.LittleEndian.Uint32(fallEntry[6:10]), "fall entry nameid is the Jellopy (909)")
	assert.Equal(t, uint16(1), binary.LittleEndian.Uint16(fallEntry[19:21]), "fall entry amount = 1")
	groundID := binary.LittleEndian.Uint32(fallEntry[2:6])

	// --- The bag row is gone from MariaDB. ---
	assert.Equal(t, uint32(0), readInventoryAmount(t, cfg, seedCharID, jellopyNameID), "bag row removed after the drop")

	// --- Close the loot loop: pick the floor item back up (M10a path). ---
	var pickup bytes.Buffer
	require.NoError(t, (packet.CZItemPickupRequest{GroundID: groundID}).Encode(&pickup))
	_, err = mapConn.Write(pickup.Bytes())
	require.NoError(t, err, "send CZ_ITEM_PICKUP for the dropped Jellopy")
	pickupEnd := time.Now().Add(3 * time.Second)
	var pickupAck []byte
	for pickupAck == nil && time.Now().Before(pickupEnd) {
		require.NoError(t, mapConn.SetReadDeadline(time.Now().Add(500*time.Millisecond)))
		frame, ok := readMapFrame(t, mapConn)
		if !ok {
			continue
		}
		if binary.LittleEndian.Uint16(frame) == packet.HeaderZCItemPickupAck {
			pickupAck = frame
		}
	}
	require.NotNil(t, pickupAck, "no ZC_ITEM_PICKUP_ACK after re-picking the dropped item")
	assert.Equal(t, uint8(0), pickupAck[33], "pickup result=0 (success)")
	assert.Equal(t, uint32(1), readInventoryAmount(t, cfg, seedCharID, jellopyNameID), "picked item is back in the bag")
}

// seedJellopyInBag inserts one Jellopy (nameid 909) at bag slot 0 for the drop e2e.
func seedJellopyInBag(t *testing.T, cfg *config.Config, charID uint32) {
	t.Helper()
	gdb, err := gorm.Open(mysql.Open(cfg.DBConnString()), &gorm.Config{})
	require.NoError(t, err, "open gorm to seed Jellopy")
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, gdb.Exec(
		`INSERT INTO inventory (char_id, nameid, amount, equip) VALUES (?, 909, 1, 0)`,
		charID).Error, "insert Jellopy (nameid 909) into the char's bag")
	t.Cleanup(func() { _ = gdb.Exec("DELETE FROM inventory WHERE char_id = ?", charID).Error })
}
