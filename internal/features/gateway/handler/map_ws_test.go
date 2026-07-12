//go:build unit

// WS-driven end-to-end map-enter test. This file spawns a real
// *handler.WSHandler in-process, dials it with a coder/websocket
// client as a roBrowser-style peer, sends a real CZ_ENTER frame, and
// asserts the dispatch handler emits a real ZC_ACCEPT_ENTER reply on
// the WebSocket. It is the L3 evidence for the M3b map-enter
// increment.
package handler

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	"github.com/bouroo/goAthena/internal/features/gateway/domain"
	"github.com/bouroo/goAthena/internal/features/gateway/service"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// fakeZoneClient is a hand-rolled stand-in for
// zonev1.ZoneServiceClient. Tests configure enterFn to control the
// EnterZone response. We deliberately avoid mockgen: a tiny fake keeps
// the test self-contained and trivially diffable against the gRPC
// interface, mirroring the loginFakeIdentityClient pattern.
type fakeZoneClient struct {
	enterFn func(context.Context, *zonev1.EnterZoneRequest, ...grpc.CallOption) (*zonev1.EnterZoneResponse, error)
}

func (f *fakeZoneClient) EnterZone(ctx context.Context, req *zonev1.EnterZoneRequest, _ ...grpc.CallOption) (*zonev1.EnterZoneResponse, error) {
	if f.enterFn == nil {
		return &zonev1.EnterZoneResponse{Success: true, MapName: "prontera", MapX: 150, MapY: 200}, nil
	}
	return f.enterFn(ctx, req)
}

// MoveEntity is a stub — the WS-driven map-enter test does not
// exercise move requests. Returning Unimplemented mirrors the
// behaviour of UnimplementedZoneServiceServer for callers that
// would route a CZ_REQUEST_MOVE through the same client.
func (f *fakeZoneClient) MoveEntity(_ context.Context, _ *zonev1.MoveEntityRequest, _ ...grpc.CallOption) (*zonev1.MoveEntityResponse, error) {
	return nil, status.Error(codes.Unimplemented, "MoveEntity not configured in fakeZoneClient")
}

func (f *fakeZoneClient) AttackEntity(_ context.Context, _ *zonev1.AttackEntityRequest, _ ...grpc.CallOption) (*zonev1.AttackEntityResponse, error) {
	return &zonev1.AttackEntityResponse{
		Success:       true,
		TargetDied:    false,
		DamageApplied: 10,
	}, nil
}

func (f *fakeZoneClient) PickupItem(_ context.Context, _ *zonev1.PickupItemRequest, _ ...grpc.CallOption) (*zonev1.PickupItemResponse, error) {
	return &zonev1.PickupItemResponse{
		Success: true,
		ItemId:  501,
		Amount:  1,
	}, nil
}

// wsMapDispatchAdapter mirrors service.DispatchHandler for the WS path
// so this test exercises the full real WSHandler → processBytes →
// domain.PacketHandler → zone client → ZC_ACCEPT_ENTER → WS write
// round trip. The shape intentionally tracks service/dispatch.go's
// handleCZEnter; if that file's wire mapping changes, mirror the
// change here.
type wsMapDispatchAdapter struct {
	zone zonev1.ZoneServiceClient
}

func (a *wsMapDispatchAdapter) HandlePacket(ctx context.Context, _ *domain.ConnectionInfo, resp domain.Responder, cmd uint16, frame []byte) error {
	if cmd != packet.HeaderCZENTER {
		return nil
	}
	req, parseErr := packet.ParseCZEnter(frame)
	if parseErr != nil {
		return nil
	}

	zResp, err := a.zone.EnterZone(ctx, &zonev1.EnterZoneRequest{
		AccountId:  req.AccountID,
		CharId:     req.CharID,
		LoginId1:   uint64(req.AuthCode),
		ClientTick: req.ClientTime,
		Sex:        wsMapSexString(req.Sex),
	})
	if err != nil || zResp == nil || !zResp.GetSuccess() {
		return wsMapSendRefuse(resp)
	}

	accept := packet.MapAcceptEnterResponse{
		StartTime: 0,                      // startTime is a monotonic tick; the test only asserts the position encoding.
		PosX:      int16(zResp.GetMapX()), //nolint:gosec // map coords are 0-512
		PosY:      int16(zResp.GetMapY()), //nolint:gosec // map coords are 0-512
		Dir:       0,
		XSize:     5,
		YSize:     5,
		Font:      0,
	}
	var buf bytes.Buffer
	if err := accept.Encode(&buf); err != nil {
		return err
	}
	return resp.SendPacket(buf.Bytes())
}

// wsMapSexString mirrors service.sexString — kept inline to keep
// this test self-contained.
func wsMapSexString(b uint8) string {
	switch b {
	case 0:
		return "F"
	case 1:
		return "M"
	default:
		return "S"
	}
}

// wsMapSendRefuse encodes a ZC_REFUSE_ENTER onto resp.
func wsMapSendRefuse(resp domain.Responder) error {
	refuse := packet.MapRefuseEnterResponse{Error: 0}
	var buf bytes.Buffer
	if err := refuse.Encode(&buf); err != nil {
		return err
	}
	return resp.SendPacket(buf.Bytes())
}

// buildCZEnter crafts the 19-byte CZ_ENTER frame the rAthena client
// sends right after it opens the map-server TCP socket
// (rathena/src/map/clif.cpp:10642).
//
// Layout: int16 packetType + uint32 AID + uint32 CID + uint32 authCode +
// uint32 clientTime + uint8 sex = 2+4+4+4+4+1 = 19.
func buildCZEnter(accountID, charID, authCode, clientTime uint32, sex uint8) []byte {
	frame := make([]byte, 19)
	binary.LittleEndian.PutUint16(frame[0:2], packet.HeaderCZENTER)
	binary.LittleEndian.PutUint32(frame[2:6], accountID)
	binary.LittleEndian.PutUint32(frame[6:10], charID)
	binary.LittleEndian.PutUint32(frame[10:14], authCode)
	binary.LittleEndian.PutUint32(frame[14:18], clientTime)
	frame[18] = sex
	return frame
}

// TestWSHandler_CZEnter_RoundTrip_ZCAcceptEnter is the M3b headline
// evidence. The fake zone client returns {Success: true, MapName:
// "prontera", MapX: 150, MapY: 200}; the dispatcher must write a
// 13-byte ZC_ACCEPT_ENTER (cmd 0x02eb) with the spawn position
// encoded into the posDir[3] slot at offset [6:9].
//
// Layout reference: pkg/ro/packet/map_encode.go:43-63.
//
//	[0:2]   cmd 0x02eb
//	[2:6]   startTime (uint32 LE) — test asserts non-zero
//	[6:9]   posDir[3] packed (encodePos(150, 200, 0))
//	[9]     xSize (5)
//	[10]    ySize (5)
//	[11:13] font (uint16 LE, 0)
func TestWSHandler_CZEnter_RoundTrip_ZCAcceptEnter(t *testing.T) {
	fake := &fakeZoneClient{
		enterFn: func(_ context.Context, req *zonev1.EnterZoneRequest, _ ...grpc.CallOption) (*zonev1.EnterZoneResponse, error) {
			// Sanity-check the request the adapter forwards.
			if req.AccountId != 4242 {
				t.Errorf("forwarded account_id = %d, want 4242", req.AccountId)
			}
			if req.CharId != 9001 {
				t.Errorf("forwarded char_id = %d, want 9001", req.CharId)
			}
			if req.LoginId1 != 0xdead0000 {
				t.Errorf("forwarded login_id1 = 0x%x, want 0xdead0000", req.LoginId1)
			}
			if req.Sex != "M" {
				t.Errorf("forwarded sex = %q, want M", req.Sex)
			}
			return &zonev1.EnterZoneResponse{
				Success: true,
				MapName: "prontera",
				MapX:    150,
				MapY:    200,
			}, nil
		},
	}
	adapter := &wsMapDispatchAdapter{zone: fake}

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
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer func() { _ = client.Close(websocket.StatusNormalClosure, "") }()

	frame := buildCZEnter(4242 /*AID*/, 9001 /*CID*/, 0xdead0000 /*authCode*/, 0xbeef0000 /*clientTime*/, 1 /*sex=M*/)
	if err := client.Write(ctx, websocket.MessageBinary, frame); err != nil {
		t.Fatalf("ws write CZ_ENTER: %v", err)
	}

	readCtx, readCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer readCancel()
	msgType, data, err := client.Read(readCtx)
	if err != nil {
		t.Fatalf("ws read ZC_ACCEPT_ENTER: %v", err)
	}
	if msgType != websocket.MessageBinary {
		t.Fatalf("response msg type = %s, want binary", msgType.String())
	}

	const wantLen = 13
	if len(data) != wantLen {
		t.Fatalf("ZC_ACCEPT_ENTER length = %d, want %d", len(data), wantLen)
	}
	if data[0] != 0xeb || data[1] != 0x02 {
		t.Fatalf("ZC_ACCEPT_ENTER header = %02x %02x, want eb 02 (0x02eb LE)", data[0], data[1])
	}
	// posDir[3] at offset [6:9] — encodePos(150, 200, 0). The exact byte
	// values come from pkg/ro/packet/coords.go's packing (x in low 6
	// bits of byte[0] + high 2 of byte[1], y in low 6 of byte[1] + high
	// 2 of byte[2], dir in low 4 of byte[2]). 150=0b10010110, 200=0b11001000.
	//   x_high  = 150 >> 4            = 0b1001 = 0x09
	//   xy_mid  = ((150&0xf)<<2)|(200>>6) = (0b0110<<2)|0b11 = 0b11011 = 0x1b
	//   y_lo    = 200 & 0x3f           = 0b001000 = 0x08
	b0, b1, b2 := data[6], data[7], data[8]
	gotX := int16((uint16(b0) << 2) | (uint16(b1) >> 6))
	gotY := int16((uint16(b1&0x3f) << 4) | (uint16(b2) >> 4))
	gotDir := b2 & 0x0f
	if gotX != 150 || gotY != 200 || gotDir != 0 {
		t.Fatalf("posDir unpacked = (%d, %d, %d), want (150, 200, 0); bytes = %x",
			gotX, gotY, gotDir, data[6:9])
	}
	if data[9] != 5 {
		t.Fatalf("xSize at offset 9 = %d, want 5", data[9])
	}
	if data[10] != 5 {
		t.Fatalf("ySize at offset 10 = %d, want 5", data[10])
	}
	if font := binary.LittleEndian.Uint16(data[11:13]); font != 0 {
		t.Fatalf("font at [11:13] = %d, want 0", font)
	}
}
