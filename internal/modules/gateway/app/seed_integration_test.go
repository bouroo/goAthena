//go:build integration

package app_test

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	charinfra "github.com/bouroo/goAthena/internal/modules/character/infra"
	worldapp "github.com/bouroo/goAthena/internal/modules/world/app"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
	"github.com/bouroo/goAthena/pkg/ro/script"
)

// The Phase-33 L3 proofs: a world seeded from a real-syntax script corpus
// shows its NPCs to an entering client (ZC_SPAWN_UNIT ObjectType=6
// back-fill), and a shop NPC click drives the 0x0090 → 0x00c4 deal-type
// chain that the dev-shop test previously injected mid-flow.

const seedCorpusCity = "new_1-1,52,111,3\tscript\tShuger\t98,{\n\tmes \"hi\";\n\tclose;\n}\n" +
	"new_1-1,54,111,0\tshop\tButcher\t54,517:-1\n"

// startSeededMapListener builds the shared harness, seeds it from the corpus
// through the production compile path, and returns the conn plus the seeded
// shop NPC GID.
func startSeededMapListener(t *testing.T, port int) (net.Conn, uint32) {
	t.Helper()
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1,
	})
	ms, env := buildTestMapDeps(t, sessions)

	set := script.NewCompiledScriptSet()
	if err := script.CompileInto([]byte(seedCorpusCity), set); err != nil {
		t.Fatalf("compile city corpus: %v", err)
	}
	seeder := worldapp.NewWorldSeeder(env.world, env.spawn, nil, slog.Default())
	var shopG uint32
	npcs, _ := seeder.Seed(set,
		func(_ uint32, _ string) {},
		func(gid uint32, _ string) { shopG = gid },
	)
	if npcs != 2 {
		t.Fatalf("seeded %d npcs, want 2 (dialog + shop)", npcs)
	}
	// Bind the shop GID to the harness's dev catalog so the click resolves a
	// real shop service behind it.
	ms.BindShop(shopG, devTestShopName)

	conn := startAndDial(t, ms, port)
	return conn, shopG
}

// enterMap performs CZ_ENTER and drains the fixed 13-byte ZC_ACCEPT_ENTER
// plus the AOI back-fill spawn-units that ride the same enter write (fixed
// 107-byte frames, count returned).
func enterMap(t *testing.T, conn net.Conn, r *bufio.Reader) (spawnUnits int) {
	t.Helper()
	sendCZEnter(t, conn, 2000001, 150001, 0x11111111)
	// The enter write is 13B ZC_ACCEPT_ENTER followed by the AOI back-fill
	// (107B ZC_SPAWN_UNIT frames). Scan from a single buffered stream so the
	// unconsumed tail stays aligned for later readers.
	var pre []byte
	for spawnUnits < 2 && len(pre) < 8192 {
		chunk, err := r.Peek(4096)
		if len(chunk) == 0 {
			if err != nil {
				t.Fatalf("read enter stream: %v", err)
			}
			continue
		}
		pre = append(pre, chunk...)
		_, _ = r.Discard(len(chunk))
		for i := 0; i+107 <= len(pre); i++ {
			if binary.LittleEndian.Uint16(pre[i:]) == ropacket.HeaderZCSPAWNUNIT && pre[i+4] == 6 {
				spawnUnits++
			}
		}
	}
	return spawnUnits
}

// drainLoadEndAckBurst consumes the LoadEndAck inventory + skill-list burst
// so a test can read the frames that follow it.
func drainLoadEndAckBurst(t *testing.T, conn net.Conn, r *bufio.Reader) {
	t.Helper()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte{0x7d, 0x00}); err != nil { // CZ_NOTIFY_ACTORINIT
		t.Fatalf("send LoadEndAck: %v", err)
	}
	if _, err := io.ReadFull(r, make([]byte, 20)); err != nil { // inventory burst
		t.Fatalf("drain inventory burst: %v", err)
	}
	hdr := make([]byte, 4)
	if _, err := io.ReadFull(r, hdr); err != nil {
		t.Fatalf("read skill-list header: %v", err)
	}
	n := int(binary.LittleEndian.Uint16(hdr[2:4]))
	if n < 4 {
		t.Fatalf("skill-list len = %d", n)
	}
	if _, err := io.ReadFull(r, make([]byte, n-4)); err != nil {
		t.Fatalf("drain skill list: %v", err)
	}
}

// fixedSize maps the fixed-size reply commands these tests read to their
// total wire length (cmd 2B + body).
var fixedSize = map[uint16]int{
	ropacket.HeaderZCSELECTDEALTYPE: 6, // cmd + NpcID
}

// readVarFrame reads one frame and returns its command and body past the
// 4-byte header. Fixed-length packets resolve via fixedSize; variable-length
// ones carry uint16 length at offset 2.
func readVarFrame(t *testing.T, r *bufio.Reader) (cmd uint16, body []byte) {
	t.Helper()
	head := make([]byte, 2)
	if _, err := io.ReadFull(r, head); err != nil {
		t.Fatalf("read frame cmd: %v", err)
	}
	cmd = binary.LittleEndian.Uint16(head)
	if sz, ok := fixedSize[cmd]; ok {
		body := make([]byte, sz-2)
		if _, err := io.ReadFull(r, body); err != nil {
			t.Fatalf("read frame body (cmd 0x%04x): %v", cmd, err)
		}
		return cmd, body
	}
	hdr := make([]byte, 4)
	copy(hdr, head)
	if _, err := io.ReadFull(r, hdr[2:4]); err != nil {
		t.Fatalf("read frame length: %v", err)
	}
	cmd = binary.LittleEndian.Uint16(hdr[0:2])
	var n int
	if sz, ok := fixedSize[cmd]; ok {
		n = sz - 4
	} else {
		n = int(binary.LittleEndian.Uint16(hdr[2:4])) - 4
		if n < 0 {
			t.Fatalf("frame 0x%04x len underflow", cmd)
		}
	}
	body = make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		t.Fatalf("read frame body (cmd 0x%04x): %v", cmd, err)
	}
	return cmd, body
}

// TestMap_SeededNPCVisibleOnEnter: a client entering a seeded map receives a
// ZC_SPAWN_UNIT with ObjectType=6 for the dialog NPC in the AOI back-fill,
// carrying the NPC's GID and sprite.
func TestMap_SeededNPCVisibleOnEnter(t *testing.T) {
	port := freePort(t)
	conn, _ := startSeededMapListener(t, port)
	defer conn.Close()

	if n := enterMap(t, conn, bufio.NewReader(conn)); n < 2 {
		t.Fatalf("NPC back-fill spawn-units = %d, want >= 2 (dialog + shop)", n)
	}
}

// TestMap_SeededShopClickOpensDealType: CZ_CONTACT_NPC on the seeded shop
// GID returns ZC_SELECT_DEALTYPE (0x00c4) with that GID.
func TestMap_SeededShopClickOpensDealType(t *testing.T) {
	port := freePort(t)
	conn, shopGID := startSeededMapListener(t, port)
	defer conn.Close()

	r := bufio.NewReader(conn)
	enterMap(t, conn, r)
	drainLoadEndAckBurst(t, conn, r)

	frame := make([]byte, 7) // CZ_CONTACT_NPC: cmd + GID + type
	binary.LittleEndian.PutUint16(frame[0:], 0x0090)
	binary.LittleEndian.PutUint32(frame[2:], shopGID)
	frame[6] = 1 // click
	if _, err := conn.Write(frame); err != nil {
		t.Fatalf("send CZ_CONTACT_NPC: %v", err)
	}

	cmd, body := readVarFrame(t, r)
	if cmd != ropacket.HeaderZCSELECTDEALTYPE {
		t.Fatalf("reply = 0x%04x, want 0x00c4 ZC_SELECT_DEALTYPE", cmd)
	}
	if got := binary.LittleEndian.Uint32(body[0:4]); got != shopGID {
		t.Fatalf("dealtype NpcID = %d, want %d", got, shopGID)
	}
}

// TestMap_WarpPortalTeleports proves the Phase-35 portal path: a corpus warp
// portal registered by the seeder, a player walking onto the trigger tile is
// relocated — the client receives ZC_NPCACK_MAPMOVE naming the destination
// map+cell, and the live world entity sits at the destination.
func TestMap_WarpPortalTeleports(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1,
	})
	ms, env := buildTestMapDeps(t, sessions)
	conn := startAndDial(t, ms, port)
	defer conn.Close()

	// A portal on the tile east of the player's spawn (53,111): trigger (54,111)
	// → destination (1,1) on "prontera".
	env.spawn.RegisterPortals([]script.WarpDef{{
		MapName: "new_1-1", X: 54, Y: 111, TriggerX: 54, TriggerY: 111,
		DestMap: "prontera", DestX: 1, DestY: 1,
	}})

	sendCZEnter(t, conn, 2000001, 150001, 0x11111111)
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, make([]byte, 13)); err != nil {
		t.Fatalf("drain accept-enter: %v", err)
	}

	// Walk one cell east onto the trigger tile.
	moveReq := make([]byte, 5)
	binary.LittleEndian.PutUint16(moveReq[0:], ropacket.HeaderCZREQUESTMOVE)
	dest := packMoveDest(54, 111)
	copy(moveReq[2:], dest[:])
	sendRaw(t, conn, moveReq)

	// Self move-ack (12B ZC_NOTIFY_PLAYERMOVE) then the portal's
	// ZC_NPCACK_MAPMOVE (22B) carrying the destination map+cell.
	readTradeFrame(t, conn, ropacket.HeaderZCNOTIFYPLAYERMOVE, 12)
	move := readTradeFrame(t, conn, ropacket.HeaderZCNPCACKMAPMOVE, 22)
	gotMap := string(bytes.TrimRight(move[2:18], "\x00"))
	if gotMap != "prontera" {
		t.Fatalf("portal dest map = %q, want prontera", gotMap)
	}
	if got := binary.LittleEndian.Uint16(move[18:20]); got != 1 {
		t.Fatalf("portal dest X = %d, want 1", got)
	}
	if got := binary.LittleEndian.Uint16(move[20:22]); got != 1 {
		t.Fatalf("portal dest Y = %d, want 1", got)
	}

	// The live entity relocated immediately (the client's re-enter will
	// back-fill from here).
	e, err := env.world.Get(150001)
	if err != nil {
		t.Fatalf("get player: %v", err)
	}
	if e.Map != "prontera" || e.Pos.X != 1 || e.Pos.Y != 1 {
		t.Fatalf("player at %s(%d,%d), want prontera(1,1)", e.Map, e.Pos.X, e.Pos.Y)
	}
}
