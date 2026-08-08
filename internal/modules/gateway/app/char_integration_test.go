//go:build integration

package app_test

import (
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"strconv"
	"testing"
	"time"

	charapp "github.com/bouroo/goAthena/internal/modules/character/app"
	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	charinfra "github.com/bouroo/goAthena/internal/modules/character/infra"
	gwapp "github.com/bouroo/goAthena/internal/modules/gateway/app"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
)

// startCharListener spins a real CharServer (gnet reactor) on a free port,
// backed by a memory repo + session store seeded with one character.
func startCharListener(t *testing.T, port int, sessions *charinfra.MemorySessionStore) net.Conn {
	t.Helper()
	repo := charinfra.NewMemoryCharacterRepository()
	_, _ = repo.Create(t.Context(), chardomain.Character{
		AccountID: 2000001, CharNum: 0, Name: "Hero", BaseLevel: 1, JobLevel: 1,
		MaxHP: 1000, HP: 1000, MaxSP: 50, SP: 50, LastMap: "new_1-1",
	})
	chars := charapp.NewCharService(repo, sessions, 9)
	cs, err := gwapp.NewCharServer(chars, slog.Default(), "127.0.0.1", 5121)
	if err != nil {
		t.Fatal(err)
	}
	cs.Start("tcp://127.0.0.1:" + strconv.Itoa(port))
	t.Cleanup(cs.Stop)

	var conn net.Conn
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c, derr := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 500*time.Millisecond); derr == nil {
			conn = c
			break
		}
	}
	if conn == nil {
		t.Fatal("char listener never accepted")
	}
	return conn
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
	c.SetDeadline(time.Now().Add(3 * time.Second))
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
	rest := make([]byte, n-4)
	if _, err := io.ReadFull(c, rest); err != nil {
		t.Fatalf("read accept-enter body: %v", err)
	}
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
	conn := startCharListener(t, port, sessions)
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
	conn := startCharListener(t, port, sessions)
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
	conn := startCharListener(t, port, sessions)
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
	conn := startCharListener(t, port, sessions) // repo seeded with "Hero"
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
	conn := startCharListener(t, port, sessions) // repo seeded with "Hero" in slot 0
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
