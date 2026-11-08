//go:build integration

package app_test

import (
	"bytes"
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

// stubMapEndpoints answers a fixed address for one map and the local
// address for the rest — the shape the transit directory adapter has.
type stubMapEndpoints struct {
	local  gwapp.MapEndpoint
	remote map[string]gwapp.MapEndpoint
}

func (s stubMapEndpoints) ResolveMapEndpointFor(_ context.Context, mapName string) (gwapp.MapEndpoint, error) {
	if ep, ok := s.remote[mapName]; ok {
		return ep, nil
	}
	return s.local, nil
}

// TestChar_SelectCharAdvertisesTheRemoteMapAddress proves the char server
// hands the client the address of the shard that serves the character's
// map.
//
// In a multi-process deployment the character may be on a map another
// shard serves. The client dials whatever address the handshake carries,
// so a shard that always advertised its own address would send the
// character into the wrong world.
//
// The character's map here is "new_1-1" (the seeded character), and the
// resolver answers it with 10.0.4.7:6121.
func TestChar_SelectCharAdvertisesTheRemoteMapAddress(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1,
	})
	accountRepo := &testAccountRepo{accounts: map[accountdomain.AccountID]accountdomain.Account{
		2000001: {ID: 2000001, UserID: "testuser", Birthdate: mustTime("2000-06-15")},
	}}

	conn, cs := startCharListenerWithServer(t, port, sessions, accountRepo)
	defer conn.Close()

	// The resolver answers the character's map with another shard's
	// address. The char server is told about it after construction, the
	// way the composition root wires it.
	cs.SetMapEndpoints(stubMapEndpoints{
		local: gwapp.MapEndpoint{Host: "127.0.0.1", Port: 5121},
		remote: map[string]gwapp.MapEndpoint{
			"new_1-1": {Host: "10.0.4.7", Port: 6121},
		},
	})

	sendCHEnter(t, conn, 2000001, 0x11111111, 0x22222222, 1)
	drainAcceptEnter(t, conn)
	sendCHSelectChar(t, conn, 0)

	conn.SetDeadline(time.Now().Add(3 * time.Second))
	resp := make([]byte, 156)
	if _, err := io.ReadFull(conn, resp); err != nil {
		t.Fatalf("read notify-zone: %v", err)
	}
	if got := binary.LittleEndian.Uint16(resp); got != ropacket.HeaderHCNOTIFYZONESVR() {
		t.Fatalf("response header = 0x%04x, want HC_NOTIFY_ZONESVR", got)
	}

	// The IP at offset 22 must be the remote shard's address, in the
	// bytes the client reads back as 10.0.4.7: the little-endian memory
	// image of htonl(10.0.4.7) = 0x0a000407.
	const ipOff = 2 + 4 + 16
	if got := resp[ipOff : ipOff+4]; !bytes.Equal(got, []byte{0x07, 0x04, 0x00, 0x0a}) {
		t.Errorf("advertised map ip bytes = % x, want 07 04 00 0a (10.0.4.7)", got)
	}
	// The port at offset 26 is the remote shard's port.
	const portOff = ipOff + 4
	if got := binary.LittleEndian.Uint16(resp[portOff:]); got != 6121 {
		t.Errorf("advertised map port = %d, want 6121", got)
	}
}

// startCharListenerWithServer starts a char listener and returns the
// client connection and the server, so a test can wire the server after
// construction.
func startCharListenerWithServer(
	t *testing.T, port int, sessions *charinfra.MemorySessionStore, accountRepo gwapp.AccountRepo,
) (net.Conn, *gwapp.CharServer) {
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
	return conn, cs
}
