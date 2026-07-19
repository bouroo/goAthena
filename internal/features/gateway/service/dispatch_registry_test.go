//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	identityv1 "github.com/bouroo/goAthena/api/pb/identity/v1"
	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
	"github.com/bouroo/goAthena/internal/features/gateway/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// TestDispatchHandler_CZEnter_RegistersSession asserts the Phase 1
// Step 2c wiring: a successful CZ_ENTER installs a Session in the
// registry with the right CharID + MapName, the View is populated from
// the same GetCharacter RPC that feeds the self-spawn (no duplicate
// RPC), and the Responder is the per-connection transport Responder
// (the test exercises this with the same bufResponder the handler
// receives).
func TestDispatchHandler_CZEnter_RegistersSession(t *testing.T) {
	t.Parallel()

	zone := &fakeZoneClient{
		enterFn: func(_ context.Context, _ *zonev1.EnterZoneRequest, _ ...grpc.CallOption) (*zonev1.EnterZoneResponse, error) {
			return &zonev1.EnterZoneResponse{
				Success: true,
				MapName: "prt_fild08",
				MapX:    150,
				MapY:    200,
			}, nil
		},
	}
	identity := &fakeIdentityClient{
		getCharacterFn: func(_ context.Context, _ *identityv1.GetCharacterRequest) (*identityv1.GetCharacterResponse, error) {
			return &identityv1.GetCharacterResponse{
				Success: true,
				Character: &identityv1.CharacterDetail{
					CharId:       9001,
					Name:         "alpha",
					ClassId:      7,
					BaseLevel:    50,
					Hp:           1234,
					MaxHp:        2000,
					Hair:         5,
					HairColor:    3,
					ClothesColor: 1,
					Weapon:       1101,
					Shield:       0,
					HeadTop:      0,
					HeadMid:      0,
					HeadBottom:   0,
					Robe:         0,
					Sex:          1,
				},
			}, nil
		},
	}

	registry := NewSessionRegistry()
	h := NewDispatchHandler(identity, zone, 20250604, 20000000, 20260000,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, registry, nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1}
	if err := h.HandlePacket(context.Background(), &conn, resp, packet.HeaderCZENTER,
		buildCZEnter(4242, 9001, 0xdead0000, 0xbeef0000, 1)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	// Connection-side state must be cached as before.
	require.Equal(t, uint32(4242), conn.AccountID, "conn.AccountID must be cached after CZ_ENTER")
	require.Equal(t, uint32(9001), conn.CharID, "conn.CharID must be cached after CZ_ENTER")
	require.Equal(t, "prt_fild08", conn.MapName, "conn.MapName must be cached after CZ_ENTER")

	// The session must be installed in the registry, keyed by
	// AccountID, with CharID/MapName/View populated from the
	// same GetCharacter RPC that fed the self-spawn.
	got, ok := registry.Get(4242)
	require.True(t, ok, "registry.Get(4242) must succeed after successful CZ_ENTER")
	assert.Equal(t, uint32(9001), got.CharID, "session CharID must match conn.CharID")
	assert.Equal(t, "prt_fild08", got.MapName, "session MapName must match zone EnterZone response")
	assert.NotNil(t, got.Responder, "session Responder must be the per-connection transport Responder")
	// View must be populated — the helper mirrors the self-spawn
	// mapping; verify a handful of representative fields.
	assert.Equal(t, uint8(0), got.View.ObjectType, "View.ObjectType must be 0 (PC)")
	assert.Equal(t, uint32(4242), got.View.AID, "View.AID must be the account ID")
	assert.Equal(t, uint32(9001), got.View.GID, "View.GID must be the char ID")
	assert.Equal(t, int16(150), got.View.Speed, "View.Speed must default to 150")
	assert.Equal(t, int16(7), got.View.Job, "View.Job must come from identity.ClassId")
	assert.Equal(t, uint16(5), got.View.Head, "View.Head must come from identity.Hair")
	assert.Equal(t, uint32(1101), got.View.Weapon, "View.Weapon must come from identity.Weapon")
	assert.Equal(t, int16(50), got.View.CLevel, "View.CLevel must come from identity.BaseLevel")
	assert.Equal(t, int32(2000), got.View.MaxHP, "View.MaxHP must come from identity.MaxHp")
	assert.Equal(t, int32(1234), got.View.HP, "View.HP must come from identity.Hp")
	assert.Equal(t, "alpha", got.View.Name, "View.Name must come from identity.Name")
}

// TestDispatchHandler_CZEnter_IdentityFails_StillRegisters asserts
// that a successful zone EnterZone but a failing identity
// GetCharacter still installs a session — the View snapshot is left
// at its zero value, but the session is in the registry so the
// per-character View can be back-filled later. The handshake must
// still complete (existing M7a behaviour).
func TestDispatchHandler_CZEnter_IdentityFails_StillRegisters(t *testing.T) {
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
			return nil, status.Error(codes.Unavailable, "identity down")
		},
	}

	registry := NewSessionRegistry()
	h := NewDispatchHandler(identity, zone, 20250604, 20000000, 20260000,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, registry, nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1}
	if err := h.HandlePacket(context.Background(), &conn, resp, packet.HeaderCZENTER,
		buildCZEnter(4242, 9001, 0xdead0000, 0xbeef0000, 1)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	// Session must be installed with CharID + MapName, View empty
	// (the helper is only called when char != nil).
	got, ok := registry.Get(4242)
	require.True(t, ok, "session must be installed even when identity GetCharacter fails")
	assert.Equal(t, uint32(9001), got.CharID)
	assert.Equal(t, "prontera", got.MapName)
	assert.Equal(t, domain.ViewData{}, got.View, "View must be zero-valued on identity failure")
}

// TestDispatchHandler_CZEnter_ZoneRejects_DoesNotRegister asserts
// the negative case: a zone-side reject must NOT leave a session in
// the registry, and the connection's cached AccountID/CharID must
// remain zero (existing M7a behaviour).
func TestDispatchHandler_CZEnter_ZoneRejects_DoesNotRegister(t *testing.T) {
	t.Parallel()

	zone := &fakeZoneClient{
		enterFn: func(_ context.Context, _ *zonev1.EnterZoneRequest, _ ...grpc.CallOption) (*zonev1.EnterZoneResponse, error) {
			return &zonev1.EnterZoneResponse{Success: false, Error: "aoi grid full"}, nil
		},
	}

	registry := NewSessionRegistry()
	h := NewDispatchHandler(&fakeIdentityClient{}, zone, 20250604, 20000000, 20260000,
		newDispatchTestLogger(t), "prontera", parseIPv4("127.0.0.1"), 5121, registry, nil, nil)

	resp := &bufResponder{}
	conn := domain.ConnectionInfo{ID: 1}
	if err := h.HandlePacket(context.Background(), &conn, resp, packet.HeaderCZENTER,
		buildCZEnter(4242, 9001, 0xdead0000, 0xbeef0000, 1)); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}

	assert.Equal(t, uint32(0), conn.AccountID, "conn.AccountID must remain 0 on zone reject")
	assert.Equal(t, 0, registry.Len(), "registry must be empty on zone reject")
}
