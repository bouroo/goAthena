//go:build integration

package app_test

// End-to-end proof for the warehouse open path: the source opens the kafra
// warehouse from the NPC script — BUILDIN_FUNC(openstorage)
// (third_party/rathenaThailand/src/map/script.cpp:10958) calls
// storage_storageopen, which sends the two item lists and then the item count
// (third_party/rathenaThailand/src/map/storage.cpp:147-148). There is no client
// request that opens the warehouse.
//
// The path under test: script `openstorage` → content ScriptHost → the
// content domain's ScriptStorage port → the gateway's MapServer.OpenStorage →
// the warehouse-open burst on the player's connection.

import (
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	charinfra "github.com/bouroo/goAthena/internal/modules/character/infra"
	storageapp "github.com/bouroo/goAthena/internal/modules/commerce/storage/app"
	storageinfra "github.com/bouroo/goAthena/internal/modules/commerce/storage/infra"
	contentapp "github.com/bouroo/goAthena/internal/modules/content/app"
	contentinfra "github.com/bouroo/goAthena/internal/modules/content/infra"
	gwapp "github.com/bouroo/goAthena/internal/modules/gateway/app"
	invapp "github.com/bouroo/goAthena/internal/modules/inventory/app"
	invinfra "github.com/bouroo/goAthena/internal/modules/inventory/infra"
	worldapp "github.com/bouroo/goAthena/internal/modules/world/app"
	worlddomain "github.com/bouroo/goAthena/internal/modules/world/domain"
	worldinfra "github.com/bouroo/goAthena/internal/modules/world/infra"
	"github.com/bouroo/goAthena/pkg/ro/mobdb"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
	"github.com/bouroo/goAthena/pkg/ro/script"
)

// kafraScript is the NPC script under test: it opens the player's warehouse.
// The header's name field ("kafra") is the NPC name the store registers.
const kafraScript = "-\tscript\tkafra\t-1,{\n\topenstorage;\n\tclose;\n}\n"

// kafraNPCGID is the NPC entity id the test clicks.
const kafraNPCGID = 900001

// kafraAccountID is the account whose warehouse is opened (the seeded player).
const kafraAccountID = 2000001

// kafraCharID is the player's entity id.
const kafraCharID = 150001

// TestMap_OpenStorageScriptOpensWarehouse proves the source's script-triggered
// warehouse open end-to-end over a real TCP connection: the player enters,
// clicks the kafra NPC, the NPC script's `openstorage` builtin reaches the
// gateway's storage action, and the player receives the warehouse-open burst —
// the item lists followed by ZC_NOTIFY_STOREITEM_COUNTINFO (the source sends
// the lists then the count, storage.cpp:147-148).
func TestMap_OpenStorageScriptOpensWarehouse(t *testing.T) {
	port := freePort(t)
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: kafraAccountID, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1,
	})
	ms := startStorageMapServer(t, sessions)

	conn := startAndDial(t, ms, port)
	defer conn.Close()
	sendCZEnter(t, conn, kafraAccountID, kafraCharID, 0x11111111)
	// The map-enter reply is ZC_ACCEPT_ENTER; drain it so the warehouse
	// frames are the only ones the scan has to skip.
	awaitAcceptEnter(t, conn)

	// Click the kafra NPC: CZ_CONTACT_NPC (0x0090) with the NPC's AID.
	sendContactNPC(t, conn, kafraNPCGID)

	// The warehouse burst: the two item lists (ZC_STORE_ITEMLIST_NORMAL
	// 0x0b09, ZC_STORE_ITEMLIST_EQUIP 0x0b39) then
	// ZC_NOTIFY_STOREITEM_COUNTINFO (0x00f2).
	scanServerFrame(t, conn, ropacket.HeaderZCSTOREITEMLISTNORMAL(), 3*time.Second)
	scanServerFrame(t, conn, ropacket.HeaderZCSTOREITEMLISTEQUIP(), 3*time.Second)
	// The count notice (ZC_NOTIFY_STOREITEM_COUNTINFO, 6 bytes) is not in the
	// map-server packet DB, so the harness reader cannot frame it; read its six
	// bytes directly.
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	countFrame := make([]byte, 6)
	if _, err := io.ReadFull(conn, countFrame); err != nil {
		t.Fatalf("read warehouse count notice: %v", err)
	}
	if got := binary.LittleEndian.Uint16(countFrame[0:]); got != ropacket.HeaderZCNOTIFYSTOREITEMCOUNTINFO() {
		t.Fatalf("warehouse count notice header = 0x%04x, want 0x%04x", got, ropacket.HeaderZCNOTIFYSTOREITEMCOUNTINFO())
	}
	if got := binary.LittleEndian.Uint16(countFrame[2:]); got != 0 {
		t.Errorf("warehouse item count = %d, want 0 (empty warehouse)", got)
	}
}

// startStorageMapServer builds a MapServer whose content engine runs the
// kafra script and whose storage service is wired, the way the DI root wires
// them: the gateway owns the connection and the storage frames, and hands the
// content module its ScriptStorage port.
func startStorageMapServer(t *testing.T, sessions *charinfra.MemorySessionStore) *gwapp.MapServer {
	t.Helper()
	wrepo := worldinfra.NewMemoryWorldRepository(worlddomain.Entity{
		ID: kafraCharID, Account: kafraAccountID, Map: "new_1-1",
		Pos: worlddomain.Position{X: 53, Y: 111}, Sex: 1, Job: 0, Level: 1,
		Name: "Hero", HP: 1000, MaxHP: 1000, SP: 100, MaxSP: 100, Speed: 150,
	})
	world := worldapp.NewWorldService(wrepo, slog.Default(), 50)
	mobs, err := mobDB()
	if err != nil {
		t.Fatalf("load mob_db: %v", err)
	}
	spawn := worldapp.NewSpawnService(world, mobs, nil)
	itemRepo := invinfra.NewMemoryItemRepository()
	inv := invapp.NewInventoryService(itemRepo)
	items := testItemDB(t)
	equipSvc := worldapp.NewEquipService(inv, items)
	itemUse := worldapp.NewItemUseService(inv, items, world)
	combat := worldapp.NewCombatService(world, mobs, equipSvc)
	mobAI := worldapp.NewMobAIService(world, mobs, combat, slog.Default())
	skills := worldapp.NewSkillService(world, combat, testSkillDB())
	skills.SetTree(testSkillTree())

	// The content engine runs the kafra script: the NPC store maps the NPC's
	// GID to the script name, the compiled set holds the script.
	scripts, err := script.Compile([]byte(kafraScript))
	if err != nil {
		t.Fatalf("compile kafra script: %v", err)
	}
	npcs := contentinfra.NewMemoryNPCStore()
	npcs.Register(kafraNPCGID, "kafra")
	content := contentapp.NewEngine(scripts, npcs, nil, nil, nil, nil, slog.Default())

	ms, err := gwapp.NewMapServer(world, spawn, combat, mobAI, equipSvc, itemUse, inv, content, skills, nil, nil, nil, sessions, testSkillDB(), slog.Default())
	if err != nil {
		t.Fatalf("new map server: %v", err)
	}
	ms.SetItemDB(items)

	// The storage service: the world's orchestrator over the commerce/storage
	// service and the inventory service (the same shape the DI root builds).
	sto := storageapp.NewStorageService(storageinfra.NewMemoryStorageRepository())
	ms.SetStorage(worldapp.NewStorageService(inv, sto))

	// The gateway hands the content module its ScriptStorage port, exactly as
	// the DI root does (gateway/di.go).
	content.SetStorage(ms)
	return ms
}

// mobDB loads the harness's mob_db fixture.
func mobDB() (*mobdb.Registry, error) {
	return mobdb.Load(strings.NewReader(testMobAIFixture))
}

// sendContactNPC writes a CZ_CONTACT_NPC frame naming the NPC's AID.
func sendContactNPC(t *testing.T, c net.Conn, npcGID uint32) {
	t.Helper()
	frame := make([]byte, 7)
	binary.LittleEndian.PutUint16(frame[0:], ropacket.HeaderCZCONTACTNPC())
	binary.LittleEndian.PutUint32(frame[2:], npcGID)
	sendRaw(t, c, frame)
}
