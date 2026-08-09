//go:build integration

package app_test

import (
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	charinfra "github.com/bouroo/goAthena/internal/modules/character/infra"
	shop "github.com/bouroo/goAthena/internal/modules/commerce/shop"
	shopapp "github.com/bouroo/goAthena/internal/modules/commerce/shop/app"
	shopdomain "github.com/bouroo/goAthena/internal/modules/commerce/shop/domain"
	contentapp "github.com/bouroo/goAthena/internal/modules/content/app"
	contentinfra "github.com/bouroo/goAthena/internal/modules/content/infra"
	economyapp "github.com/bouroo/goAthena/internal/modules/economy/app"
	gwapp "github.com/bouroo/goAthena/internal/modules/gateway/app"
	invapp "github.com/bouroo/goAthena/internal/modules/inventory/app"
	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	invinfra "github.com/bouroo/goAthena/internal/modules/inventory/infra"
	worldapp "github.com/bouroo/goAthena/internal/modules/world/app"
	worlddomain "github.com/bouroo/goAthena/internal/modules/world/domain"
	worldinfra "github.com/bouroo/goAthena/internal/modules/world/infra"
	"github.com/bouroo/goAthena/pkg/ro/equip"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
	"github.com/bouroo/goAthena/pkg/ro/mobdb"
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
	ms, env := buildTestMapDeps(t, sessions)
	conn := startAndDial(t, ms, port)
	return conn, env.spawn
}

// startMapListenerWithShopEnv is like startMapListenerWithSpawn but returns the
// full env (character repo + shared item repo) so the shop test can seed a
// character's zeny before a buy and assert the item add + zeny deduction after.
func startMapListenerWithShopEnv(t *testing.T, port int, sessions *charinfra.MemorySessionStore) (net.Conn, mapTestEnv) {
	t.Helper()
	ms, env := buildTestMapDeps(t, sessions)
	conn := startAndDial(t, ms, port)
	return conn, env
}

// mapTestEnv bundles the wired deps a shop test can seed/assert against.
type mapTestEnv struct {
	spawn    *worldapp.SpawnService
	charRepo *charinfra.MemoryCharacterRepository
	itemRepo *invinfra.MemoryItemRepository
	world    *worldapp.WorldService
	mobAI    *worldapp.MobAIService
}

// testMobAIFixture is a one-mob mob_db.yml with a single aggressive monster
// (Ai=4 ≥ aggressive threshold 3). It backs the gateway's combat + mob AI so a
// spawned monster resolves real stats and the aggro loop swings at the player.
// WalkSpeed (400 ms/cell) makes it chasable: a target within ChaseRange but
// outside AttackRange is pursued one cell per WalkSpeed of accumulated tick time.
const testMobAIFixture = `Header:
  Type: MOB_DB
  Version: 5
Body:
  - Id: 8001
    Name: AggroMob
    Ai: 04
    Level: 10
    Attack: 50
    Attack2: 10
    Str: 20
    Dex: 20
    Defense: 0
    Vit: 0
    AttackRange: 2
    ChaseRange: 12
    WalkSpeed: 400
`

// buildTestMapDeps constructs the MapServer's collaborators against in-memory
// repos, wiring the shop commerce verb: a real EconomyService over a memory
// character repo (so buy/sell move real zeny), the dev "Tool Shop" catalog bound
// to its NPC GID, and the shared item repository the inventory and shop services
// both use. Returns the server + env.
func buildTestMapDeps(t *testing.T, sessions *charinfra.MemorySessionStore) (*gwapp.MapServer, mapTestEnv) {
	t.Helper()
	wrepo := worldinfra.NewMemoryWorldRepository(worlddomain.Entity{
		ID: 150001, Account: 2000001, Map: "new_1-1",
		Pos: worlddomain.Position{X: 53, Y: 111}, Sex: 1, Job: 0, Level: 1,
		Name: "Hero", HP: 1000, MaxHP: 1000, SP: 100, MaxSP: 100, Speed: 150,
		// Save point distinct from the enter cell so respawn relocate/reseat is
		// observable (died PC respawns here, not at its death cell).
		SaveMap: "new_1-1", SavePos: worlddomain.Position{X: 1, Y: 1},
	})
	world := worldapp.NewWorldService(wrepo, slog.Default(), 50)
	spawn := worldapp.NewSpawnService(world, nil, nil)
	// A tiny mob_db with one aggressive mob (8001, Ai=4) backs combat + mob AI so a
	// spawned monster resolves real stats and the aggro loop can swing at the
	// player. Existing tests spawn mob 1002 (not in this fixture), which mob_db
	// resolves to nil → 0 DEF, preserving their damage behaviour.
	mobs, err := mobdb.Load(strings.NewReader(testMobAIFixture))
	if err != nil {
		t.Fatalf("load mob_db: %v", err)
	}
	itemRepo := invinfra.NewMemoryItemRepository()
	inv := invapp.NewInventoryService(itemRepo)
	// A tiny item_db with one weapon (Knife 1201, ATK 50, right-hand) backs the
	// EquipService so combat picks up WeaponATK when the player equips it.
	items := testItemDB(t)
	equipSvc := worldapp.NewEquipService(inv, items)
	itemUse := worldapp.NewItemUseService(inv, items, world)
	combat := worldapp.NewCombatService(world, mobs, equipSvc)
	mobAI := worldapp.NewMobAIService(world, mobs, combat, slog.Default())
	content := contentapp.NewEngine(nil, nil, nil, slog.Default()) // no scripts/npcs in test; StartDialog early-returns
	skills := worldapp.NewSkillService(world, combat, testSkillDB())

	// Shop commerce: a real economy over a memory character repo + the dev shop
	// catalog, bound to its NPC GID so a CZ_ACK_SELECT_DEALTYPE on that GID opens it.
	charRepo := charinfra.NewMemoryCharacterRepository()
	shops := shopapp.NewShopService(devTestCatalog(), itemRepo,
		testEconPort{svc: economyapp.NewEconomyService(charRepo)})
	shopStore := contentinfra.NewMemoryShopStore()
	shopStore.RegisterShop(shop.DevShopGID, devTestShopName)

	ms, err := gwapp.NewMapServer(world, spawn, combat, mobAI, equipSvc, itemUse, inv, content, skills, shops, shopStore, nil, sessions, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	return ms, mapTestEnv{spawn: spawn, charRepo: charRepo, itemRepo: itemRepo, world: world, mobAI: mobAI}
}

// startAndDial starts the map listener on port and dials it, failing the test if
// it never accepts within the deadline. The server is cleaned up on test end.
func startAndDial(t *testing.T, ms *gwapp.MapServer, port int) net.Conn {
	t.Helper()
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
	return conn
}

// devTestShopName is the catalog name the test shop and its NPC-GID binding share.
const devTestShopName = "Tool Shop"

// devTestCatalog mirrors the dev seed (commerce/shop/seed.go): a "Tool Shop"
// selling a Red Potion (501) at 50z and a Knife (1201) at 25z with half-price
// sell-back. Duplicated here because the seed builder is unexported; kept in sync.
func devTestCatalog() *shopdomain.CatalogRegistry {
	return shopdomain.NewCatalogRegistry(shopdomain.Shop{
		Name: devTestShopName,
		Items: []shopdomain.ShopItem{
			{NameID: 501, Price: 50, SellPrice: 25},
			{NameID: 1201, Price: 25, SellPrice: 12},
		},
	})
}

// testEconPort adapts the real economy service to the shop EconomyPort so the
// integration test exercises real zeny movement (production di.go uses the same
// adapter shape; it is unexported there, so the test re-implements it).
type testEconPort struct{ svc *economyapp.EconomyService }

func (e testEconPort) DeductZeny(ctx context.Context, charID uint32, amount int32) error {
	return e.svc.DeductZeny(ctx, charID, amount)
}

func (e testEconPort) CreditZeny(ctx context.Context, charID uint32, amount int32) error {
	return e.svc.CreditZeny(ctx, charID, amount)
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

// testItemDB seeds a tiny item registry for integration tests: a Knife (id 1201,
// ATK 50) wearable in the right hand (Locations → equip.HandRight so the wear
// request's position validates), and a Red Potion (id 501, itemheal 45,0) so the
// usable-item verb resolves a healing item. The Knife's WeaponATK folds into
// combat once equipped; the Red Potion drives the use→heal path.
func testItemDB(t *testing.T) *itemdb.Registry {
	t.Helper()
	const yaml = "Header:\n  Type: ITEM_DB\n  Version: 3\nBody:\n" +
		"  - Id: 1201\n    AegisName: Knife\n    Name: Knife\n    Type: Weapon\n" +
		"    SubType: Dagger\n    Attack: 50\n    Locations:\n      Right_Hand: true\n" +
		"  - Id: 501\n    AegisName: Red_Potion\n    Name: Red Potion\n    Type: Healing\n" +
		"    Script: itemheal 45,0\n"
	reg, err := itemdb.Load(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("load test item_db: %v", err)
	}
	if e := reg.Get(1201); e == nil || e.Attack != 50 || e.EquipLocations != equip.HandRight {
		t.Fatalf("test item_db knife not resolved: %+v", e)
	}
	if e := reg.Get(501); e == nil {
		t.Fatalf("test item_db red potion not resolved")
	} else if hpMin, hpMax, spMin, spMax, ok := e.Heal(); !ok || hpMin != 45 || hpMax != 45 || spMin != 0 || spMax != 0 {
		t.Fatalf("test item_db red potion heal not parsed: hp=[%d,%d] sp=[%d,%d] ok=%v", hpMin, hpMax, spMin, spMax, ok)
	}
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

// TestMap_RegenEmitsParChange verifies the production regen wiring end-to-end
// through the real gnet reactor: RegenTick advances a living PC's HP and the
// NewMapServer-wired OnStatChange sink delivers ZC_PAR_CHANGE to that player's
// connection. RegenTick is driven directly (no real ticker) so the assertion is
// deterministic, not timing-fragile.
func TestMap_RegenEmitsParChange(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, Sex: 1,
	})
	ms, env := buildTestMapDeps(t, sessions)
	conn := startAndDial(t, ms, port)
	defer conn.Close()

	// 1. CZ_ENTER -> ZC_ACCEPT_ENTER; draining the 13-byte reply guarantees the
	//    reactor has registered the conn for charID 150001 before we proceed.
	sendCZEnter(t, conn, 2000001, 150001, 0x11111111)
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	enterReply := make([]byte, 13)
	if _, err := io.ReadFull(conn, enterReply); err != nil {
		t.Fatalf("read accept-enter: %v", err)
	}
	if got := binary.LittleEndian.Uint16(enterReply[0:2]); got != ropacket.HeaderZCACCEPTENTER {
		t.Fatalf("accept-enter header = 0x%04x, want 0x%04x", got, ropacket.HeaderZCACCEPTENTER)
	}

	// 2. Damage the PC (entered at full HP) by re-seeding it in the world; the
	//    conn stays registered (RemoveEntity does not touch ms.conns).
	if err := env.world.RemoveEntity(150001); err != nil {
		t.Fatalf("RemoveEntity: %v", err)
	}
	// MaxHP 1000, Vit 0 -> HP regen = 5 + 0 + 1 = 6 per 6 s interval.
	if err := env.world.AddEntity(worlddomain.Entity{
		ID: 150001, Type: worlddomain.EntityTypePC, Map: "new_1-1",
		Pos: worlddomain.Position{X: 53, Y: 111}, HP: 500, MaxHP: 1000, Vit: 0,
		SP: 40, MaxSP: 100, Int: 0, Speed: 150,
	}); err != nil {
		t.Fatalf("AddEntity: %v", err)
	}

	// 3. Advance one HP interval (6 s). HP 500 -> 506; SP not yet due (6 s < 8 s),
	//    so it is echoed unchanged. Expect two 8-byte ZC_PAR_CHANGE frames.
	env.world.RegenTick(6 * time.Second)
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	frames := make([]byte, 16) // two ParChangeResponse (8 bytes each)
	if _, err := io.ReadFull(conn, frames); err != nil {
		t.Fatalf("read regen frames: %v", err)
	}
	if got := binary.LittleEndian.Uint16(frames[0:2]); got != ropacket.HeaderZCPARCHANGE {
		t.Fatalf("frame 0 header = 0x%04x, want ZC_PAR_CHANGE", got)
	}
	if got := binary.LittleEndian.Uint16(frames[2:4]); got != ropacket.SPHP {
		t.Fatalf("frame 0 varID = %d, want SPHP (%d)", got, ropacket.SPHP)
	}
	if got := binary.LittleEndian.Uint32(frames[4:8]); got != 506 {
		t.Fatalf("HP after regen = %d, want 506", got)
	}
	if got := binary.LittleEndian.Uint16(frames[8:10]); got != ropacket.HeaderZCPARCHANGE {
		t.Fatalf("frame 1 header = 0x%04x, want ZC_PAR_CHANGE", got)
	}
	if got := binary.LittleEndian.Uint16(frames[10:12]); got != ropacket.SPSP {
		t.Fatalf("frame 1 varID = %d, want SPSP (%d)", got, ropacket.SPSP)
	}
	if got := binary.LittleEndian.Uint32(frames[12:16]); got != 40 {
		t.Fatalf("SP echoed = %d, want 40 (not yet regen)", got)
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

// TestMap_MobAttacksPlayer proves the full mob→player→notify path through the
// real gnet reactor: an aggressive mob (Ai=4) within AttackRange of the player
// swings on MonsterTick, and the NewMapServer-wired OnMobAttack sink delivers
// ZC_NOTIFY_ACT to the player (showing the mob's hit + damage) followed by a
// ZC_PAR_CHANGE whose HP matches the damage dealt. MonsterTick is driven directly
// (no real ticker) so the assertion is deterministic, not timing-fragile.
func TestMap_MobAttacksPlayer(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, Sex: 1,
	})
	ms, env := buildTestMapDeps(t, sessions)
	conn := startAndDial(t, ms, port)
	defer conn.Close()

	// CZ_ENTER -> ZC_ACCEPT_ENTER; draining the 13-byte reply registers the conn
	// for charID 150001 (the PC, full HP 1000 at new_1-1 (53,111)).
	sendCZEnter(t, conn, 2000001, 150001, 0x11111111)
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, make([]byte, 13)); err != nil {
		t.Fatalf("drain accept-enter: %v", err)
	}

	// Spawn the aggressive mob one cell east of the player (Chebyshev dist 1 ≤ its
	// AttackRange 2) so the cadence-accumulated swing lands.
	const mobGID = 160000
	if err := env.spawn.SpawnMob(mobGID, 8001, "new_1-1", worlddomain.Position{X: 54, Y: 111}, "AggroMob", 1000, 1000, 0); err != nil {
		t.Fatalf("spawn mob: %v", err)
	}

	// One cadence interval (2 s) elapsed ⇒ exactly one swing. Driving MonsterTick
	// directly keeps this deterministic.
	env.mobAI.MonsterTick(t.Context(), 2*time.Second)

	// ZC_NOTIFY_ACT (34 B) is broadcast first, then ZC_PAR_CHANGE HP + SP (8 B
	// each) refresh the target's own vitals.
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	act := make([]byte, (ropacket.NotifyActResponse{}).Size())
	if _, err := io.ReadFull(conn, act); err != nil {
		t.Fatalf("read ZC_NOTIFY_ACT: %v", err)
	}
	if got := binary.LittleEndian.Uint16(act[0:2]); got != ropacket.HeaderZCNOTIFYACT {
		t.Fatalf("ZC_NOTIFY_ACT header = 0x%04x, want 0x%04x", got, ropacket.HeaderZCNOTIFYACT)
	}
	if got := binary.LittleEndian.Uint32(act[2:6]); got != mobGID {
		t.Fatalf("ZC_NOTIFY_ACT srcID = %d, want mob GID %d", got, mobGID)
	}
	if got := binary.LittleEndian.Uint32(act[6:10]); got != 150001 {
		t.Fatalf("ZC_NOTIFY_ACT targetID = %d, want player GID 150001", got)
	}
	dmg := int32(binary.LittleEndian.Uint32(act[22:26]))
	if dmg <= 0 {
		t.Fatalf("ZC_NOTIFY_ACT damage = %d, want > 0", dmg)
	}

	// The player's own HP/SP refresh follows: two 8-byte ZC_PAR_CHANGE frames. HP
	// must equal 1000 − damage (applyDamage ran before the hook fired).
	par := make([]byte, 16)
	if _, err := io.ReadFull(conn, par); err != nil {
		t.Fatalf("read ZC_PAR_CHANGE frames: %v", err)
	}
	if got := binary.LittleEndian.Uint16(par[0:2]); got != ropacket.HeaderZCPARCHANGE {
		t.Fatalf("frame 0 header = 0x%04x, want ZC_PAR_CHANGE", got)
	}
	if got := binary.LittleEndian.Uint16(par[2:4]); got != ropacket.SPHP {
		t.Fatalf("frame 0 varID = %d, want SPHP (%d)", got, ropacket.SPHP)
	}
	hpAfter := int32(binary.LittleEndian.Uint32(par[4:8]))
	if want := int32(1000) - dmg; hpAfter != want {
		t.Fatalf("player HP after mob hit = %d, want %d (1000 − %d damage)", hpAfter, want, dmg)
	}
}

// TestMap_MobChasesPlayer proves the full chase→approach→swing→notify path: an
// aggressive mob spawned within ChaseRange but outside AttackRange pursues one
// greedy cell toward the player per WalkSpeed of accumulated tick time, the
// NewMapServer-wired OnMobMove sink delivers a ZC_UNIT_WALKING (ObjectType=MOB)
// frame to the player's conn each step, the mob stops at AttackRange without
// overshooting, and the next attack-cadence tick lands a hit (HP drops). The mob
// reaches range in (chaseCells × WalkSpeed) ms; MonsterTick is driven directly so
// the assertions are deterministic, not timing-fragile.
func TestMap_MobChasesPlayer(t *testing.T) {
	const (
		mobGID    = 160000
		mobClass  = 8001
		mobWalkMs = 400 // matches testMobAIFixture WalkSpeed (ms/cell)
		// Fixture mob: AttackRange=2, ChaseRange=12. Player enters at (53,111).
		// Spawn 7 cells east ⇒ Chebyshev dist 7 ≤ ChaseRange, > AttackRange: pursue.
		mobStartX      = 60
		playerX        = 53
		attackRange    = 2 // fixture AttackRange; mob halts at dist attackRange from the player
		attackInterval = 2 * time.Second
		wantSteps      = mobStartX - (playerX + attackRange) // 60-55 = 5 steps to reach range
		// mobObjType mirrors the wire object-type byte for a monster (rAthena
		// clif_bl_type: 5=MOB); it is unexported in package app, so the test asserts
		// the literal the production notifyMobMove writes into ZC_UNIT_WALKING[4].
		mobObjType uint8 = 5
	)
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, Sex: 1,
	})
	ms, env := buildTestMapDeps(t, sessions)
	conn := startAndDial(t, ms, port)
	defer conn.Close()

	// CZ_ENTER -> ZC_ACCEPT_ENTER; draining the 13-byte reply registers the conn
	// for charID 150001 (the PC, full HP 1000 at new_1-1 (53,111)).
	sendCZEnter(t, conn, 2000001, 150001, 0x11111111)
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, make([]byte, 13)); err != nil {
		t.Fatalf("drain accept-enter: %v", err)
	}

	// Spawn the aggressive mob 7 cells east (Chebyshev dist 7: within ChaseRange
	// 12, outside AttackRange 2) so the chase loop pursues toward the player.
	if err := env.spawn.SpawnMob(mobGID, mobClass, "new_1-1",
		worlddomain.Position{X: mobStartX, Y: 111}, "AggroMob", 1000, 1000, 0); err != nil {
		t.Fatalf("spawn mob: %v", err)
	}

	// Drive one WalkSpeed of tick time per step: each call banks exactly one
	// chase step (moveDue resets on a full cell), and the step's ZC_UNIT_WALKING
	// frame lands on the player's conn. Assert the mob's world cell advances one
	// toward the player and each frame is the MOB walk broadcast.
	mobEntity := func() worlddomain.Entity {
		e, err := env.world.Get(mobGID)
		if err != nil {
			t.Fatalf("Get mob: %v", err)
		}
		return e
	}
	unitWalkSize := (ropacket.UnitWalkingResponse{}).Size()
	for step := 1; step <= wantSteps; step++ {
		env.mobAI.MonsterTick(t.Context(), time.Duration(mobWalkMs)*time.Millisecond)
		if got := int(mobEntity().Pos.X); got != mobStartX-step {
			t.Fatalf("chase step %d: mob X = %d, want %d (one cell toward player)", step, got, mobStartX-step)
		}
		// One ZC_UNIT_WALKING frame per step, broadcast at the mob's new cell.
		frame := make([]byte, unitWalkSize)
		conn.SetDeadline(time.Now().Add(3 * time.Second))
		if _, err := io.ReadFull(conn, frame); err != nil {
			t.Fatalf("chase step %d: read ZC_UNIT_WALKING: %v", step, err)
		}
		if got := binary.LittleEndian.Uint16(frame[0:2]); got != ropacket.HeaderZCUNITWALKING {
			t.Fatalf("chase step %d: header = 0x%04x, want ZC_UNIT_WALKING (0x%04x)", step, got, ropacket.HeaderZCUNITWALKING)
		}
		if frame[4] != mobObjType {
			t.Fatalf("chase step %d: ObjectType = %d, want MOB (%d)", step, frame[4], mobObjType)
		}
		if got := binary.LittleEndian.Uint32(frame[9:13]); got != mobGID {
			t.Fatalf("chase step %d: GID = %d, want mob GID %d", step, got, mobGID)
		}
	}

	// Stop-short: the mob halted at AttackRange (dist 2), not on the player's
	// cell. Further chase ticks must not move it (resolve switches to the attack
	// branch at dist ≤ AttackRange).
	finalX := int(mobEntity().Pos.X)
	if want := playerX + attackRange; finalX != want {
		t.Fatalf("mob stopped at X=%d, want %d (player's cell %d + AttackRange %d)", finalX, want, playerX, attackRange)
	}

	// The mob is now in range. Drive one attack-cadence tick (attackInterval) to
	// land a swing — no move frame this tick, just the hit + HP refresh. This
	// proves the mob both closes to range AND swings once there.
	env.mobAI.MonsterTick(t.Context(), attackInterval)
	if got := int(mobEntity().Pos.X); got != finalX {
		t.Fatalf("attack tick moved the mob: X = %d, want %d (must hold at AttackRange)", got, finalX)
	}
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	act := make([]byte, (ropacket.NotifyActResponse{}).Size())
	if _, err := io.ReadFull(conn, act); err != nil {
		t.Fatalf("read ZC_NOTIFY_ACT: %v", err)
	}
	if got := binary.LittleEndian.Uint16(act[0:2]); got != ropacket.HeaderZCNOTIFYACT {
		t.Fatalf("ZC_NOTIFY_ACT header = 0x%04x, want 0x%04x", got, ropacket.HeaderZCNOTIFYACT)
	}
	dmg := int32(binary.LittleEndian.Uint32(act[22:26]))
	if dmg <= 0 {
		t.Fatalf("ZC_NOTIFY_ACT damage = %d, want > 0", dmg)
	}
	// The target's HP refresh: ZC_PAR_CHANGE HP frame, HP = 1000 − damage.
	par := make([]byte, 8)
	if _, err := io.ReadFull(conn, par); err != nil {
		t.Fatalf("read ZC_PAR_CHANGE HP frame: %v", err)
	}
	if got := binary.LittleEndian.Uint16(par[2:4]); got != ropacket.SPHP {
		t.Fatalf("PAR_CHANGE varID = %d, want SPHP (%d)", got, ropacket.SPHP)
	}
	if got := int32(binary.LittleEndian.Uint32(par[4:8])); got != int32(1000)-dmg {
		t.Fatalf("player HP after mob hit = %d, want %d (1000 − %d damage)", got, int32(1000)-dmg, dmg)
	}
}

// TestMap_MobKillsPlayerRespawns proves the full player-death → respawn path:
// an aggressive mob kills a PC (HP→0, died=true), the PC vanishes at its death
// cell (ZC_NOTIFY_VANISH VanishDead, seen by the dying player), then respawns at
// its save point with HP/SP restored and relocates (ZC_ACCEPT_ENTER). The death +
// vanish are driven by the real MonsterTick; respawn is driven deterministically
// via RespawnPlayer (the same code the ArmRespawn timer runs), so the test is not
// timing-fragile. The bounded goAthena model: no ghost/tomb — vanish then respawn.
func TestMap_MobKillsPlayerRespawns(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, Sex: 1,
	})
	ms, env := buildTestMapDeps(t, sessions)
	// Stop cancels the respawn timer armed by the death path so it does not fire
	// spuriously after this test (respawn is driven directly below).
	defer env.world.Stop()
	conn := startAndDial(t, ms, port)
	defer conn.Close()

	// CZ_ENTER → ZC_ACCEPT_ENTER; draining the 13-byte reply registers the conn
	// for charID 150001 (the PC, HP 1000 at new_1-1 (53,111), save point (1,1)).
	sendCZEnter(t, conn, 2000001, 150001, 0x11111111)
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, make([]byte, 13)); err != nil {
		t.Fatalf("drain accept-enter: %v", err)
	}

	// Re-seed the PC at 1 HP so a single mob swing is lethal (the conn stays
	// registered: RemoveEntity does not touch ms.conns). The save point is
	// preserved so respawn relocates to (1,1), not the death cell.
	if err := env.world.RemoveEntity(150001); err != nil {
		t.Fatalf("RemoveEntity: %v", err)
	}
	if err := env.world.AddEntity(worlddomain.Entity{
		ID: 150001, Type: worlddomain.EntityTypePC, Account: 2000001, Map: "new_1-1",
		Pos: worlddomain.Position{X: 53, Y: 111}, HP: 1, MaxHP: 1000,
		SP: 100, MaxSP: 100, Speed: 150,
		SaveMap: "new_1-1", SavePos: worlddomain.Position{X: 1, Y: 1},
	}); err != nil {
		t.Fatalf("AddEntity: %v", err)
	}

	// Spawn the aggressive mob one cell east (Chebyshev dist 1 ≤ AttackRange 2).
	const mobGID = 160000
	if err := env.spawn.SpawnMob(mobGID, 8001, "new_1-1", worlddomain.Position{X: 54, Y: 111}, "AggroMob", 1000, 1000, 0); err != nil {
		t.Fatalf("spawn mob: %v", err)
	}

	// One cadence interval (2 s) ⇒ one lethal swing: HP 1 → 0, died=true.
	env.mobAI.MonsterTick(t.Context(), 2*time.Second)
	conn.SetDeadline(time.Now().Add(3 * time.Second))

	// ZC_NOTIFY_ACT (34 B): the killing blow, srcID=mob, targetID=PC.
	act := make([]byte, (ropacket.NotifyActResponse{}).Size())
	if _, err := io.ReadFull(conn, act); err != nil {
		t.Fatalf("read ZC_NOTIFY_ACT: %v", err)
	}
	if got := binary.LittleEndian.Uint16(act[0:2]); got != ropacket.HeaderZCNOTIFYACT {
		t.Fatalf("ZC_NOTIFY_ACT header = 0x%04x, want 0x%04x", got, ropacket.HeaderZCNOTIFYACT)
	}
	if got := binary.LittleEndian.Uint32(act[2:6]); got != mobGID {
		t.Fatalf("ZC_NOTIFY_ACT srcID = %d, want mob GID %d", got, mobGID)
	}
	if got := binary.LittleEndian.Uint32(act[6:10]); got != 150001 {
		t.Fatalf("ZC_NOTIFY_ACT targetID = %d, want player GID 150001", got)
	}

	// ZC_PAR_CHANGE HP+SP (16 B): HP dropped to 0 on the killing blow.
	par := make([]byte, 16)
	if _, err := io.ReadFull(conn, par); err != nil {
		t.Fatalf("read ZC_PAR_CHANGE after kill: %v", err)
	}
	if hpAfter := int32(binary.LittleEndian.Uint32(par[4:8])); hpAfter != 0 {
		t.Fatalf("player HP after kill = %d, want 0", hpAfter)
	}

	// ZC_NOTIFY_VANISH (7 B): VanishDead at the death cell, broadcast to the
	// dying player (exclude 0) so it sees its own death.
	van := make([]byte, (ropacket.NotifyVanishResponse{}).Size())
	if _, err := io.ReadFull(conn, van); err != nil {
		t.Fatalf("read ZC_NOTIFY_VANISH: %v", err)
	}
	if got := binary.LittleEndian.Uint16(van[0:2]); got != ropacket.HeaderZCNOTIFYVANISH {
		t.Fatalf("ZC_NOTIFY_VANISH header = 0x%04x, want 0x%04x", got, ropacket.HeaderZCNOTIFYVANISH)
	}
	if got := binary.LittleEndian.Uint32(van[2:6]); got != 150001 {
		t.Fatalf("ZC_NOTIFY_VANISH GID = %d, want player GID 150001", got)
	}
	if van[6] != ropacket.VanishDead {
		t.Fatalf("ZC_NOTIFY_VANISH type = %d, want VanishDead (%d)", van[6], ropacket.VanishDead)
	}

	// Respawn deterministically (the ArmRespawn timer runs the same RespawnPlayer
	// after playerRespawnDelay). OnRespawn fires the save-point appear burst.
	if err := env.world.RespawnPlayer(150001); err != nil {
		t.Fatalf("RespawnPlayer: %v", err)
	}

	// ZC_PAR_CHANGE HP+SP (16 B): vitals restored to full (respawnRevivePct=100).
	par2 := make([]byte, 16)
	if _, err := io.ReadFull(conn, par2); err != nil {
		t.Fatalf("read ZC_PAR_CHANGE after respawn: %v", err)
	}
	if hpRestored := int32(binary.LittleEndian.Uint32(par2[4:8])); hpRestored != 1000 {
		t.Fatalf("player HP after respawn = %d, want 1000 (full)", hpRestored)
	}

	// ZC_ACCEPT_ENTER (13 B): the player's own client relocates to the save point.
	enter := make([]byte, 13)
	if _, err := io.ReadFull(conn, enter); err != nil {
		t.Fatalf("read ZC_ACCEPT_ENTER relocate: %v", err)
	}
	if got := binary.LittleEndian.Uint16(enter[0:2]); got != ropacket.HeaderZCACCEPTENTER {
		t.Fatalf("ZC_ACCEPT_ENTER header = 0x%04x, want 0x%04x", got, ropacket.HeaderZCACCEPTENTER)
	}
	// posDir[3] at [6:9] packs (x,y,dir); decode to confirm the save cell (1,1).
	x := int16((uint16(enter[6]) << 2) | (uint16(enter[7]) >> 6))      //nolint:gosec // G115: wire bit layout; coords are non-negative.
	y := int16((uint16(enter[7]&0x3f) << 4) | (uint16(enter[8]) >> 4)) //nolint:gosec // G115: wire bit layout; coords are non-negative.
	if x != 1 || y != 1 {
		t.Fatalf("relocate cell = (%d,%d), want save point (1,1)", x, y)
	}

	// The world is the source of truth: the PC is at its save point, alive.
	pc, err := env.world.Get(150001)
	if err != nil {
		t.Fatalf("Get respawned PC: %v", err)
	}
	if pc.HP != 1000 {
		t.Errorf("respawned PC HP = %d, want 1000", pc.HP)
	}
	if pc.Pos != (worlddomain.Position{X: 1, Y: 1}) {
		t.Errorf("respawned PC pos = %+v, want {1,1}", pc.Pos)
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

// castSkillHit sends a CZ_USE_SKILL2 (skill SM_BASH id 5, level 1) at targetGID
// and returns the resolved damage from the ZC_NOTIFY_SKILL frame. The conn must
// be quiescent (no pending frames) so the next 33 bytes are the notify-skill.
func castSkillHit(t *testing.T, c net.Conn, targetGID uint32) int32 {
	t.Helper()
	req := make([]byte, 10)
	binary.LittleEndian.PutUint16(req[0:], ropacket.HeaderCZUSESKILL)
	binary.LittleEndian.PutUint16(req[2:], 1) // skillLv
	binary.LittleEndian.PutUint16(req[4:], 5) // SM_BASH
	binary.LittleEndian.PutUint32(req[6:], targetGID)
	c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write(req); err != nil {
		t.Fatalf("send CZ_USE_SKILL2: %v", err)
	}
	resp := make([]byte, 33)
	if _, err := io.ReadFull(c, resp); err != nil {
		t.Fatalf("read ZC_NOTIFY_SKILL: %v", err)
	}
	if got := binary.LittleEndian.Uint16(resp[0:2]); got != ropacket.HeaderZCNOTIFYSKILL {
		t.Fatalf("notify-skill header = 0x%04x, want ZC_NOTIFY_SKILL (0x%04x)", got, ropacket.HeaderZCNOTIFYSKILL)
	}
	return int32(binary.LittleEndian.Uint32(resp[24:28])) //nolint:gosec // G115: damage is a bounded game value
}

// TestMap_Dispatch_EquipIncreasesDamage proves the M10 equipment→combat path
// end-to-end through the wire. The player casts a melee skill bare-handed
// (ZC_NOTIFY_SKILL carries the resolved damage), equips a Knife via
// CZ_REQ_WEAR_EQUIP (asserting the success ack), then casts again. The armed hit
// must land strictly harder: the weapon's WeaponATK now folds into the damage
// base. Element/size/crit are deliberately out of scope — the assertion is only
// that an equipped weapon increases melee damage.
func TestMap_Dispatch_EquipIncreasesDamage(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1,
	})
	conn, env := startMapListenerWithShopEnv(t, port, sessions)
	defer conn.Close()

	// Seed one Knife (item_db 1201) at bag slot 1 so the wear request's index 1
	// resolves to it.
	if _, err := env.itemRepo.Add(t.Context(), 150001, 1201, 1); err != nil {
		t.Fatalf("seed knife: %v", err)
	}

	sendCZEnter(t, conn, 2000001, 150001, 0x11111111)
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, make([]byte, 13)); err != nil { // drain accept-enter
		t.Fatalf("drain accept-enter: %v", err)
	}

	// High-HP mob on the player's tile so it survives both hits (no death burst
	// interleaving the reads).
	const mobGID = 160020
	if err := env.spawn.SpawnMob(mobGID, 1002, "new_1-1",
		worlddomain.Position{X: 53, Y: 111}, "Poring", 200, 200, 0); err != nil {
		t.Fatalf("spawn mob: %v", err)
	}

	// 1. Bare-handed hit.
	bareDmg := castSkillHit(t, conn, mobGID)

	// 2. Equip the Knife: CZ_REQ_WEAR_EQUIP_V5 (0x0998, index 1, right hand).
	wear := make([]byte, 8)
	binary.LittleEndian.PutUint16(wear[0:], ropacket.HeaderCZREQWEAREQUIPV5)
	binary.LittleEndian.PutUint16(wear[2:], 1)               // inventory index
	binary.LittleEndian.PutUint32(wear[4:], equip.HandRight) // EQP position
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write(wear); err != nil {
		t.Fatalf("send CZ_REQ_WEAR_EQUIP: %v", err)
	}
	ack := make([]byte, 11)
	if _, err := io.ReadFull(conn, ack); err != nil {
		t.Fatalf("read ZC_REQ_WEAR_EQUIP_ACK_V5: %v", err)
	}
	if got := binary.LittleEndian.Uint16(ack[0:2]); got != ropacket.HeaderZCREQWEAREQUIPACKV5 {
		t.Fatalf("wear-ack header = 0x%04x, want ZC_REQ_WEAR_EQUIP_ACK_V5 (0x0999)", got)
	}
	if got := binary.LittleEndian.Uint32(ack[4:8]); got != equip.HandRight {
		t.Fatalf("wear-ack WearLocation = 0x%08x, want HandRight", got)
	}
	if ack[10] != 1 {
		t.Fatalf("wear-ack result = %d, want 1 (success)", ack[10])
	}

	// 3. The armed hit must exceed the bare-handed hit (the weapon's ATK now folds
	// into the damage base).
	armedDmg := castSkillHit(t, conn, mobGID)
	if armedDmg <= bareDmg {
		t.Fatalf("equipped weapon did not increase damage: bare=%d armed=%d (want armed > bare)", bareDmg, armedDmg)
	}
}

// TestMap_Dispatch_UseItemHeals proves the usable-item verb end-to-end through
// the real gnet reactor: a player drinks a Red Potion (itemheal 45,0), the server
// consumes one unit, applies the flat heal, and emits the client-visible result.
// The world tick loop is idle in tests (no StartTick), so the only frames on the
// wire are the ones from this use. AddVitals runs the NewMapServer-wired
// OnStatChange hook synchronously inside the use, so ZC_PAR_CHANGE (HP then SP)
// arrives before ZC_USE_ITEM_ACK2; the handler emits only the ack to avoid a
// duplicate stat-change frame. The single-unit stack must then be consumed.
func TestMap_Dispatch_UseItemHeals(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1,
	})
	ms, env := buildTestMapDeps(t, sessions)
	conn := startAndDial(t, ms, port)
	defer conn.Close()

	// 1. CZ_ENTER -> ZC_ACCEPT_ENTER (drain 13 bytes so the reactor has the conn).
	sendCZEnter(t, conn, 2000001, 150001, 0x11111111)
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, make([]byte, 13)); err != nil {
		t.Fatalf("drain accept-enter: %v", err)
	}

	// 2. Seed one Red Potion (id 501) at inventory slot 1 and drop the player to
	//    HP 500/1000 so the +45 heal lands at 545 (not clamped). RemoveEntity does
	//    not touch ms.conns, so the live wire stays registered.
	if _, err := env.itemRepo.Add(t.Context(), 150001, 501, 1); err != nil {
		t.Fatalf("seed red potion: %v", err)
	}
	if err := env.world.RemoveEntity(150001); err != nil {
		t.Fatalf("RemoveEntity: %v", err)
	}
	if err := env.world.AddEntity(worlddomain.Entity{
		ID: 150001, Type: worlddomain.EntityTypePC, Map: "new_1-1",
		Pos: worlddomain.Position{X: 53, Y: 111}, HP: 500, MaxHP: 1000,
		SP: 100, MaxSP: 100, Speed: 150,
	}); err != nil {
		t.Fatalf("AddEntity at HP 500: %v", err)
	}

	// 3. Send CZ_USE_ITEM2 (0x0439, 8B): cmd + inventory index 1 + AID.
	useReq := make([]byte, 8)
	binary.LittleEndian.PutUint16(useReq[0:], ropacket.HeaderCZUSEITEM2)
	binary.LittleEndian.PutUint16(useReq[2:], 1)       // inventory index (1-based)
	binary.LittleEndian.PutUint32(useReq[4:], 2000001) // AID
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write(useReq); err != nil {
		t.Fatalf("send CZ_USE_ITEM2: %v", err)
	}

	// 4. Expected wire order: ZC_PAR_CHANGE HP (8B), ZC_PAR_CHANGE SP (8B), then
	//    ZC_USE_ITEM_ACK2 (13B). PAR_CHANGE fires first because AddVitals runs the
	//    OnStatChange hook synchronously inside the use before the ack is emitted.
	out := make([]byte, 29)
	if _, err := io.ReadFull(conn, out); err != nil {
		t.Fatalf("read use-item response: %v", err)
	}
	// PAR_CHANGE HP: 500 + 45 = 545.
	if got := binary.LittleEndian.Uint16(out[0:2]); got != ropacket.HeaderZCPARCHANGE {
		t.Fatalf("frame 0 header = 0x%04x, want ZC_PAR_CHANGE", got)
	}
	if got := binary.LittleEndian.Uint16(out[2:4]); got != ropacket.SPHP {
		t.Fatalf("frame 0 varID = %d, want SPHP (%d)", got, ropacket.SPHP)
	}
	if got := binary.LittleEndian.Uint32(out[4:8]); got != 545 {
		t.Fatalf("HP after potion = %d, want 545", got)
	}
	// PAR_CHANGE SP: the potion heals 0 SP, so SP echoes its unchanged value (100).
	if got := binary.LittleEndian.Uint16(out[8:10]); got != ropacket.HeaderZCPARCHANGE {
		t.Fatalf("frame 1 header = 0x%04x, want ZC_PAR_CHANGE", got)
	}
	if got := binary.LittleEndian.Uint16(out[10:12]); got != ropacket.SPSP {
		t.Fatalf("frame 1 varID = %d, want SPSP (%d)", got, ropacket.SPSP)
	}
	if got := binary.LittleEndian.Uint32(out[12:16]); got != 100 {
		t.Fatalf("SP after potion = %d, want 100 (potion heals 0 SP)", got)
	}
	// ZC_USE_ITEM_ACK2: index=server(1)+2=3, itemID=501, AID=2000001, amount=0,
	// result=1 (success).
	if got := binary.LittleEndian.Uint16(out[16:18]); got != ropacket.HeaderZCUSEITEMACK2 {
		t.Fatalf("ack header = 0x%04x, want ZC_USE_ITEM_ACK2 (0x01c8)", got)
	}
	if got := binary.LittleEndian.Uint16(out[18:20]); got != 3 {
		t.Fatalf("ack index = %d, want 3 (server index 1 + 2)", got)
	}
	if got := binary.LittleEndian.Uint16(out[20:22]); got != 501 {
		t.Fatalf("ack itemID = %d, want 501", got)
	}
	if got := binary.LittleEndian.Uint32(out[22:26]); got != 2000001 {
		t.Fatalf("ack AID = %d, want 2000001", got)
	}
	if got := binary.LittleEndian.Uint16(out[26:28]); got != 0 {
		t.Fatalf("ack amount = %d, want 0 (stack consumed)", got)
	}
	if got := out[28]; got != 1 {
		t.Fatalf("ack result = %d, want 1 (success)", got)
	}

	// 5. The single-unit stack must be deleted from the inventory.
	remaining, err := env.itemRepo.LoadByChar(t.Context(), 0, 150001)
	if err != nil {
		t.Fatalf("load inventory after use: %v", err)
	}
	for _, it := range remaining {
		if it.NameID == 501 {
			t.Fatalf("red potion not consumed: %+v", it)
		}
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

// TestMap_Dispatch_ShopBuyRoundTrip exercises the NPC shop commerce verb (#15):
// CZ_ACK_SELECT_DEALTYPE (Buy) opens the shop and the server replies with
// ZC_PC_PURCHASE_ITEMLIST carrying the catalog; then CZ_PC_PURCHASE_ITEMLIST buys
// one Red Potion and the server replies ZC_PC_PURCHASE_RESULT(success). The
// transaction is real — zeny is deducted (1000 → 950) and the item lands in
// inventory — proving the full buy round-trip through ShopService.
func TestMap_Dispatch_ShopBuyRoundTrip(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, Sex: 1,
	})
	conn, env := startMapListenerWithShopEnv(t, port, sessions)
	defer conn.Close()

	// Seed the economy character. The first create in a fresh repo gets id
	// 150001, matching the pre-seeded world entity and the CZ_ENTER gid.
	const gid uint32 = 150001
	hero, err := env.charRepo.Create(t.Context(), chardomain.Character{
		AccountID: 2000001, Name: "Hero", Zeny: 1000,
	})
	if err != nil {
		t.Fatalf("seed character: %v", err)
	}
	if uint32(hero.ID) != gid {
		t.Fatalf("seeded character id = %d, want %d (world entity)", hero.ID, gid)
	}

	// 1. CZ_ENTER → ZC_ACCEPT_ENTER (13B): drain the full reply.
	sendCZEnter(t, conn, 2000001, gid, 0x11111111)
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	enterReply := make([]byte, 13)
	if _, err := io.ReadFull(conn, enterReply); err != nil {
		t.Fatalf("read accept-enter: %v", err)
	}
	if got := binary.LittleEndian.Uint16(enterReply[0:2]); got != ropacket.HeaderZCACCEPTENTER {
		t.Fatalf("accept-enter header = 0x%04x, want 0x%04x", got, ropacket.HeaderZCACCEPTENTER)
	}

	// 2. CZ_ACK_SELECT_DEALTYPE (Buy) on the dev shop NPC → ZC_PC_PURCHASE_ITEMLIST.
	ack := make([]byte, 7)
	binary.LittleEndian.PutUint16(ack[0:], ropacket.HeaderCZACKSELECTDEALTYPE)
	binary.LittleEndian.PutUint32(ack[2:], shop.DevShopGID)
	ack[6] = 0 // type = Buy
	if _, err := conn.Write(ack); err != nil {
		t.Fatalf("send CZ_ACK_SELECT_DEALTYPE: %v", err)
	}
	hdr := make([]byte, 4) // cmd + packetLength
	if _, err := io.ReadFull(conn, hdr); err != nil {
		t.Fatalf("read purchase-list header: %v", err)
	}
	if got := binary.LittleEndian.Uint16(hdr[0:2]); got != ropacket.HeaderZCPCPURCHASEITEMLIST {
		t.Fatalf("purchase-list header = 0x%04x, want ZC_PC_PURCHASE_ITEMLIST (0x%04x)", got, ropacket.HeaderZCPCPURCHASEITEMLIST)
	}
	listLen := int(binary.LittleEndian.Uint16(hdr[2:4]))
	if listLen < 4 {
		t.Fatalf("purchase-list length = %d, want >= 4", listLen)
	}
	body := make([]byte, listLen-4)
	if _, err := io.ReadFull(conn, body); err != nil {
		t.Fatalf("read purchase-list body: %v", err)
	}
	if len(body) < 4 {
		t.Fatalf("purchase-list body too short for one item: %d bytes", len(body))
	}
	// First ShopBuyItem: uint32 itemId at offset 0. The dev shop lists Red
	// Potion (501) first.
	if got := binary.LittleEndian.Uint32(body[0:4]); got != 501 {
		t.Fatalf("first buy item = %d, want Red Potion (501)", got)
	}

	// 3. CZ_PC_PURCHASE_ITEMLIST: buy 1 Red Potion (variable: cmd + len + entry).
	pur := make([]byte, 10) // 4 header + 6 (itemId uint32 + amount uint16)
	binary.LittleEndian.PutUint16(pur[0:], ropacket.HeaderCZPCPURCHASEITEMLIST)
	binary.LittleEndian.PutUint16(pur[2:], 10)  // packet length
	binary.LittleEndian.PutUint32(pur[4:], 501) // itemId
	binary.LittleEndian.PutUint16(pur[8:], 1)   // amount
	if _, err := conn.Write(pur); err != nil {
		t.Fatalf("send CZ_PC_PURCHASE_ITEMLIST: %v", err)
	}
	res := make([]byte, 3) // ZC_PC_PURCHASE_RESULT: cmd + result byte
	if _, err := io.ReadFull(conn, res); err != nil {
		t.Fatalf("read purchase-result: %v", err)
	}
	if got := binary.LittleEndian.Uint16(res[0:2]); got != ropacket.HeaderZCPCPURCHASERESULT {
		t.Fatalf("purchase-result header = 0x%04x, want ZC_PC_PURCHASE_RESULT (0x%04x)", got, ropacket.HeaderZCPCPURCHASERESULT)
	}
	if res[2] != 0 {
		t.Fatalf("purchase-result = %d, want 0 (success)", res[2])
	}

	// 4. Real transaction: zeny deducted (1000 - 50 = 950) and the potion added.
	after, err := env.charRepo.FindByID(t.Context(), hero.ID)
	if err != nil {
		t.Fatalf("reload character: %v", err)
	}
	if after.Zeny != 950 {
		t.Fatalf("zeny after buy = %d, want 950", after.Zeny)
	}
	items, err := env.itemRepo.LoadByChar(t.Context(), 2000001, gid)
	if err != nil {
		t.Fatalf("load inventory after buy: %v", err)
	}
	var potionAmount uint32
	for _, it := range items {
		if it.NameID == 501 {
			potionAmount = it.Amount
		}
	}
	if potionAmount != 1 {
		t.Fatalf("potion amount after buy = %d, want 1", potionAmount)
	}
}

// buildTradeMapDeps is the 2-player variant of buildTestMapDeps: it seeds TWO PC
// entities on the same map (so trade.Request's same-map/online check passes) and
// wires the TradeService over the real inventory + economy services. The two
// connections are dialed separately by startAndDialTwo.
func buildTradeMapDeps(t *testing.T, sessions *charinfra.MemorySessionStore) (*gwapp.MapServer, mapTestEnv) {
	t.Helper()
	wrepo := worldinfra.NewMemoryWorldRepository(
		worlddomain.Entity{
			ID: 150001, Account: 2000001, Map: "new_1-1",
			Pos: worlddomain.Position{X: 53, Y: 111}, Sex: 1, Job: 0, Level: 1,
			Name: "Hero", HP: 1000, MaxHP: 1000, SP: 100, MaxSP: 100, Speed: 150,
		},
		worlddomain.Entity{
			ID: 150002, Account: 2000002, Map: "new_1-1",
			Pos: worlddomain.Position{X: 54, Y: 111}, Sex: 1, Job: 0, Level: 1,
			Name: "Partner", HP: 1000, MaxHP: 1000, SP: 100, MaxSP: 100, Speed: 150,
		},
	)
	world := worldapp.NewWorldService(wrepo, slog.Default(), 50)
	spawn := worldapp.NewSpawnService(world, nil, nil)
	combat := worldapp.NewCombatService(world, nil, nil)
	itemRepo := invinfra.NewMemoryItemRepository()
	inv := invapp.NewInventoryService(itemRepo)
	content := contentapp.NewEngine(nil, nil, nil, slog.Default())
	skills := worldapp.NewSkillService(world, combat, testSkillDB())
	charRepo := charinfra.NewMemoryCharacterRepository()
	econ := economyapp.NewEconomyService(charRepo)
	trade := worldapp.NewTradeService(world, inv, econ)

	ms, err := gwapp.NewMapServer(world, spawn, combat, nil, nil, nil, inv, content, skills, nil, nil, trade, sessions, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	return ms, mapTestEnv{spawn: spawn, charRepo: charRepo, itemRepo: itemRepo, world: world}
}

// startAndDialTwo starts one map listener and dials two independent connections
// (the two trade partners).
func startAndDialTwo(t *testing.T, ms *gwapp.MapServer, port int) (net.Conn, net.Conn) {
	t.Helper()
	ms.Start("tcp://127.0.0.1:" + strconv.Itoa(port))
	t.Cleanup(ms.Stop)
	dial := func() net.Conn {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if c, derr := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 500*time.Millisecond); derr == nil {
				return c
			}
		}
		t.Fatal("map listener never accepted")
		return nil
	}
	return dial(), dial()
}

// readTradeFrame reads exactly size bytes and asserts the leading header. Trade
// responses are fixed-length and the conn is quiescent between steps (no tick
// broadcasts in the test harness), so an exact read is reliable.
func readTradeFrame(t *testing.T, c net.Conn, want uint16, size int) []byte {
	t.Helper()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	buf := make([]byte, size)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("read frame 0x%04x: %v", want, err)
	}
	if got := binary.LittleEndian.Uint16(buf[0:2]); got != want {
		t.Fatalf("frame header = 0x%04x, want 0x%04x", got, want)
	}
	return buf
}

// sendRaw writes frame bytes to conn with a deadline.
func sendRaw(t *testing.T, c net.Conn, frame []byte) {
	t.Helper()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := c.Write(frame); err != nil {
		t.Fatalf("send frame 0x%04x: %v", binary.LittleEndian.Uint16(frame), err)
	}
}

// packMoveDest encodes (x, y) into the kRO 3-byte packed position carried by
// CZ_REQUEST_MOVE (mirrors pkg/ro/packet.encodePos). Raw coordinate bytes decode
// to a different cell, so the move frame must use this packing to land on an
// actual on-map cell near the players.
func packMoveDest(x, y int16) [3]byte {
	ux := uint16(x)
	uy := uint16(y)
	return [3]byte{
		byte(ux >> 2),                        //nolint:gosec // G115: kRO WBUFPOS bit layout, low byte.
		byte((ux << 6) | ((uy >> 4) & 0x3f)), //nolint:gosec // ditto
		byte(uy << 4),                        //nolint:gosec // ditto
	}
}

// TestMap_Dispatch_TradeItemSwap drives the full player-to-player trade flow over
// two real connections: request → both see the open → ack(accept) → stage one item
// → both OK → atomic conclude. It asserts every state-machine packet header lands
// on the right conn, and that the atomic swap moved the staged item from the
// offerer to the partner (no duplication or loss).
func TestMap_Dispatch_TradeItemSwap(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{AccountID: 2000001, LoginID1: 0x11111111, Sex: 1})
	_ = sessions.PutSession(t.Context(), chardomain.Session{AccountID: 2000002, LoginID1: 0x33333333, Sex: 1})

	ms, env := buildTradeMapDeps(t, sessions)
	conn1, conn2 := startAndDialTwo(t, ms, port)
	defer conn1.Close()
	defer conn2.Close()

	// Offerer (player 1) owns one Red Potion (501) at bag slot 1.
	if _, err := env.itemRepo.Add(t.Context(), 150001, 501, 1); err != nil {
		t.Fatalf("seed offerer item: %v", err)
	}

	// 1. Both players enter (each drains its 13-byte ZC_ACCEPT_ENTER).
	sendCZEnter(t, conn1, 2000001, 150001, 0x11111111)
	if _, err := io.ReadFull(conn1, make([]byte, 13)); err != nil {
		t.Fatalf("drain p1 accept-enter: %v", err)
	}
	sendCZEnter(t, conn2, 2000002, 150002, 0x33333333)
	if _, err := io.ReadFull(conn2, make([]byte, 13)); err != nil {
		t.Fatalf("drain p2 accept-enter: %v", err)
	}
	// Player 1 also receives player 2's spawn (ZC_SPAWN_UNIT): the enter handler
	// now broadcasts the newcomer to neighbors. Drain it off conn1 so the
	// subsequent trade frames read cleanly.
	readTradeFrame(t, conn1, ropacket.HeaderZCSPAWNUNIT, 107)
	// Player 2 (the newcomer) also receives player 1's spawn via the Phase-11
	// enter-sight back-fill. Drain it off conn2.
	readTradeFrame(t, conn2, ropacket.HeaderZCSPAWNUNIT, 107)

	// 2. Player 1 requests a trade with player 2. Real wire format: the TARGET
	//    (player 2) gets ZC_REQ_EXCHANGE_ITEM; the requester gets nothing yet.
	reqFrame := make([]byte, 6)
	binary.LittleEndian.PutUint16(reqFrame[0:], ropacket.HeaderCZTRADEREQUEST)
	binary.LittleEndian.PutUint32(reqFrame[2:], 150002)
	sendRaw(t, conn1, reqFrame)
	readTradeFrame(t, conn2, ropacket.HeaderZCREQEXCHANGEITEM, 32)

	// 3. Player 2 accepts (CZ_TRADE_ACK type=3). Both get ZC_ACK_EXCHANGE_ITEM.
	ackFrame := make([]byte, 3)
	binary.LittleEndian.PutUint16(ackFrame[0:], ropacket.HeaderCZTRADEACK)
	ackFrame[2] = ropacket.CZTradeAckAccept
	sendRaw(t, conn2, ackFrame)
	ack1 := readTradeFrame(t, conn1, ropacket.HeaderZCACKEXCHANGEITEM, 9)
	ack2 := readTradeFrame(t, conn2, ropacket.HeaderZCACKEXCHANGEITEM, 9)
	if ack1[2] != ropacket.TradeAckAccept || ack2[2] != ropacket.TradeAckAccept {
		t.Fatalf("trade ack result = %d/%d, want %d", ack1[2], ack2[2], ropacket.TradeAckAccept)
	}

	// 4. Player 1 stages the potion (slot 1, amount 1). Self gets the per-add ack,
	//    partner gets the staged view.
	addFrame := make([]byte, 8)
	binary.LittleEndian.PutUint16(addFrame[0:], ropacket.HeaderCZADDEXCHANGEITEM)
	binary.LittleEndian.PutUint16(addFrame[2:], 1) // index (slot 1)
	binary.LittleEndian.PutUint32(addFrame[4:], 1) // amount
	sendRaw(t, conn1, addFrame)
	selfAck := readTradeFrame(t, conn1, ropacket.HeaderZCACKADDEXCHANGEITEM, 5)
	if selfAck[4] != ropacket.TradeItemAddSuccess {
		t.Fatalf("add-item self result = %d, want %d", selfAck[4], ropacket.TradeItemAddSuccess)
	}
	readTradeFrame(t, conn2, ropacket.HeaderZCADDEXCHANGEITEM, 62)

	// 5. Player 1 presses OK → both get ZC_CONCLUDE_EXCHANGE_ITEM (Who=0 to p1,
	//    Who=1 to p2).
	okFrame := make([]byte, 2)
	binary.LittleEndian.PutUint16(okFrame[0:], ropacket.HeaderCZTRADEOK)
	sendRaw(t, conn1, okFrame)
	conc1a := readTradeFrame(t, conn1, ropacket.HeaderZCCONCLUDEEXCHANGEITEM, 3)
	conc2a := readTradeFrame(t, conn2, ropacket.HeaderZCCONCLUDEEXCHANGEITEM, 3)
	if conc1a[2] != 0 || conc2a[2] != 1 {
		t.Fatalf("first OK conclude who = %d/%d, want 0/1", conc1a[2], conc2a[2])
	}

	// 6. Player 2 presses OK → both locked → atomic conclude swap. Both get a
	//    conclude notification; player 2 (the locker) Who=0, player 1 Who=1.
	sendRaw(t, conn2, okFrame)
	conc2b := readTradeFrame(t, conn2, ropacket.HeaderZCCONCLUDEEXCHANGEITEM, 3)
	conc1b := readTradeFrame(t, conn1, ropacket.HeaderZCCONCLUDEEXCHANGEITEM, 3)
	if conc2b[2] != 0 || conc1b[2] != 1 {
		t.Fatalf("second OK conclude who = %d/%d, want 0/1", conc2b[2], conc1b[2])
	}

	// 7. Atomic swap: player 1 lost the potion, player 2 gained it (no dup/loss).
	p1Items, err := env.itemRepo.LoadByChar(t.Context(), 2000001, 150001)
	if err != nil {
		t.Fatalf("load p1 inventory: %v", err)
	}
	if hasItem(p1Items, 501) {
		t.Fatalf("offerer still owns the traded item after conclude (not removed)")
	}
	p2Items, err := env.itemRepo.LoadByChar(t.Context(), 2000002, 150002)
	if err != nil {
		t.Fatalf("load p2 inventory: %v", err)
	}
	if amt := itemAmount(p2Items, 501); amt != 1 {
		t.Fatalf("partner potion amount after conclude = %d, want 1", amt)
	}
}

// hasItem reports whether the item list contains nameID.
func hasItem(items []invdomain.Item, nameID uint32) bool { return itemAmount(items, nameID) > 0 }

// itemAmount returns the stacked amount of nameID in the list, or 0 if absent.
func itemAmount(items []invdomain.Item, nameID uint32) uint32 {
	for _, it := range items {
		if it.NameID == nameID {
			return it.Amount
		}
	}
	return 0
}

// TestMap_Dispatch_SharedWorld_Visibility is the core shared-world proof: when
// player A acts, OTHER players on the same map see it — not just A. It seeds two
// adjacent players, then asserts that B's connection receives A's move broadcast
// (ZC_UNIT_WALKING carrying A's GID) and A's drop broadcast (ZC_ITEM_FALL_ENTRY),
// while A's own connection gets its private move-ack (0x0087) — never the walk.
func TestMap_Dispatch_SharedWorld_Visibility(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{AccountID: 2000001, LoginID1: 0x11111111, Sex: 1})
	_ = sessions.PutSession(t.Context(), chardomain.Session{AccountID: 2000002, LoginID1: 0x33333333, Sex: 1})

	ms, env := buildTradeMapDeps(t, sessions)
	conn1, conn2 := startAndDialTwo(t, ms, port)
	defer conn1.Close()
	defer conn2.Close()

	// Player A (150001 @ {53,111}, conn1) and player B (150002 @ {54,111}, conn2).
	// Give A a Red Potion stack so the drop verb has something to throw.
	if _, err := env.itemRepo.Add(t.Context(), 150001, 501, 5); err != nil {
		t.Fatalf("seed item: %v", err)
	}

	// 1. Both enter. Each drains its 13-byte accept-enter; A's conn also receives
	//    B's spawn (B's enter broadcasts the newcomer to neighbors) and drains it.
	sendCZEnter(t, conn1, 2000001, 150001, 0x11111111)
	if _, err := io.ReadFull(conn1, make([]byte, 13)); err != nil {
		t.Fatalf("drain p1 accept-enter: %v", err)
	}
	sendCZEnter(t, conn2, 2000002, 150002, 0x33333333)
	if _, err := io.ReadFull(conn2, make([]byte, 13)); err != nil {
		t.Fatalf("drain p2 accept-enter: %v", err)
	}
	readTradeFrame(t, conn1, ropacket.HeaderZCSPAWNUNIT, 107) // A sees B spawn in.
	// B also sees A: the Phase-11 enter-sight back-fill sends one ZC_SPAWN_UNIT
	// per existing nearby PC to the newcomer's own conn on enter. Drain it before
	// the move-broadcast read below.
	readTradeFrame(t, conn2, ropacket.HeaderZCSPAWNUNIT, 107) // B sees A on enter.

	// 2. A moves. A's own conn gets the 12-byte self-ack (ZC_NOTIFY_PLAYERMOVE);
	//    B's conn gets the 114-byte walk broadcast (ZC_UNIT_WALKING) carrying A's
	//    GID — the core "shared world" win.
	moveReq := make([]byte, 5)
	binary.LittleEndian.PutUint16(moveReq[0:], ropacket.HeaderCZREQUESTMOVE)
	dest := packMoveDest(55, 111) // dest adjacent to B at {54,111}
	copy(moveReq[2:], dest[:])
	sendRaw(t, conn1, moveReq)

	// A: self-ack only (0x0087), NOT the walk (0x09fd) — A is the excluded mover.
	readTradeFrame(t, conn1, ropacket.HeaderZCNOTIFYPLAYERMOVE, 12)

	// B: the walk broadcast with A's GID at offset [9:13].
	walk := readTradeFrame(t, conn2, ropacket.HeaderZCUNITWALKING, 114)
	if got := binary.LittleEndian.Uint32(walk[9:13]); got != 150001 {
		t.Fatalf("walk broadcast GID = %d, want mover A's GID 150001", got)
	}

	// 3. A drops an item. B's conn sees the floor item land (ZC_ITEM_FALL_ENTRY);
	//    A's own conn gets the throw-ack+fall burst (not asserted here).
	dropReq := make([]byte, 6)
	binary.LittleEndian.PutUint16(dropReq[0:], ropacket.HeaderCZDROPITEM0363)
	binary.LittleEndian.PutUint16(dropReq[2:], 1) // inventory index (1-based)
	binary.LittleEndian.PutUint16(dropReq[4:], 1) // amount
	sendRaw(t, conn1, dropReq)

	drop := readTradeFrame(t, conn2, ropacket.HeaderZCItemFallEntry, 24)
	if got := binary.LittleEndian.Uint32(drop[6:10]); got != 501 {
		t.Fatalf("drop broadcast nameID = %d, want Red Potion 501", got)
	}
}
