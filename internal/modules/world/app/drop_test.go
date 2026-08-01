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

// dropFixture assembles the DropService collaborators plus a dropper standing at
// (100,100) on "prontera" whose capture conn buffers every SELF + AREA frame. The
// dropper is registered as an AOI EntityPlayer at its cell so the AREA fall-entry
// broadcast the success path emits reaches it.
type dropFixture struct {
	svc        *app.DropService
	floorItems *domain.FloorItemRegistry
	players    *domain.PlayerRegistry
	mp         *domain.Map
	repo       *infra.MemoryInventoryRepository
	dropper    *domain.Player
	conn       *captureConn
}

// newDropFixture builds the dropper and service. repo may be nil to exercise the
// no-inventory rejection path; itemDB may be nil to exercise the nil-item_db
// fallback. The map store always carries "prontera" so the in-range path loads.
func newDropFixture(t *testing.T, repo *infra.MemoryInventoryRepository, itemDB *itemdb.Registry) dropFixture {
	t.Helper()
	floorItems := domain.NewFloorItemRegistry()
	players := domain.NewPlayerRegistry()
	mp := newTestMap(200, 200)
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": mp}}

	conn := &captureConn{role: gwdomain.RoleMap, remote: "dropper"}
	dropper := &domain.Player{
		Conn:      conn,
		EntityID:  aoi.EntityID(pickAccID),
		AccountID: pickAccID,
		CharID:    pickCharID,
		MapName:   "prontera",
		PosX:      100,
		PosY:      100,
	}
	require.NoError(t, players.Register(dropper))
	require.NoError(t, mp.AOI.AddEntity(&aoi.Entity{
		ID: aoi.EntityID(pickAccID), Type: aoi.EntityPlayer, X: 100, Y: 100,
	}))

	svc := app.NewDropService(floorItems, players, maps, repo, itemDB)
	return dropFixture{
		svc: svc, floorItems: floorItems, players: players, mp: mp,
		repo: repo, dropper: dropper, conn: conn,
	}
}

// seedBagItem adds one bag row for the dropper's char and returns its Index (the
// client-visible 0-based slot the drop request carries). The repo is the memory
// implementation so the row lands at slot 0 for a fresh char.
func seedBagItem(t *testing.T, f dropFixture, nameID uint32, amount uint32) uint16 {
	t.Helper()
	row, err := f.repo.AddItem(context.Background(), pickAccID, pickCharID, invdomain.NewItem{
		NameID: nameID, Amount: amount, Stackable: false,
	})
	require.NoError(t, err)
	return row.Index
}

// expectThrowAck encodes the success ZC_ITEM_THROW_ACK the drop path should emit,
// built independently so a drift in Index/Count surfaces as a mismatch.
func expectThrowAck(t *testing.T, index, count uint16) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (&packet.ItemThrowAckResponse{Index: index, Count: count}).Encode(&buf))
	return buf.Bytes()
}

// expectFallEntry encodes the ZC_ITEM_FALL_ENTRY area broadcast the drop path
// should emit for the given floor item, built independently of the service.
func expectFallEntry(t *testing.T, id uint32, nameID uint32, itemType uint16, x, y int16, amount uint16) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (&packet.ItemFallEntryResponse{
		ID:             id,
		NameID:         nameID,
		Type:           itemType,
		Identified:     0,
		X:              uint16(x), //nolint:gosec // G115: test cell fits uint16
		Y:              uint16(y), //nolint:gosec // G115: test cell fits uint16
		Amount:         amount,
		ShowDropEffect: 0,
		DropEffectMode: 0,
	}).Encode(&buf))
	return buf.Bytes()
}

// TestDropService_Success_DropsOneAndBroadcasts exercises the happy path: a bag
// row at slot 0 drops 1 Jellopy, the bag row disappears, a floor item is
// registered on the dropper's cell, the dropper gets the throw ack (index + count
// echoing its own request) and — because it is inside its own AOI — the area
// fall-entry broadcast.
func TestDropService_Success_DropsOneAndBroadcasts(t *testing.T) {
	t.Parallel()
	f := newDropFixture(t, infra.NewMemoryInventoryRepository(), dropItemDB(t))
	slot := seedBagItem(t, f, 909, 1) // Jellopy
	assert.Equal(t, uint16(0), slot, "fresh char's first bag row is slot 0")

	require.NoError(t, f.svc.Drop(context.Background(), pickAccID, slot, 1))

	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 2, "throw ack + fall entry on a successful drop")
	// The floor item EntityID is allocated from the registry allocator; read it
	// back so the expected fall-entry frame can use the exact id.
	fi, ok := f.floorItems.ByEntity(aoi.EntityID(frames[0][2:6][0]))
	if !ok {
		// Fall-entry is emitted second; find the registered floor item directly.
		fi, ok = f.floorItems.ByEntity(aoi.EntityID(domain.FloorItemIDBase))
		require.True(t, ok, "drop registered a floor item")
	}
	assert.Equal(t, expectThrowAck(t, slot, 1), frames[1], "throw ack echoes the raw client slot + amount")
	assert.Equal(t, expectFallEntry(t, uint32(fi.EntityID), 909, itemdb.WireType("Etc"), 100, 100, 1), frames[0],
		"fall entry broadcast carries the dropper's cell + item detail")

	bag, err := f.repo.LoadByChar(context.Background(), pickAccID, pickCharID)
	require.NoError(t, err)
	assert.Len(t, bag, 0, "the dropped row left the bag")
}

// TestDropService_Success_PartialStackDropsAmount exercises the stacked case:
// a slot holding 5 Jellopies drops 2, leaving 3 in the bag and a floor item of
// amount 2 on the ground.
func TestDropService_Success_PartialStackDropsAmount(t *testing.T) {
	t.Parallel()
	f := newDropFixture(t, infra.NewMemoryInventoryRepository(), dropItemDB(t))
	slot := seedBagItem(t, f, 909, 5)

	require.NoError(t, f.svc.Drop(context.Background(), pickAccID, slot, 2))

	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 2, "throw ack + fall entry on a partial-stack drop")
	assert.Equal(t, expectThrowAck(t, slot, 2), frames[1])
	assert.Equal(t, expectFallEntry(t, uint32(domain.FloorItemIDBase), 909, itemdb.WireType("Etc"), 100, 100, 2), frames[0])

	bag, err := f.repo.LoadByChar(context.Background(), pickAccID, pickCharID)
	require.NoError(t, err)
	require.Len(t, bag, 1, "the stack row remains")
	assert.Equal(t, uint32(3), bag[0].Amount, "3 of the 5 Jellopies remain")
}

// TestDropService_Fail_BadSlotAcksZero asserts the invalid-index path: an unknown
// slot yields the throw ack with count=0 (rAthena's "client does not like being
// ignored"), no floor item is registered, and the bag is untouched.
func TestDropService_Fail_BadSlotAcksZero(t *testing.T) {
	t.Parallel()
	f := newDropFixture(t, infra.NewMemoryInventoryRepository(), dropItemDB(t))
	seedBagItem(t, f, 909, 3)

	require.NoError(t, f.svc.Drop(context.Background(), pickAccID, 99, 1))

	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 1, "only the reject ack")
	assert.Equal(t, expectThrowAck(t, 0, 0), frames[0])
	assert.Equal(t, 0, f.floorItems.Len(), "no floor item on a rejected drop")
	bag, err := f.repo.LoadByChar(context.Background(), pickAccID, pickCharID)
	require.NoError(t, err)
	assert.Len(t, bag, 1, "bag untouched on a rejected drop")
}

// TestDropService_Fail_QtyBeyondStackAcksZero asserts the over-drop path: asking
// for more than the stack holds is a rejection (count=0), not a partial drop.
func TestDropService_Fail_QtyBeyondStackAcksZero(t *testing.T) {
	t.Parallel()
	f := newDropFixture(t, infra.NewMemoryInventoryRepository(), dropItemDB(t))
	seedBagItem(t, f, 909, 2)

	require.NoError(t, f.svc.Drop(context.Background(), pickAccID, 0, 5))

	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 1, "only the reject ack")
	assert.Equal(t, expectThrowAck(t, 0, 0), frames[0])
	bag, err := f.repo.LoadByChar(context.Background(), pickAccID, pickCharID)
	require.NoError(t, err)
	assert.Len(t, bag, 1, "bag untouched when qty exceeds the stack")
}

// TestDropService_NoSession_SilentDrop asserts the no-session path resolves: a
// request with no live player writes nothing — there is no conn to ack to.
func TestDropService_NoSession_SilentDrop(t *testing.T) {
	t.Parallel()
	f := newDropFixture(t, infra.NewMemoryInventoryRepository(), dropItemDB(t))

	const unknownAcc uint32 = 9999999
	require.NoError(t, f.svc.Drop(context.Background(), unknownAcc, 0, 1))

	assert.Empty(t, f.conn.buf.Bytes(), "no ack for a session-less request")
	assert.Equal(t, 0, f.floorItems.Len(), "no floor item for a session-less drop")
}
