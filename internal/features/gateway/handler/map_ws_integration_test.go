//go:build unit

// Gateway→zone gRPC integration test. Unlike map_ws_test.go which
// uses a hand-rolled fakeZoneClient, this test spins up a real zone
// gRPC server on an in-memory bufconn listener, dials it with a real
// gRPC client, and wires that client into the WS dispatch adapter.
// The test proves the full CZ_ENTER → zone.EnterZone gRPC →
// ZC_ACCEPT_ENTER round-trip crosses the real gRPC transport boundary
// correctly. The ZoneService use-case layer is mocked via
// domainmock.NewMockZoneService; the point is the transport boundary,
// not the service internals.
package handler

import (
	"context"
	"encoding/binary"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	"github.com/bouroo/goAthena/internal/features/gateway/service"
	tradedomainmock "github.com/bouroo/goAthena/internal/features/trade/domain/mock"
	"github.com/bouroo/goAthena/internal/features/zone/domain"
	znmock "github.com/bouroo/goAthena/internal/features/zone/domain/mock"
	zonehandler "github.com/bouroo/goAthena/internal/features/zone/handler"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// TestWSHandler_CZEnter_RealZoneGRPC_Bufconn is the M4b headline
// evidence. It replaces the hand-rolled fakeZoneClient from
// TestWSHandler_CZEnter_RoundTrip_ZCAcceptEnter with a real zone gRPC
// server bound to an in-memory bufconn listener. The test asserts:
//
//  1. CZ_ENTER crosses the WebSocket boundary into the real WSHandler.
//  2. The gateway dispatch adapter invokes zone.EnterZone over a real
//     gRPC transport (bufconn).
//  3. The real zone gRPC handler validates the request, calls the
//     mocked ZoneService.AddEntity with the expected Entity shape
//     (ID=EntityID(accountID), Type=EntityPlayer, X=spawnX, Y=spawnY).
//  4. The success response flows back across gRPC and the adapter
//     encodes a 13-byte ZC_ACCEPT_ENTER with the spawn position.
//
// Layout reference: pkg/ro/packet/map_encode.go:43-63.
//
//	[0:2]   cmd 0x02eb
//	[2:6]   startTime (uint32 LE)
//	[6:9]   posDir[3] packed (encodePos(150, 200, 0))
//	[9]     xSize (5)
//	[10]    ySize (5)
//	[11:13] font (uint16 LE, 0)
func TestWSHandler_CZEnter_RealZoneGRPC_Bufconn(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	mockSvc := znmock.NewMockZoneService(ctrl)

	// Expect exactly one AddEntity call with the Entity shape the real
	// zone handler constructs from a validated EnterZoneRequest.
	mockSvc.EXPECT().
		AddEntity(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, e *domain.Entity) error {
			assert.Equal(t, domain.EntityID(4242), e.ID, "Entity.ID should be EntityID(accountID)")
			assert.Equal(t, domain.EntityPlayer, e.Type, "Entity.Type should be EntityPlayer")
			assert.Equal(t, 150, e.X, "Entity.X should be the configured spawnX")
			assert.Equal(t, 200, e.Y, "Entity.Y should be the configured spawnY")
			return nil
		}).
		Times(1)

	// Real zone gRPC handler wired with the mock ZoneService and TradeService.
	nopLogger := zerolog.Nop()
	mockTradeSvc := tradedomainmock.NewMockTradeService(gomock.NewController(t))
	zoneHandler := zonehandler.NewGRPCHandler(mockSvc, mockTradeSvc, "prontera", 150, 200, &nopLogger)

	// Real gRPC server on an in-memory bufconn listener.
	const bufSize = 1024 * 1024
	lis := bufconn.Listen(bufSize)
	grpcServer := grpc.NewServer()
	zonev1.RegisterZoneServiceServer(grpcServer, zoneHandler)
	go func() {
		_ = grpcServer.Serve(lis)
	}()
	defer grpcServer.Stop()

	// Real gRPC client dialing the bufconn listener. grpc.NewClient is
	// non-blocking; the passthrough resolver combined with a custom
	// context dialer routes every call through bufconn.
	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err, "bufconn dial")
	defer func() { _ = conn.Close() }()

	zoneClient := zonev1.NewZoneServiceClient(conn)

	// Real WS dispatch adapter using the real zone gRPC client.
	adapter := &wsMapDispatchAdapter{zone: zoneClient}

	// WSHandler + httptest server (same pattern as
	// TestWSHandler_CZEnter_RoundTrip_ZCAcceptEnter).
	db := packet.NewLoginServerDB()
	db.Merge(packet.NewCharServerDB())
	db.Merge(packet.NewMapServerDB())
	h := NewWSHandler(db, adapter, service.NewSessionRegistry(), "unused", "/ws/",
		zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.Disabled), nil)

	mux := http.NewServeMux()
	mux.HandleFunc(h.path, h.ServeHTTP)
	mux.HandleFunc("/", h.rejectNonUpgrade)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := wsURLFromTestServer(t, srv.URL) + h.path

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPClient: srv.Client(),
	})
	require.NoError(t, err, "ws dial")
	defer func() { _ = client.Close(websocket.StatusNormalClosure, "") }()

	// buildCZEnter / wsURLFromTestServer / wsMapDispatchAdapter are
	// defined in map_ws_test.go (same package).
	frame := buildCZEnter(4242 /*AID*/, 9001 /*CID*/, 0xdead0000 /*authCode*/, 0xbeef0000 /*clientTime*/, 1 /*sex=M*/)
	require.NoError(t, client.Write(ctx, websocket.MessageBinary, frame), "ws write CZ_ENTER")

	readCtx, readCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer readCancel()
	msgType, data, err := client.Read(readCtx)
	require.NoError(t, err, "ws read ZC_ACCEPT_ENTER")
	require.Equal(t, websocket.MessageBinary, msgType, "response msg type should be binary")

	const wantLen = 13
	require.Equal(t, wantLen, len(data), "ZC_ACCEPT_ENTER length")
	require.Equal(t, byte(0xeb), data[0], "ZC_ACCEPT_ENTER header low byte (0x02eb LE)")
	require.Equal(t, byte(0x02), data[1], "ZC_ACCEPT_ENTER header high byte (0x02eb LE)")

	// posDir[3] at offset [6:9] — encodePos(150, 200, 0). The byte
	// values come from pkg/ro/packet/coords.go's packing: x in low 6
	// bits of byte[0] + high 2 of byte[1], y in low 6 of byte[1] +
	// high 2 of byte[2], dir in low 4 of byte[2].
	b0, b1, b2 := data[6], data[7], data[8]
	gotX := int16((uint16(b0) << 2) | (uint16(b1) >> 6))
	gotY := int16((uint16(b1&0x3f) << 4) | (uint16(b2) >> 4))
	gotDir := b2 & 0x0f
	assert.Equal(t, int16(150), gotX, "posDir.X unpacked from bytes %x", data[6:9])
	assert.Equal(t, int16(200), gotY, "posDir.Y unpacked from bytes %x", data[6:9])
	assert.Equal(t, byte(0), gotDir, "posDir.Dir unpacked from bytes %x", data[6:9])

	assert.Equal(t, byte(5), data[9], "xSize at offset 9")
	assert.Equal(t, byte(5), data[10], "ySize at offset 10")
	assert.Equal(t, uint16(0), binary.LittleEndian.Uint16(data[11:13]), "font at [11:13]")
}
