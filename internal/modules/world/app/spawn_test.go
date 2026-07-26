//go:build unit

package app_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	chardomain "github.com/bouroo/goAthena/internal/modules/character/domain"
	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// fakeCharGetter is a world/domain.CharacterGetter stand-in for the spawn use
// case: it returns a fixed character for the (accountID, charID) it was seeded
// with, or ErrCharacterNotFound otherwise — the same contract the GORM adapter
// upholds. AccountID-scoped lookup is the impersonation guard, so the fake
// honors it: a mismatched account yields not-found, never the wrong character.
type fakeCharGetter struct {
	chars map[uint64]chardomain.Character
}

func charKey(accountID, charID uint32) uint64 { return uint64(accountID)<<32 | uint64(charID) }

func (f *fakeCharGetter) GetByID(_ context.Context, accountID, charID uint32) (*chardomain.Character, error) {
	if c, ok := f.chars[charKey(accountID, charID)]; ok {
		cp := c
		return &cp, nil
	}
	return nil, chardomain.ErrCharacterNotFound
}

// memMapStore is a domain.MapStore backed by an in-memory map of pre-built
// *domain.Map, so the spawn test does not touch the filesystem. Each entry
// carries a fresh AOI grid sized to the test scenario.
type memMapStore struct {
	maps map[string]*domain.Map
}

func (s *memMapStore) Load(_ context.Context, name string) (*domain.Map, error) {
	if mp, ok := s.maps[name]; ok {
		return mp, nil
	}
	return nil, errors.New("memMapStore: unknown map " + name)
}

// newTestMap builds a domain.Map with a real AOI grid of the given dimensions
// and no pathfinder (spawn does not pathfind). The grid is the only engine
// EnterWorld touches.
func newTestMap(width, height int) *domain.Map {
	return &domain.Map{AOI: aoi.NewGridManager(width, height)}
}

// aNovice is a minimal combat-slice character at the Prontera novice start
// cell, matching the seededAccountID/char_id the M3 e2e uses so the L2 and L3
// assertions share one mental model of the spawn appearance.
func aNovice(accountID, charID uint32) chardomain.Character {
	return chardomain.Character{
		CharID: charID, AccountID: accountID, Name: "Tester", Class: 0,
		BaseLevel: 1, Hair: 1, HairColor: 7, Sex: 1,
		LastMap: "prontera", LastX: 53, LastY: 111,
		MaxHP: 40, HP: 40, Manner: 5,
	}
}

// expectSpawnUnit encodes the ZC_SPAWN_UNIT frame the spawn use case should emit
// for a PC with the given identity/position, applying the PACKETVER 20250604 PC
// defaults the Player.SpawnUnit builder also applies (Speed=150, ObjectType=0,
// MaxHP=HP=-1, xSize=ySize=5, Body=0, honor=manner). Built independently from
// the SUT so a drift in SpawnUnit() is caught as a byte mismatch.
func expectSpawnUnit(t *testing.T, aid, gid uint32, posX, posY int16, dir uint8, name string) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (packet.SpawnUnitResponse{
		ObjectType: 0, AID: aid, GID: gid,
		Speed: domain.PCWalkSpeed, Job: 0, Head: 1, HeadPalette: 7,
		Honor: 5, Sex: 1, PosX: posX, PosY: posY, Dir: dir,
		XSize: 5, YSize: 5, CLevel: 1, MaxHP: -1, HP: -1, Body: 0,
		Name: name,
	}).Encode(&buf))
	return buf.Bytes()
}

// splitFrames slices a captured byte stream into per-packet frames using the
// 2-byte length prefix every map packet carries at [2:4]. ZC_SPAWN_UNIT is 107
// bytes; this helper is generic so it tolerates any length-prefixed frame the
// spawn flow emits.
func splitFrames(t *testing.T, b []byte) [][]byte {
	t.Helper()
	var out [][]byte
	for len(b) >= 4 {
		length := int(binary.LittleEndian.Uint16(b[2:4])) // total length incl. header
		require.GreaterOrEqual(t, length, 4, "frame length covers the header")
		require.LessOrEqual(t, length, len(b), "frame length does not overrun the buffer")
		out = append(out, b[:length])
		b = b[length:]
	}
	require.Empty(t, b, "no trailing bytes after the last frame")
	return out
}

// frameAID reads the AID field at [5:9] of a ZC_SPAWN_UNIT frame so the
// neighbor-broadcast test can identify which frame belongs to which player
// without re-deriving the whole layout.
func frameAID(frame []byte) uint32 { return binary.LittleEndian.Uint32(frame[5:9]) }

// TestSpawnService_EnterWorld_SelfSpawn is the M4b happy path: an entering
// player with no neighbors loads its character, registers, joins the AOI grid,
// and receives exactly one ZC_SPAWN_UNIT (its own self-spawn). The spawn frame
// must be byte-identical to the independently-encoded expectation, and the
// player must be resolvable in the registry afterward.
func TestSpawnService_EnterWorld_SelfSpawn(t *testing.T) {
	t.Parallel()
	const aid, cid uint32 = 2000000, 150000
	chars := &fakeCharGetter{chars: map[uint64]chardomain.Character{
		charKey(aid, cid): aNovice(aid, cid),
	}}
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": newTestMap(200, 200)}}
	registry := domain.NewPlayerRegistry()
	svc := app.NewSpawnService(chars, maps, registry)

	conn := &captureConn{role: gwdomain.RoleMap, remote: "127.0.0.1:5121"}
	// spawn zeros ⇒ use the character's persisted last_x/last_y (the M4b path).
	err := svc.EnterWorld(context.Background(), conn, aid, cid, app.SpawnPoint{})
	require.NoError(t, err)

	// Exactly one frame: the self-spawn. A lone enterer has no neighbors to
	// exchange with, so nothing else is written.
	frames := splitFrames(t, conn.buf.Bytes())
	require.Len(t, frames, 1, "lone enterer sees only its self-spawn")
	want := expectSpawnUnit(t, aid, cid, 53, 111, 0, "Tester")
	assert.Equal(t, want, frames[0], "self-spawn ZC_SPAWN_UNIT bytes")

	p, ok := registry.ByAccount(aid)
	require.True(t, ok, "player registered")
	assert.Same(t, conn, p.Conn, "registry holds the entering conn")
}

// TestSpawnService_EnterWorld_NeighborBroadcast is the two-way spawn exchange:
// a player already on the map (registered + in AOI) sees the newcomer's spawn,
// and the newcomer sees the existing player's spawn. Both conns receive exactly
// one neighbor frame; the newcomer's self-spawn is not duplicated to itself.
func TestSpawnService_EnterWorld_NeighborBroadcast(t *testing.T) {
	t.Parallel()
	const newcomerAID, newcomerCID uint32 = 2000001, 150001
	const neighborAID, neighborCID uint32 = 2000002, 150002
	chars := &fakeCharGetter{chars: map[uint64]chardomain.Character{
		charKey(newcomerAID, newcomerCID): aNovice(newcomerAID, newcomerCID),
		charKey(neighborAID, neighborCID): withName(aNovice(neighborAID, neighborCID), "Neighbor"),
	}}
	mp := newTestMap(200, 200)
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": mp}}
	registry := domain.NewPlayerRegistry()

	// Seed the neighbor first: register it and place its AOI entity at the same
	// cell the newcomer will land on so it is within AOI range. The neighbor
	// carries the same novice appearance the aNovice char yields, so its
	// SpawnUnit() frame is byte-identical to the expected novice frame and the
	// test can assert the broadcast bytes exactly rather than just the routing.
	neighborConn := &captureConn{role: gwdomain.RoleMap, remote: "n"}
	neighbor := &domain.Player{
		Conn: neighborConn, EntityID: aoi.EntityID(neighborAID),
		AccountID: neighborAID, CharID: neighborCID, MapName: "prontera",
		PosX: 53, PosY: 111, Name: "Neighbor",
		Head: 1, HeadPalette: 7, Manner: 5, Sex: 1, CLevel: 1,
	}
	require.NoError(t, registry.Register(neighbor))
	require.NoError(t, mp.AOI.AddEntity(&aoi.Entity{ID: neighbor.EntityID, Type: aoi.EntityPlayer, X: 53, Y: 111}))

	svc := app.NewSpawnService(chars, maps, registry)
	newcomerConn := &captureConn{role: gwdomain.RoleMap, remote: "c"}
	err := svc.EnterWorld(context.Background(), newcomerConn, newcomerAID, newcomerCID, app.SpawnPoint{})
	require.NoError(t, err)

	// Newcomer: self-spawn + one neighbor spawn, byte-exact.
	newcomerFrames := splitFrames(t, newcomerConn.buf.Bytes())
	require.Len(t, newcomerFrames, 2, "newcomer sees self + one neighbor")
	assert.Equal(t, expectSpawnUnit(t, newcomerAID, newcomerCID, 53, 111, 0, "Tester"), newcomerFrames[0], "first frame is the self-spawn")
	assert.Equal(t, expectSpawnUnit(t, neighborAID, neighborCID, 53, 111, 0, "Neighbor"), newcomerFrames[1], "second frame is the neighbor")

	// Neighbor: only the newcomer's spawn (no self-spawn to itself).
	neighborFrames := splitFrames(t, neighborConn.buf.Bytes())
	require.Len(t, neighborFrames, 1, "neighbor sees only the newcomer")
	assert.Equal(t, expectSpawnUnit(t, newcomerAID, newcomerCID, 53, 111, 0, "Tester"), neighborFrames[0], "neighbor received the newcomer's spawn")

	// Both players are registered and on the grid.
	_, ok := registry.ByAccount(newcomerAID)
	require.True(t, ok, "newcomer registered")
	_, ok = registry.ByAccount(neighborAID)
	require.True(t, ok, "neighbor still registered")
}

// TestSpawnService_EnterWorld_CharNotFound asserts the impersonation guard: a
// CZ_ENTER whose (accountID, charID) is not in the store fails with
// ErrCharacterNotFound wrapped in context, and nothing is written to the conn
// or registered.
func TestSpawnService_EnterWorld_CharNotFound(t *testing.T) {
	t.Parallel()
	chars := &fakeCharGetter{chars: map[uint64]chardomain.Character{}}
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": newTestMap(200, 200)}}
	registry := domain.NewPlayerRegistry()
	svc := app.NewSpawnService(chars, maps, registry)

	conn := &captureConn{role: gwdomain.RoleMap}
	err := svc.EnterWorld(context.Background(), conn, 999, 1, app.SpawnPoint{})
	assert.ErrorIs(t, err, chardomain.ErrCharacterNotFound)
	assert.Empty(t, conn.buf.Bytes(), "no frame written on a failed lookup")
	_, ok := registry.ByAccount(999)
	assert.False(t, ok, "nothing registered on failure")
}

// TestSpawnService_EnterWorld_MapLoadFail asserts a map-store failure is
// propagated (not swallowed) and the player is not registered.
func TestSpawnService_EnterWorld_MapLoadFail(t *testing.T) {
	t.Parallel()
	const aid, cid uint32 = 2000000, 150000
	chars := &fakeCharGetter{chars: map[uint64]chardomain.Character{
		charKey(aid, cid): aNovice(aid, cid),
	}}
	// No "prontera" entry ⇒ Load returns an error.
	maps := &memMapStore{maps: map[string]*domain.Map{}}
	registry := domain.NewPlayerRegistry()
	svc := app.NewSpawnService(chars, maps, registry)

	conn := &captureConn{role: gwdomain.RoleMap}
	err := svc.EnterWorld(context.Background(), conn, aid, cid, app.SpawnPoint{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "prontera")
	_, ok := registry.ByAccount(aid)
	assert.False(t, ok, "not registered when the map fails to load")
}

// TestSpawnService_EnterWorld_SpawnOverride asserts a non-zero caller spawn
// overrides the character's persisted position — the M3 default-spawn path that
// M4b's caller (MapEnterHandler) still drives via DefaultSpawn when it does not
// want the persisted cell.
func TestSpawnService_EnterWorld_SpawnOverride(t *testing.T) {
	t.Parallel()
	const aid, cid uint32 = 2000000, 150000
	chars := &fakeCharGetter{chars: map[uint64]chardomain.Character{
		charKey(aid, cid): aNovice(aid, cid),
	}}
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": newTestMap(200, 200)}}
	registry := domain.NewPlayerRegistry()
	svc := app.NewSpawnService(chars, maps, registry)

	conn := &captureConn{role: gwdomain.RoleMap}
	override := app.SpawnPoint{PosX: 100, PosY: 50, Dir: 4}
	require.NoError(t, svc.EnterWorld(context.Background(), conn, aid, cid, override))

	frames := splitFrames(t, conn.buf.Bytes())
	require.Len(t, frames, 1)
	assert.Equal(t, expectSpawnUnit(t, aid, cid, 100, 50, 4, "Tester"), frames[0], "override wins over char last_x/last_y")
}

// TestSpawnService_EnterWorld_DuplicateAccountRollsBack asserts a second enter
// for an already-registered account fails at Register (one PC per account) and
// leaves no AOI entity behind. The first session's player is untouched.
func TestSpawnService_EnterWorld_DuplicateAccountRollsBack(t *testing.T) {
	t.Parallel()
	const aid, cid uint32 = 2000000, 150000
	chars := &fakeCharGetter{chars: map[uint64]chardomain.Character{
		charKey(aid, cid): aNovice(aid, cid),
	}}
	mp := newTestMap(200, 200)
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": mp}}
	registry := domain.NewPlayerRegistry()
	// Pre-register the same account to force the duplicate path.
	require.NoError(t, registry.Register(&domain.Player{AccountID: aid, MapName: "prontera"}))

	svc := app.NewSpawnService(chars, maps, registry)
	conn := &captureConn{role: gwdomain.RoleMap}
	err := svc.EnterWorld(context.Background(), conn, aid, cid, app.SpawnPoint{})
	assert.ErrorIs(t, err, domain.ErrPlayerAlreadyRegistered)
	assert.Empty(t, conn.buf.Bytes())
	assert.Equal(t, 0, mp.AOI.EntityCount(), "no AOI entity added on a failed register")
}

// TestSpawnService_EnterWorld_DeadNeighborDropped asserts a neighbor whose Conn
// write fails is torn down (unregistered + removed from AOI) so its stale entity
// stops polluting future broadcasts, and the entering player's spawn still
// succeeds — a dead peer is a routine disconnect, not an abort.
func TestSpawnService_EnterWorld_DeadNeighborDropped(t *testing.T) {
	t.Parallel()
	const newcomerAID, newcomerCID uint32 = 2000001, 150001
	const neighborAID, neighborCID uint32 = 2000002, 150002
	chars := &fakeCharGetter{chars: map[uint64]chardomain.Character{
		charKey(newcomerAID, newcomerCID): aNovice(newcomerAID, newcomerCID),
	}}
	mp := newTestMap(200, 200)
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": mp}}
	registry := domain.NewPlayerRegistry()

	// A neighbor whose Write always fails — simulates a closed socket.
	deadConn := &errorConn{err: errors.New("socket closed")}
	neighbor := &domain.Player{
		Conn: deadConn, EntityID: aoi.EntityID(neighborAID),
		AccountID: neighborAID, CharID: neighborCID, MapName: "prontera",
		PosX: 53, PosY: 111, Name: "Dead",
	}
	require.NoError(t, registry.Register(neighbor))
	require.NoError(t, mp.AOI.AddEntity(&aoi.Entity{ID: neighbor.EntityID, Type: aoi.EntityPlayer, X: 53, Y: 111}))

	svc := app.NewSpawnService(chars, maps, registry)
	newcomerConn := &captureConn{role: gwdomain.RoleMap}
	err := svc.EnterWorld(context.Background(), newcomerConn, newcomerAID, newcomerCID, app.SpawnPoint{})
	require.NoError(t, err, "enter succeeds despite a dead neighbor")

	// The dead neighbor is gone from both the registry and the grid.
	_, ok := registry.ByAccount(neighborAID)
	assert.False(t, ok, "dead neighbor unregistered")
	_, _, _, alive := mp.AOI.EntityLocation(neighbor.EntityID)
	assert.False(t, alive, "dead neighbor removed from AOI")

	// The newcomer still got its self-spawn (the dead neighbor's spawn-to-newcomer
	// write also fails, but that path drops the neighbor rather than aborting).
	frames := splitFrames(t, newcomerConn.buf.Bytes())
	require.GreaterOrEqual(t, len(frames), 1, "newcomer still self-spawns")
	assert.Equal(t, newcomerAID, frameAID(frames[0]), "first frame is the newcomer's self-spawn")
}

// withName returns a copy of c with the name field overridden, for seeding a
// neighbor whose spawn frame must carry a distinct name.
func withName(c chardomain.Character, name string) chardomain.Character {
	c.Name = name
	return c
}

// errorConn is a gwdomain.Conn whose Write always fails — for the dead-neighbor
// scenario. The other Conn methods are unused in the spawn flow.
type errorConn struct{ err error }

func (c *errorConn) Role() gwdomain.Role       { return gwdomain.RoleMap }
func (c *errorConn) SetRole(gwdomain.Role)     {}
func (c *errorConn) Auth() gwdomain.ConnAuth   { return gwdomain.ConnAuth{} }
func (c *errorConn) SetAuth(gwdomain.ConnAuth) {}
func (c *errorConn) RemoteAddr() string        { return "dead" }
func (c *errorConn) Write([]byte) error        { return c.err }
func (c *errorConn) Close() error              { return nil }
