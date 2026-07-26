//go:build unit

package app_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/aoi"
	"github.com/bouroo/goAthena/pkg/ro/packet"
	"github.com/bouroo/goAthena/pkg/ro/pathfinding"
)

// fixedClock returns a deterministic moveStartTime so the self-ack and
// broadcast frames are byte-predictable. The value is a non-zero uint32 that
// fits the wire slot; the test asserts the exact byte, not the clock source.
type fixedClock struct{ val uint32 }

func (c fixedClock) MoveStart() uint32 { return c.val }

// walkableGrid is a tiny all-walkable Grid for the pathfinder so the move test
// does not need a real .gat file. Every in-bounds cell is walkable; the
// pathfinder's own validateEndpoints rejects an out-of-bounds target before
// Walkable is consulted, so an all-walkable grid still yields an OOB error for
// a target past width/height.
type walkableGrid struct{ w, h int }

func (g walkableGrid) Width() int             { return g.w }
func (g walkableGrid) Height() int            { return g.h }
func (g walkableGrid) Walkable(int, int) bool { return true }

// newTestMapWithPathfinder builds a domain.Map with a real AOI grid and a
// pathfinder over an all-walkable grid. newTestMap (spawn_test.go) has no
// pathfinder; the move test needs one so FindPath resolves a path.
func newTestMapWithPathfinder(width, height int) *domain.Map {
	return &domain.Map{
		AOI:        aoi.NewGridManager(width, height),
		Pathfinder: pathfinding.New(walkableGrid{width, height}),
	}
}

// expectNotifyPlayerMove encodes the ZC_NOTIFY_PLAYERMOVE self-ack the move
// worker should emit to the mover. Built independently from the SUT so a drift
// in the kernel encoder is caught as a byte mismatch.
//
// ZC_NOTIFY_PLAYERMOVE is a FIXED 12-byte frame with NO length prefix
// (moveStartTime occupies [2:6]); the shared splitFrames helper reads [2:4]
// as a length and would mis-parse it, so callers compare the whole conn
// buffer raw rather than splitting.
func expectNotifyPlayerMove(t *testing.T, moveStart uint32, srcX, srcY, destX, destY int16) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (packet.MapNotifyPlayerMoveResponse{
		MoveStartTime: moveStart,
		SrcX:          srcX,
		SrcY:          srcY,
		DestX:         destX,
		DestY:         destY,
	}).Encode(&buf))
	return buf.Bytes()
}

// expectUnitWalking encodes the ZC_UNIT_WALKING observer broadcast the move
// worker should emit to AOI neighbors. The appearance slice mirrors WalkUnit
// for a player seeded with the novice defaults (Head=1, HeadPalette=7,
// Manner=5, Sex=1, CLevel=1); only the move-specific fields differ.
func expectUnitWalking(t *testing.T, aid, gid uint32, srcX, srcY, destX, destY int16, moveStart uint32, name string) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (packet.UnitWalkingResponse{
		ObjectType:    0, // PC
		AID:           aid,
		GID:           gid,
		Speed:         domain.PCWalkSpeed,
		BodyState:     0,
		HealthState:   0,
		EffectState:   0,
		Job:           0,
		Head:          1,
		Weapon:        0,
		Shield:        0,
		Accessory:     0,
		MoveStartTime: moveStart,
		Accessory2:    0,
		Accessory3:    0,
		HeadPalette:   7,
		BodyPalette:   0,
		HeadDir:       0,
		Robe:          0,
		GUID:          0,
		GEmblemVer:    0,
		Honor:         5,
		Virtue:        0,
		IsPKModeON:    0,
		Sex:           1,
		SrcX:          srcX,
		SrcY:          srcY,
		DestX:         destX,
		DestY:         destY,
		XSize:         5,
		YSize:         5,
		CLevel:        1,
		Font:          0,
		MaxHP:         -1,
		HP:            -1,
		IsBoss:        0,
		Body:          0,
		Name:          name,
	}).Encode(&buf))
	return buf.Bytes()
}

// seedPlayer registers a player and adds its AOI entity at the given cell.
// The player carries the same novice appearance aNovice yields so the
// WalkUnit frame is byte-identical to the expected frame.
func seedPlayer(t *testing.T, registry *domain.PlayerRegistry, mp *domain.Map, conn gwdomain.Conn, accountID, charID uint32, posX, posY int16, name string) *domain.Player {
	t.Helper()
	p := &domain.Player{
		Conn:        conn,
		EntityID:    aoi.EntityID(accountID),
		AccountID:   accountID,
		CharID:      charID,
		MapName:     "prontera",
		PosX:        posX,
		PosY:        posY,
		Name:        name,
		Head:        1,
		HeadPalette: 7,
		Manner:      5,
		Sex:         1,
		CLevel:      1,
	}
	require.NoError(t, registry.Register(p))
	require.NoError(t, mp.AOI.AddEntity(&aoi.Entity{ID: p.EntityID, Type: aoi.EntityPlayer, X: int(posX), Y: int(posY)}))
	return p
}

// TestMoveService_RequestMove_SelfAck is the M4c happy path: a lone player
// sends a CZ_REQUEST_MOVE, the worker resolves the path, and the mover receives
// exactly one ZC_NOTIFY_PLAYERMOVE self-ack with the correct src/dest and the
// deterministic moveStartTime. The player's position is updated after the move.
//
// The self-ack is a fixed 12-byte frame with no length prefix, so the whole
// conn buffer is compared raw — splitFrames would mis-parse it.
func TestMoveService_RequestMove_SelfAck(t *testing.T) {
	t.Parallel()
	const aid, cid uint32 = 2000000, 150000
	const moveStart uint32 = 0x12345678

	mp := newTestMapWithPathfinder(200, 200)
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": mp}}
	registry := domain.NewPlayerRegistry()
	conn := &captureConn{role: gwdomain.RoleMap, remote: "127.0.0.1:5121"}
	seedPlayer(t, registry, mp, conn, aid, cid, 53, 111, "Tester")

	svc := app.NewMoveService(registry, maps, fixedClock{moveStart}, 8)
	// Resolve synchronously via the test seam (export_test.go) so assertions
	// run after the move resolves, with no Run-goroutine drain race.
	svc.ResolveForTest(context.Background(), aid, packet.CZRequestMoveRequest{DestX: 60, DestY: 115})

	// The mover receives exactly one frame: the self-ack. Compared raw because
	// ZC_NOTIFY_PLAYERMOVE has no length prefix.
	want := expectNotifyPlayerMove(t, moveStart, 53, 111, 60, 115)
	assert.Equal(t, want, conn.buf.Bytes(), "ZC_NOTIFY_PLAYERMOVE bytes")

	// Position is updated to the resolved destination.
	moved, ok := registry.ByAccount(aid)
	require.True(t, ok, "mover still registered after move")
	posX, posY, _ := moved.Position()
	assert.Equal(t, int16(60), posX)
	assert.Equal(t, int16(115), posY)
}

// TestMoveService_RequestMove_NeighborBroadcast is the two-way move broadcast:
// a player moves, the mover gets the self-ack, and a neighbor within AOI range
// gets the ZC_UNIT_WALKING broadcast. Each conn receives exactly one frame, so
// each buffer is compared raw — no splitFrames needed.
func TestMoveService_RequestMove_NeighborBroadcast(t *testing.T) {
	t.Parallel()
	const moverAID, moverCID uint32 = 2000001, 150001
	const neighborAID, neighborCID uint32 = 2000002, 150002
	const moveStart uint32 = 0xDEADBEEF

	mp := newTestMapWithPathfinder(200, 200)
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": mp}}
	registry := domain.NewPlayerRegistry()

	moverConn := &captureConn{role: gwdomain.RoleMap, remote: "m"}
	seedPlayer(t, registry, mp, moverConn, moverAID, moverCID, 53, 111, "Mover")

	neighborConn := &captureConn{role: gwdomain.RoleMap, remote: "n"}
	seedPlayer(t, registry, mp, neighborConn, neighborAID, neighborCID, 55, 111, "Neighbor")

	svc := app.NewMoveService(registry, maps, fixedClock{moveStart}, 8)
	svc.ResolveForTest(context.Background(), moverAID, packet.CZRequestMoveRequest{DestX: 60, DestY: 115})

	// Mover: self-ack only (raw compare — no length prefix).
	assert.Equal(t,
		expectNotifyPlayerMove(t, moveStart, 53, 111, 60, 115),
		moverConn.buf.Bytes(),
		"mover sees only the self-ack")

	// Neighbor: one ZC_UNIT_WALKING broadcast (raw compare — single frame).
	assert.Equal(t,
		expectUnitWalking(t, moverAID, moverCID, 53, 111, 60, 115, moveStart, "Mover"),
		neighborConn.buf.Bytes(),
		"neighbor sees the walk broadcast")
}

// TestMoveService_RequestMove_NoPathIgnored asserts a move to an out-of-bounds
// cell is silently dropped: no self-ack, no broadcast, no disconnect. The
// pathfinder's validateEndpoints rejects the OOB target before Walkable is
// consulted, and the worker ignores the error.
func TestMoveService_RequestMove_NoPathIgnored(t *testing.T) {
	t.Parallel()
	const aid, cid uint32 = 2000000, 150000

	// A tiny 10x10 map so a target at (99,99) is OOB.
	mp := newTestMapWithPathfinder(10, 10)
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": mp}}
	registry := domain.NewPlayerRegistry()
	conn := &captureConn{role: gwdomain.RoleMap}
	seedPlayer(t, registry, mp, conn, aid, cid, 5, 5, "Tester")

	svc := app.NewMoveService(registry, maps, fixedClock{0}, 8)
	svc.ResolveForTest(context.Background(), aid, packet.CZRequestMoveRequest{DestX: 99, DestY: 99})

	// No frame written — the move was silently dropped.
	assert.Empty(t, conn.buf.Bytes(), "no frame on an OOB target")

	// Position unchanged.
	stayed, ok := registry.ByAccount(aid)
	require.True(t, ok, "player still registered after dropped move")
	posX, posY, _ := stayed.Position()
	assert.Equal(t, int16(5), posX)
	assert.Equal(t, int16(5), posY)
}

// TestMoveService_RequestMove_NotRegistered asserts a move from an account not
// in the registry is a no-op: the worker finds no player and returns without
// writing anything.
func TestMoveService_RequestMove_NotRegistered(t *testing.T) {
	t.Parallel()
	mp := newTestMapWithPathfinder(200, 200)
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": mp}}
	registry := domain.NewPlayerRegistry()
	conn := &captureConn{role: gwdomain.RoleMap}

	svc := app.NewMoveService(registry, maps, fixedClock{0}, 8)
	svc.ResolveForTest(context.Background(), 999, packet.CZRequestMoveRequest{DestX: 60, DestY: 115})

	assert.Empty(t, conn.buf.Bytes(), "no frame for an unregistered account")
}

// TestMoveHandler_ParseError asserts a malformed CZ_REQUEST_MOVE frame returns
// a parse error (so ProcessBytes logs it) rather than panicking or enqueuing.
func TestMoveHandler_ParseError(t *testing.T) {
	t.Parallel()
	svc := app.NewMoveService(domain.NewPlayerRegistry(), &memMapStore{}, fixedClock{0}, 8)
	h := app.NewMoveHandler(svc)

	// A frame shorter than 5 bytes.
	err := h.Handle(context.Background(), &captureConn{role: gwdomain.RoleMap}, gwdomain.Frame{
		Cmd: packet.HeaderCZREQUESTMOVE,
		Raw: []byte{0x85, 0x00}, // only 2 bytes
	})
	assert.Error(t, err, "parse error for a short frame")
	assert.Contains(t, err.Error(), "CZ_REQUEST_MOVE")
}

// TestMoveHandler_NoAuth asserts a CZ_REQUEST_MOVE on a connection with no
// cached auth (CZ_ENTER not completed) returns an error rather than enqueuing
// a move for account 0.
func TestMoveHandler_NoAuth(t *testing.T) {
	t.Parallel()
	svc := app.NewMoveService(domain.NewPlayerRegistry(), &memMapStore{}, fixedClock{0}, 8)
	h := app.NewMoveHandler(svc)

	var buf bytes.Buffer
	require.NoError(t, (packet.CZRequestMoveRequest{DestX: 60, DestY: 115}).Encode(&buf))

	err := h.Handle(context.Background(), &captureConn{role: gwdomain.RoleMap}, gwdomain.Frame{
		Cmd: packet.HeaderCZREQUESTMOVE,
		Raw: buf.Bytes(),
	})
	assert.Error(t, err, "error for a move without cached auth")
	assert.Contains(t, err.Error(), "no verified account")
}
