//go:build integration

package app_test

import (
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"strconv"
	"testing"
	"time"

	accountdomain "github.com/bouroo/goAthena/internal/modules/account/domain"
	charapp "github.com/bouroo/goAthena/internal/modules/character/app"
	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	charinfra "github.com/bouroo/goAthena/internal/modules/character/infra"
	gwapp "github.com/bouroo/goAthena/internal/modules/gateway/app"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
)

// testAccountRepo is a minimal in-memory account repository stub for char integration tests.
type testAccountRepo struct {
	accounts map[accountdomain.AccountID]accountdomain.Account
}

func (r *testAccountRepo) FindByAccountID(_ context.Context, id accountdomain.AccountID) (accountdomain.Account, error) {
	a, ok := r.accounts[id]
	if !ok {
		return accountdomain.Account{}, accountdomain.ErrAccountNotFound
	}
	return a, nil
}

// mustTime parses "2006-01-02" and panics on error. Used only in test helpers.
func mustTime(s string) *time.Time {
	t, err := time.ParseInLocation("2006-01-02", s, time.UTC)
	if err != nil {
		panic(err)
	}
	return &t
}

// startCharListener spins a real CharServer (gnet reactor) on a free port,
// backed by a memory repo + session store seeded with one character (ID=150000).
// Returns the connection, character ID, and server address for use in tests.
func startCharListener(t *testing.T, port int, sessions *charinfra.MemorySessionStore, accountRepo gwapp.AccountRepo) (net.Conn, chardomain.CharID, string) {
	t.Helper()
	repo := charinfra.NewMemoryCharacterRepository()
	_, _ = repo.CreateWithID(t.Context(), chardomain.Character{
		ID: 150000, AccountID: 2000001, CharNum: 0, Name: "Hero",
		BaseLevel: 1, JobLevel: 1, MaxHP: 1000, HP: 1000, MaxSP: 50, SP: 50,
		LastMap: "new_1-1",
	})
	chars := charapp.NewCharService(repo, sessions, 9)
	cs, err := gwapp.NewCharServer(chars, accountRepo, slog.Default(), "127.0.0.1", 5121)
	if err != nil {
		t.Fatal(err)
	}
	serverAddr := "127.0.0.1:" + strconv.Itoa(port)
	cs.Start("tcp://" + serverAddr)
	t.Cleanup(cs.Stop)

	var conn net.Conn
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c, derr := net.DialTimeout("tcp", serverAddr, 500*time.Millisecond); derr == nil {
			conn = c
			break
		}
	}
	if conn == nil {
		t.Fatal("char listener never accepted")
	}
	return conn, 150000, serverAddr
}

// sendCHEnter builds and sends a 17-byte CH_ENTER (0x0065) frame.
func sendCHEnter(t *testing.T, c net.Conn, aid, id1, id2 uint32, sex uint8) {
	t.Helper()
	buf := make([]byte, 17)
	binary.LittleEndian.PutUint16(buf[0:], ropacket.HeaderCHENTER)
	binary.LittleEndian.PutUint32(buf[2:], aid)
	binary.LittleEndian.PutUint32(buf[6:], id1)
	binary.LittleEndian.PutUint32(buf[10:], id2)
	buf[16] = sex
	c.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write(buf); err != nil {
		t.Fatalf("send CH_ENTER: %v", err)
	}
}

// drainAcceptEnter reads and discards the full HC_ACCEPT_ENTER reply (the char
// list) so the next packet the test reads is a fresh response. It fails the test
// if the reply is not HC_ACCEPT_ENTER.
func drainAcceptEnter(t *testing.T, c net.Conn) {
	t.Helper()
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(c, hdr); err != nil {
		t.Fatalf("read accept-enter header: %v", err)
	}
	if got := binary.LittleEndian.Uint16(hdr); got != ropacket.HeaderHCACCEPTENTER {
		t.Fatalf("want HC_ACCEPT_ENTER before next packet, got 0x%04x", got)
	}
	n := int(binary.LittleEndian.Uint16(hdr[2:4]))
	if n < 4 {
		t.Fatalf("accept-enter length %d too small", n)
	}
	// Consume the full packet body so the next Read starts at a frame boundary.
	if n > 4 {
		rest := make([]byte, n-4)
		if _, err := io.ReadFull(c, rest); err != nil {
			t.Fatalf("read accept-enter body: %v", err)
		}
	}
	// Drain any additional bytes that arrived while we were reading (e.g., from
	// async writes on the same connection). Short reads on a deadline mean the
	// buffer is empty — the next test read will block until new data arrives.
	c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	_, _ = io.Copy(io.Discard, c)
}

// sendCHMakeChar builds and sends a 36-byte CH_MAKE_CHAR (0x0a39) frame.
// name is zero-padded into the 24-byte name slot; hair/style/job are left zero.
func sendCHMakeChar(t *testing.T, c net.Conn, name string, slot, sex uint8) {
	t.Helper()
	buf := make([]byte, 36)
	binary.LittleEndian.PutUint16(buf[0:], ropacket.HeaderCHMAKECHAR)
	copy(buf[2:26], name) // remainder stays zero-padded
	buf[26] = slot
	buf[35] = sex
	c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write(buf); err != nil {
		t.Fatalf("send CH_MAKE_CHAR: %v", err)
	}
}

// sendCHSelectChar builds and sends a 3-byte CH_SELECT_CHAR (0x0066) frame.
func sendCHSelectChar(t *testing.T, c net.Conn, slot uint8) {
	t.Helper()
	buf := make([]byte, 3)
	binary.LittleEndian.PutUint16(buf[0:], ropacket.HeaderCHSELECTCHAR)
	buf[2] = slot
	c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write(buf); err != nil {
		t.Fatalf("send CH_SELECT_CHAR: %v", err)
	}
}

func TestChar_AcceptEnterOverTCP(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1,
	})
	conn, _, _ := startCharListener(t, port, sessions, &testAccountRepo{accounts: map[accountdomain.AccountID]accountdomain.Account{
		2000001: {ID: 2000001, UserID: "testuser", Birthdate: mustTime("2000-06-15")},
	}})
	defer conn.Close()

	sendCHEnter(t, conn, 2000001, 0x11111111, 0x22222222, 1)

	conn.SetDeadline(time.Now().Add(3 * time.Second))
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		t.Fatalf("read reply header: %v", err)
	}
	got := binary.LittleEndian.Uint16(hdr)
	if got != ropacket.HeaderHCACCEPTENTER {
		t.Fatalf("response header = 0x%04x, want HC_ACCEPT_ENTER (0x%04x)", got, ropacket.HeaderHCACCEPTENTER)
	}
}

func TestChar_RefuseEnterBadSessionOverTCP(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	// session has loginID1 = 0xAAAA; client sends 0xBBBB → mismatch → refuse
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0xAAAAAAAA, LoginID2: 0, Sex: 1,
	})
	conn, _, _ := startCharListener(t, port, sessions, &testAccountRepo{accounts: map[accountdomain.AccountID]accountdomain.Account{
		2000001: {ID: 2000001, UserID: "testuser", Birthdate: mustTime("2000-06-15")},
	}})
	defer conn.Close()

	sendCHEnter(t, conn, 2000001, 0xBBBBBBBB, 0, 1)

	conn.SetDeadline(time.Now().Add(3 * time.Second))
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		t.Fatalf("read reply header: %v", err)
	}
	got := binary.LittleEndian.Uint16(hdr)
	if got != ropacket.HeaderHCREFUSEENTER {
		t.Fatalf("response header = 0x%04x, want HC_REFUSE_ENTER (0x%04x)", got, ropacket.HeaderHCREFUSEENTER)
	}
}

func TestChar_MakeCharAcceptOverTCP(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1,
	})
	conn, _, _ := startCharListener(t, port, sessions, &testAccountRepo{accounts: map[accountdomain.AccountID]accountdomain.Account{
		2000001: {ID: 2000001, UserID: "testuser", Birthdate: mustTime("2000-06-15")},
	}})
	defer conn.Close()

	sendCHEnter(t, conn, 2000001, 0x11111111, 0x22222222, 1)
	drainAcceptEnter(t, conn) // consume the char list before the next reply

	// Hero already occupies slot 0; create a new character in slot 1.
	sendCHMakeChar(t, conn, "Newbie", 1, 1)

	conn.SetDeadline(time.Now().Add(3 * time.Second))
	resp := make([]byte, 2+ropacket.CharacterInfoSize) // HC_ACCEPT_MAKECHAR = 2 + 175
	if _, err := io.ReadFull(conn, resp); err != nil {
		t.Fatalf("read accept-makechar: %v", err)
	}
	if got := binary.LittleEndian.Uint16(resp); got != ropacket.HeaderHCACCEPTMAKECHAR {
		t.Fatalf("response header = 0x%04x, want HC_ACCEPT_MAKECHAR (0x%04x)", got, ropacket.HeaderHCACCEPTMAKECHAR)
	}
	// CHARACTER_INFO.name[24] begins at offset 108; preceded by the 2-byte cmd.
	const nameOff = 2 + 108
	if got := string(resp[nameOff : nameOff+6]); got != "Newbie" {
		t.Fatalf("created name = %q, want Newbie", got)
	}
}

func TestChar_MakeCharRefuseNameTakenOverTCP(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1,
	})
	conn, _, _ := startCharListener(t, port, sessions, &testAccountRepo{accounts: map[accountdomain.AccountID]accountdomain.Account{
		2000001: {ID: 2000001, UserID: "testuser", Birthdate: mustTime("2000-06-15")},
	}}) // repo seeded with "Hero"
	defer conn.Close()

	sendCHEnter(t, conn, 2000001, 0x11111111, 0x22222222, 1)
	drainAcceptEnter(t, conn)

	// "Hero" already exists → ErrNameTaken → HC_REFUSE_MAKECHAR 0x00.
	sendCHMakeChar(t, conn, "Hero", 2, 1)

	conn.SetDeadline(time.Now().Add(3 * time.Second))
	resp := make([]byte, 3) // HC_REFUSE_MAKECHAR = 2 cmd + 1 error
	if _, err := io.ReadFull(conn, resp); err != nil {
		t.Fatalf("read refuse-makechar: %v", err)
	}
	if got := binary.LittleEndian.Uint16(resp); got != ropacket.HeaderHCREFUSEMAKECHAR {
		t.Fatalf("response header = 0x%04x, want HC_REFUSE_MAKECHAR (0x%04x)", got, ropacket.HeaderHCREFUSEMAKECHAR)
	}
	if resp[2] != 0x00 {
		t.Fatalf("refuse error = 0x%02x, want 0x00 (charname already exists)", resp[2])
	}
}

func TestChar_SelectCharNotifyZoneOverTCP(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1,
	})
	conn, _, _ := startCharListener(t, port, sessions, &testAccountRepo{accounts: map[accountdomain.AccountID]accountdomain.Account{
		2000001: {ID: 2000001, UserID: "testuser", Birthdate: mustTime("2000-06-15")},
	}}) // repo seeded with "Hero" in slot 0
	defer conn.Close()

	sendCHEnter(t, conn, 2000001, 0x11111111, 0x22222222, 1)
	drainAcceptEnter(t, conn)

	// Select slot 0 (Hero) → HC_NOTIFY_ZONESVR redirecting to the map server.
	sendCHSelectChar(t, conn, 0)

	conn.SetDeadline(time.Now().Add(3 * time.Second))
	// HC_NOTIFY_ZONESVR = 2 cmd + 4 CID + 16 mapName + 4 IP + 2 port + 128 domain = 156.
	resp := make([]byte, 156)
	if _, err := io.ReadFull(conn, resp); err != nil {
		t.Fatalf("read notify-zone: %v", err)
	}
	if got := binary.LittleEndian.Uint16(resp); got != ropacket.HeaderHCNOTIFYZONESVR {
		t.Fatalf("response header = 0x%04x, want HC_NOTIFY_ZONESVR (0x%04x)", got, ropacket.HeaderHCNOTIFYZONESVR)
	}
	// mapName[16] begins at offset 6.
	if got := string(resp[6 : 6+7]); got != "new_1-1" {
		t.Fatalf("map name = %q, want new_1-1", got)
	}
	// port is the uint16 at offset 26 (after CID[4] + mapName[16] + IP[4]).
	const portOff = 2 + 4 + 16 + 4
	if got := binary.LittleEndian.Uint16(resp[portOff:]); got != 5121 {
		t.Fatalf("advertised map port = %d, want 5121", got)
	}
}

// --- Delete-character flow helpers ---

// sendCHDeleteReserved sends a CH_DELETE_CHAR3_RESERVED frame.
func sendCHDeleteReserved(t *testing.T, conn net.Conn, cid uint32) {
	t.Helper()
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 6)
	binary.LittleEndian.PutUint16(buf[0:], ropacket.HeaderCHDELETECHAR3RESERVED)
	binary.LittleEndian.PutUint32(buf[2:], cid)
	if _, err := conn.Write(buf); err != nil {
		t.Fatalf("write CH_DELETE_CHAR3_RESERVED: %v", err)
	}
}

// readHCDeleteReserved reads and parses an HC_DELETE_CHAR3_RESERVED response.
func readHCDeleteReserved(t *testing.T, conn net.Conn) (cid uint32, result int32, date uint32) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer conn.SetReadDeadline(time.Time{})
	resp := make([]byte, 14)
	if _, err := io.ReadFull(conn, resp); err != nil {
		t.Fatalf("read HC_DELETE_CHAR3_RESERVED: %v", err)
	}
	if got := binary.LittleEndian.Uint16(resp); got != ropacket.HeaderHCDELETECHAR3RESERVED {
		t.Fatalf("response header = 0x%04x, want HC_DELETE_CHAR3_RESERVED (0x%04x)", got, ropacket.HeaderHCDELETECHAR3RESERVED)
	}
	return binary.LittleEndian.Uint32(resp[2:6]),
		int32(binary.LittleEndian.Uint32(resp[6:10])),
		binary.LittleEndian.Uint32(resp[10:14])
}

// sendCHDelete sends a CH_DELETE_CHAR3 frame with the given CID and 6-byte birthdate.
func sendCHDelete(t *testing.T, conn net.Conn, cid uint32, birthdate []byte) {
	t.Helper()
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if len(birthdate) != 6 {
		t.Fatalf("birthdate must be 6 bytes, got %d", len(birthdate))
	}
	buf := make([]byte, 12)
	binary.LittleEndian.PutUint16(buf[0:], ropacket.HeaderCHDELETECHAR3)
	binary.LittleEndian.PutUint32(buf[2:], cid)
	copy(buf[6:], birthdate)
	if _, err := conn.Write(buf); err != nil {
		t.Fatalf("write CH_DELETE_CHAR3: %v", err)
	}
}

// readHCDelete reads and parses an HC_DELETE_CHAR3 response.
func readHCDelete(t *testing.T, conn net.Conn) (cid uint32, result int32) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer conn.SetReadDeadline(time.Time{})
	resp := make([]byte, 10)
	if _, err := io.ReadFull(conn, resp); err != nil {
		t.Fatalf("read HC_DELETE_CHAR3: %v", err)
	}
	if got := binary.LittleEndian.Uint16(resp); got != ropacket.HeaderHCDELETECHAR3 {
		t.Fatalf("response header = 0x%04x, want HC_DELETE_CHAR3 (0x%04x)", got, ropacket.HeaderHCDELETECHAR3)
	}
	return binary.LittleEndian.Uint32(resp[2:6]),
		int32(binary.LittleEndian.Uint32(resp[6:10]))
}

// sendCHDeleteCancel sends a CH_DELETE_CHAR3_CANCEL frame.
func sendCHDeleteCancel(t *testing.T, conn net.Conn, cid uint32) {
	t.Helper()
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 6)
	binary.LittleEndian.PutUint16(buf[0:], ropacket.HeaderCHDELETECHAR3CANCEL)
	binary.LittleEndian.PutUint32(buf[2:], cid)
	if _, err := conn.Write(buf); err != nil {
		t.Fatalf("write CH_DELETE_CHAR3_CANCEL: %v", err)
	}
}

// readHCDeleteCancel reads and parses an HC_DELETE_CHAR3_CANCEL response.
func readHCDeleteCancel(t *testing.T, conn net.Conn) (cid uint32, result int32) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer conn.SetReadDeadline(time.Time{})
	resp := make([]byte, 10)
	if _, err := io.ReadFull(conn, resp); err != nil {
		t.Fatalf("read HC_DELETE_CHAR3_CANCEL: %v", err)
	}
	if got := binary.LittleEndian.Uint16(resp); got != ropacket.HeaderHCDELETECHAR3CANCEL {
		t.Fatalf("response header = 0x%04x, want HC_DELETE_CHAR3_CANCEL (0x%04x)", got, ropacket.HeaderHCDELETECHAR3CANCEL)
	}
	return binary.LittleEndian.Uint32(resp[2:6]),
		int32(binary.LittleEndian.Uint32(resp[6:10]))
}

// drainAcceptEnterUntilCharCount reads HC_ACCEPT_ENTER frames until it has collected
// at least n characters. Mirrors the packetLength-based parsing used by drainAcceptEnter.
// Returns all character GIDs extracted from the received frames.
func drainAcceptEnterUntilCharCount(t *testing.T, conn net.Conn, n int) (chars []uint32) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer conn.SetReadDeadline(time.Time{})
	for len(chars) < n {
		// Read cmd (2 bytes) + packetLength (2 bytes) to get total frame size.
		hdr := make([]byte, 4)
		if _, err := io.ReadFull(conn, hdr); err != nil {
			t.Fatalf("read HC_ACCEPT_ENTER header: %v", err)
		}
		if got := binary.LittleEndian.Uint16(hdr); got != ropacket.HeaderHCACCEPTENTER {
			t.Fatalf("want HC_ACCEPT_ENTER, got 0x%04x", got)
		}
		pktLen := int(binary.LittleEndian.Uint16(hdr[2:4]))
		if pktLen < 4 {
			t.Fatalf("accept-enter length %d too small", pktLen)
		}
		// Read the remaining body bytes (pktLen - 4, since we already read 4 header bytes).
		body := make([]byte, pktLen-4)
		if _, err := io.ReadFull(conn, body); err != nil {
			t.Fatalf("read HC_ACCEPT_ENTER body: %v", err)
		}
		// Parse character count from the total field at body[0], or derive from
		// packet length: body contains 27-byte sub-header + N×175-char entries.
		total := (pktLen - 27) / 175
		// First CharacterInfo block starts at body[23] (packet offset 27: 27 - 4 = 23).
		// Each CharacterInfo is 175 bytes; GID is at byte offset 0 within each block.
		for i := 0; i < total; i++ {
			gid := binary.LittleEndian.Uint32(body[23+i*175:])
			chars = append(chars, gid)
		}
	}
	return chars
}

// TestCharDeleteFlow_Reserve_OK verifies that reserving a deletion slot for a valid
// character returns result=1.
func TestCharDeleteFlow_Reserve_OK(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(context.Background(), chardomain.Session{AccountID: 2000001, LoginID1: 0x11111111})

	conn, _, _ := startCharListener(t, port, sessions, &testAccountRepo{accounts: map[accountdomain.AccountID]accountdomain.Account{
		2000001: {ID: 2000001, UserID: "testuser", Birthdate: mustTime("2000-06-15")},
	}})
	defer conn.Close()

	sendCHEnter(t, conn, 2000001, 0x11111111, 0x22222222, 1)
	drainAcceptEnter(t, conn)

	// Reserve deletion for known CID=150000.
	sendCHDeleteReserved(t, conn, 150000)
	cid, result, date := readHCDeleteReserved(t, conn)
	if cid != 150000 {
		t.Errorf("CID = %d, want 150000", cid)
	}
	if result != 1 {
		t.Errorf("result = %d, want 1 (OK)", result)
	}
	// char_del_delay is 0 today, so the remaining-seconds date field is 0 —
	// the wire contract at PACKETVER ≥20150513 (delete_date − now). Assert
	// the semantics, not the epoch: result 1 + non-negative date.
	if date != 0 {
		t.Errorf("date = %d, want 0 (remaining seconds; delay is 0)", date)
	}
}

// TestCharDeleteFlow_Reserve_UnknownCID verifies that reserving for an unknown
// character ID returns result=3.
func TestCharDeleteFlow_Reserve_UnknownCID(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(context.Background(), chardomain.Session{AccountID: 2000001, LoginID1: 0x11111111})

	conn, _, _ := startCharListener(t, port, sessions, &testAccountRepo{accounts: map[accountdomain.AccountID]accountdomain.Account{
		2000001: {ID: 2000001, UserID: "testuser", Birthdate: mustTime("2000-06-15")},
	}})
	defer conn.Close()

	sendCHEnter(t, conn, 2000001, 0x11111111, 0x22222222, 1)
	drainAcceptEnter(t, conn)

	// Reserve with unknown CID.
	sendCHDeleteReserved(t, conn, 999999)
	cid, result, date := readHCDeleteReserved(t, conn)
	if cid != 999999 {
		t.Errorf("CID = %d, want 999999", cid)
	}
	if result != 3 {
		t.Errorf("result = %d, want 3 (not found)", result)
	}
	if date != 0 {
		t.Errorf("date = %d, want 0", date)
	}
}

// TestCharDeleteFlow_Accept_WrongBirthdate verifies that accepting deletion with
// a mismatched birthdate returns result=5.
func TestCharDeleteFlow_Accept_WrongBirthdate(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(context.Background(), chardomain.Session{AccountID: 2000001, LoginID1: 0x11111111})

	conn, _, _ := startCharListener(t, port, sessions, &testAccountRepo{accounts: map[accountdomain.AccountID]accountdomain.Account{
		2000001: {ID: 2000001, UserID: "testuser", Birthdate: mustTime("2000-06-15")},
	}})
	defer conn.Close()

	sendCHEnter(t, conn, 2000001, 0x11111111, 0x22222222, 1)
	drainAcceptEnter(t, conn)

	// Reserve deletion for known CID=150000.
	sendCHDeleteReserved(t, conn, 150000)
	readHCDeleteReserved(t, conn) // discard

	// Accept with WRONG birthdate.
	sendCHDelete(t, conn, 150000, []byte("010101"))
	cid, result := readHCDelete(t, conn)
	if cid != 150000 {
		t.Errorf("CID = %d, want 150000", cid)
	}
	if result != 5 {
		t.Errorf("result = %d, want 5 (birthdate mismatch)", result)
	}
}

// TestCharDeleteFlow_Accept_CorrectBirthdate verifies that accepting deletion
// with the correct birthdate returns result=1 and the character is gone.
func TestCharDeleteFlow_Accept_CorrectBirthdate(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(context.Background(), chardomain.Session{AccountID: 2000001, LoginID1: 0x11111111})

	conn, _, serverAddr := startCharListener(t, port, sessions, &testAccountRepo{accounts: map[accountdomain.AccountID]accountdomain.Account{
		2000001: {ID: 2000001, UserID: "testuser", Birthdate: mustTime("2000-06-15")},
	}})
	defer conn.Close()

	sendCHEnter(t, conn, 2000001, 0x11111111, 0x22222222, 1)
	drainAcceptEnter(t, conn)

	// Reserve deletion for known CID=150000.
	sendCHDeleteReserved(t, conn, 150000)
	readHCDeleteReserved(t, conn) // discard

	// Accept with CORRECT birthdate: "000615" (year=2000 → 00, month=06, day=15).
	sendCHDelete(t, conn, 150000, []byte("000615"))
	cid, result := readHCDelete(t, conn)
	if cid != 150000 {
		t.Errorf("CID = %d, want 150000", cid)
	}
	if result != 1 {
		t.Errorf("result = %d, want 1 (deleted)", result)
	}

	// rAthena does NOT push a new char list after delete — open a new connection.
	conn.Close()
	conn2, err := net.DialTimeout("tcp", serverAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial for re-enter: %v", err)
	}
	defer conn2.Close()
	sendCHEnter(t, conn2, 2000001, 0x11111111, 0x22222222, 1)
	// Expect HC_ACCEPT_ENTER with 0 characters.
	conn2.SetDeadline(time.Now().Add(5 * time.Second))
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(conn2, hdr); err != nil {
		t.Fatalf("read HC_ACCEPT_ENTER: %v", err)
	}
	if got := binary.LittleEndian.Uint16(hdr); got != ropacket.HeaderHCACCEPTENTER {
		t.Fatalf("want HC_ACCEPT_ENTER, got 0x%04x", got)
	}
	pktLen := int(binary.LittleEndian.Uint16(hdr[2:4]))
	body := make([]byte, pktLen-4)
	if _, err := io.ReadFull(conn2, body); err != nil {
		t.Fatalf("read HC_ACCEPT_ENTER body: %v", err)
	}
	// body[2] is the Total sub-header field (max slots = maxChars = 9).
	// Actual character count is (pktLen - 27) / 175.
	total := (pktLen - 27) / 175
	if total != 0 {
		t.Errorf("remaining chars after delete = %d, want 0", total)
	}
}

// TestCharDeleteFlow_Cancel verifies that cancelling a pending deletion
// returns result=1 and the character remains in the list.
func TestCharDeleteFlow_Cancel(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(context.Background(), chardomain.Session{AccountID: 2000001, LoginID1: 0x11111111})

	conn, _, serverAddr := startCharListener(t, port, sessions, &testAccountRepo{accounts: map[accountdomain.AccountID]accountdomain.Account{
		2000001: {ID: 2000001, UserID: "testuser", Birthdate: mustTime("2000-06-15")},
	}})
	defer conn.Close()

	// Enter char-select on conn (drains HC_ACCEPT_ENTER).
	sendCHEnter(t, conn, 2000001, 0x11111111, 0x22222222, 1)
	drainAcceptEnter(t, conn)

	// Use a second connection for reserve+cancel to avoid the drainAcceptEnter
	// buffer-draining interfering with subsequent response reads.
	conn2, err := net.DialTimeout("tcp", serverAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial conn2: %v", err)
	}
	defer conn2.Close()
	// conn2 needs its own auth session.
	_ = sessions.PutSession(context.Background(), chardomain.Session{AccountID: 2000001, LoginID1: 0x11111111})
	sendCHEnter(t, conn2, 2000001, 0x11111111, 0x22222222, 1)
	drainAcceptEnter(t, conn2)

	// Reserve deletion for known CID=150000.
	sendCHDeleteReserved(t, conn2, 150000)
	readHCDeleteReserved(t, conn2)

	// Cancel deletion.
	sendCHDeleteCancel(t, conn2, 150000)
	cid, result := readHCDeleteCancel(t, conn2)
	if cid != 150000 {
		t.Errorf("CID = %d, want 150000", cid)
	}
	if result != 1 {
		t.Errorf("result = %d, want 1 (cancelled)", result)
	}

	// rAthena does NOT push a new char list after cancel — open a third connection.
	conn3, err := net.DialTimeout("tcp", serverAddr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial conn3 for re-enter: %v", err)
	}
	defer conn3.Close()
	_ = sessions.PutSession(context.Background(), chardomain.Session{AccountID: 2000001, LoginID1: 0x11111111})
	sendCHEnter(t, conn3, 2000001, 0x11111111, 0x22222222, 1)
	conn3.SetDeadline(time.Now().Add(5 * time.Second))
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(conn3, hdr); err != nil {
		t.Fatalf("read HC_ACCEPT_ENTER: %v", err)
	}
	if got := binary.LittleEndian.Uint16(hdr); got != ropacket.HeaderHCACCEPTENTER {
		t.Fatalf("want HC_ACCEPT_ENTER, got 0x%04x", got)
	}
	pktLen := int(binary.LittleEndian.Uint16(hdr[2:4]))
	body := make([]byte, pktLen-4)
	if _, err := io.ReadFull(conn3, body); err != nil {
		t.Fatalf("read HC_ACCEPT_ENTER body: %v", err)
	}
	t.Logf("conn3 pktLen=%d bodyLen=%d charCount=(pktLen-27)/175=%d", pktLen, len(body), (pktLen-27)/175)
	total := (pktLen - 27) / 175
	if total != 1 {
		t.Errorf("remaining chars after cancel = %d, want 1", total)
	}
}
