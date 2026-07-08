//go:build unit

package service

import (
	"encoding/binary"
	"testing"

	natsgo "github.com/nats-io/nats.go"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	zonev1 "github.com/bouroo/goAthena/api/pb/zone/v1"
)

// On-wire opcodes the tests assert against. Constants inlined rather
// than imported from pkg/ro/packet so the test reads as "the gateway
// emits a 0x09fd packet" without a side trip to the packet package.
const (
	opcodeUnitWalking  = 0x09fd
	opcodeSpawnUnit    = 0x09fe
	opcodeNotifyVanish = 0x0080
)

// mustMoveMsg marshals a ZoneEvent_Moved for the given map and wraps
// it in a *natsgo.Msg with the matching subject. The marshalling is
// the production path's marshalling, so the test exercises the same
// payload the gateway will see in the integration test.
func mustMoveMsg(t *testing.T, mapName string, m *zonev1.EntityMoved) *natsgo.Msg {
	t.Helper()
	evt := &zonev1.ZoneEvent{Event: &zonev1.ZoneEvent_Moved{Moved: m}}
	data, err := proto.Marshal(evt)
	require.NoError(t, err, "marshal EntityMoved")
	return &natsgo.Msg{Subject: "zone.event." + mapName, Data: data}
}

// mustSpawnMsg mirrors mustMoveMsg for EntitySpawned.
func mustSpawnMsg(t *testing.T, mapName string, s *zonev1.EntitySpawned) *natsgo.Msg {
	t.Helper()
	evt := &zonev1.ZoneEvent{Event: &zonev1.ZoneEvent_Spawned{Spawned: s}}
	data, err := proto.Marshal(evt)
	require.NoError(t, err, "marshal EntitySpawned")
	return &natsgo.Msg{Subject: "zone.event." + mapName, Data: data}
}

// mustVanishMsg mirrors mustMoveMsg for EntityVanished.
func mustVanishMsg(t *testing.T, mapName string, v *zonev1.EntityVanished) *natsgo.Msg {
	t.Helper()
	evt := &zonev1.ZoneEvent{Event: &zonev1.ZoneEvent_Vanished{Vanished: v}}
	data, err := proto.Marshal(evt)
	require.NoError(t, err, "marshal EntityVanished")
	return &natsgo.Msg{Subject: "zone.event." + mapName, Data: data}
}

// newTestSubscriber wires a BroadcastSubscriber with a real registry
// and a no-op logger. The *natsinfra.Client is left nil — handleMsg
// does not touch nc — so unit tests do not need a live NATS server.
func newTestSubscriber(t *testing.T) (*BroadcastSubscriber, SessionRegistry) {
	t.Helper()
	registry := NewSessionRegistry()
	b := NewBroadcastSubscriber(nil, registry, zerolog.Nop())
	return b, registry
}

// countPacketsOfOpcode returns the number of recorded packets whose
// first 2 bytes (little-endian uint16 opcode) match the given
// opcode. Used to assert "observer received exactly N packets of
// opcode X" without indexing into the slice by hand.
func countPacketsOfOpcode(f *fakeResponder, opcode uint16) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, p := range f.packets {
		if len(p) >= 2 && binary.LittleEndian.Uint16(p[0:2]) == opcode {
			n++
		}
	}
	return n
}

// firstPacketOfOpcode returns the first recorded packet matching
// opcode, or nil if none. Useful for shape assertions
// (length == Size()) where any matching packet suffices.
func firstPacketOfOpcode(f *fakeResponder, opcode uint16) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, p := range f.packets {
		if len(p) >= 2 && binary.LittleEndian.Uint16(p[0:2]) == opcode {
			out := make([]byte, len(p))
			copy(out, p)
			return out
		}
	}
	return nil
}

// packetCount returns the total number of recorded packets. Lock-safe
// for use after the broadcast callback has returned.
func packetCount(f *fakeResponder) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.packets)
}

func TestBroadcastSubscriber_handleMsg_Moved_FansOutToObservers(t *testing.T) {
	t.Parallel()
	b, registry := newTestSubscriber(t)

	// Mover (aid=1) and observer (aid=2) both on prontera. The
	// responder is the package-level fakeResponder so we can assert
	// on the recorded bytes.
	mover := newSampleSession(1, 11, "prontera")
	observer := newSampleSession(2, 22, "prontera")
	registry.Register(1, mover)
	registry.Register(2, observer)
	moverResp := mover.Responder.(*fakeResponder)
	observerResp := observer.Responder.(*fakeResponder)

	evt := &zonev1.EntityMoved{
		EntityId:      1,
		SrcX:          5,
		SrcY:          5,
		DestX:         10,
		DestY:         10,
		MoveStartTime: 1_000_000_000,
	}
	b.handleMsg(mustMoveMsg(t, "prontera", evt))

	assert.Equal(t, 1, countPacketsOfOpcode(observerResp, opcodeUnitWalking),
		"observer must receive exactly one 0x09fd packet")
	pkt := firstPacketOfOpcode(observerResp, opcodeUnitWalking)
	require.NotNil(t, pkt)
	assert.Equal(t, 114, len(pkt), "UnitWalkingResponse wire length is 114")

	assert.Equal(t, 0, countPacketsOfOpcode(moverResp, opcodeUnitWalking),
		"mover must be excluded from its own broadcast")

	pos, ok := b.posOf(1)
	require.True(t, ok, "mover's cell must be cached after onMoved")
	assert.Equal(t, int16(10), pos.X, "cache X reflects DestX")
	assert.Equal(t, int16(10), pos.Y, "cache Y reflects DestY")
}

func TestBroadcastSubscriber_handleMsg_Spawned_FansOutToObservers(t *testing.T) {
	t.Parallel()
	b, registry := newTestSubscriber(t)

	spawned := newSampleSession(1, 11, "prontera")
	observer := newSampleSession(2, 22, "prontera")
	registry.Register(1, spawned)
	registry.Register(2, observer)
	spawnedResp := spawned.Responder.(*fakeResponder)
	observerResp := observer.Responder.(*fakeResponder)

	evt := &zonev1.EntitySpawned{
		EntityId:   1,
		EntityType: 0,
		X:          42,
		Y:          84,
		Name:       "alpha",
	}
	b.handleMsg(mustSpawnMsg(t, "prontera", evt))

	assert.Equal(t, 1, countPacketsOfOpcode(observerResp, opcodeSpawnUnit),
		"observer must receive exactly one 0x09fe packet")
	pkt := firstPacketOfOpcode(observerResp, opcodeSpawnUnit)
	require.NotNil(t, pkt)
	assert.Equal(t, 107, len(pkt), "SpawnUnitResponse wire length is 107")

	assert.Equal(t, 0, countPacketsOfOpcode(spawnedResp, opcodeSpawnUnit),
		"spawning entity must be excluded from its own spawn broadcast")

	pos, ok := b.posOf(1)
	require.True(t, ok, "spawned entity's cell must be cached after onSpawned")
	assert.Equal(t, int16(42), pos.X, "cache X reflects spawn X")
	assert.Equal(t, int16(84), pos.Y, "cache Y reflects spawn Y")
}

func TestBroadcastSubscriber_handleMsg_Vanished_FansOutToObservers(t *testing.T) {
	t.Parallel()
	b, registry := newTestSubscriber(t)

	vanished := newSampleSession(1, 11, "prontera")
	observer := newSampleSession(2, 22, "prontera")
	registry.Register(1, vanished)
	registry.Register(2, observer)
	vanishedResp := vanished.Responder.(*fakeResponder)
	observerResp := observer.Responder.(*fakeResponder)

	evt := &zonev1.EntityVanished{EntityId: 1, Type: 1} // LOGOUT
	b.handleMsg(mustVanishMsg(t, "prontera", evt))

	assert.Equal(t, 1, countPacketsOfOpcode(observerResp, opcodeNotifyVanish),
		"observer must receive exactly one 0x0080 packet")
	pkt := firstPacketOfOpcode(observerResp, opcodeNotifyVanish)
	require.NotNil(t, pkt)
	assert.Equal(t, 7, len(pkt), "NotifyVanishResponse wire length is 7")

	assert.Equal(t, 0, countPacketsOfOpcode(vanishedResp, opcodeNotifyVanish),
		"vanishing entity must be excluded from its own vanish broadcast")

	// LOGOUT (type 1) must clear the cache so a future player entering
	// the same map does not re-spawn the gone character.
	_, ok := b.posOf(1)
	assert.False(t, ok, "LOGOUT must untrack the entity from the position cache")
}

func TestBroadcastSubscriber_handleMsg_BadSubject_DropsQuietly(t *testing.T) {
	t.Parallel()
	b, registry := newTestSubscriber(t)
	observer := newSampleSession(2, 22, "prontera")
	registry.Register(2, observer)
	observerResp := observer.Responder.(*fakeResponder)

	assert.NotPanics(t, func() {
		b.handleMsg(&natsgo.Msg{Subject: "other.thing", Data: []byte{0x00}})
	}, "bad subject must not panic")

	assert.Equal(t, 0, packetCount(observerResp), "bad subject must not produce any packet")
}

func TestBroadcastSubscriber_handleMsg_UnmarshalFailure_DropsQuietly(t *testing.T) {
	t.Parallel()
	b, registry := newTestSubscriber(t)
	observer := newSampleSession(2, 22, "prontera")
	registry.Register(2, observer)
	observerResp := observer.Responder.(*fakeResponder)

	// Subject is well-formed (so parseMapFromSubject accepts it) but
	// the body is not a valid ZoneEvent.
	assert.NotPanics(t, func() {
		b.handleMsg(&natsgo.Msg{Subject: "zone.event.prontera", Data: []byte{0xff, 0xfe, 0xfd}})
	}, "garbage payload must not panic")

	assert.Equal(t, 0, packetCount(observerResp), "garbage payload must not produce any packet")
}

func TestBroadcastSubscriber_handleMsg_Moved_EntityNotRegistered_NoFanout(t *testing.T) {
	t.Parallel()
	b, registry := newTestSubscriber(t)
	observer := newSampleSession(2, 22, "prontera")
	registry.Register(2, observer)
	observerResp := observer.Responder.(*fakeResponder)

	evt := &zonev1.EntityMoved{
		EntityId: 999, // not registered
		SrcX:     1,
		SrcY:     1,
		DestX:    7,
		DestY:    7,
	}
	assert.NotPanics(t, func() {
		b.handleMsg(mustMoveMsg(t, "prontera", evt))
	}, "unregistered mover must not panic")

	assert.Equal(t, 0, packetCount(observerResp),
		"unregistered mover must not produce a fan-out")

	// trackPos runs before lookup, so the cache must still be
	// updated. This is intentional: a future SetView on aid=999
	// (or a re-attribution by the dispatch handler) should see
	// up-to-date coordinates.
	pos, ok := b.posOf(999)
	require.True(t, ok, "cache must be updated even for unregistered entities")
	assert.Equal(t, int16(7), pos.X)
	assert.Equal(t, int16(7), pos.Y)
}

func TestBroadcastSubscriber_SendAreaEntities_SpawnsExistingEntities(t *testing.T) {
	t.Parallel()
	b, registry := newTestSubscriber(t)

	a := newSampleSession(1, 11, "prontera")
	bSession := newSampleSession(2, 22, "prontera")
	registry.Register(1, a)
	registry.Register(2, bSession)

	// respA and respB are the "entering client" responders used for
	// the on-enter SendAreaEntities call. They are distinct from the
	// responders inside the registered sessions so the test can
	// assert on the backfill spawn without also catching the
	// priming broadcast (which legitimately lands on bSession when
	// A spawns).
	respA := &fakeResponder{}
	respB := &fakeResponder{}

	// Prime A's cached position via a Spawned event for aid=1.
	primeA := &zonev1.EntitySpawned{EntityId: 1, EntityType: 0, X: 10, Y: 20, Name: "alpha"}
	b.handleMsg(mustSpawnMsg(t, "prontera", primeA))
	require.Equal(t, 0, packetCount(respA),
		"priming must not produce any packet on the on-enter responder for A")

	// B enters the map; SendAreaEntities must send B A's spawn.
	b.SendAreaEntities("prontera", 2, respB)
	assert.Equal(t, 1, countPacketsOfOpcode(respB, opcodeSpawnUnit),
		"B must receive exactly one spawn for A")
	pkt := firstPacketOfOpcode(respB, opcodeSpawnUnit)
	require.NotNil(t, pkt)
	assert.Equal(t, 107, len(pkt), "spawn packet wire length is 107")

	// Now ask SendAreaEntities to spawn the area for A (entering A
	// after B). B has no cached position yet, so A must receive
	// nothing — the no-position skip path.
	b.SendAreaEntities("prontera", 1, respA)
	assert.Equal(t, 0, packetCount(respA),
		"A must receive nothing because B has no cached position")

	// Prime B's position; A must then receive B's spawn.
	primeB := &zonev1.EntitySpawned{EntityId: 2, EntityType: 0, X: 30, Y: 40, Name: "beta"}
	b.handleMsg(mustSpawnMsg(t, "prontera", primeB))
	require.Equal(t, 0, packetCount(respA),
		"priming B must not produce any packet on the on-enter responder for A")

	b.SendAreaEntities("prontera", 1, respA)
	assert.Equal(t, 1, countPacketsOfOpcode(respA, opcodeSpawnUnit),
		"A must now receive B's spawn after B's position is cached")
}

func TestBroadcastSubscriber_NilReceiver_SendAreaEntities_NoPanic(t *testing.T) {
	t.Parallel()
	var nilSub *BroadcastSubscriber
	resp := &fakeResponder{}
	assert.NotPanics(t, func() {
		nilSub.SendAreaEntities("prontera", 1, resp)
	}, "nil *BroadcastSubscriber.SendAreaEntities must be a safe no-op")
}

func TestBroadcastSubscriber_Stop_Idempotent(t *testing.T) {
	t.Parallel()
	b, _ := newTestSubscriber(t)

	// Never started — first Stop must be a no-op that returns nil.
	require.NoError(t, b.Stop(), "first Stop on never-started subscriber must return nil")
	// And again — repeated Stop must also be a no-op.
	require.NoError(t, b.Stop(), "second Stop must remain idempotent")
}
