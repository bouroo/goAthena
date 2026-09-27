//go:build integration

package app_test

// M12 cross-zone handoff (L3): a warp portal whose destination map resolves
// to a REMOTE zone redirects the client with ZC_NPCACK_SERVERMOVE (0x0ac7,
// 156B — rAthena clif_changemapserver) instead of the local
// ZC_NPCACK_MAPMOVE, and the world entity leaves this zone's world (the
// disconnect path's LeaveMap on the already-removed entity is the idempotent
// no-op the OnClose contract promises).

import (
	"encoding/binary"
	"strings"
	"testing"
	"time"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	charinfra "github.com/bouroo/goAthena/internal/modules/character/infra"
	transitdomain "github.com/bouroo/goAthena/internal/modules/transit/domain"

	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
	"github.com/bouroo/goAthena/pkg/ro/script"
)

// TestMap_CrossZonePortalRedirects proves the remote leg of the M12 portal
// path: with a directory that answers "geffen" with a remote zone address,
// walking onto the portal trigger emits the 156-byte server-move redirect
// carrying that address, and the player is out of this zone's world.
func TestMap_CrossZonePortalRedirects(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1,
	})
	ms, env := buildTestMapDeps(t, sessions)

	// A directory where "geffen" belongs to zone-b (127.0.0.1:5121 wire
	// values); every other map resolves local (zero Zone).
	ms.SetZoneDirectory(remoteGeffenDirectory{})

	conn := startAndDial(t, ms, port)
	defer conn.Close()

	env.spawn.RegisterPortals([]script.WarpDef{{
		MapName: "new_1-1", X: 54, Y: 111, TriggerX: 54, TriggerY: 111,
		DestMap: "geffen", DestX: 120, DestY: 77,
	}})

	sendCZEnter(t, conn, 2000001, 150001, 0x11111111)
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	awaitAcceptEnter(t, conn)

	// Walk east onto the trigger tile.
	moveReq := make([]byte, 5)
	binary.LittleEndian.PutUint16(moveReq[0:], ropacket.HeaderCZREQUESTMOVE)
	moveDest := packMoveDest(54, 111)
	copy(moveReq[2:], moveDest[:])
	sendRaw(t, conn, moveReq)

	// Self move-ack, then the 156B ZC_NPCACK_SERVERMOVE redirect.
	readTradeFrame(t, conn, ropacket.HeaderZCNOTIFYPLAYERMOVE, 12)
	move := readTradeFrame(t, conn, ropacket.HeaderZCNPCACKSERVERMOVE, 156)
	if got := strings.TrimRight(string(move[2:26]), "\x00"); got != "geffen" {
		t.Errorf("dest map = %q, want geffen", got)
	}
	if x := binary.LittleEndian.Uint16(move[26:28]); x != 120 {
		t.Errorf("dest X = %d, want 120", x)
	}
	if y := binary.LittleEndian.Uint16(move[28:30]); y != 77 {
		t.Errorf("dest Y = %d, want 77", y)
	}
	if ip := binary.BigEndian.Uint32(move[30:34]); ip != 0x7f000001 {
		t.Errorf("zone ip = %#x, want 0x7f000001 (big-endian wire)", ip)
	}
	if p := binary.BigEndian.Uint16(move[34:36]); p != 0x1401 {
		t.Errorf("zone port = %#x, want 0x1401 (byte-swapped wire)", p)
	}

	// The redirect's LeaveRemoteZone removed the entity: the disconnect
	// path's later LeaveMap is the idempotent no-op, and the player is gone.
	if err := env.world.LeaveMap(t.Context(), 150001); err != nil {
		t.Fatalf("second leave after redirect: %v", err)
	}
	if _, err := env.world.Get(150001); err == nil {
		t.Errorf("player still in this zone's world after redirect")
	}
}

// remoteGeffenDirectory routes "geffen" to a fake remote zone; everything
// else stays local (zero Zone).
type remoteGeffenDirectory struct{}

func (remoteGeffenDirectory) Resolve(mapName string) (transitdomain.Zone, error) {
	if mapName == "geffen" {
		return transitdomain.Zone{Name: "zone-b", IPv4: 0x7f000001, Port: 0x1401}, nil
	}
	return transitdomain.Zone{}, nil
}
