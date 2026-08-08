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
