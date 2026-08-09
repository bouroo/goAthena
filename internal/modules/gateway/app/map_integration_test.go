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
	"github.com/bouroo/goAthena/pkg/ro/skilldb"
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
		Name: "Hero", HP: 1000, MaxHP: 1000, SP: 100, MaxSP: 100, Speed: 150,
	})
	world := worldapp.NewWorldService(wrepo, slog.Default(), 50)
	spawn := worldapp.NewSpawnService(world, nil, nil)
	combat := worldapp.NewCombatService(world, nil)
	inv := invapp.NewInventoryService(invinfra.NewMemoryItemRepository())
	content := contentapp.NewEngine(nil, nil, nil, slog.Default()) // no scripts/npcs in test; StartDialog early-returns
	skills := worldapp.NewSkillService(world, combat, testSkillDB())
	ms, err := gwapp.NewMapServer(world, spawn, combat, inv, content, skills, sessions, slog.Default())
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

// testSkillDB seeds a one-skill registry for integration tests: a melee-range
// offensive skill (TargetType "Enemy") so a cast routes through the real combat
// path. Skill id 5 mirrors rAthena SM_BASH. SP cost 8 is affordable against the
// seeded player's SP=100.
func testSkillDB() *skilldb.Registry {
	reg := skilldb.NewRegistry()
	reg.Register(&skilldb.SkillEntry{
		ID:         5,
		Name:       "SM_BASH",
		MaxLevel:   10,
		TargetType: "Enemy",
		Range:      skilldb.Range{IsScalar: true, Value: 1},
		Requires:   skilldb.Requires{SpCost: skilldb.SpCost{IsScalar: true, Value: 8}},
	})
	return reg
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

// TestMap_Dispatch_CastAttackSkill exercises CZ_USE_SKILL2 (0x0438 @ 20250604):
// the player casts an enemy-targeted attack skill onto a mob. The cast is
// validated (skill known, level/range/SP), SP is spent, and the resolved
// melee-equivalent damage is broadcast as ZC_NOTIFY_SKILL (0x01de). The damage
// is reusing the proven combat path; full skill-damage modeling is future work.
func TestMap_Dispatch_CastAttackSkill(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1,
	})
	conn, spawn := startMapListenerWithSpawn(t, port, sessions)
	defer conn.Close()

	sendCZEnter(t, conn, 2000001, 150001, 0x11111111)
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, make([]byte, 13)); err != nil { // drain accept-enter
		t.Fatalf("drain accept-enter: %v", err)
	}

	// Spawn a mob on the player's tile (Chebyshev distance 0 <= skill range 1).
	const mobGID = 160010
	if err := spawn.SpawnMob(mobGID, 1002, "new_1-1", worlddomain.Position{X: 53, Y: 111}, "Poring", 50, 50, 0); err != nil {
		t.Fatalf("spawn mob: %v", err)
	}

	// CZ_USE_SKILL2 (0x0438, 10B): cmd + int16 skillLv + uint16 skillID + uint32 targetID.
	const skillID = 5
	req := make([]byte, 10)
	binary.LittleEndian.PutUint16(req[0:], ropacket.HeaderCZUSESKILL)
	binary.LittleEndian.PutUint16(req[2:], 1) // skillLv
	binary.LittleEndian.PutUint16(req[4:], skillID)
	binary.LittleEndian.PutUint32(req[6:], mobGID)
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("send CZ_USE_SKILL2: %v", err)
	}

	// ZC_NOTIFY_SKILL (0x01de, 33B) is the next frame on a successful cast.
	resp := make([]byte, 33)
	if _, err := io.ReadFull(conn, resp); err != nil {
		t.Fatalf("read ZC_NOTIFY_SKILL: %v", err)
	}
	if got := binary.LittleEndian.Uint16(resp[0:2]); got != ropacket.HeaderZCNOTIFYSKILL {
		t.Fatalf("notify-skill header = 0x%04x, want ZC_NOTIFY_SKILL (0x%04x)", got, ropacket.HeaderZCNOTIFYSKILL)
	}
	if got := binary.LittleEndian.Uint16(resp[2:4]); got != skillID {
		t.Fatalf("notify-skill SKID = %d, want %d", got, skillID)
	}
	if got := binary.LittleEndian.Uint32(resp[4:8]); got != 150001 {
		t.Fatalf("notify-skill AID = %d, want caster GID 150001", got)
	}
	if got := binary.LittleEndian.Uint32(resp[8:12]); got != mobGID {
		t.Fatalf("notify-skill TargetID = %d, want mob GID %d", got, mobGID)
	}
	if got := int16(binary.LittleEndian.Uint16(resp[28:30])); got != 1 {
		t.Fatalf("notify-skill Level = %d, want 1", got)
	}
	if dmg := int32(binary.LittleEndian.Uint32(resp[24:28])); dmg <= 0 {
		t.Fatalf("notify-skill Damage = %d, want > 0 (real combat hit)", dmg)
	}
}

// TestMap_Dispatch_CastGroundSkill exercises CZ_USE_SKILL_TOPOS (0x0AF4 @
// 20250604): the player casts a ground-target skill onto a tile. The handler
// must emit ZC_NOTIFY_GROUNDSKILL (0x0117) placing the cast visual on the tile
// and keep the connection alive (a follow-up CZ_REQUEST_MOVE still gets its
// ZC_NOTIFY_PLAYERMOVE reply). With no mob on the cast tile the single-target
// damage path finds nothing to hit — honest: full ground-AoE damage is kernel
// future work — so the only expected response is the 18-byte visual frame.
func TestMap_Dispatch_CastGroundSkill(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1,
	})
	conn, _ := startMapListenerWithSpawn(t, port, sessions)
	defer conn.Close()

	sendCZEnter(t, conn, 2000001, 150001, 0x11111111)
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, make([]byte, 13)); err != nil { // drain accept-enter
		t.Fatalf("drain accept-enter: %v", err)
	}

	// CZ_USE_SKILL_TOPOS (0x0AF4, 11B): cmd + int16 skillLv + uint16 skillID +
	// uint16 xPos + uint16 yPos + uint8 moreinfo (server-ignored).
	const (
		skillID = 5
		castX   = 60
		castY   = 110
	)
	req := make([]byte, 11)
	binary.LittleEndian.PutUint16(req[0:], ropacket.HeaderCZUSESKILLTOPOS)
	binary.LittleEndian.PutUint16(req[2:], 1) // skillLv
	binary.LittleEndian.PutUint16(req[4:], skillID)
	binary.LittleEndian.PutUint16(req[6:], castX) // xPos
	binary.LittleEndian.PutUint16(req[8:], castY) // yPos
	req[10] = 0                                   // moreinfo
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("send CZ_USE_SKILL_TOPOS: %v", err)
	}

	// ZC_NOTIFY_GROUNDSKILL (0x0117, 18B): the cast visual placement on the tile.
	resp := make([]byte, 18)
	if _, err := io.ReadFull(conn, resp); err != nil {
		t.Fatalf("read ZC_NOTIFY_GROUNDSKILL: %v (handler did not emit the cast visual, or closed the conn)", err)
	}
	if got := binary.LittleEndian.Uint16(resp[0:2]); got != ropacket.HeaderZCNOTIFYGROUNDSKILL {
		t.Fatalf("ground-skill header = 0x%04x, want ZC_NOTIFY_GROUNDSKILL (0x%04x)", got, ropacket.HeaderZCNOTIFYGROUNDSKILL)
	}
	if got := binary.LittleEndian.Uint16(resp[2:4]); got != skillID {
		t.Fatalf("ground-skill SKID = %d, want %d", got, skillID)
	}
	if got := binary.LittleEndian.Uint32(resp[4:8]); got != 150001 {
		t.Fatalf("ground-skill AID = %d, want caster GID 150001", got)
	}
	if got := binary.LittleEndian.Uint16(resp[10:12]); got != castX {
		t.Fatalf("ground-skill XPos = %d, want %d", got, castX)
	}
	if got := binary.LittleEndian.Uint16(resp[12:14]); got != castY {
		t.Fatalf("ground-skill YPos = %d, want %d", got, castY)
	}

	// Conn stays alive: a follow-up CZ_REQUEST_MOVE still reaches the dispatcher
	// and gets its ZC_NOTIFY_PLAYERMOVE reply, proving the handler did not close
	// or desync the connection.
	moveReq := make([]byte, 5)
	binary.LittleEndian.PutUint16(moveReq[0:], ropacket.HeaderCZREQUESTMOVE)
	moveReq[2] = 55  // dest x
	moveReq[3] = 111 // dest y
	moveReq[4] = 0   // dest dir
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write(moveReq); err != nil {
		t.Fatalf("send CZ_REQUEST_MOVE after ground-skill: %v", err)
	}
	moveReply := make([]byte, 12)
	if _, err := io.ReadFull(conn, moveReply); err != nil {
		t.Fatalf("read player-move reply after ground-skill: %v (conn closed by handler?)", err)
	}
	if got := binary.LittleEndian.Uint16(moveReply[0:2]); got != ropacket.HeaderZCNOTIFYPLAYERMOVE {
		t.Fatalf("player-move header = 0x%04x, want ZC_NOTIFY_PLAYERMOVE (0x%04x)", got, ropacket.HeaderZCNOTIFYPLAYERMOVE)
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

// TestMap_Dispatch_InputEditDlgStrVariableFrameKeepsConnection drives the
// variable-length CZ_INPUT_EDITDLGSTR (0x01d5) handler over a real socket. With
// no NPC dialog active, Engine.Signal is a no-op, so the handler parses and
// returns without a wire reply — but the dispatcher must consume the WHOLE
// variable frame (its on-wire length at offset 2), not a fixed prefix. A
// following CZ_REQUEST_MOVE proves stream alignment: had 0x01d5 been read with
// the wrong length, its trailing value bytes would be parsed as the next opcode
// and the move reply read would fail or return the wrong header.
func TestMap_Dispatch_InputEditDlgStrVariableFrameKeepsConnection(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, Sex: 1,
	})
	conn, _ := startMapListenerWithSpawn(t, port, sessions)
	defer conn.Close()

	// 1. CZ_ENTER → drain the 13-byte ZC_ACCEPT_ENTER.
	sendCZEnter(t, conn, 2000001, 150001, 0x11111111)
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, make([]byte, 13)); err != nil {
		t.Fatalf("drain accept-enter: %v", err)
	}

	// 2. CZ_INPUT_EDITDLGSTR (0x01d5): int16 cmd | uint16 pktLen | uint32 NpcID |
	//    char[] value+NUL. A multi-byte value makes the frame longer than the
	//    8-byte minimum so the length-prefix path is actually exercised.
	const value = "goAthena"
	pktLen := 8 + len(value) + 1 // header(8) + value + NUL
	strReq := make([]byte, pktLen)
	binary.LittleEndian.PutUint16(strReq[0:], ropacket.HeaderCZINPUTEDITDLGSTR)
	binary.LittleEndian.PutUint16(strReq[2:], uint16(pktLen)) //nolint:gosec // G115: bounded by pktLen (<256).
	binary.LittleEndian.PutUint32(strReq[4:], 0)              // NpcID: no dialog active
	copy(strReq[8:], value)
	strReq[pktLen-1] = 0 // NUL terminator
	if _, err := conn.Write(strReq); err != nil {
		t.Fatalf("send CZ_INPUT_EDITDLGSTR: %v", err)
	}

	// 3. CZ_REQUEST_MOVE on the same connection. If the variable-length frame had
	//    been read with the wrong length, its trailing bytes would be parsed as
	//    this opcode and the reply read would fail or mismatch.
	moveReq := make([]byte, 5)
	binary.LittleEndian.PutUint16(moveReq[0:], ropacket.HeaderCZREQUESTMOVE)
	moveReq[2] = 100 // dest x
	moveReq[3] = 200 // dest y
	moveReq[4] = 0   // dest dir
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write(moveReq); err != nil {
		t.Fatalf("send CZ_REQUEST_MOVE after inputstr: %v", err)
	}

	moveReply := make([]byte, 12)
	if _, err := io.ReadFull(conn, moveReply); err != nil {
		t.Fatalf("read player-move reply after inputstr: %v (conn likely closed / stream desynced)", err)
	}
	if got := binary.LittleEndian.Uint16(moveReply[0:2]); got != ropacket.HeaderZCNOTIFYPLAYERMOVE {
		t.Fatalf("player-move header = 0x%04x, want ZC_NOTIFY_PLAYERMOVE (0x%04x) (variable frame mis-lengthed?)", got, ropacket.HeaderZCNOTIFYPLAYERMOVE)
	}
}
