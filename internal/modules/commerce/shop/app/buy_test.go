//go:build unit

package app_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	characterinfra "github.com/bouroo/goAthena/internal/modules/character/infra"
	shopapp "github.com/bouroo/goAthena/internal/modules/commerce/shop/app"
	shopdomain "github.com/bouroo/goAthena/internal/modules/commerce/shop/domain"
	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	inventoryinfra "github.com/bouroo/goAthena/internal/modules/inventory/infra"
	worlddomain "github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/itemdb"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

const (
	buyAccID      uint32 = 2000900
	buyCharID     uint32 = 180001
	buyShopID     uint32 = 500000000
	buyPriceID    uint32 = 501
	buyPrice             = uint32(50)
	buyFallbackID uint32 = 909
)

// captureConn is a test double for gwdomain.Conn that records every Write into
// buf so tests can decode the server→client stream without a real socket.
type captureConn struct {
	role gwdomain.Role
	auth gwdomain.ConnAuth
	mu   sync.Mutex
	buf  bytes.Buffer
}

func (c *captureConn) Role() gwdomain.Role         { return c.role }
func (c *captureConn) SetRole(r gwdomain.Role)     { c.role = r }
func (c *captureConn) Auth() gwdomain.ConnAuth     { return c.auth }
func (c *captureConn) SetAuth(a gwdomain.ConnAuth) { c.auth = a }
func (c *captureConn) RemoteAddr() string          { return "test" }
func (c *captureConn) Write(p []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.buf.Write(p)
	return err
}
func (c *captureConn) Close() error { return nil }

func (c *captureConn) Bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.buf.Bytes()...)
}

func (c *captureConn) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.buf.Reset()
}

type buyFixture struct {
	svc      *shopapp.BuyService
	catalog  *shopdomain.MemoryShopCatalog
	chars    *characterinfra.MemoryCharacterRepository
	inv      *inventoryinfra.MemoryInventoryRepository
	registry *worlddomain.PlayerRegistry
	conn     *captureConn
}

func newBuyFixture(t *testing.T, itemDB *itemdb.Registry) *buyFixture {
	t.Helper()
	catalog := shopdomain.NewMemoryShopCatalog()
	catalog.Add(shopdomain.Shop{
		NPCID: buyShopID,
		Name:  "Tool Shop",
		Items: []shopdomain.ShopItem{
			{NameID: buyPriceID, Price: buyPrice},
			{NameID: 504, Price: 1000},
		},
	})
	chars := characterinfra.NewMemoryCharacterRepository(chardomain.Character{
		CharID:    buyCharID,
		AccountID: buyAccID,
		Slot:      0,
		Name:      "Buyer",
		Zeny:      1000,
	})
	inv := inventoryinfra.NewMemoryInventoryRepository()
	registry := worlddomain.NewPlayerRegistry()
	conn := &captureConn{role: gwdomain.RoleMap, auth: gwdomain.ConnAuth{AccountID: buyAccID}}
	player := &worlddomain.Player{
		Conn:      conn,
		EntityID:  aoi.EntityID(buyAccID),
		AccountID: buyAccID,
		CharID:    buyCharID,
		MapName:   "prontera",
	}
	require.NoError(t, registry.Register(player))
	svc := shopapp.NewBuyService(catalog, chars, inv, registry, itemDB)
	return &buyFixture{svc: svc, catalog: catalog, chars: chars, inv: inv, registry: registry, conn: conn}
}

func buyItemDB(t *testing.T) *itemdb.Registry {
	t.Helper()
	const yaml = `Header:
  Type: ITEM_DB
  Version: 3
Body:
  - Id: 501
    Name: Red_Potion
    Type: Healing
    View: 1
  - Id: 909
    Name: Jellopy
    Type: Etc
  - Id: 1104
    Name: Sword
    Type: Weapon
    View: 2
  - Id: 2301
    Name: Cotton_Shirt
    Type: Armor
    View: 3
`
	reg, err := itemdb.Load(strings.NewReader(yaml))
	require.NoError(t, err)
	return reg
}

func makeAckSelectDealtype(t *testing.T, npcID uint32, dealType uint8) gwdomain.Frame {
	var buf bytes.Buffer
	require.NoError(t, (&packet.CZAckSelectDealTypeRequest{NpcID: npcID, Type: dealType}).Encode(&buf))
	return gwdomain.Frame{Cmd: packet.HeaderCZACKSELECTDEALTYPE, Raw: buf.Bytes()}
}

func makePurchaseItemList(t *testing.T, entries ...packet.CZPCPurchaseItemListEntry) gwdomain.Frame {
	var buf bytes.Buffer
	require.NoError(t, (&packet.CZPCPurchaseItemListRequest{Entries: entries}).Encode(&buf))
	return gwdomain.Frame{Cmd: packet.HeaderCZPCPURCHASEITEMLIST, Raw: buf.Bytes()}
}

func parseFrames(t *testing.T, b []byte) [][]byte {
	t.Helper()
	var out [][]byte
	for len(b) >= 2 {
		cmd := binary.LittleEndian.Uint16(b[0:2])
		var size int
		switch cmd {
		case packet.HeaderZCPCPURCHASEITEMLIST:
			require.GreaterOrEqual(t, len(b), 4, "ZC_PC_PURCHASE_ITEMLIST too short")
			total := int(binary.LittleEndian.Uint16(b[2:4]))
			size = total
		case packet.HeaderZCPCPURCHASERESULT:
			size = packet.PurchaseResultResponse{}.Size()
		case packet.HeaderZCLONGPARCHANGE:
			size = (&packet.LongParChangeResponse{}).Size()
		default:
			t.Fatalf("unknown cmd 0x%04x in captured stream", cmd)
		}
		require.LessOrEqual(t, size, len(b), "frame for cmd 0x%04x overruns", cmd)
		out = append(out, b[:size])
		b = b[size:]
	}
	require.Empty(t, b, "trailing bytes after splitting")
	return out
}

func TestBuy_AckSelectDealtype_SendsCatalog(t *testing.T) {
	t.Parallel()
	f := newBuyFixture(t, buyItemDB(t))

	require.NoError(t, f.svc.HandleAckSelectDealtype(context.Background(), f.conn, makeAckSelectDealtype(t, buyShopID, 0)))

	frames := parseFrames(t, f.conn.Bytes())
	require.Len(t, frames, 1, "single ZC_PC_PURCHASE_ITEMLIST frame")
	require.Equal(t, packet.HeaderZCPCPURCHASEITEMLIST, binary.LittleEndian.Uint16(frames[0][0:2]))

	// ZC_PC_PURCHASE_ITEMLIST has no entry-count field; the client derives
	// the count from (packetLength - 4) / 19.
	totalLen := binary.LittleEndian.Uint16(frames[0][2:4])
	itemCount := (int(totalLen) - 4) / 19
	require.Equal(t, 2, itemCount, "two items in the seeded shop")
	first := frames[0][4 : 4+19]
	assert.Equal(t, buyPriceID, binary.LittleEndian.Uint32(first[0:4]), "itemID")
	assert.Equal(t, buyPrice, binary.LittleEndian.Uint32(first[4:8]), "price")
	assert.Equal(t, buyPrice, binary.LittleEndian.Uint32(first[8:12]), "discountPrice=price")
	assert.Equal(t, uint8(0), first[12], "IT_HEALING for a Healing item")
	assert.Equal(t, uint16(1), binary.LittleEndian.Uint16(first[13:15]), "View=1 from itemdb")
}

func TestBuy_AckSelectDealtype_SellIsNoOp(t *testing.T) {
	t.Parallel()
	f := newBuyFixture(t, buyItemDB(t))

	require.NoError(t, f.svc.HandleAckSelectDealtype(context.Background(), f.conn, makeAckSelectDealtype(t, buyShopID, 1)))
	assert.Empty(t, f.conn.Bytes(), "Sell/Cancel does not write a packet")
}

func TestBuy_PurchaseItemList_DeductsZenyAndAddsItems(t *testing.T) {
	t.Parallel()
	f := newBuyFixture(t, buyItemDB(t))
	ctx := context.Background()

	require.NoError(t, f.svc.HandleAckSelectDealtype(ctx, f.conn, makeAckSelectDealtype(t, buyShopID, 0)))
	f.conn.Reset()
	require.NoError(t, f.svc.HandlePurchaseItemList(ctx, f.conn, makePurchaseItemList(t,
		packet.CZPCPurchaseItemListEntry{ItemID: buyPriceID, Amount: 2},
	)))

	frames := parseFrames(t, f.conn.Bytes())
	require.Len(t, frames, 2, "purchase success + zeny broadcast")
	require.Equal(t, packet.HeaderZCPCPURCHASERESULT, binary.LittleEndian.Uint16(frames[0][0:2]))
	assert.Equal(t, uint8(0), frames[0][2], "result=0 success")
	require.Equal(t, packet.HeaderZCLONGPARCHANGE, binary.LittleEndian.Uint16(frames[1][0:2]))
	assert.Equal(t, packet.SPZeny, binary.LittleEndian.Uint16(frames[1][2:4]))
	assert.Equal(t, int32(1000-2*50), int32(binary.LittleEndian.Uint32(frames[1][4:8])), //nolint:gosec // G115: test wire decode
		"zeny broadcast must equal 1000 - 2 * 50 = 900")

	// Inventory + char row mutated.
	rows, err := f.inv.LoadByChar(ctx, buyAccID, buyCharID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, buyPriceID, rows[0].NameID)
	assert.Equal(t, uint32(2), rows[0].Amount)
	char, err := f.chars.GetByID(ctx, buyAccID, buyCharID)
	require.NoError(t, err)
	assert.Equal(t, uint32(900), char.Zeny, "zeny debited in the persisted progression")
}

func TestBuy_PurchaseItemList_FailsOnInsufficientZeny(t *testing.T) {
	t.Parallel()
	f := newBuyFixture(t, buyItemDB(t))
	// Drop the seeded zeny to 10 (less than 50).
	char, err := f.chars.GetByID(context.Background(), buyAccID, buyCharID)
	require.NoError(t, err)
	char.Zeny = 10
	require.NoError(t, f.chars.SaveProgression(context.Background(), buyAccID, buyCharID, chardomain.ProgressionOf(char)))
	ctx := context.Background()

	require.NoError(t, f.svc.HandleAckSelectDealtype(ctx, f.conn, makeAckSelectDealtype(t, buyShopID, 0)))
	f.conn.Reset()
	require.NoError(t, f.svc.HandlePurchaseItemList(ctx, f.conn, makePurchaseItemList(t,
		packet.CZPCPurchaseItemListEntry{ItemID: buyPriceID, Amount: 1},
	)))

	frames := parseFrames(t, f.conn.Bytes())
	require.Len(t, frames, 1, "only the fail ack; no zeny broadcast")
	require.Equal(t, packet.HeaderZCPCPURCHASERESULT, binary.LittleEndian.Uint16(frames[0][0:2]))
	assert.Equal(t, uint8(1), frames[0][2], "result=1 on insufficient zeny")

	// No bag mutation, no zeny mutation.
	rows, err := f.inv.LoadByChar(ctx, buyAccID, buyCharID)
	require.NoError(t, err)
	assert.Empty(t, rows, "bag untouched on rejection")
	char, err = f.chars.GetByID(ctx, buyAccID, buyCharID)
	require.NoError(t, err)
	assert.Equal(t, uint32(10), char.Zeny, "zeny untouched on rejection")
}

func TestBuy_PurchaseItemList_FailsWhenNoSession(t *testing.T) {
	t.Parallel()
	f := newBuyFixture(t, buyItemDB(t))
	ctx := context.Background()

	// No prior AckSelectDealtype — the session map is empty.
	require.NoError(t, f.svc.HandlePurchaseItemList(ctx, f.conn, makePurchaseItemList(t,
		packet.CZPCPurchaseItemListEntry{ItemID: buyPriceID, Amount: 1},
	)))

	frames := parseFrames(t, f.conn.Bytes())
	require.Len(t, frames, 1)
	require.Equal(t, packet.HeaderZCPCPURCHASERESULT, binary.LittleEndian.Uint16(frames[0][0:2]))
	assert.Equal(t, uint8(1), frames[0][2], "result=1 when no active shop session")
}

func TestBuy_AckSelectDealtype_ConcurrentLocked(t *testing.T) {
	t.Parallel()
	f := newBuyFixture(t, buyItemDB(t))
	// Hammer the same session map from many goroutines to confirm the mutex
	// keeps the writers from racing on the map (the race detector would catch
	// a missing lock).
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = f.svc.HandleAckSelectDealtype(context.Background(), f.conn, makeAckSelectDealtype(t, buyShopID, 0))
		}()
	}
	wg.Wait()
}
