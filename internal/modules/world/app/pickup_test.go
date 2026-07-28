//go:build unit

package app_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	"github.com/bouroo/goAthena/internal/modules/inventory/infra"
	"github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

const (
	pickAccID  uint32 = 2000700
	pickCharID uint32 = 150002
	// pickClock is the deterministic server tick broadcastPickupAct stamps the
	// ZC_NOTIFY_ACT pickup frame with. A non-zero value asserts the exact byte
	// rather than the clock source.
	pickClock uint32 = 0x11223344
)

// pickupFixture assembles the PickupService collaborators plus a picker standing
// at (100,100) on "prontera" whose capture conn buffers every SELF + AREA frame.
// The picker is registered as an AOI EntityPlayer at its cell so the AREA
// broadcasts (NotifyAct pickup, ItemDisappear) the success path emits reach it.
type pickupFixture struct {
	svc        *app.PickupService
	floorItems *domain.FloorItemRegistry
	players    *domain.PlayerRegistry
	mp         *domain.Map
	repo       *infra.MemoryInventoryRepository
	picker     *domain.Player
	conn       *captureConn
}

// newPickupFixture builds the picker and service. repo may be nil to exercise the
// no-inventory fail-ack path; itemDB may be nil to exercise the nil-item_db
// fallback. The map store always carries "prontera" so the in-range path loads.
func newPickupFixture(t *testing.T, repo *infra.MemoryInventoryRepository, itemDB *itemdb.Registry) pickupFixture {
	t.Helper()
	floorItems := domain.NewFloorItemRegistry()
	players := domain.NewPlayerRegistry()
	mp := newTestMap(200, 200)
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": mp}}

	conn := &captureConn{role: gwdomain.RoleMap, remote: "picker"}
	picker := &domain.Player{
		Conn:      conn,
		EntityID:  aoi.EntityID(pickAccID),
		AccountID: pickAccID,
		CharID:    pickCharID,
		MapName:   "prontera",
		PosX:      100,
		PosY:      100,
	}
	require.NoError(t, players.Register(picker))
	require.NoError(t, mp.AOI.AddEntity(&aoi.Entity{
		ID: aoi.EntityID(pickAccID), Type: aoi.EntityPlayer, X: 100, Y: 100,
	}))

	var items invdomain.InventoryRepository
	if repo != nil {
		items = repo
	}
	svc := app.NewPickupService(floorItems, players, maps, items, itemDB, fixedClock{pickClock})
	return pickupFixture{
		svc: svc, floorItems: floorItems, players: players, mp: mp,
		repo: repo, picker: picker, conn: conn,
	}
}

// placeFloorItem registers a dropped item on the given map/cell and returns its
// allocated EntityID (the CZ_ITEM_PICKUP ground id the client sends back).
func placeFloorItem(t *testing.T, f pickupFixture, mapName string, x, y int16, nameID uint32, itemType uint16, amount uint16) aoi.EntityID {
	t.Helper()
	id := f.floorItems.NextEntityID()
	require.NoError(t, f.floorItems.Register(&domain.FloorItem{
		EntityID: id, MapName: mapName, NameID: nameID,
		Type: itemType, Amount: amount, PosX: x, PosY: y,
	}))
	return id
}

// expectPickupAckSuccess encodes the success ZC_ITEM_PICKUP_ACK (result=0), built
// independently so a drift in Index/Count/NameID/Type surfaces as a mismatch.
// Mirrors sendPickupAck: the IT_* wire enum (uint16) narrows to the wire byte
// (uint8), an unidentified drop with no cards/options/look.
func expectPickupAckSuccess(t *testing.T, index uint16, count uint16, nameID uint32, itemType uint16) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (&packet.ItemPickupAckResponse{
		Index:        index,
		Count:        count,
		NameID:       nameID,
		IsIdentified: 0,
		Type:         uint8(itemType), //nolint:gosec // G115: IT_* enum fits a wire byte
		Result:       0,
	}).Encode(&buf))
	return buf.Bytes()
}

// expectPickupAckFail encodes the fail ZC_ITEM_PICKUP_ACK (result=6): clif_additem
// zeroes every field but result. Built independently so any stray field is caught.
func expectPickupAckFail(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (&packet.ItemPickupAckResponse{Result: 6}).Encode(&buf))
	return buf.Bytes()
}

// expectPickupNotifyAct encodes the ZC_NOTIFY_ACT pickup-animation frame, built
// independently so a drift in SrcID/TargetID/ServerTick/Type surfaces. Mirrors
// broadcastPickupAct: zero damage/amotion, Type=DMGPickup, Div=1.
func expectPickupNotifyAct(t *testing.T, srcID, targetID, serverTick uint32) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (packet.NotifyActResponse{
		SrcID:      srcID,
		TargetID:   targetID,
		ServerTick: serverTick,
		Type:       packet.DMGPickup,
		Div:        1,
	}).Encode(&buf))
	return buf.Bytes()
}

// expectPickupItemDisappear encodes the ZC_ITEM_DISAPPEAR (0x00a1) frame the
// success path broadcasts so the floor sprite is removed. AID = ground id.
func expectPickupItemDisappear(t *testing.T, aid uint32) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (&packet.ItemDisappearResponse{AID: aid}).Encode(&buf))
	return buf.Bytes()
}

// TestPickupService_Success_ThreeFrames is the M10a happy path: a picker within
// range (Chebyshev ≤ 2) picks up a Jellopy drop. Pickup credits the bag, then
// emits the verified 3-frame set — ack (SELF, result=0) + NotifyAct pickup
// (AREA) + ItemDisappear (AREA). The bag gains one Jellopy row and the floor
// item is unregistered (no double-credit on a racing request).
func TestPickupService_Success_ThreeFrames(t *testing.T) {
	t.Parallel()
	f := newPickupFixture(t, infra.NewMemoryInventoryRepository(), dropItemDB(t))

	// Jellopy at (101,100): Chebyshev distance 1 from the picker, in range.
	groundID := placeFloorItem(t, f, "prontera", 101, 100, 909, itemdb.WireType("Etc"), 1)

	require.NoError(t, f.svc.Pickup(context.Background(), pickAccID, uint32(groundID)))

	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 3, "ack + notify-act + disappear")
	assert.Equal(t, expectPickupAckSuccess(t, 0, 1, 909, itemdb.WireType("Etc")), frames[0],
		"byte-exact success ack: index 0, count 1, nameid 909, IT_ETC, result 0")
	assert.Equal(t, expectPickupNotifyAct(t, pickAccID, uint32(groundID), pickClock), frames[1],
		"byte-exact pickup NotifyAct: SrcID=picker, TargetID=groundID, Type=DMGPickup")
	assert.Equal(t, expectPickupItemDisappear(t, uint32(groundID)), frames[2],
		"byte-exact ItemDisappear for the same ground id")

	// The bag gained the Jellopy and the floor item is gone.
	bag, err := f.repo.LoadByChar(context.Background(), pickAccID, pickCharID)
	require.NoError(t, err)
	require.Len(t, bag, 1)
	assert.Equal(t, uint32(909), bag[0].NameID)
	assert.Equal(t, uint32(1), bag[0].Amount)
	assert.Equal(t, 0, f.floorItems.Len(), "floor item unregistered on success")
}

// TestPickupService_NoSession_SilentDrop mirrors clif_parse_TakeItem before the
// session resolves: a request with no live player is a silent drop — no frame is
// written, the bag is untouched, and Pickup returns nil (not a session fault).
func TestPickupService_NoSession_SilentDrop(t *testing.T) {
	t.Parallel()
	f := newPickupFixture(t, infra.NewMemoryInventoryRepository(), dropItemDB(t))
	groundID := placeFloorItem(t, f, "prontera", 101, 100, 909, itemdb.WireType("Etc"), 1)

	const unknownAcc uint32 = 9999999
	require.NoError(t, f.svc.Pickup(context.Background(), unknownAcc, uint32(groundID)))
	assert.Empty(t, f.conn.buf.Bytes(), "no ack for a session-less request")
	assert.Equal(t, 1, f.floorItems.Len(), "floor item untouched")
}

// TestPickupService_Fail_UnknownItem asserts the client-lock invariant: a ground
// id the floor registry does not know yields the fail ack (result=6) and nothing
// else — the client must always get a response or it can no longer pick items.
func TestPickupService_Fail_UnknownItem(t *testing.T) {
	t.Parallel()
	f := newPickupFixture(t, infra.NewMemoryInventoryRepository(), dropItemDB(t))

	require.NoError(t, f.svc.Pickup(context.Background(), pickAccID, uint32(domain.FloorItemIDBase)+12345))

	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 1, "only the fail ack")
	assert.Equal(t, expectPickupAckFail(t), frames[0])
}

// TestPickupService_Fail_WrongMap asserts the fitem->m != sd->m path
// (clif.cpp:12040) is a fail-ack, not a silent drop: an item id the client cached
// from another map answers result=6.
func TestPickupService_Fail_WrongMap(t *testing.T) {
	t.Parallel()
	f := newPickupFixture(t, infra.NewMemoryInventoryRepository(), dropItemDB(t))
	// Item on "geffen" while the picker is on "prontera"; cell would be in range.
	groundID := placeFloorItem(t, f, "geffen", 101, 100, 909, itemdb.WireType("Etc"), 1)

	require.NoError(t, f.svc.Pickup(context.Background(), pickAccID, uint32(groundID)))

	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 1, "only the fail ack")
	assert.Equal(t, expectPickupAckFail(t), frames[0])
	assert.Equal(t, 1, f.floorItems.Len(), "floor item untouched on wrong-map fail")
}

// TestPickupService_Fail_OutOfRange asserts pc_takeitem's check_distance_bl(2)
// (pc.cpp:6247) is a fail-ack: a Jellopy 10 cells away is too far to pick up.
func TestPickupService_Fail_OutOfRange(t *testing.T) {
	t.Parallel()
	f := newPickupFixture(t, infra.NewMemoryInventoryRepository(), dropItemDB(t))
	// (110,100) is Chebyshev distance 10 from the picker at (100,100) — beyond the
	// pickup range of 2 but inside the AOI radius, so only the range gate rejects.
	groundID := placeFloorItem(t, f, "prontera", 110, 100, 909, itemdb.WireType("Etc"), 1)

	require.NoError(t, f.svc.Pickup(context.Background(), pickAccID, uint32(groundID)))

	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 1, "only the fail ack")
	assert.Equal(t, expectPickupAckFail(t), frames[0])
}

// TestPickupService_Fail_BagFull asserts ADDITEM_FAIL (pc_additem full) maps to
// the fail ack: a non-stackable Knife pickup when the bag is at the slot cap
// yields result=6, the floor item stays, and no NotifyAct/Disappear is sent.
func TestPickupService_Fail_BagFull(t *testing.T) {
	t.Parallel()
	// Seed the bag to the slot cap with non-stackable rows; Knife (919) is the
	// Weapon the pickup tries to insert, so it cannot merge and hits the cap.
	var seeds []invdomain.InventoryItem
	for i := 0; i < invdomain.MaxInventorySlots; i++ {
		seeds = append(seeds, invdomain.InventoryItem{
			ID: uint32(i + 1), CharID: pickCharID, NameID: uint32(2000 + i), Amount: 1,
		})
	}
	f := newPickupFixture(t, infra.NewMemoryInventoryRepository(seeds...), dropItemDB(t))
	// Knife (Weapon) within range: a non-stackable pickup with no free slot.
	groundID := placeFloorItem(t, f, "prontera", 101, 100, 919, itemdb.WireType("Weapon"), 1)

	require.NoError(t, f.svc.Pickup(context.Background(), pickAccID, uint32(groundID)))

	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 1, "only the fail ack")
	assert.Equal(t, expectPickupAckFail(t), frames[0])
	assert.Equal(t, 1, f.floorItems.Len(), "floor item untouched on full-bag fail")
}

// TestPickupService_Fail_NoInventory asserts that even with no inventory context
// wired (a defensive nil), the client-lock still holds: an in-range pickup yields
// the fail ack rather than faulting or leaving the client waiting.
func TestPickupService_Fail_NoInventory(t *testing.T) {
	t.Parallel()
	f := newPickupFixture(t, nil, dropItemDB(t))
	groundID := placeFloorItem(t, f, "prontera", 101, 100, 909, itemdb.WireType("Etc"), 1)

	require.NoError(t, f.svc.Pickup(context.Background(), pickAccID, uint32(groundID)))

	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 1, "only the fail ack")
	assert.Equal(t, expectPickupAckFail(t), frames[0])
}

// TestPickupService_StackableMergesIntoExisting asserts a second Jellopy pickup
// merges into the existing stack (no new slot) and the ack carries the merged
// row's index (0) — the contract the CZ_REQ_WEAR_EQUIP/CZ_USE_ITEM reuse relies on.
func TestPickupService_StackableMergesIntoExisting(t *testing.T) {
	t.Parallel()
	f := newPickupFixture(t, infra.NewMemoryInventoryRepository(), dropItemDB(t))
	groundID := placeFloorItem(t, f, "prontera", 101, 100, 909, itemdb.WireType("Etc"), 1)

	require.NoError(t, f.svc.Pickup(context.Background(), pickAccID, uint32(groundID)))
	// Clear the conn so only the second pickup's frames are inspected.
	f.conn.buf.Reset()

	// Drop a second Jellopy and pick it up: merge into the existing index-0 stack.
	groundID2 := placeFloorItem(t, f, "prontera", 101, 100, 909, itemdb.WireType("Etc"), 1)
	require.NoError(t, f.svc.Pickup(context.Background(), pickAccID, uint32(groundID2)))

	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 3, "ack + notify-act + disappear on the merge")
	// Index stays 0 (merged into the first row), count is the new stack size of 2.
	assert.Equal(t, expectPickupAckSuccess(t, 0, 1, 909, itemdb.WireType("Etc")), frames[0],
		"merged ack keeps index 0")

	bag, err := f.repo.LoadByChar(context.Background(), pickAccID, pickCharID)
	require.NoError(t, err)
	require.Len(t, bag, 1, "merge produced no new slot")
	assert.Equal(t, uint32(2), bag[0].Amount, "stack grew to 2")
}
