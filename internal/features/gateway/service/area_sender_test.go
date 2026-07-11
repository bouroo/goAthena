//go:build unit

package service

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	identityv1 "github.com/bouroo/goAthena/api/pb/identity/v1"
	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	"github.com/bouroo/goAthena/internal/features/gateway/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// fakeAreaSender records every SendAreaEntities call so the on-enter
// path can be asserted without booting the NATS-backed subscriber. It
// is file-scoped because it is only meaningful to this test; the
// broadcast_test.go suite uses a real *BroadcastSubscriber instead.
type fakeAreaSender struct {
	calls []fakeAreaSenderCall
}

type fakeAreaSenderCall struct {
	mapName    string
	excludeAID uint32
}

func (f *fakeAreaSender) SendAreaEntities(mapName string, excludeAID uint32, _ domain.Responder) {
	f.calls = append(f.calls, fakeAreaSenderCall{mapName: mapName, excludeAID: excludeAID})
}

// TestDispatchHandler_OnEnter_FiresAreaSender proves the Phase-1
// "second player sees the first" contract: when CZ_ENTER completes
// successfully, the dispatch handler invokes the broadcast area-spawner
// exactly once, with the map name returned by the zone service and the
// entering player's own AID as the excludeAID (so the entering player
// does not double-spawn its own sprite).
//
// The test mirrors TestDispatchHandler_CZEnter_Success_CachesAccountID
// — same fake zone/identity clients, same CZ_ENTER frame — and only
// adds the area-sender assertion. buildCZEnter is the dispatch_test.go
// helper; same package, so it is in scope.
func TestDispatchHandler_OnEnter_FiresAreaSender(t *testing.T) {
	t.Parallel()

	zone := &fakeZoneClient{
		enterFn: func(_ context.Context, _ *zonev1.EnterZoneRequest, _ ...grpc.CallOption) (*zonev1.EnterZoneResponse, error) {
			return &zonev1.EnterZoneResponse{
				Success: true,
				MapName: "prontera",
				MapX:    150,
				MapY:    200,
			}, nil
		},
	}
	identity := &fakeIdentityClient{
		getCharacterFn: func(_ context.Context, _ *identityv1.GetCharacterRequest) (*identityv1.GetCharacterResponse, error) {
			return &identityv1.GetCharacterResponse{
				Success:   true,
				Character: &identityv1.CharacterDetail{CharId: 9001, Name: "alpha"},
			}, nil
		},
	}

	h := NewDispatchHandler(identity, zone, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)
	area := &fakeAreaSender{}
	h.SetAreaSender(area)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1}
	if err := h.HandlePacket(context.Background(), &conn, resp, packet.HeaderCZENTER,
		buildCZEnter(4242, 9001, 0xdead0000, 0xbeef0000, 1)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	if conn.AccountID != 4242 {
		t.Fatalf("after successful CZ_ENTER, conn.AccountID = %d, want 4242", conn.AccountID)
	}
	if conn.MapName != "prontera" {
		t.Fatalf("after successful CZ_ENTER, conn.MapName = %q, want %q", conn.MapName, "prontera")
	}
	if got := len(area.calls); got != 1 {
		t.Fatalf("area-sender call count = %d, want 1", got)
	}
	call := area.calls[0]
	if call.mapName != "prontera" {
		t.Errorf("area-sender mapName = %q, want %q", call.mapName, "prontera")
	}
	if call.excludeAID != 4242 {
		t.Errorf("area-sender excludeAID = %d, want 4242 (entering player's AID)", call.excludeAID)
	}
}

// TestDispatchHandler_OnEnter_NoAreaSender_NoPanic proves the nil
// guard on the area-sender field: when SetAreaSender is not called (the
// unit-test default), the CZ_ENTER happy path still completes without
// panicking. This is the regression net for the interface-vs-pointer
// nil-safety split (the field is an interface type, so a nil guard is
// required at the call site — a nil *BroadcastSubscriber would be
// nil-receiver-safe, but the field is the interface type).
func TestDispatchHandler_OnEnter_NoAreaSender_NoPanic(t *testing.T) {
	t.Parallel()

	zone := &fakeZoneClient{
		enterFn: func(_ context.Context, _ *zonev1.EnterZoneRequest, _ ...grpc.CallOption) (*zonev1.EnterZoneResponse, error) {
			return &zonev1.EnterZoneResponse{Success: true, MapName: "prontera", MapX: 150, MapY: 200}, nil
		},
	}
	identity := &fakeIdentityClient{
		getCharacterFn: func(_ context.Context, _ *identityv1.GetCharacterRequest) (*identityv1.GetCharacterResponse, error) {
			return nil, status.Error(codes.Unavailable, "identity down")
		},
	}
	h := NewDispatchHandler(identity, zone, 20250604,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, NewSessionRegistry(), nil, nil)
	// Deliberately do NOT call SetAreaSender — the field stays at the
	// zero value (nil interface).

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1}
	if err := h.HandlePacket(context.Background(), &conn, resp, packet.HeaderCZENTER,
		buildCZEnter(4242, 9001, 0xdead0000, 0xbeef0000, 1)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil (nil area-sender must not abort the handshake)", err)
	}
}
