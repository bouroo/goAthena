//go:build unit

package app_test

import (
	"bytes"
	"context"
	"strings"
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

const useItemAccID uint32 = 2000800

type useItemFixture struct {
	svc      *app.UseItemService
	h        *app.UseItemHandler
	registry *domain.PlayerRegistry
	player   *domain.Player
	conn     *captureConn
	repo     *infra.MemoryInventoryRepository
}

func newUseItemFixture(t *testing.T, repo *infra.MemoryInventoryRepository, itemDB *itemdb.Registry) useItemFixture {
	t.Helper()
	registry := domain.NewPlayerRegistry()
	conn := &captureConn{role: gwdomain.RoleMap, remote: "useitem", auth: gwdomain.ConnAuth{AccountID: useItemAccID}}
	player := &domain.Player{
		Conn:      conn,
		EntityID:  aoi.EntityID(useItemAccID),
		AccountID: useItemAccID,
		CharID:    150005,
		MapName:   "prontera",
		PosX:      100,
		PosY:      100,
		MaxHP:     100,
		HP:        50,
		MaxSP:     50,
		SP:        20,
	}
	require.NoError(t, registry.Register(player))
	var items invdomain.InventoryRepository
	if repo != nil {
		items = repo
	}
	svc := app.NewUseItemService(items, itemDB, registry)
	return useItemFixture{svc: svc, h: app.NewUseItemHandler(svc), registry: registry, player: player, conn: conn, repo: repo}
}

func healingItemDB(t *testing.T) *itemdb.Registry {
	t.Helper()
	const yaml = `Header:
  Type: ITEM_DB
  Version: 3
Body:
  - Id: 503
    Name: Red_Potion
    Type: Healing
    Script: itemheal 100,50;
  - Id: 909
    Name: Jellopy
    Type: Etc
`
	reg, err := itemdb.Load(strings.NewReader(yaml))
	require.NoError(t, err)
	return reg
}

func expectUseAck(t *testing.T, index uint16, itemID uint16, amount uint16, result uint8) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (packet.UseItemAck2Response{
		Index: index, ItemID: itemID, AID: useItemAccID, Amount: amount, Result: result,
	}).Encode(&buf))
	return buf.Bytes()
}

func expectUseItemParChange(t *testing.T, varID uint16, count int32) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (packet.ParChangeResponse{VarID: varID, Count: count}).Encode(&buf))
	return buf.Bytes()
}

func TestUseItem_Success_HealsAndAcknowledges(t *testing.T) {
	t.Parallel()
	repo := infra.NewMemoryInventoryRepository(invdomain.InventoryItem{
		ID: 1, CharID: 150005, AccountID: useItemAccID, NameID: 503, Amount: 2,
	})
	f := newUseItemFixture(t, repo, healingItemDB(t))

	require.NoError(t, f.svc.UseItem(context.Background(), useItemAccID, 0))

	assert.Equal(t, uint32(100), f.player.HP, "HP clamped to MaxHP (50 + 100 = 150 → 100)")
	assert.Equal(t, uint32(50), f.player.SP, "SP clamped to MaxSP (20 + 50 = 70 → 50)")

	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 3, "ack + SPHP + SPSP")
	assert.Equal(t, expectUseAck(t, 0, 503, 1, 1), frames[0],
		"ZC_USE_ITEM_ACK2 Result=1, remaining amount=1, itemid=503")
	assert.Equal(t, expectUseItemParChange(t, packet.SPHP, 100), frames[1],
		"ZC_PAR_CHANGE SPHP=100")
	assert.Equal(t, expectUseItemParChange(t, packet.SPSP, 50), frames[2],
		"ZC_PAR_CHANGE SPSP=50")

	rows, err := repo.LoadByChar(context.Background(), useItemAccID, 150005)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, uint32(1), rows[0].Amount, "one unit consumed")
}

func TestUseItem_NonHealing_FailsWithoutConsume(t *testing.T) {
	t.Parallel()
	repo := infra.NewMemoryInventoryRepository(invdomain.InventoryItem{
		ID: 1, CharID: 150005, AccountID: useItemAccID, NameID: 909, Amount: 1,
	})
	f := newUseItemFixture(t, repo, healingItemDB(t))

	require.NoError(t, f.svc.UseItem(context.Background(), useItemAccID, 0))

	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 1, "only the fail ack; no heal, no SP update")
	assert.Equal(t, uint8(0), frames[0][12], "Result=0 (failure)")

	rows, err := repo.LoadByChar(context.Background(), useItemAccID, 150005)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, uint32(1), rows[0].Amount, "non-Healing item untouched")
}

func TestUseItem_MissingIndex_FailsNoOp(t *testing.T) {
	t.Parallel()
	repo := infra.NewMemoryInventoryRepository()
	f := newUseItemFixture(t, repo, healingItemDB(t))

	require.NoError(t, f.svc.UseItem(context.Background(), useItemAccID, 0))

	frames := splitFixedFrames(t, f.conn.buf.Bytes())
	require.Len(t, frames, 1, "only the fail ack")
	assert.Equal(t, uint8(0), frames[0][12], "Result=0 (failure)")
}
