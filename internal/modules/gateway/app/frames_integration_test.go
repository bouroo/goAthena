//go:build integration

package app_test

// Phase 43 regression anchor for the enter-frame ordering invariant.
//
// A connection's FIRST server frame must be its own ZC_ACCEPT_ENTER. The
// gateway guarantees this by submitting the accept-enter before registerConn
// makes the connection addressable (handleEnter, map.go): registerConn is the
// gate broadcast/trade/whisper all resolve through, so registering first let a
// peer's dispatch goroutine deliver a ZC_SPAWN_UNIT ahead of the accept-enter.
//
// This is the interleaving that used to desync every two-player test. Both
// CZ_ENTERs are written back-to-back with no read in between, so the two
// dispatch goroutines genuinely race — the same shape the suite's enter
// helpers have.

import (
	"fmt"
	"net"
	"testing"
	"time"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	charinfra "github.com/bouroo/goAthena/internal/modules/character/infra"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
)

// TestMap_ConcurrentEnterFirstFrameIsAcceptEnter enters two players whose
// CZ_ENTERs are handled concurrently and asserts neither connection receives
// anything before its own accept-enter.
//
// The assertion is deliberately on frame #1 rather than "somewhere in the
// stream": an accept-enter that arrives second is the defect, and scanning past
// it would hide exactly what this test exists to catch. The iteration count is
// what gives the race a chance to show itself — a single pass can pass by luck.
func TestMap_ConcurrentEnterFirstFrameIsAcceptEnter(t *testing.T) {
	const iterations = 20
	for i := range iterations {
		t.Run(fmt.Sprintf("iter%02d", i), func(t *testing.T) {
			port := freePort(t)
			sessions := charinfra.NewMemorySessionStore()
			_ = sessions.PutSession(t.Context(), chardomain.Session{AccountID: 2000001, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1})
			_ = sessions.PutSession(t.Context(), chardomain.Session{AccountID: 2000002, LoginID1: 0x33333333, Sex: 1})

			ms, _ := buildTradeMapDeps(t, sessions)
			conn1, conn2 := startAndDialTwo(t, ms, port)

			// Write both enters with no read in between: the handlers run on
			// concurrent goroutines, which is the interleaving under test.
			sendCZEnter(t, conn1, 2000001, 150001, 0x11111111)
			sendCZEnter(t, conn2, 2000002, 150002, 0x33333333)

			check := func(c net.Conn, which string) {
				t.Helper()
				cmd, frame := nextServerFrame(t, c, 3*time.Second)
				if cmd != ropacket.HeaderZCACCEPTENTER {
					t.Fatalf("%s: first frame = 0x%04x (%dB), want 0x%04x ZC_ACCEPT_ENTER (13B)",
						which, cmd, len(frame), ropacket.HeaderZCACCEPTENTER)
				}
				if len(frame) != 13 {
					t.Fatalf("%s: ZC_ACCEPT_ENTER length = %d, want 13", which, len(frame))
				}
			}
			check(conn1, "player 1")
			check(conn2, "player 2")
		})
	}
}
