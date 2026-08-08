//go:build unit

package app_test

import (
	"context"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	invdomain "github.com/bouroo/goAthena/internal/modules/inventory/domain"
	invinfra "github.com/bouroo/goAthena/internal/modules/inventory/infra"
	"github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// tradeItemDB builds a minimal item_db with two healing items (501 Red_Potion,
// 502 Apple) so AddItem can resolve type/location/look.
func tradeItemDB(t *testing.T) *itemdb.Registry {
	t.Helper()
	reg, err := itemdb.Load(strings.NewReader(`Header:
  Type: ITEM_DB
  Version: 3
Body:
  - Id: 501
    AegisName: Red_Potion
    Name: Red Potion
    Type: Healing
  - Id: 502
    AegisName: Apple
    Name: Apple
    Type: Healing
`))
	require.NoError(t, err)
	return reg
}

// s2Fixture seeds two online players (A at 100,100; B at 101,100) with a SHARED
// memory inventory (A owns two items at Index 0,1) + item_db, opens a trade
// (request→ack accept), and returns the live pieces the S2 tests inspect.
func s2Fixture(t *testing.T) (svc *app.TradeService, a, b *domain.Player, aConn, bConn *captureConn, repo *invinfra.MemoryInventoryRepository) {
	t.Helper()
	const aAID, bAID uint32 = 2000100, 2000101
	const aCID, bCID uint32 = 150100, 150101
	repo = invinfra.NewMemoryInventoryRepository(
		invdomain.InventoryItem{ID: 1, CharID: aCID, NameID: 501, Amount: 10},
		invdomain.InventoryItem{ID: 2, CharID: aCID, NameID: 502, Amount: 5},
	)
	registry := domain.NewPlayerRegistry()
	aConn = &captureConn{role: gwdomain.RoleMap, remote: "A"}
	bConn = &captureConn{role: gwdomain.RoleMap, remote: "B"}
	a = &domain.Player{Conn: aConn, AccountID: aAID, CharID: aCID, EntityID: aoi.EntityID(aAID), Name: "Alice", MapName: "prontera", PosX: 100, PosY: 100, CLevel: 42}
	b = &domain.Player{Conn: bConn, AccountID: bAID, CharID: bCID, EntityID: aoi.EntityID(bAID), Name: "Bob", MapName: "prontera", PosX: 101, PosY: 100, CLevel: 7}
	require.NoError(t, registry.Register(a))
	require.NoError(t, registry.Register(b))
	svc = app.NewTradeService(registry, repo, tradeItemDB(t))
	// Open the trade: A requests B, B accepts.
	require.NoError(t, svc.RequestTrade(context.Background(), aAID, packet.CZTradeRequest{TargetGID: bCID}))
	aConn.buf.Reset()
	bConn.buf.Reset()
	require.NoError(t, svc.AckTrade(context.Background(), bAID, packet.CZTradeAck{Type: packet.CZTradeAckAccept}))
	aConn.buf.Reset()
	bConn.buf.Reset()
	return svc, a, b, aConn, bConn, repo
}

func TestTrade_AddItemStagesItemToPartner(t *testing.T) {
	svc, a, _, aConn, bConn, _ := s2Fixture(t)
	// A stages the item at Index 1 (NameID 502, Amount 5) — 3 of 5.
	require.NoError(t, svc.AddItem(context.Background(), a.AccountID, packet.CZAddExchangeItem{Index: 1, Amount: 3}))

	// Partner (B) gets ZC_ADD (62B) carrying the item's full detail.
	add := bConn.buf.Bytes()
	require.Len(t, add, packet.ZCAddExchangeItem{}.Size(), "B got one ZC_ADD frame")
	assert.Equal(t, packet.HeaderZCADDEXCHANGEITEM, binary.LittleEndian.Uint16(add[0:2]))
	assert.Equal(t, uint32(502), binary.LittleEndian.Uint32(add[2:6]), "ZC_ADD itemId = 502")
	assert.Equal(t, int32(3), int32(binary.LittleEndian.Uint32(add[7:11])), "ZC_ADD amount = 3")
	// Adder (A) gets ZC_ACK_ADD success.
	ack := aConn.buf.Bytes()
	require.Len(t, ack, packet.AckAddExchangeItem{}.Size())
	assert.Equal(t, packet.HeaderZCACKADDEXCHANGEITEM, binary.LittleEndian.Uint16(ack[0:2]))
	assert.Equal(t, uint8(packet.TradeItemAddSuccess), ack[4])
}

func TestTrade_AddItemZenyStagesClaim(t *testing.T) {
	svc, a, _, _, bConn, _ := s2Fixture(t)
	// Index==0 is the zeny sentinel; A stages 500 zeny.
	require.NoError(t, svc.AddItem(context.Background(), a.AccountID, packet.CZAddExchangeItem{Index: 0, Amount: 500}))
	add := bConn.buf.Bytes()
	require.Len(t, add, packet.ZCAddExchangeItem{}.Size(), "B got ZC_ADD zeny frame")
	assert.Equal(t, packet.HeaderZCADDEXCHANGEITEM, binary.LittleEndian.Uint16(add[0:2]))
	assert.Equal(t, int32(500), int32(binary.LittleEndian.Uint32(add[7:11])), "ZC_ADD amount = zeny count")
	assert.Equal(t, uint32(0), binary.LittleEndian.Uint32(add[2:6]), "ZC_ADD itemId = 0 for zeny")
}

func TestTrade_AddItemRejects(t *testing.T) {
	t.Run("oob index → StackExceed", func(t *testing.T) {
		svc, a, _, aConn, _, _ := s2Fixture(t)
		require.NoError(t, svc.AddItem(context.Background(), a.AccountID, packet.CZAddExchangeItem{Index: 99, Amount: 1}))
		assert.Equal(t, uint8(packet.TradeItemAddStackExceed), aConn.buf.Bytes()[4])
	})
	t.Run("over stack → StackExceed", func(t *testing.T) {
		svc, a, _, aConn, _, _ := s2Fixture(t)
		// Index 0 has 10; ask for 99.
		require.NoError(t, svc.AddItem(context.Background(), a.AccountID, packet.CZAddExchangeItem{Index: 1, Amount: 99}))
		assert.Equal(t, uint8(packet.TradeItemAddStackExceed), aConn.buf.Bytes()[4])
	})
	t.Run("amount<=0 → StackExceed", func(t *testing.T) {
		svc, a, _, aConn, _, _ := s2Fixture(t)
		require.NoError(t, svc.AddItem(context.Background(), a.AccountID, packet.CZAddExchangeItem{Index: 1, Amount: 0}))
		assert.Equal(t, uint8(packet.TradeItemAddStackExceed), aConn.buf.Bytes()[4])
	})
}

func TestTrade_AddItemMovesNoInventory(t *testing.T) {
	// The zero-dupe S2 guarantee: staging leaves the adder's bag untouched.
	svc, a, _, _, _, repo := s2Fixture(t)
	require.NoError(t, svc.AddItem(context.Background(), a.AccountID, packet.CZAddExchangeItem{Index: 1, Amount: 3}))
	rows, err := repo.LoadByChar(context.Background(), a.AccountID, a.CharID)
	require.NoError(t, err)
	// The staged item (Index 1) still has its full stack (5), not 2.
	assert.Equal(t, uint32(5), rows[1].Amount, "staging removed nothing from the bag")
}

func TestTrade_OkLocksSide(t *testing.T) {
	svc, a, _, aConn, bConn, _ := s2Fixture(t)
	require.NoError(t, svc.TradeOk(context.Background(), a.AccountID))
	// Presser (A) gets ZC_ACK_ADD (5B, success) THEN ZC_CONCLUDE Who=0 (3B).
	aBytes := aConn.buf.Bytes()
	require.Len(t, aBytes, packet.AckAddExchangeItem{}.Size()+packet.ConcludeExchangeItem{}.Size(),
		"presser gets ACK_ADD + CONCLUDE")
	assert.Equal(t, packet.HeaderZCACKADDEXCHANGEITEM, binary.LittleEndian.Uint16(aBytes[0:2]))
	assert.Equal(t, uint8(packet.TradeItemAddSuccess), aBytes[4], "ACK_ADD result=success")
	off := packet.AckAddExchangeItem{}.Size()
	assert.Equal(t, packet.HeaderZCCONCLUDEEXCHANGEITEM, binary.LittleEndian.Uint16(aBytes[off:off+2]))
	assert.Equal(t, uint8(0), aBytes[off+2], "presser CONCLUDE Who=0")
	// Partner (B) gets only ZC_CONCLUDE Who=1.
	bFrame := bConn.buf.Bytes()
	require.Len(t, bFrame, packet.ConcludeExchangeItem{}.Size())
	assert.Equal(t, uint8(1), bFrame[2], "partner CONCLUDE Who=1")
}

func TestTrade_AddItemMergesSameIndex(t *testing.T) {
	// Re-staging the SAME bag slot replaces (rAthena ARR_FIND merge), not
	// duplicates — the partner sees one updated ZC_ADD row, not two.
	svc, a, _, _, bConn, _ := s2Fixture(t)
	require.NoError(t, svc.AddItem(context.Background(), a.AccountID, packet.CZAddExchangeItem{Index: 1, Amount: 2}))
	bConn.buf.Reset()
	require.NoError(t, svc.AddItem(context.Background(), a.AccountID, packet.CZAddExchangeItem{Index: 1, Amount: 4}))
	// Exactly one ZC_ADD frame on the re-add (no duplicate row).
	assert.Len(t, bConn.buf.Bytes(), packet.ZCAddExchangeItem{}.Size(),
		"re-stage emits one ZC_ADD, not a duplicate")
	assert.Equal(t, int32(4), int32(binary.LittleEndian.Uint32(bConn.buf.Bytes()[7:11])),
		"re-stage amount = updated value")
}
