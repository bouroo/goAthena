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
)

// encodeRestartReq builds the on-wire CZ_RESTART frame for a given type byte.
func encodeRestartReq(t *testing.T, restartType uint8) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (packet.CZRestartRequest{Type: restartType}).Encode(&buf))
	return buf.Bytes()
}

// TestRestartService_CharSelect_AcksUnregistersAndRemovesAOI is the happy path: a
// registered player char-select-returns; the service writes a byte-exact
// ZC_RESTART_ACK(type=1), unregisters the player, and removes its AOI entity.
func TestRestartService_CharSelect_AcksUnregistersAndRemovesAOI(t *testing.T) {
	t.Parallel()
	const aid uint32 = 2000040
	mp := newTestMap(200, 200)
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": mp}}
	registry := domain.NewPlayerRegistry()
	svc := app.NewRestartService(registry, maps)

	conn := &captureConn{role: gwdomain.RoleMap}
	seedPlayer(t, registry, mp, conn, aid, aid, 80, 80, "Leaver")

	err := svc.ReturnToCharSelect(context.Background(), conn, aid)
	require.NoError(t, err)

	got := conn.buf.Bytes()
	require.Len(t, got, 3, "ZC_RESTART_ACK is 3 bytes")
	assert.Equal(t, []byte{0xb3, 0x00, 0x01}, got, "ZC_RESTART_ACK type=1 (char-select allowed)")

	_, ok := registry.ByAccount(aid)
	assert.False(t, ok, "player unregistered on char-select return")
	assert.ErrorIs(t, mp.AOI.RemoveEntity(aoi.EntityID(aid)), aoi.ErrEntityMissing,
		"AOI entity removed on char-select return")
}

// TestRestartService_CharSelect_LateRestartAcksWithoutTeardown covers a duplicate
// or late CZ_RESTART (player already torn down): the ack is still sent so the
// client leaves, but there is nothing to tear down and no error.
func TestRestartService_CharSelect_LateRestartAcksWithoutTeardown(t *testing.T) {
	t.Parallel()
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": newTestMap(200, 200)}}
	svc := app.NewRestartService(domain.NewPlayerRegistry(), maps)

	conn := &captureConn{role: gwdomain.RoleMap}
	err := svc.ReturnToCharSelect(context.Background(), conn, 2000041)
	require.NoError(t, err)

	assert.Equal(t, []byte{0xb3, 0x00, 0x01}, conn.buf.Bytes(),
		"ack sent even for an already-torn-down session")
}

// TestRestartService_CharSelect_AllowsReRegister proves the correctness property
// the teardown exists for: after a char-select return, a fresh CZ_ENTER for the
// same account re-registers cleanly instead of hitting ErrPlayerAlreadyRegistered.
func TestRestartService_CharSelect_AllowsReRegister(t *testing.T) {
	t.Parallel()
	const aid uint32 = 2000042
	mp := newTestMap(200, 200)
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": mp}}
	registry := domain.NewPlayerRegistry()
	svc := app.NewRestartService(registry, maps)

	conn := &captureConn{role: gwdomain.RoleMap}
	seedPlayer(t, registry, mp, conn, aid, aid, 80, 80, "First")
	require.NoError(t, svc.ReturnToCharSelect(context.Background(), conn, aid))

	// Re-enter: a brand-new player for the same account registers without error.
	require.NoError(t, registry.Register(&domain.Player{
		AccountID: aid, EntityID: aoi.EntityID(aid), MapName: "prontera", Conn: conn,
	}), "re-register after char-select return must not hit ErrPlayerAlreadyRegistered")
}

// TestRestartService_CharSelect_MapLoadFailurePropagates asserts a map-store
// failure is surfaced (the gateway logs it) BEFORE any ack or teardown, so the
// client stays put and the player stays registered — no partial teardown.
func TestRestartService_CharSelect_MapLoadFailurePropagates(t *testing.T) {
	t.Parallel()
	const aid uint32 = 2000043
	// memMapStore with no "prontera" entry → Load errors; the player is seeded on
	// a separate grid so ByAccount/RemoveEntity are unaffected by the store gap.
	maps := &memMapStore{maps: map[string]*domain.Map{}}
	registry := domain.NewPlayerRegistry()
	mp := newTestMap(200, 200)
	conn := &captureConn{role: gwdomain.RoleMap}
	seedPlayer(t, registry, mp, conn, aid, aid, 80, 80, "Stuck")

	svc := app.NewRestartService(registry, maps)
	err := svc.ReturnToCharSelect(context.Background(), conn, aid)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "load map")

	_, ok := registry.ByAccount(aid)
	assert.True(t, ok, "player stays registered when the map load fails")
	assert.Empty(t, conn.buf.Bytes(), "no ack sent on map-load failure")
}

// --- handler-level tests (Frame parsing + auth guard + branch) ---

// TestRestartHandler_CharSelectAcksAndCloses wires the full handler: a verified
// connection sends CZ_RESTART type 1; the handler runs the use case (ack +
// teardown) and closes the map connection so the client reconnects to the char
// listener.
func TestRestartHandler_CharSelectAcksAndCloses(t *testing.T) {
	t.Parallel()
	const aid uint32 = 2000044
	mp := newTestMap(200, 200)
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": mp}}
	registry := domain.NewPlayerRegistry()
	h := app.NewRestartHandler(app.NewRestartService(registry, maps))

	// The handler's conn is the player's own socket, so it carries the verified
	// auth cache the impersonation guard reads.
	conn := &captureConn{role: gwdomain.RoleMap, auth: gwdomain.ConnAuth{AccountID: aid}}
	seedPlayer(t, registry, mp, conn, aid, aid, 80, 80, "Leaver")

	err := h.Handle(context.Background(), conn, gwdomain.Frame{
		Cmd: packet.HeaderCZRESTART,
		Raw: encodeRestartReq(t, 1),
	})
	require.NoError(t, err)
	assert.Equal(t, []byte{0xb3, 0x00, 0x01}, conn.buf.Bytes(), "ack written")
	_, ok := registry.ByAccount(aid)
	assert.False(t, ok, "player torn down via the handler")
}

// TestRestartHandler_NoAuth asserts the impersonation guard: a CZ_RESTART with no
// cached AccountID is refused with an error, nothing written.
func TestRestartHandler_NoAuth(t *testing.T) {
	t.Parallel()
	h := app.NewRestartHandler(app.NewRestartService(domain.NewPlayerRegistry(), &memMapStore{}))
	err := h.Handle(context.Background(), &captureConn{role: gwdomain.RoleMap}, gwdomain.Frame{
		Cmd: packet.HeaderCZRESTART,
		Raw: encodeRestartReq(t, 1),
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no verified account")
}

// TestRestartHandler_RespawnIsNoop asserts type 0 (respawn) is a documented no-op
// pending the death/savepoint system: nil error, nothing written, player stays.
func TestRestartHandler_RespawnIsNoop(t *testing.T) {
	t.Parallel()
	const aid uint32 = 2000045
	mp := newTestMap(200, 200)
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": mp}}
	registry := domain.NewPlayerRegistry()
	h := app.NewRestartHandler(app.NewRestartService(registry, maps))
	conn := &captureConn{role: gwdomain.RoleMap, auth: gwdomain.ConnAuth{AccountID: aid}}
	seedPlayer(t, registry, mp, conn, aid, aid, 80, 80, "Alive")

	err := h.Handle(context.Background(), conn, gwdomain.Frame{
		Cmd: packet.HeaderCZRESTART,
		Raw: encodeRestartReq(t, 0),
	})
	require.NoError(t, err)
	assert.Empty(t, conn.buf.Bytes(), "respawn is a no-op until the death system lands")
	_, ok := registry.ByAccount(aid)
	assert.True(t, ok, "player unaffected by a respawn no-op")
}

// TestRestartHandler_ParseError asserts a truncated frame yields a parse error.
func TestRestartHandler_ParseError(t *testing.T) {
	t.Parallel()
	h := app.NewRestartHandler(app.NewRestartService(domain.NewPlayerRegistry(), &memMapStore{}))
	err := h.Handle(context.Background(),
		&captureConn{role: gwdomain.RoleMap, auth: gwdomain.ConnAuth{AccountID: 1}}, gwdomain.Frame{
			Cmd: packet.HeaderCZRESTART,
			Raw: []byte{0xb2, 0x00}, // short — no type byte
		})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "CZ_RESTART")
}
