//go:build unit

package app_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gwdomain "github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/internal/modules/world/app"
	"github.com/bouroo/goAthena/internal/modules/world/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// errFailingConn is the sentinel failingConn.Write returns; the teardown test
// checks the observable effect (registry membership) rather than the error text.
var errFailingConn = errors.New("failingConn: write disabled")

// failingConn is a captureConn whose Write always errors, to exercise the
// dead-self-socket teardown: broadcast writes the originator first, and a
// failing write must drop the player (registry + AOI) and skip the neighbors.
type failingConn struct {
	captureConn
}

func (c *failingConn) Write(_ []byte) error { return errFailingConn }

// seedPlayer (defined in move_test.go) registers a player and places its AOI
// entity at a cell; the social tests reuse it for both the originator and the
// neighbor so the registry/grid state matches what a live world would hold.

// expectNotifyTime, expectChangeDir, expectEmotion encode the server→client
// response frames independently from the SUT so a drift in the encoder or in
// the handler's field sourcing surfaces as a byte mismatch (the same discipline
// expectSpawnUnit applies).
func expectNotifyTime(t *testing.T, time uint32) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (packet.NotifyTimeResponse{Time: time}).Encode(&buf))
	return buf.Bytes()
}

func expectChangeDir(t *testing.T, srcID uint32, headDir uint16, dir uint8) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (packet.ChangeDirResponse{SrcID: srcID, HeadDir: headDir, Dir: dir}).Encode(&buf))
	return buf.Bytes()
}

func expectEmotion(t *testing.T, gid uint32, typ uint8) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (packet.EmotionResponse{GID: gid, Type: typ}).Encode(&buf))
	return buf.Bytes()
}

// encodeChangeDirReq / encodeEmotionReq build the client→map request frames the
// handlers parse, using the request structs' own encoders (the wire layout is
// the parser's contract, so encoding with the same struct keeps the test honest
// about the cmd header + field offsets).
func encodeChangeDirReq(t *testing.T, headDir uint16, dir uint8) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (packet.CZChangeDirRequest{HeadDir: headDir, Dir: dir}).Encode(&buf))
	return buf.Bytes()
}

func encodeEmotionReq(t *testing.T, typ uint8) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (packet.CZReqEmotionRequest{EmotionType: typ}).Encode(&buf))
	return buf.Bytes()
}

func encodeTimeReq(t *testing.T, clientTick uint32) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (packet.CZRequestTimeRequest{ClientTick: clientTick}).Encode(&buf))
	return buf.Bytes()
}

// TestTimeHandler_Echo is the CZ_REQUEST_TIME happy path: the handler echoes the
// shared server tick as ZC_NOTIFY_TIME, byte-exact, and — unlike dir/emotion —
// needs no verified account (it is a stateless clock echo). The conn carries an
// empty auth cache to prove the stateless property.
func TestTimeHandler_Echo(t *testing.T) {
	t.Parallel()
	const tick uint32 = 0x12345678
	h := app.NewTimeHandler(fixedClock{tick})

	conn := &captureConn{role: gwdomain.RoleMap} // no auth cached on purpose
	err := h.Handle(context.Background(), conn, gwdomain.Frame{
		Cmd: packet.HeaderCZREQUESTTIME,
		Raw: encodeTimeReq(t, 0xdeadbeef),
	})
	require.NoError(t, err)
	assert.Equal(t, expectNotifyTime(t, tick), conn.buf.Bytes(), "ZC_NOTIFY_TIME echoes the server tick verbatim")
}

// TestTimeHandler_ParseError asserts a truncated CZ_REQUEST_TIME frame yields a
// parse error (so ProcessBytes logs it) rather than panicking or echoing.
func TestTimeHandler_ParseError(t *testing.T) {
	t.Parallel()
	h := app.NewTimeHandler(fixedClock{1})

	err := h.Handle(context.Background(), &captureConn{role: gwdomain.RoleMap}, gwdomain.Frame{
		Cmd: packet.HeaderCZREQUESTTIME,
		Raw: []byte{byte(packet.HeaderCZREQUESTTIME), 0}, // short — no uint32 tick
	})
	assert.Error(t, err, "parse error for a short frame")
	assert.Contains(t, err.Error(), "CZ_REQUEST_TIME")
}

// TestChangeDirHandler_BroadcastSelfAndNeighbor is the CZ_CHANGE_DIR happy path
// and impersonation proof: a registered player and one AOI neighbor both receive
// a byte-exact ZC_CHANGE_DIR carrying the player's account_id as srcId (never a
// packet field — CZ_CHANGE_DIR carries none), and the player's cached facing is
// updated so a later spawn/walk reads the new dir.
func TestChangeDirHandler_BroadcastSelfAndNeighbor(t *testing.T) {
	t.Parallel()
	const aid, neighborAID uint32 = 2000010, 2000011
	const headDir uint16 = 1
	const dir uint8 = 3

	mp := newTestMap(200, 200)
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": mp}}
	registry := domain.NewPlayerRegistry()
	svc := app.NewSocialService(registry, maps)
	h := app.NewChangeDirHandler(svc)

	selfConn := &captureConn{role: gwdomain.RoleMap}
	neighborConn := &captureConn{role: gwdomain.RoleMap}
	seedPlayer(t, registry, mp, selfConn, aid, aid, 53, 111, "Self")
	seedPlayer(t, registry, mp, neighborConn, neighborAID, neighborAID, 54, 111, "Neighbor") // adjacent cell, within AOI

	conn := &captureConn{
		role: gwdomain.RoleMap,
		auth: gwdomain.ConnAuth{AccountID: aid}, // verified by CZ_ENTER upstream
	}
	err := h.Handle(context.Background(), conn, gwdomain.Frame{
		Cmd: packet.HeaderCZCHANGEDIR,
		Raw: encodeChangeDirReq(t, headDir, dir),
	})
	require.NoError(t, err)

	want := expectChangeDir(t, aid, headDir, dir)
	assert.Equal(t, want, selfConn.buf.Bytes(), "originator sees its own dir change, srcId=account_id")
	assert.Equal(t, want, neighborConn.buf.Bytes(), "neighbor sees the dir change too")

	// The body dir is committed to the cached position; head dir is not stored
	// (rAthena does not persist headdir). A concurrent read must see the new dir.
	p, ok := registry.ByAccount(aid)
	require.True(t, ok)
	_, _, gotDir := p.Position()
	assert.Equal(t, dir, gotDir, "body dir persisted on the player")
}

// TestChangeDirHandler_NoAuth asserts the impersonation guard: a CZ_CHANGE_DIR
// on a connection whose CZ_ENTER did not complete (no cached AccountID) is
// refused with an error rather than broadcast.
func TestChangeDirHandler_NoAuth(t *testing.T) {
	t.Parallel()
	mp := newTestMap(200, 200)
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": mp}}
	h := app.NewChangeDirHandler(app.NewSocialService(domain.NewPlayerRegistry(), maps))

	err := h.Handle(context.Background(), &captureConn{role: gwdomain.RoleMap}, gwdomain.Frame{
		Cmd: packet.HeaderCZCHANGEDIR,
		Raw: encodeChangeDirReq(t, 0, 0),
	})
	assert.Error(t, err, "no verified account")
	assert.Contains(t, err.Error(), "no verified account")
}

// TestChangeDirHandler_NotRegistered asserts a late packet after disconnect (the
// account is not in the registry) is silently dropped — nil error, nothing
// written — matching MoveService.resolve's late-packet contract.
func TestChangeDirHandler_NotRegistered(t *testing.T) {
	t.Parallel()
	mp := newTestMap(200, 200)
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": mp}}
	h := app.NewChangeDirHandler(app.NewSocialService(domain.NewPlayerRegistry(), maps))

	conn := &captureConn{role: gwdomain.RoleMap, auth: gwdomain.ConnAuth{AccountID: 999999}}
	err := h.Handle(context.Background(), conn, gwdomain.Frame{
		Cmd: packet.HeaderCZCHANGEDIR,
		Raw: encodeChangeDirReq(t, 0, 1),
	})
	assert.NoError(t, err, "late packet after disconnect is dropped silently")
	assert.Empty(t, conn.buf.Bytes(), "nothing written for an unknown account")
}

// TestChangeDirHandler_ParseError asserts a truncated frame yields a parse error.
func TestChangeDirHandler_ParseError(t *testing.T) {
	t.Parallel()
	h := app.NewChangeDirHandler(app.NewSocialService(domain.NewPlayerRegistry(), &memMapStore{}))
	err := h.Handle(context.Background(), &captureConn{role: gwdomain.RoleMap, auth: gwdomain.ConnAuth{AccountID: 1}}, gwdomain.Frame{
		Cmd: packet.HeaderCZCHANGEDIR,
		Raw: []byte{byte(packet.HeaderCZCHANGEDIR), 0}, // short — no headDir/dir
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "CZ_CHANGE_DIR")
}

// TestEmotionHandler_BroadcastSelfAndNeighbor is the CZ_REQ_EMOTION happy path:
// the emotion byte is forwarded verbatim, the GID is the player's account_id,
// and both the originator and an AOI neighbor receive a byte-exact ZC_EMOTION.
func TestEmotionHandler_BroadcastSelfAndNeighbor(t *testing.T) {
	t.Parallel()
	const aid, neighborAID uint32 = 2000020, 2000021
	const emotionType uint8 = 1 // ET_SMILE

	mp := newTestMap(200, 200)
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": mp}}
	registry := domain.NewPlayerRegistry()
	h := app.NewEmotionHandler(app.NewSocialService(registry, maps))

	selfConn := &captureConn{role: gwdomain.RoleMap}
	neighborConn := &captureConn{role: gwdomain.RoleMap}
	seedPlayer(t, registry, mp, selfConn, aid, aid, 70, 70, "Self")
	seedPlayer(t, registry, mp, neighborConn, neighborAID, neighborAID, 71, 70, "Neighbor")

	conn := &captureConn{role: gwdomain.RoleMap, auth: gwdomain.ConnAuth{AccountID: aid}}
	err := h.Handle(context.Background(), conn, gwdomain.Frame{
		Cmd: packet.HeaderCZREQEMOTION,
		Raw: encodeEmotionReq(t, emotionType),
	})
	require.NoError(t, err)

	want := expectEmotion(t, aid, emotionType)
	assert.Equal(t, want, selfConn.buf.Bytes(), "originator sees its own emotion, GID=account_id")
	assert.Equal(t, want, neighborConn.buf.Bytes(), "neighbor sees the emotion")
}

// TestEmotionHandler_NoAuth / ParseError / NotRegistered mirror the changedir
// guard surface for the emotion handler.
func TestEmotionHandler_NoAuth(t *testing.T) {
	t.Parallel()
	h := app.NewEmotionHandler(app.NewSocialService(domain.NewPlayerRegistry(), &memMapStore{}))
	err := h.Handle(context.Background(), &captureConn{role: gwdomain.RoleMap}, gwdomain.Frame{
		Cmd: packet.HeaderCZREQEMOTION,
		Raw: encodeEmotionReq(t, 1),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no verified account")
}

func TestEmotionHandler_NotRegistered(t *testing.T) {
	t.Parallel()
	h := app.NewEmotionHandler(app.NewSocialService(domain.NewPlayerRegistry(), &memMapStore{}))
	conn := &captureConn{role: gwdomain.RoleMap, auth: gwdomain.ConnAuth{AccountID: 999999}}
	err := h.Handle(context.Background(), conn, gwdomain.Frame{
		Cmd: packet.HeaderCZREQEMOTION,
		Raw: encodeEmotionReq(t, 1),
	})
	assert.NoError(t, err)
	assert.Empty(t, conn.buf.Bytes())
}

func TestEmotionHandler_ParseError(t *testing.T) {
	t.Parallel()
	h := app.NewEmotionHandler(app.NewSocialService(domain.NewPlayerRegistry(), &memMapStore{}))
	err := h.Handle(context.Background(), &captureConn{role: gwdomain.RoleMap, auth: gwdomain.ConnAuth{AccountID: 1}}, gwdomain.Frame{
		Cmd: packet.HeaderCZREQEMOTION,
		Raw: []byte{byte(packet.HeaderCZREQEMOTION), 0}, // short — no emotion byte
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "CZ_REQ_EMOTION")
}

// TestSocial_Broadcast_DeadSelfSocketDropsPlayer asserts the teardown contract:
// when the originator's own socket write fails, broadcast drops the player
// (registry + AOI) and skips the neighbors, rather than looping dead writes.
// This mirrors MoveService's dropPlayer-on-dead-self path.
func TestSocial_Broadcast_DeadSelfSocketDropsPlayer(t *testing.T) {
	t.Parallel()
	const aid, neighborAID uint32 = 2000030, 2000031
	mp := newTestMap(200, 200)
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": mp}}
	registry := domain.NewPlayerRegistry()
	svc := app.NewSocialService(registry, maps)

	deadConn := &failingConn{captureConn: captureConn{role: gwdomain.RoleMap}}
	neighborConn := &captureConn{role: gwdomain.RoleMap}
	seedPlayer(t, registry, mp, deadConn, aid, aid, 80, 80, "Self")
	seedPlayer(t, registry, mp, neighborConn, neighborAID, neighborAID, 81, 80, "Neighbor")

	err := svc.ChangeDir(context.Background(), aid, 0, 2)
	require.NoError(t, err, "a dead self socket is a teardown, not an error to the caller")

	_, ok := registry.ByAccount(aid)
	assert.False(t, ok, "originator dropped from the registry after a failed self-write")
	assert.Empty(t, neighborConn.buf.Bytes(), "neighbors are not written to when the self-write failed first")
}
