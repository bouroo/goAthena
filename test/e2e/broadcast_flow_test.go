//go:build e2e

package e2e

import (
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/bouroo/goAthena/internal/features/identity/domain"
	netcodec "github.com/bouroo/goAthena/internal/infrastructure/net"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// scanForCmd reads framed packets from conn until one decodes with a
// command equal to wantCmd, or until deadline elapses. Non-matching
// frames are discarded — the broadcast e2e tolerates spawn packets
// (0x09fe) the gateway interleaves onto a client's stream while it
// waits for the movement broadcast (0x09fd) or the self move-ack
// (0x0087). Returns (matchedFrame, true) on success; (nil, false) on
// timeout (the caller decides how to fatal; real read errors fatal
// here so the test cannot silently continue past a broken stream).
func scanForCmd(t *testing.T, conn net.Conn, dec *netcodec.Decoder, deadline time.Duration, wantCmd uint16) ([]byte, bool) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(deadline)); err != nil {
		t.Fatalf("scan set read deadline: %v", err)
	}
	chunk := make([]byte, 4096)
	for {
		cmd, frame, err := dec.Next()
		if err == nil {
			if cmd == wantCmd {
				return frame, true
			}
			if uerr := conn.SetReadDeadline(time.Now().Add(deadline)); uerr != nil {
				t.Fatalf("scan re-arm deadline: %v", uerr)
			}
			continue
		}
		if err != netcodec.ErrIncomplete {
			t.Fatalf("scan decode: %v", err)
		}
		n, rerr := conn.Read(chunk)
		if n > 0 {
			dec.Feed(chunk[:n])
			continue
		}
		if rerr == nil {
			continue
		}
		if netErr, ok := rerr.(net.Error); ok && netErr.Timeout() {
			return nil, false
		}
		t.Fatalf("scan read: %v", rerr)
	}
}

// enterMap runs the full login → char-select → map-enter handshake
// for a freshly-created account/character and returns the live conn +
// decoder along with the auth + spawn state the caller needs to drive
// a move or assert a broadcast.
func enterMap(t *testing.T, h *E2EHarness, ioDeadline time.Duration, userID, password, charName string) (net.Conn, *netcodec.Decoder, uint32, uint32, uint8, uint32, int16, int16) {
	t.Helper()
	accountID := createTestAccount(t, h, userID, password, domain.SexMale)
	t.Cleanup(func() { deleteTestAccount(t, h, accountID) })
	t.Cleanup(func() {
		dctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		deleteValkeySession(dctx, h, accountID)
	})

	charID := createTestCharacter(t, h, accountID, 0, charName)
	t.Cleanup(func() { deleteTestCharacter(t, h, charID) })

	conn, dec := dialGatewayAndDecode(t, h.Config.GatewayTCPAddr)
	if err := conn.SetDeadline(time.Now().Add(ioDeadline)); err != nil {
		t.Fatalf("set conn deadline: %v", err)
	}

	aid, loginID1, sexByte := stageCALogin(t, conn, dec, ioDeadline, userID, password)
	if aid != accountID {
		t.Fatalf("AC_ACCEPT_LOGIN AID = %d, want %d (account fixture)", aid, accountID)
	}
	slotGID := stageCHEnter(t, conn, dec, ioDeadline, aid, loginID1, sexByte, charID, charName)
	stageCHSelectChar(t, conn, dec, ioDeadline)
	spawnX, spawnY := stageCZEnter(t, conn, dec, ioDeadline, aid, slotGID, loginID1, sexByte, charName)
	return conn, dec, aid, loginID1, sexByte, slotGID, spawnX, spawnY
}

// TestE2E_TwoClientsSeeEachOtherMove proves the Phase-1 multiplayer
// broadcast end to end: two distinct accounts enter the same default
// map; client A issues CZ_REQUEST_MOVE; client B receives a
// ZC_UNIT_WALKING (0x09fd, 114B) carrying A's account ID in the AID
// slot — the observer leg of the zone→NATS→gateway→observer fan-out
// built in Step 2d. A receives its own ZC_NOTIFY_PLAYERMOVE (0x0087)
// self-ack, confirming the zone accepted the move before it
// broadcast. Skips when the cluster is unreachable.
func TestE2E_TwoClientsSeeEachOtherMove(t *testing.T) {
	_ = TestContext(t)

	h := NewE2EHarness(t)

	const ioDeadline = 10 * time.Second

	userIDA := UniqueUserID()
	charNameA := UniqueCharName()
	connA, decA, accountAID, _, _, slotA, spawnXA, spawnYA := enterMap(t, h, ioDeadline, userIDA, "bcast-pass-A", charNameA)

	userIDB := UniqueUserID()
	charNameB := UniqueCharName()
	connB, decB, accountBID, _, _, _, _, _ := enterMap(t, h, ioDeadline, userIDB, "bcast-pass-B", charNameB)

	// After B enters the map, the gateway may buffer A's ZC_SPAWN_UNIT
	// (0x09fe) onto B's stream via the on-enter area-spawn path. scanForCmd
	// tolerates that interleaving — the strict "next frame must be X"
	// pattern used by single-client stages does not.
	if err := (packet.CZRequestMoveRequest{DestX: spawnXA + 5, DestY: spawnYA + 5}).Encode(connA); err != nil {
		t.Fatalf("encode CZ_REQUEST_MOVE (A): %v", err)
	}

	selfFrame, ok := scanForCmd(t, connA, decA, ioDeadline, packet.HeaderZCNOTIFYPLAYERMOVE)
	if !ok || len(selfFrame) != zcNotifyPlayerMoveSize {
		t.Fatalf("A did not receive its 0x0087 self move-ack within %s (frame_len=%d)", ioDeadline, len(selfFrame))
	}

	moveFrame, ok := scanForCmd(t, connB, decB, ioDeadline, packet.HeaderZCUNITWALKING)
	if !ok {
		t.Fatalf("B did not receive A's ZC_UNIT_WALKING (0x09fd) broadcast within %s; two-client broadcast broken", ioDeadline)
	}
	if len(moveFrame) != 114 {
		t.Fatalf("B received ZC_UNIT_WALKING length = %d, want 114 (frame=% x)", len(moveFrame), moveFrame)
	}
	if got := binary.LittleEndian.Uint32(moveFrame[5:9]); got != accountAID {
		t.Fatalf("B received ZC_UNIT_WALKING AID = %d, want %d (A's accountID)", got, accountAID)
	}
	t.Logf("two-client broadcast ok: B received A's move (0x09fd, 114B, AID=%d); AID-B=%d AID-A=%d charA=%q slotA=%d",
		accountAID, accountBID, accountAID, charNameA, slotA)
}
