//go:build integration

package app_test

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"encoding/hex"
	"io"
	"log/slog"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/bouroo/goAthena/internal/modules/account/app"
	"github.com/bouroo/goAthena/internal/modules/account/domain"
	"github.com/bouroo/goAthena/internal/modules/account/infra"
	charinfra "github.com/bouroo/goAthena/internal/modules/character/infra"
	gwapp "github.com/bouroo/goAthena/internal/modules/gateway/app"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
)

// freePort returns a TCP port currently free on localhost (best-effort; small
// race window before gnet rebinds).
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// readReply reads the 2-byte little-endian response header from the login server.
func readReply(t *testing.T, c net.Conn) uint16 {
	t.Helper()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(c, hdr); err != nil {
		t.Fatalf("read reply header: %v", err)
	}
	return binary.LittleEndian.Uint16(hdr)
}

// startLoginListener spins a real LoginServer (gnet reactor) on a free port,
// backed by a memory repo seeded with a known MD5 account. Returns the address.
func startLoginListener(t *testing.T, port int) (*gwapp.LoginServer, net.Conn) {
	t.Helper()
	repo := infra.NewMemoryAccountRepository(domain.Account{
		ID: 2000001, UserID: "testacc",
		UserPass: md5hex("s3cret"), Sex: domain.SexMale,
	})
	auth := app.NewAuthService(repo, true)
	sessions := charinfra.NewMemorySessionStore()
	ls, err := gwapp.NewLoginServer(auth, sessions, slog.Default(), "127.0.0.1", "goathena-test", 6121)
	if err != nil {
		t.Fatal(err)
	}
	ls.Start("tcp://127.0.0.1:" + strconv.Itoa(port))
	t.Cleanup(ls.Stop)

	// Wait for the reactor to accept connections.
	var conn net.Conn
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c, derr := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 500*time.Millisecond); derr == nil {
			conn = c
			break
		}
	}
	if conn == nil {
		t.Fatal("login listener never accepted")
	}
	return ls, conn
}

func sendCALogin(t *testing.T, c net.Conn, user, pass string) {
	t.Helper()
	buf := &bytes.Buffer{}
	req := ropacket.CALoginRequest{Version: 55, Username: user, Password: pass, ClientType: 0x0}
	if err := req.Encode(buf); err != nil {
		t.Fatalf("encode CA_LOGIN: %v", err)
	}
	c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write(buf.Bytes()); err != nil {
		t.Fatalf("send CA_LOGIN: %v", err)
	}
}

func md5hex(s string) string {
	d := md5.Sum([]byte(s))
	return hex.EncodeToString(d[:])
}

func TestLogin_AcceptOverTCP(t *testing.T) {
	port := freePort(t)
	_, conn := startLoginListener(t, port)
	defer conn.Close()

	sendCALogin(t, conn, "testacc", "s3cret")
	hdr := readReply(t, conn)
	if hdr != ropacket.HeaderACACCEPTLOGIN {
		t.Fatalf("response header = 0x%04x, want AC_ACCEPT_LOGIN (0x%04x)", hdr, ropacket.HeaderACACCEPTLOGIN)
	}
}

func TestLogin_RefuseBadPasswordOverTCP(t *testing.T) {
	port := freePort(t)
	_, conn := startLoginListener(t, port)
	defer conn.Close()

	sendCALogin(t, conn, "testacc", "wrong")
	hdr := readReply(t, conn)
	if hdr != ropacket.HeaderACREFUSELOGIN {
		t.Fatalf("response header = 0x%04x, want AC_REFUSE_LOGIN (0x%04x)", hdr, ropacket.HeaderACREFUSELOGIN)
	}
}

func TestLogin_RefuseUnknownAccountOverTCP(t *testing.T) {
	port := freePort(t)
	_, conn := startLoginListener(t, port)
	defer conn.Close()

	sendCALogin(t, conn, "nobody", "x")
	hdr := readReply(t, conn)
	if hdr != ropacket.HeaderACREFUSELOGIN {
		t.Fatalf("response header = 0x%04x, want AC_REFUSE_LOGIN (0x%04x)", hdr, ropacket.HeaderACREFUSELOGIN)
	}
}
