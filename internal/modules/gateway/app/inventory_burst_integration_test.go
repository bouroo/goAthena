//go:build integration

package app_test

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	charinfra "github.com/bouroo/goAthena/internal/modules/character/infra"
	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	ropacket "github.com/bouroo/goAthena/pkg/ro/packet"
)

// The Phase-42 L3 proofs. Three things are asserted together because they are
// one behaviour: the LoadEndAck burst carries the character's real inventory,
// every entry's wire index is in CLIENT index space (server row + 2), and the
// verb that consumes that index resolves the SAME row the burst advertised.
//
// The pre-Phase-42 suite could not see the index convention at all: every test
// transmitted client index 1, and with the old -1 resolution that happened to
// be a valid row. TestMap_ClientIndexAddressesSameRow is the anchor that makes
// the convention observable, so a future off-by-one cannot pass both the burst
// and the verb.

// wireEntry describes one parsed inventory list entry, keeping only the fields
// these tests assert on.
type wireEntry struct {
	index uint16
	itid  uint16
	typ   uint8
	count uint16
	// location is only populated for equip-list entries.
	location uint32
	// sprite is only populated for equip-list entries.
	sprite uint16
}

// entryStride returns the per-entry byte width for the given list opcode.
func entryStride(cmd uint16) int {
	if cmd == ropacket.HeaderZCINVENTORYITEMLISTEQUIP {
		return 57
	}
	return 26
}

// readInventoryList reads one ZC_INVENTORY_ITEMLIST_* frame and returns its
// entries. The frame is [2 cmd][2 len][1 invType][26 or 57 per entry].
func readInventoryList(t *testing.T, r io.Reader) (uint16, []wireEntry) {
	t.Helper()
	hdr := make([]byte, 5)
	if _, err := io.ReadFull(r, hdr); err != nil {
		t.Fatalf("read inventory list header: %v", err)
	}
	cmd := binary.LittleEndian.Uint16(hdr[0:2])
	if cmd != ropacket.HeaderZCINVENTORYITEMLISTNORMAL && cmd != ropacket.HeaderZCINVENTORYITEMLISTEQUIP {
		t.Fatalf("inventory list cmd = 0x%04x, want 0x0b09 or 0x0b39", cmd)
	}
	total := int(binary.LittleEndian.Uint16(hdr[2:4]))
	stride := entryStride(cmd)
	body := total - 5
	if body < 0 || body%stride != 0 {
		t.Fatalf("inventory list len = %d: body %d not a multiple of %d", total, body, stride)
	}
	raw := make([]byte, body)
	if _, err := io.ReadFull(r, raw); err != nil {
		t.Fatalf("read inventory list body: %v", err)
	}
	entries := make([]wireEntry, 0, body/stride)
	for off := 0; off < body; off += stride {
		e := wireEntry{
			index: binary.LittleEndian.Uint16(raw[off : off+2]),
			itid:  binary.LittleEndian.Uint16(raw[off+2 : off+4]),
			typ:   raw[off+4],
			count: binary.LittleEndian.Uint16(raw[off+5 : off+7]),
		}
		if stride == 57 {
			// EQUIPITEM_INFO: index(2) ITID(2) type(1) location(4 @5) ...
			// sprite sits after wearState(4)+refine(1)+card(8)+hire(4)+bind(2).
			e.location = binary.LittleEndian.Uint32(raw[off+5 : off+9])
			e.sprite = binary.LittleEndian.Uint16(raw[off+28 : off+30])
		}
		entries = append(entries, e)
	}
	return cmd, entries
}

// prepareInventoryTestClient enters the map and consumes ZC_ACCEPT_ENTER,
// leaving the connection positioned at the start of the LoadEndAck burst.
func prepareInventoryTestClient(t *testing.T, port int) (net.Conn, mapTestEnv) {
	t.Helper()
	sessions := charinfra.NewMemorySessionStore()
	_ = sessions.PutSession(t.Context(), chardomain.Session{
		AccountID: 2000001, LoginID1: 0x11111111, LoginID2: 0x22222222, Sex: 1,
	})
	ms, env := buildTestMapDeps(t, sessions)
	conn := startAndDial(t, ms, port)
	sendCZEnter(t, conn, 2000001, 150001, 0x11111111)
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	awaitAcceptEnter(t, conn)
	return conn, env
}

// sendLoadEndAck writes CZ_NOTIFY_ACTORINIT (0x007d, cmd-only).
func sendLoadEndAck(t *testing.T, c io.Writer) {
	t.Helper()
	if _, err := c.Write([]byte{0x7d, 0x00}); err != nil {
		t.Fatalf("send LoadEndAck: %v", err)
	}
}

// TestMap_EnterShowsOwnedInventory proves the LoadEndAck burst carries the
// character's real inventory rows: two bag items in the NORMAL list and one
// worn item in the EQUIP list, each with the item_db-derived wire type and the
// client-space index.
func TestMap_EnterShowsOwnedInventory(t *testing.T) {
	port := freePort(t)
	conn, env := prepareInventoryTestClient(t, port)
	defer conn.Close()

	// Seed three rows in a known order: Red Potion (501, healing, x5),
	// Knife (1201, weapon x1) and a Big Knife (1201 x1) that we then wear.
	// Row order is the repo's id order, which the burst's indices must match.
	if _, err := env.itemRepo.Add(t.Context(), 150001, 501, 5); err != nil {
		t.Fatalf("seed red potion: %v", err)
	}
	knife, err := env.itemRepo.Add(t.Context(), 150001, 1201, 1)
	if err != nil {
		t.Fatalf("seed knife: %v", err)
	}
	knife2, err := env.itemRepo.Add(t.Context(), 150001, 1201, 1)
	if err != nil {
		t.Fatalf("seed second knife: %v", err)
	}
	// Wear the second knife (row 2) in the right hand so the EQUIP list is
	// non-empty and carries the location bitmask.
	if err := env.itemRepo.SetEquip(t.Context(), knife2.ID, 0x2); err != nil {
		t.Fatalf("equip second knife: %v", err)
	}
	_ = knife

	sendLoadEndAck(t, conn)

	// ZC_INVENTORY_START (6 bytes) precedes the lists.
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, make([]byte, 6)); err != nil {
		t.Fatalf("drain inventory start: %v", err)
	}

	cmd, normal := readInventoryList(t, conn)
	if cmd != ropacket.HeaderZCINVENTORYITEMLISTNORMAL {
		t.Fatalf("first list cmd = 0x%04x, want NORMAL (0x0b09)", cmd)
	}
	if len(normal) != 2 {
		t.Fatalf("NORMAL entries = %d, want 2 (the two unworn rows)", len(normal))
	}
	// Row 0 = Red Potion: client index 2, IT_HEALING=0, count 5.
	if normal[0].index != 2 || normal[0].itid != 501 || normal[0].count != 5 || normal[0].typ != 0 {
		t.Errorf("normal[0] = %+v, want {index:2 itid:501 count:5 type:0}", normal[0])
	}
	// Row 1 = Knife: client index 3, IT_WEAPON=5.
	if normal[1].index != 3 || normal[1].itid != 1201 || normal[1].typ != 5 {
		t.Errorf("normal[1] = %+v, want {index:3 itid:1201 type:5}", normal[1])
	}

	cmd, equipped := readInventoryList(t, conn)
	if cmd != ropacket.HeaderZCINVENTORYITEMLISTEQUIP {
		t.Fatalf("second list cmd = 0x%04x, want EQUIP (0x0b39)", cmd)
	}
	if len(equipped) != 1 {
		t.Fatalf("EQUIP entries = %d, want 1", len(equipped))
	}
	// Row 2 = the worn knife: client index 4, right-hand location bit.
	if equipped[0].index != 4 || equipped[0].itid != 1201 || equipped[0].location != 0x2 {
		t.Errorf("equipped[0] = %+v, want {index:4 itid:1201 location:2}", equipped[0])
	}
}

// TestMap_ClientIndexAddressesSameRow is the convention anchor: the client
// index the burst advertises for row N must resolve, through the real consume
// verb, to exactly row N. Three distinct items are seeded so picking the wrong
// row is observable rather than coincidentally correct.
func TestMap_ClientIndexAddressesSameRow(t *testing.T) {
	port := freePort(t)
	conn, env := prepareInventoryTestClient(t, port)
	defer conn.Close()

	// Three distinct usable items — rows 0/1/2 — so consuming the wrong row
	// is observable rather than coincidentally correct.
	if _, err := env.itemRepo.Add(t.Context(), 150001, 501, 1); err != nil { // row 0
		t.Fatalf("seed row 0: %v", err)
	}
	if _, err := env.itemRepo.Add(t.Context(), 150001, 502, 1); err != nil { // row 1
		t.Fatalf("seed row 1: %v", err)
	}
	third, err := env.itemRepo.Add(t.Context(), 150001, 503, 1) // row 2 (usable)
	if err != nil {
		t.Fatalf("seed row 2: %v", err)
	}

	// Advertise row 2 with its client index per the codec convention.
	clientIdx := ropacket.ClientIndex(2)
	// Deliberately NOT fatal: the pinned-value check lives in the packet unit
	// test, so this test must fail on row identity instead of on a constant.
	if clientIdx != 4 {
		t.Errorf("ClientIndex(2) = %d, want 4", clientIdx)
	}

	// CZ_USE_ITEM2 (0x0439, 8B): cmd + index + AID. Send the index the client
	// would have learned for row 2.
	req := make([]byte, 8)
	binary.LittleEndian.PutUint16(req[0:], ropacket.HeaderCZUSEITEM2)
	binary.LittleEndian.PutUint16(req[2:], clientIdx)
	binary.LittleEndian.PutUint32(req[4:], 150001)
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("send CZ_USE_ITEM2: %v", err)
	}

	// The consumed row must be row 2 (item 503), not row 0 or 1.
	deadline := time.Now().Add(3 * time.Second)
	var consumed bool
	for time.Now().Before(deadline) {
		items, lerr := env.itemRepo.LoadByChar(t.Context(), 2000001, 150001)
		if lerr != nil {
			t.Fatalf("load inventory: %v", lerr)
		}
		gone := true
		for _, it := range items {
			if it.ID == third.ID {
				gone = false
			}
		}
		if gone {
			consumed = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !consumed {
		items, _ := env.itemRepo.LoadByChar(t.Context(), 2000001, 150001)
		t.Fatalf("row 2 (item 503, id %d) was not consumed; inventory still %+v — the client index did not resolve to the advertised row", third.ID, items)
	}
	// Rows 0 and 1 must be untouched: a wrong resolution would have eaten one.
	items, err := env.itemRepo.LoadByChar(t.Context(), 2000001, 150001)
	if err != nil {
		t.Fatalf("load inventory after use: %v", err)
	}
	remaining := map[uint32]int{}
	for _, it := range items {
		remaining[it.NameID] += int(it.Amount)
	}
	if remaining[501] != 1 || remaining[502] != 1 {
		t.Errorf("rows 0/1 disturbed: 501=%d 502=%d, want 1 each (only row 2 should be consumed)", remaining[501], remaining[502])
	}
}

// TestMap_EquipAckCarriesItemDBView proves the equip ack resolves the item_db
// view sprite for a visible equip position, and keeps it 0 for a non-visible
// one — mirroring rAthena's EQP_VISIBLE gate (clif.cpp:4316-4322). Both
// directions are asserted, so the test fails whether the sprite is always 0 or
// always written.
func TestMap_EquipAckCarriesItemDBView(t *testing.T) {
	port := freePort(t)
	conn, env := prepareInventoryTestClient(t, port)
	defer conn.Close()

	// Row 0: Hat (2201) — armor in Head_Top (EQP_HEAD_TOP, 0x100), View 77.
	// Row 1: Knife (1201) — weapon in the right hand, no View and a position
	// outside EQP_VISIBLE. The two rows give the positive and negative cases.
	hat, err := env.itemRepo.Add(t.Context(), 150001, 2201, 1)
	if err != nil {
		t.Fatalf("seed hat: %v", err)
	}
	if _, err := env.itemRepo.Add(t.Context(), 150001, 1201, 1); err != nil {
		t.Fatalf("seed knife: %v", err)
	}

	// Positive: wear the hat into the head-top slot. Client index for row 0.
	if ack := wearAndReadAck(t, conn, 0, 0x100); binary.LittleEndian.Uint16(ack[8:10]) != 77 {
		t.Errorf("hat ack sprite = %d, want 77 (item_db View for a visible head item)", binary.LittleEndian.Uint16(ack[8:10]))
	} else if binary.LittleEndian.Uint16(ack[2:4]) != ropacket.ClientIndex(0) {
		t.Errorf("hat ack index = %d, want the client index %d echoed back", binary.LittleEndian.Uint16(ack[2:4]), ropacket.ClientIndex(0))
	}
	_ = hat

	// Negative: wear the knife into the right hand — not in EQP_VISIBLE, so the
	// sprite stays 0 even though the ack is otherwise successful.
	ack := wearAndReadAck(t, conn, 1, 0x2)
	if got := binary.LittleEndian.Uint16(ack[8:10]); got != 0 {
		t.Errorf("knife ack sprite = %d, want 0 (right hand is not EQP_VISIBLE)", got)
	}
	if got := binary.LittleEndian.Uint16(ack[2:4]); got != ropacket.ClientIndex(1) {
		t.Errorf("knife ack index = %d, want %d", got, ropacket.ClientIndex(1))
	}
}

// wearAndReadAck sends CZ_REQ_WEAR_EQUIP_V5 for the given server row into
// position and returns the 11-byte ZC_ACK_WEAR_EQUIP_V5 ack
// (cmd(2) index(2) location(4) sprite(2) result(1)).
func wearAndReadAck(t *testing.T, conn net.Conn, serverRow int, position uint32) []byte {
	t.Helper()
	req := make([]byte, 8)
	binary.LittleEndian.PutUint16(req[0:], ropacket.HeaderCZREQWEAREQUIPV5)
	binary.LittleEndian.PutUint16(req[2:], ropacket.ClientIndex(uint16(serverRow))) //nolint:gosec // G115: test-local row index
	binary.LittleEndian.PutUint32(req[4:], position)
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("send CZ_REQ_WEAR_EQUIP_V5: %v", err)
	}
	ack := make([]byte, 11)
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(conn, ack); err != nil {
		t.Fatalf("read equip ack: %v", err)
	}
	if got := binary.LittleEndian.Uint16(ack[0:2]); got != 0x0999 {
		t.Fatalf("ack cmd = 0x%04x, want 0x0999 (ZC_ACK_WEAR_EQUIP_V5)", got)
	}
	return ack
}

// TestMemoryItemRepoOrdersByID pins the ordering the index convention depends
// on. A map-iteration order would let a client index resolve to a different row
// between the burst and the consume verb.
func TestMemoryItemRepoOrdersByID(t *testing.T) {
	port := freePort(t)
	_, env := prepareInventoryTestClient(t, port)

	// Insert in a deliberately non-monotonic NameID order; the repo must return
	// rows in id order regardless, because the row id is what the wire index is
	// an offset into. (Row ids are assigned in insertion order, so the expected
	// result is exactly this insertion sequence — which a map-iteration order
	// would scramble.)
	insertOrder := []uint32{503, 501, 505, 502, 504}
	for _, id := range insertOrder {
		if _, err := env.itemRepo.Add(t.Context(), 150001, id, 1); err != nil {
			t.Fatalf("seed %d: %v", id, err)
		}
	}
	items, err := env.itemRepo.LoadByChar(t.Context(), 2000001, 150001)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(items) != len(insertOrder) {
		t.Fatalf("rows = %d, want %d", len(items), len(insertOrder))
	}
	// Rows ascend by id...
	for i := 1; i < len(items); i++ {
		if items[i-1].ID >= items[i].ID {
			t.Fatalf("rows not ascending by id: %v", []invdomain.ItemID{items[i-1].ID, items[i].ID})
		}
	}
	// ...and the id order reproduces the insertion order, which a map-iteration
	// order would not. Repeat the load: an unstable order can pass once by luck.
	for attempt := 0; attempt < 8; attempt++ {
		again, lerr := env.itemRepo.LoadByChar(t.Context(), 2000001, 150001)
		if lerr != nil {
			t.Fatalf("reload: %v", lerr)
		}
		for i, want := range insertOrder {
			if again[i].NameID != want {
				t.Fatalf("attempt %d: row %d NameID = %d, want %d (row order is not stable)", attempt, i, again[i].NameID, want)
			}
		}
	}
}
