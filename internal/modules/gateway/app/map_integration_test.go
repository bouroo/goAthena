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

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	charinfra "github.com/bouroo/goAthena/internal/modules/character/infra"
	contentapp "github.com/bouroo/goAthena/internal/modules/content/app"
	gwapp "github.com/bouroo/goAthena/internal/modules/gateway/app"
	invapp "github.com/bouroo/goAthena/internal/modules/inventory/app"
	invinfra "github.com/bouroo/goAthena/internal/modules/inventory/infra"
	worldapp "github.com/bouroo/goAthena/internal/modules/world/app"
	worlddomain "github.com/bouroo/goAthena/internal/modules/world/domain"
	worldinfra "github.com/bouroo/goAthena/internal/modules/world/infra"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
)

// startMapListener spins a real MapServer (gnet reactor) on a free port,
// backed by a memory world repo + session store.
// startMapListenerWithSpawn is like startMapListener but also returns the
// SpawnService so a test can pre-seed playable preconditions (spawn a mob to
// attack, drop a floor item to pick up) before driving the client packets.
func startMapListenerWithSpawn(t *testing.T, port int, sessions *charinfra.MemorySessionStore) (net.Conn, *worldapp.SpawnService) {
	t.Helper()
	wrepo := worldinfra.NewMemoryWorldRepository(worlddomain.Entity{
		ID: 150001, Account: 2000001, Map: "new_1-1",
		Pos: worlddomain.Position{X: 53, Y: 111}, Sex: 1, Job: 0, Level: 1,
		Name: "Hero", HP: 1000, MaxHP: 1000, Speed: 150,
	})
	world := worldapp.NewWorldService(wrepo, slog.Default(), 50)
	spawn := worldapp.NewSpawnService(world, nil, nil)
	combat := worldapp.NewCombatService(world, nil)
	inv := invapp.NewInventoryService(invinfra.NewMemoryItemRepository())
	content := contentapp.NewEngine(nil, nil, nil, slog.Default()) // no scripts/npcs in test; StartDialog early-returns
	ms, err := gwapp.NewMapServer(world, spawn, combat, inv, content, sessions, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	ms.Start("tcp://127.0.0.1:" + strconv.Itoa(port))
	t.Cleanup(ms.Stop)

	var conn net.Conn
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c, derr := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 500*time.Millisecond); derr == nil {
			conn = c
			break
		}
	}
	if conn == nil {
		t.Fatal("map listener never accepted")
	}
	return conn, spawn
}

// startMapListener is the thin wrapper for tests that do not need the spawn
// service (the original playable path: enter + move).
func startMapListener(t *testing.T, port int, sessions *charinfra.MemorySessionStore) net.Conn {
	conn, _ := startMapListenerWithSpawn(t, port, sessions)
	return conn
}

// sendCZEnter builds and sends a 19-byte CZ_ENTER (0x0072) frame.
func sendCZEnter(t *testing.T, c net.Conn, aid, gid, authCode uint32) {
	t.Helper()
	buf := make([]byte, 19)
	binary.LittleEndian.PutUint16(buf[0:], ropacket.HeaderCZENTER)
	binary.LittleEndian.PutUint32(buf[2:], aid)
	binary.LittleEndian.PutUint32(buf[6:], gid)
	binary.LittleEndian.PutUint32(buf[10:], authCode)
	binary.LittleEndian.PutUint32(buf[14:], 0) // clientTime
	buf[18] = 1                                // sex
	c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write(buf); err != nil {
		t.Fatalf("send CZ_ENTER: %v", err)
	}
}

func TestMap_AcceptEnterOverTCP(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1,
	})
	conn := startMapListener(t, port, sessions)
	defer conn.Close()

	sendCZEnter(t, conn, 2000001, 150001, 0x11111111)

	conn.SetDeadline(time.Now().Add(3 * time.Second))
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		t.Fatalf("read reply header: %v", err)
	}
	got := binary.LittleEndian.Uint16(hdr)
	if got != ropacket.HeaderZCACCEPTENTER {
		t.Fatalf("response header = 0x%04x, want ZC_ACCEPT_ENTER (0x%04x)", got, ropacket.HeaderZCACCEPTENTER)
	}
}

func TestMap_RefuseEnterBadAuthOverTCP(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0xAAAA, Sex: 1,
	})
	conn := startMapListener(t, port, sessions)
	defer conn.Close()

	// Client sends wrong authCode (0xBBBB vs stored 0xAAAA) → refuse.
	sendCZEnter(t, conn, 2000001, 150001, 0xBBBB)

	conn.SetDeadline(time.Now().Add(3 * time.Second))
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		t.Fatalf("read reply header: %v", err)
	}
	got := binary.LittleEndian.Uint16(hdr)
	if got != ropacket.HeaderZCREFUSEENTER {
		t.Fatalf("response header = 0x%04x, want ZC_REFUSE_ENTER (0x%04x)", got, ropacket.HeaderZCREFUSEENTER)
	}
}

func TestMap_RefuseEnterNoSessionOverTCP(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	conn := startMapListener(t, port, sessions)
	defer conn.Close()

	// No session stored for this AID → refuse.
	sendCZEnter(t, conn, 9999999, 150001, 0x11111111)

	conn.SetDeadline(time.Now().Add(3 * time.Second))
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(conn, hdr); err != nil {
		t.Fatalf("read reply header: %v", err)
	}
	got := binary.LittleEndian.Uint16(hdr)
	if got != ropacket.HeaderZCREFUSEENTER {
		t.Fatalf("response header = 0x%04x, want ZC_REFUSE_ENTER (0x%04x)", got, ropacket.HeaderZCREFUSEENTER)
	}
}

// TestMap_Dispatch_MovementAfterEnter exercises the table-driven dispatch: a
// single connection sends CZ_ENTER (admitted), then CZ_REQUEST_MOVE, and the
// dispatcher routes each to the right handler and replies.
func TestMap_Dispatch_MovementAfterEnter(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, Sex: 1,
	})
	conn := startMapListener(t, port, sessions)
	defer conn.Close()

	// 1. CZ_ENTER → ZC_ACCEPT_ENTER (drain the 13-byte response).
	sendCZEnter(t, conn, 2000001, 150001, 0x11111111)
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	enterReply := make([]byte, 13)
	if _, err := io.ReadFull(conn, enterReply); err != nil {
		t.Fatalf("read accept-enter: %v", err)
	}
	if got := binary.LittleEndian.Uint16(enterReply[0:2]); got != ropacket.HeaderZCACCEPTENTER {
		t.Fatalf("accept-enter header = 0x%04x, want 0x%04x", got, ropacket.HeaderZCACCEPTENTER)
	}

	// 2. CZ_REQUEST_MOVE → ZC_NOTIFY_PLAYERMOVE (12 bytes).
	moveReq := make([]byte, 5)
	binary.LittleEndian.PutUint16(moveReq[0:], ropacket.HeaderCZREQUESTMOVE)
	moveReq[2] = 100 // packed dest X (low byte; decodePos handles the 3-byte packing)
	moveReq[3] = 200 // packed dest Y
	moveReq[4] = 0   // dir
	if _, err := conn.Write(moveReq); err != nil {
		t.Fatalf("send move: %v", err)
	}
	moveReply := make([]byte, 12)
	if _, err := io.ReadFull(conn, moveReply); err != nil {
		t.Fatalf("read player-move reply: %v", err)
	}
	if got := binary.LittleEndian.Uint16(moveReply[0:2]); got != ropacket.HeaderZCNOTIFYPLAYERMOVE {
		t.Fatalf("player-move header = 0x%04x, want ZC_NOTIFY_PLAYERMOVE (0x%04x)", got, ropacket.HeaderZCNOTIFYPLAYERMOVE)
	}
}

// TestMap_Dispatch_SitStandActionEcho exercises CZ_ACTION_REQUEST (0x0089) on the
// non-combat branch: sit/stand echoes a ZC_ACTION_RESPONSE (0x008b) without
// touching CombatService. Covers the self-action slice of the playable surface.
func TestMap_Dispatch_SitStandActionEcho(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1,
	})
	conn, _ := startMapListenerWithSpawn(t, port, sessions)
	defer conn.Close()

	// CZ_ENTER → ZC_ACCEPT_ENTER (drain the 13-byte accept-enter).
	sendCZEnter(t, conn, 2000001, 150001, 0x11111111)
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, make([]byte, 13)); err != nil {
		t.Fatalf("drain accept-enter: %v", err)
	}

	// CZ_ACTION_REQUEST action=2 (sit), self-targeted at the player's own GID.
	const sitAction byte = 2
	req := make([]byte, 7)
	binary.LittleEndian.PutUint16(req[0:], ropacket.HeaderCZACTIONREQUEST)
	binary.LittleEndian.PutUint32(req[2:], 150001) // own GID
	req[6] = sitAction
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("send CZ_ACTION_REQUEST: %v", err)
	}

	resp := make([]byte, 11) // sizeZCActionResponse
	if _, err := io.ReadFull(conn, resp); err != nil {
		t.Fatalf("read ZC_ACTION_RESPONSE: %v", err)
	}
	if got := binary.LittleEndian.Uint16(resp[0:2]); got != ropacket.HeaderZCACTIONRESPONSE {
		t.Fatalf("action-response header = 0x%04x, want ZC_ACTION_RESPONSE (0x%04x)", got, ropacket.HeaderZCACTIONRESPONSE)
	}
	if got := binary.LittleEndian.Uint32(resp[2:6]); got != 150001 {
		t.Fatalf("action-response GID = %d, want own GID 150001", got)
	}
	if resp[6] != sitAction {
		t.Fatalf("action-response action = %d, want %d", resp[6], sitAction)
	}
}

// TestMap_Dispatch_AttackMob exercises the combat slice of the playable surface:
// CZ_ACTION_REQUEST action=0x07 resolves melee damage against a spawned mob via
// CombatService and echoes ZC_ACTION_RESPONSE with the mob's GID as target.
func TestMap_Dispatch_AttackMob(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1,
	})
	conn, spawn := startMapListenerWithSpawn(t, port, sessions)
	defer conn.Close()

	sendCZEnter(t, conn, 2000001, 150001, 0x11111111)
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, make([]byte, 13)); err != nil {
		t.Fatalf("drain accept-enter: %v", err)
	}

	// Spawn a mob in the same map; its GID is the attack target.
	const mobGID = 160000
	if err := spawn.SpawnMob(mobGID, 1002, "new_1-1", worlddomain.Position{X: 53, Y: 111}, "Poring", 50, 50, 0); err != nil {
		t.Fatalf("spawn mob: %v", err)
	}

	// CZ_ACTION_REQUEST action=0x07 (attack) targeting the mob.
	req := make([]byte, 7)
	binary.LittleEndian.PutUint16(req[0:], ropacket.HeaderCZACTIONREQUEST)
	binary.LittleEndian.PutUint32(req[2:], mobGID)
	req[6] = 0x07
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("send CZ_ACTION_REQUEST: %v", err)
	}

	resp := make([]byte, 11)
	if _, err := io.ReadFull(conn, resp); err != nil {
		t.Fatalf("read ZC_ACTION_RESPONSE: %v", err)
	}
	if got := binary.LittleEndian.Uint16(resp[0:2]); got != ropacket.HeaderZCACTIONRESPONSE {
		t.Fatalf("action-response header = 0x%04x, want ZC_ACTION_RESPONSE (0x%04x)", got, ropacket.HeaderZCACTIONRESPONSE)
	}
	if got := binary.LittleEndian.Uint32(resp[7:11]); got != mobGID {
		t.Fatalf("action-response targetGID = %d, want mob GID %d", got, mobGID)
	}
}

// TestMap_Dispatch_PickupFloorItem exercises the loot slice of the playable
// surface: CZ_ITEM_PICKUP (0x0362 @ 20250604) resolves a dropped floor item,
// moves it into the player's inventory, and replies ZC_ITEM_PICKUP_ACK (0x0b41).
func TestMap_Dispatch_PickupFloorItem(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1,
	})
	conn, spawn := startMapListenerWithSpawn(t, port, sessions)
	defer conn.Close()

	sendCZEnter(t, conn, 2000001, 150001, 0x11111111)
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, make([]byte, 13)); err != nil {
		t.Fatalf("drain accept-enter: %v", err)
	}

	// Drop a floor item; the returned GroundID is what the pickup targets.
	fi := spawn.DropItem(512, 1, "new_1-1", worlddomain.Position{X: 53, Y: 111}, 0)

	// CZ_ITEM_PICKUP (0x0362, 6B), targeting the dropped GroundID.
	req := make([]byte, 6)
	binary.LittleEndian.PutUint16(req[0:], ropacket.HeaderCZITEMTAKE0362)
	binary.LittleEndian.PutUint32(req[2:], fi.GroundID)
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("send CZ_ITEM_PICKUP: %v", err)
	}

	ack := make([]byte, 70) // sizeZCItemPickupAck
	if _, err := io.ReadFull(conn, ack); err != nil {
		t.Fatalf("read ZC_ITEM_PICKUP_ACK: %v", err)
	}
	if got := binary.LittleEndian.Uint16(ack[0:2]); got != ropacket.HeaderZCItemPickupAck {
		t.Fatalf("pickup-ack header = 0x%04x, want ZC_ITEM_PICKUP_ACK (0x%04x)", got, ropacket.HeaderZCItemPickupAck)
	}
}

// TestMap_Dispatch_DropItem exercises CZ_ITEM_DROP (0x0363 @ 20250604): the
// player drops an inventory item, which removes it from the bag and places a
// floor item on the ground. The bag is seeded through the already-wired pickup
// path (drop a floor item server-side, CZ_ITEM_PICKUP it into inventory) so the
// test needs no private access to the MapServer's InventoryService. After the
// drop the handler replies ZC_ITEM_THROW_ACK (SELF) + ZC_ITEM_FALL_ENTRY, and
// the dropped item is observable on the floor via SpawnService.FloorItems.
func TestMap_Dispatch_DropItem(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1,
	})
	conn, spawn := startMapListenerWithSpawn(t, port, sessions)
	defer conn.Close()

	sendCZEnter(t, conn, 2000001, 150001, 0x11111111)
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, make([]byte, 13)); err != nil {
		t.Fatalf("drain accept-enter: %v", err)
	}

	// Seed the bag via the pickup path: drop a floor item server-side, then
	// CZ_ITEM_PICKUP it into the player's inventory (slot 1).
	seed := spawn.DropItem(512, 3, "new_1-1", worlddomain.Position{X: 53, Y: 111}, 0)
	pickup := make([]byte, 6)
	binary.LittleEndian.PutUint16(pickup[0:], ropacket.HeaderCZITEMTAKE0362)
	binary.LittleEndian.PutUint32(pickup[2:], seed.GroundID)
	if _, err := conn.Write(pickup); err != nil {
		t.Fatalf("send seed CZ_ITEM_PICKUP: %v", err)
	}
	if _, err := io.ReadFull(conn, make([]byte, 70)); err != nil { // sizeZCItemPickupAck
		t.Fatalf("read seed pickup-ack: %v", err)
	}

	// CZ_ITEM_DROP (0x0363, 6B): drop 1 unit from inventory slot 1.
	dropReq := make([]byte, 6)
	binary.LittleEndian.PutUint16(dropReq[0:], ropacket.HeaderCZDROPITEM0363)
	binary.LittleEndian.PutUint16(dropReq[2:], 1) // inventory index (1-based)
	binary.LittleEndian.PutUint16(dropReq[4:], 1) // amount
	if _, err := conn.Write(dropReq); err != nil {
		t.Fatalf("send CZ_ITEM_DROP: %v", err)
	}

	// Expect ZC_ITEM_THROW_ACK (6B, SELF) + ZC_ITEM_FALL_ENTRY (24B).
	throwAck := make([]byte, 6) // sizeZCItemThrowAck
	if _, err := io.ReadFull(conn, throwAck); err != nil {
		t.Fatalf("read ZC_ITEM_THROW_ACK: %v", err)
	}
	if got := binary.LittleEndian.Uint16(throwAck[0:2]); got != ropacket.HeaderZCItemThrowAck {
		t.Fatalf("throw-ack header = 0x%04x, want ZC_ITEM_THROW_ACK (0x%04x)", got, ropacket.HeaderZCItemThrowAck)
	}
	if got := binary.LittleEndian.Uint16(throwAck[2:4]); got != 1 {
		t.Fatalf("throw-ack index = %d, want 1", got)
	}
	if got := binary.LittleEndian.Uint16(throwAck[4:6]); got != 1 {
		t.Fatalf("throw-ack count = %d, want 1", got)
	}
	fallEntry := make([]byte, 24) // sizeZCItemFallEntry
	if _, err := io.ReadFull(conn, fallEntry); err != nil {
		t.Fatalf("read ZC_ITEM_FALL_ENTRY: %v", err)
	}
	if got := binary.LittleEndian.Uint16(fallEntry[0:2]); got != ropacket.HeaderZCItemFallEntry {
		t.Fatalf("fall-entry header = 0x%04x, want ZC_ITEM_FALL_ENTRY (0x%04x)", got, ropacket.HeaderZCItemFallEntry)
	}
	if got := binary.LittleEndian.Uint32(fallEntry[6:10]); got != 512 {
		t.Fatalf("fall-entry nameID = %d, want 512", got)
	}

	// Observable proof: the dropped item is now on the floor.
	var found bool
	for _, fi := range spawn.FloorItems("new_1-1") {
		if fi.NameID == 512 {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("dropped item 512 not on floor")
	}
}

// TestMap_Dispatch_DropOutOfRangeKeepsConnection proves a CZ_ITEM_DROP
// (0x0363 @ 20250604) against an inventory slot the player does not own must not
// close the connection. The handler resolves the 1-based index, finds it out of
// range (no items seeded), logs, and returns without replying; a following
// CZ_REQUEST_MOVE still elicits its ZC_NOTIFY_PLAYERMOVE reply on the same socket.
func TestMap_Dispatch_DropOutOfRangeKeepsConnection(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, Sex: 1,
	})
	conn, _ := startMapListenerWithSpawn(t, port, sessions)
	defer conn.Close()

	// 1. CZ_ENTER → ZC_ACCEPT_ENTER (drain the 13-byte response).
	sendCZEnter(t, conn, 2000001, 150001, 0x11111111)
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, make([]byte, 13)); err != nil {
		t.Fatalf("drain accept-enter: %v", err)
	}

	// 2. CZ_ITEM_DROP (0x0363, 6B) against slot 1, which the player does not own
	//    (no items seeded) → handler logs + returns; no reply, connection kept.
	dropReq := make([]byte, 6)
	binary.LittleEndian.PutUint16(dropReq[0:], ropacket.HeaderCZDROPITEM0363)
	binary.LittleEndian.PutUint16(dropReq[2:], 1) // inventory index
	binary.LittleEndian.PutUint16(dropReq[4:], 1) // amount
	if _, err := conn.Write(dropReq); err != nil {
		t.Fatalf("send CZ_ITEM_DROP: %v", err)
	}

	// 3. CZ_REQUEST_MOVE on the same connection. If the rejected drop had closed
	//    the socket, this write or the reply read fails with EOF.
	moveReq := make([]byte, 5)
	binary.LittleEndian.PutUint16(moveReq[0:], ropacket.HeaderCZREQUESTMOVE)
	moveReq[2] = 100 // dest x
	moveReq[3] = 200 // dest y
	moveReq[4] = 0   // dest dir
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write(moveReq); err != nil {
		t.Fatalf("send CZ_REQUEST_MOVE after rejected drop: %v", err)
	}

	moveReply := make([]byte, 12)
	if _, err := io.ReadFull(conn, moveReply); err != nil {
		t.Fatalf("read player-move reply after rejected drop: %v (conn likely closed)", err)
	}
	if got := binary.LittleEndian.Uint16(moveReply[0:2]); got != ropacket.HeaderZCNOTIFYPLAYERMOVE {
		t.Fatalf("player-move header = 0x%04x, want ZC_NOTIFY_PLAYERMOVE (0x%04x)", got, ropacket.HeaderZCNOTIFYPLAYERMOVE)
	}
}
