//go:build unit

package app_test

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// tradeFixture builds two online players (A the requester at 100,100; B the target
// at 101,100 — within tradeDistance 2 on prontera), each with a capturing conn,
// registered in a live PlayerRegistry, plus the TradeService under test.
type tradeFixture struct {
	svc          *app.TradeService
	a, b         *domain.Player
	aConn, bConn *captureConn
}

func newTradeFixture(t *testing.T) tradeFixture {
	t.Helper()
	registry := domain.NewPlayerRegistry()
	aConn := &captureConn{role: gwdomain.RoleMap, remote: "A"}
	bConn := &captureConn{role: gwdomain.RoleMap, remote: "B"}
	const (
		aAID uint32 = 2000001
		bAID uint32 = 2000002
		aCID uint32 = 150001
		bCID uint32 = 150002
	)
	a := &domain.Player{
		Conn: aConn, AccountID: aAID, CharID: aCID, EntityID: aoi.EntityID(aAID),
		Name: "Alice", MapName: "prontera", PosX: 100, PosY: 100, CLevel: 42,
	}
	b := &domain.Player{
		Conn: bConn, AccountID: bAID, CharID: bCID, EntityID: aoi.EntityID(bAID),
		Name: "Bob", MapName: "prontera", PosX: 101, PosY: 100, CLevel: 7,
	}
	require.NoError(t, registry.Register(a))
	require.NoError(t, registry.Register(b))
	return tradeFixture{svc: app.NewTradeService(registry, nil, nil), a: a, b: b, aConn: aConn, bConn: bConn}
}

// resetConn drains a captureConn's buffered frames between phases.
func resetConn(c *captureConn) { c.buf.Reset() }

func TestTrade_RequestOpensTargetDialog(t *testing.T) {
	f := newTradeFixture(t)
	require.NoError(t, f.svc.RequestTrade(context.Background(), f.a.AccountID, packet.CZTradeRequest{TargetGID: f.b.CharID}))

	// ZC_REQ_EXCHANGE_ITEM (0x1f4, 32B) goes to the TARGET (B) only.
	recv := f.bConn.buf.Bytes()
	require.Len(t, recv, packet.TradeRequestResponse{}.Size(), "B received one ZC_REQ frame")
	assert.Equal(t, uint16(packet.HeaderZCREQEXCHANGEITEM), binary.LittleEndian.Uint16(recv[0:2]))
	assert.Equal(t, "Alice", string(recv[2:26][:5]), "ZC_REQ requester name = Alice")
	assert.Equal(t, f.a.AccountID, binary.LittleEndian.Uint32(recv[26:30]), "ZC_REQ targetId = requester account_id")
	assert.Equal(t, uint16(42), binary.LittleEndian.Uint16(recv[30:32]), "ZC_REQ targetLv = requester level")
	assert.Empty(t, f.aConn.buf.Bytes(), "requester gets no frame on a successful request")

	// Both players now hold the other as trade partner.
	pid, _ := f.a.TradePartner()
	assert.Equal(t, f.b.AccountID, pid, "A's partner = B")
	pid, lv := f.b.TradePartner()
	assert.Equal(t, f.a.AccountID, pid, "B's partner = A")
	assert.Equal(t, uint16(42), lv, "B recorded A's level")
}

func TestTrade_AckAcceptOpensBothWindows(t *testing.T) {
	f := newTradeFixture(t)
	require.NoError(t, f.svc.RequestTrade(context.Background(), f.a.AccountID, packet.CZTradeRequest{TargetGID: f.b.CharID}))
	resetConn(f.aConn)
	resetConn(f.bConn)

	// B accepts → both windows open, ZC_ACK ACCEPT to both.
	require.NoError(t, f.svc.AckTrade(context.Background(), f.b.AccountID, packet.CZTradeAck{Type: packet.CZTradeAckAccept}))
	assert.True(t, f.a.IsTrading(), "A trading")
	assert.True(t, f.b.IsTrading(), "B trading")
	for _, c := range []*captureConn{f.aConn, f.bConn} {
		ack := c.buf.Bytes()
		require.Len(t, ack, packet.TradeAckResponse{}.Size(), "one ZC_ACK frame")
		assert.Equal(t, uint16(packet.HeaderZCACKEXCHANGEITEM), binary.LittleEndian.Uint16(ack[0:2]))
		assert.Equal(t, uint8(packet.TradeAckAccept), ack[2], "ZC_ACK result = ACCEPT")
	}
}

func TestTrade_AckCancelClearsBoth(t *testing.T) {
	f := newTradeFixture(t)
	require.NoError(t, f.svc.RequestTrade(context.Background(), f.a.AccountID, packet.CZTradeRequest{TargetGID: f.b.CharID}))
	resetConn(f.aConn)
	resetConn(f.bConn)

	require.NoError(t, f.svc.AckTrade(context.Background(), f.b.AccountID, packet.CZTradeAck{Type: packet.CZTradeAckCancel}))
	for _, c := range []*captureConn{f.aConn, f.bConn} {
		ack := c.buf.Bytes()
		require.Len(t, ack, packet.TradeAckResponse{}.Size(), "one ZC_ACK frame")
		assert.Equal(t, uint8(packet.TradeAckCancel), ack[2], "ZC_ACK result = CANCEL")
	}
	pid, _ := f.a.TradePartner()
	assert.Zero(t, pid, "A cleared")
	pid, _ = f.b.TradePartner()
	assert.Zero(t, pid, "B cleared")
}

func TestTrade_CancelSendsCancelToBoth(t *testing.T) {
	f := newTradeFixture(t)
	require.NoError(t, f.svc.RequestTrade(context.Background(), f.a.AccountID, packet.CZTradeRequest{TargetGID: f.b.CharID}))
	resetConn(f.aConn)
	resetConn(f.bConn)

	// Either side's CZ_TRADE_CANCEL → ZC_CANCEL (0x00ee, 2B) to both, state cleared.
	require.NoError(t, f.svc.CancelTrade(context.Background(), f.a.AccountID))
	for _, c := range []*captureConn{f.aConn, f.bConn} {
		require.Len(t, c.buf.Bytes(), packet.CancelExchangeResponse{}.Size(), "one ZC_CANCEL frame")
		assert.Equal(t, uint16(packet.HeaderZCCANCELEXCHANGEITEM), binary.LittleEndian.Uint16(c.buf.Bytes()[0:2]))
	}
	pid, _ := f.a.TradePartner()
	assert.Zero(t, pid, "A cleared")
	pid, _ = f.b.TradePartner()
	assert.Zero(t, pid, "B cleared")
}

func TestTrade_RequestRejects(t *testing.T) {
	t.Run("target offline → CHARNOTEXIST", func(t *testing.T) {
		f := newTradeFixture(t)
		require.NoError(t, f.svc.RequestTrade(context.Background(), f.a.AccountID, packet.CZTradeRequest{TargetGID: 999999}))
		ack := f.aConn.buf.Bytes()
		require.Len(t, ack, packet.TradeAckResponse{}.Size())
		assert.Equal(t, uint8(packet.TradeAckCharNotExist), ack[2])
	})
	t.Run("already partnered → FAILED", func(t *testing.T) {
		f := newTradeFixture(t)
		f.a.SetTradePartner(f.b.AccountID, f.b.CLevel) // A is already mid-trade
		require.NoError(t, f.svc.RequestTrade(context.Background(), f.a.AccountID, packet.CZTradeRequest{TargetGID: f.b.CharID}))
		ack := f.aConn.buf.Bytes()
		require.Len(t, ack, packet.TradeAckResponse{}.Size())
		assert.Equal(t, uint8(packet.TradeAckFailed), ack[2])
	})
	t.Run("target partnered → FAILED", func(t *testing.T) {
		f := newTradeFixture(t)
		f.b.SetTradePartner(0xdead, 1) // B already has a pending partner
		require.NoError(t, f.svc.RequestTrade(context.Background(), f.a.AccountID, packet.CZTradeRequest{TargetGID: f.b.CharID}))
		ack := f.aConn.buf.Bytes()
		require.Len(t, ack, packet.TradeAckResponse{}.Size())
		assert.Equal(t, uint8(packet.TradeAckFailed), ack[2], "trade.cpp:69 returns FAILED for 'person in another trade'")
	})
	t.Run("too far → TOOFAR", func(t *testing.T) {
		f := newTradeFixture(t)
		f.b.PosX = 200 // move B out of tradeDistance (Chebyshev 100 > 2)
		require.NoError(t, f.svc.RequestTrade(context.Background(), f.a.AccountID, packet.CZTradeRequest{TargetGID: f.b.CharID}))
		ack := f.aConn.buf.Bytes()
		require.Len(t, ack, packet.TradeAckResponse{}.Size())
		assert.Equal(t, uint8(packet.TradeAckTooFar), ack[2])
	})
	t.Run("self → CHARNOTEXIST", func(t *testing.T) {
		f := newTradeFixture(t)
		require.NoError(t, f.svc.RequestTrade(context.Background(), f.a.AccountID, packet.CZTradeRequest{TargetGID: f.a.CharID}))
		assert.Equal(t, uint8(packet.TradeAckCharNotExist), f.aConn.buf.Bytes()[2])
	})
}

func TestTrade_AckBrokenTypeIgnored(t *testing.T) {
	f := newTradeFixture(t)
	require.NoError(t, f.svc.RequestTrade(context.Background(), f.a.AccountID, packet.CZTradeRequest{TargetGID: f.b.CharID}))
	resetConn(f.aConn)
	resetConn(f.bConn)
	// A non-3/non-4 type is a broken packet (trade.cpp:139): ignored, no state change.
	require.NoError(t, f.svc.AckTrade(context.Background(), f.b.AccountID, packet.CZTradeAck{Type: 9}))
	assert.Empty(t, f.aConn.buf.Bytes(), "broken ack emits nothing")
	assert.Empty(t, f.bConn.buf.Bytes(), "broken ack emits nothing")
	pid, _ := f.a.TradePartner()
	assert.Equal(t, f.b.AccountID, pid, "pending trade preserved on broken ack")
}
