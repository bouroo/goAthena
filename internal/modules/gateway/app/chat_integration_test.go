//go:build integration

package app_test

// Phase-36 L3: CZ_WHISPER over TCP between two live connections — the target
// receives ZC_WHISPER (0x09de) naming the sender, and the sender receives the
// fixed ZC_ACK_WHISPER (0x09df) success frame. A whisper to a name nobody
// carries answers result=1 (target offline).

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	charinfra "github.com/bouroo/goAthena/internal/modules/character/infra"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
)

// (Whisper tests reuse buildTradeMapDeps — a two-player "Hero"/"Partner"
// world. Whisper routing is name-based, not proximity-based, so the exact
// cells don't matter.)

// whisperFrame builds a raw CZ_WHISPER frame:
// [2:cmd][2:len][24:target NUL-padded][n:message + NUL].
func whisperFrame(target, message string) []byte {
	total := 4 + 24 + len(message) + 1
	buf := make([]byte, total)
	binary.LittleEndian.PutUint16(buf[0:], ropacket.HeaderCZWHISPER)
	binary.LittleEndian.PutUint16(buf[2:], uint16(total))
	copy(buf[4:], target)
	copy(buf[28:], message)
	return buf
}

// drainToWhisper consumes queued ZC_SPAWN_UNIT (0x09fe, 107B) frames off the
// target's connection until the ZC_WHISPER (0x09de) frame arrives, then parses
// it and returns (senderName, message). The two enters run on concurrent
// dispatch goroutines, so a connection may carry one spawn-unit per neighbor
// from BOTH the broadcast path and the AOI back-fill path — the exact count
// interleaves. Scanning forward to the whisper (rather than draining a fixed
// count) keeps the test insensitive to that ordering.
func drainToWhisper(t *testing.T, c net.Conn) (string, string) {
	t.Helper()
	for {
		c.SetReadDeadline(time.Now().Add(3 * time.Second))
		head := make([]byte, 4)
		if _, err := io.ReadFull(c, head); err != nil {
			t.Fatalf("read frame header: %v", err)
		}
		cmd := binary.LittleEndian.Uint16(head[0:2])
		plen := int(binary.LittleEndian.Uint16(head[2:4]))
		body := make([]byte, plen-4)
		if _, err := io.ReadFull(c, body); err != nil {
			t.Fatalf("read frame body (0x%04x): %v", cmd, err)
		}
		if cmd != ropacket.HeaderZCWHISPER {
			continue // spawn-unit or other enter-time frame
		}
		// body: [4:senderGID][24:senderName][1:isAdmin][n:message+null]
		name := body[4:28]
		if idx := bytes.IndexByte(name, 0); idx >= 0 {
			name = name[:idx]
		}
		msg := body[29:]
		if idx := bytes.IndexByte(msg, 0); idx >= 0 {
			msg = msg[:idx]
		}
		return string(name), string(msg)
	}
}

// drainQuiescent consumes every buffered frame until the connection goes
// quiet (200ms without bytes), tolerating any interleaved spawn-unit count.
// Used on the sender's conn, where the next expected frame is the 7B ack.
func drainQuiescent(t *testing.T, c net.Conn) {
	t.Helper()
	for {
		c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		head := make([]byte, 4)
		if _, err := io.ReadFull(c, head); err != nil {
			return // quiescent
		}
		cmd := binary.LittleEndian.Uint16(head[0:2])
		plen := int(binary.LittleEndian.Uint16(head[2:4]))
		if plen < 4 {
			t.Fatalf("frame 0x%04x bad length %d", cmd, plen)
		}
		if _, err := io.ReadFull(c, make([]byte, plen-4)); err != nil {
			t.Fatalf("drain frame body (0x%04x): %v", cmd, err)
		}
	}
}

func TestMap_WhisperBetweenPlayers(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{AccountID: 2000001, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1})
	_ = sessions.PutSession(t.Context(), chardomain.Session{AccountID: 2000002, LoginID1: 0x33333333, Sex: 1})

	// Reuse the shared two-player harness, entering both players first.
	ms, env := buildTradeMapDeps(t, sessions)
	conn1, conn2 := startAndDialTwo(t, ms, port)
	defer conn1.Close()
	defer conn2.Close()
	_ = env

	sendCZEnter(t, conn1, 2000001, 150001, 0x11111111)
	sendCZEnter(t, conn2, 2000002, 150002, 0x33333333)
	// Each enter's reply: 13B accept-enter, then the other's 107B spawn-unit
	// (same drain sequence the trade test uses).
	if _, err := io.ReadFull(conn1, make([]byte, 13)); err != nil {
		t.Fatalf("drain p1 accept-enter: %v", err)
	}
	if _, err := io.ReadFull(conn2, make([]byte, 13)); err != nil {
		t.Fatalf("drain p2 accept-enter: %v", err)
	}
	drainQuiescent(t, conn1)
	drainQuiescent(t, conn2)

	// Player 1 whispers Player 2 by name.
	conn1.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn1.Write(whisperFrame("Partner", "psst over here")); err != nil {
		t.Fatalf("send CZ_WHISPER: %v", err)
	}

	name, msg := drainToWhisper(t, conn2)
	if name != "Hero" {
		t.Fatalf("whisper sender = %q, want Hero", name)
	}
	if msg != "psst over here" {
		t.Fatalf("whisper text = %q, want %q", msg, "psst over here")
	}

	// Sender's ack: fixed 7B ZC_ACK_WHISPER, result 0, CID = sender charID.
	ack := readTradeFrame(t, conn1, ropacket.HeaderZCACKWHISPER, 7)
	if ack[2] != 0 {
		t.Fatalf("whisper ack result = %d, want 0 (success)", ack[2])
	}
	if got := binary.LittleEndian.Uint32(ack[3:]); got != 150001 {
		t.Fatalf("whisper ack CID = %d, want 150001", got)
	}
}

func TestMap_WhisperTargetOffline(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{AccountID: 2000001, LoginID1: 0x11111111, Sex: 1})

	ms, _ := buildTradeMapDeps(t, sessions)
	conn1, conn2 := startAndDialTwo(t, ms, port)
	defer conn1.Close()
	conn2.Close() // only player 1 stays

	sendCZEnter(t, conn1, 2000001, 150001, 0x11111111)
	if _, err := io.ReadFull(conn1, make([]byte, 13)); err != nil {
		t.Fatalf("drain accept-enter: %v", err)
	}
	drainQuiescent(t, conn1)

	conn1.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn1.Write(whisperFrame("Nobody", "hello?")); err != nil {
		t.Fatalf("send CZ_WHISPER: %v", err)
	}
	ack := readTradeFrame(t, conn1, ropacket.HeaderZCACKWHISPER, 7)
	if ack[2] != 1 {
		t.Fatalf("offline ack result = %d, want 1 (target offline)", ack[2])
	}
}
