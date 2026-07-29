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
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// failingConn is defined in social_test.go (shared across social/chat/move tests).

// expectNotifyChat encodes the ZC_NOTIFY_CHAT frame the chat handler should emit
// for a speaker with the given GID and message. Built independently from the SUT
// so a drift in the kernel encoder surfaces as a byte mismatch.
func expectNotifyChat(t *testing.T, gid uint32, msg string) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (packet.NotifyChatResponse{GID: gid, Message: msg}).Encode(&buf))
	return buf.Bytes()
}

// encodeGlobalMessageReq builds the CZ_GLOBAL_MESSAGE frame the handler parses.
func encodeGlobalMessageReq(t *testing.T, msg string) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, (packet.CZGlobalMessageRequest{Message: msg}).Encode(&buf))
	return buf.Bytes()
}

// TestChatHandler_BroadcastSelfAndNeighbor is the CZ_GLOBAL_MESSAGE happy path:
// a registered player and one AOI neighbor both receive a byte-exact ZC_NOTIFY_CHAT
// carrying the player's account_id as GID (never a packet field — CZ_GLOBAL_MESSAGE
// carries none), and the message text matches verbatim.
func TestChatHandler_BroadcastSelfAndNeighbor(t *testing.T) {
	t.Parallel()
	const aid, neighborAID uint32 = 2000010, 2000011
	const chatMsg = "hello world"

	mp := newTestMap(200, 200)
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": mp}}
	registry := domain.NewPlayerRegistry()
	h := app.NewChatHandler(registry, maps)

	selfConn := &captureConn{role: gwdomain.RoleMap}
	neighborConn := &captureConn{role: gwdomain.RoleMap}
	seedPlayer(t, registry, mp, selfConn, aid, aid, 53, 111, "Speaker")
	seedPlayer(t, registry, mp, neighborConn, neighborAID, neighborAID, 54, 111, "Neighbor")

	conn := &captureConn{
		role: gwdomain.RoleMap,
		auth: gwdomain.ConnAuth{AccountID: aid}, // verified by CZ_ENTER upstream
	}
	err := h.Handle(context.Background(), conn, gwdomain.Frame{
		Cmd: packet.HeaderCZGLOBALMESSAGE,
		Raw: encodeGlobalMessageReq(t, chatMsg),
	})
	require.NoError(t, err)

	want := expectNotifyChat(t, aid, chatMsg)
	assert.Equal(t, want, selfConn.buf.Bytes(), "originator sees its own message, GID=account_id")
	assert.Equal(t, want, neighborConn.buf.Bytes(), "neighbor sees the chat message too")
}

// TestChatHandler_NoAuth asserts the impersonation guard: a CZ_GLOBAL_MESSAGE on
// a connection whose CZ_ENTER did not complete (no cached AccountID) is dropped
// silently — nil error, nothing written — matching the spec.
func TestChatHandler_NoAuth(t *testing.T) {
	t.Parallel()
	mp := newTestMap(200, 200)
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": mp}}
	h := app.NewChatHandler(domain.NewPlayerRegistry(), maps)

	err := h.Handle(context.Background(), &captureConn{role: gwdomain.RoleMap}, gwdomain.Frame{
		Cmd: packet.HeaderCZGLOBALMESSAGE,
		Raw: encodeGlobalMessageReq(t, "unauthenticated"),
	})
	// The spec says: if accountID == 0, return nil (drop silently).
	assert.NoError(t, err, "no auth → silent drop, nil error")
}

// TestChatHandler_NotRegistered asserts a late packet after disconnect (the account
// is not in the registry) is silently dropped — nil error, nothing written.
func TestChatHandler_NotRegistered(t *testing.T) {
	t.Parallel()
	mp := newTestMap(200, 200)
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": mp}}
	h := app.NewChatHandler(domain.NewPlayerRegistry(), maps)

	conn := &captureConn{role: gwdomain.RoleMap, auth: gwdomain.ConnAuth{AccountID: 999999}}
	err := h.Handle(context.Background(), conn, gwdomain.Frame{
		Cmd: packet.HeaderCZGLOBALMESSAGE,
		Raw: encodeGlobalMessageReq(t, "ghost message"),
	})
	assert.NoError(t, err, "late packet after disconnect is dropped silently")
	assert.Empty(t, conn.buf.Bytes(), "nothing written for an unknown account")
}

// TestChatHandler_ParseError asserts a truncated frame yields a parse error.
func TestChatHandler_ParseError(t *testing.T) {
	t.Parallel()
	h := app.NewChatHandler(domain.NewPlayerRegistry(), &memMapStore{})
	err := h.Handle(context.Background(), &captureConn{role: gwdomain.RoleMap, auth: gwdomain.ConnAuth{AccountID: 1}}, gwdomain.Frame{
		Cmd: packet.HeaderCZGLOBALMESSAGE,
		Raw: []byte{byte(packet.HeaderCZGLOBALMESSAGE), 0, 3, 0}, // short — no message body
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "CZ_GLOBAL_MESSAGE")
}

// TestChatService_Broadcast_DeadSelfSocketDropsPlayer asserts the teardown
// contract: when the originator's own socket write fails, broadcast drops the
// player (registry + AOI) and skips the neighbors, rather than looping dead
// writes. This mirrors SocialService.broadcast's dead-self-socket path.
func TestChatService_Broadcast_DeadSelfSocketDropsPlayer(t *testing.T) {
	t.Parallel()
	const aid, neighborAID uint32 = 2000030, 2000031
	mp := newTestMap(200, 200)
	maps := &memMapStore{maps: map[string]*domain.Map{"prontera": mp}}
	registry := domain.NewPlayerRegistry()
	svc := app.NewChatService(registry, maps)

	deadConn := &failingConn{captureConn: captureConn{role: gwdomain.RoleMap}}
	neighborConn := &captureConn{role: gwdomain.RoleMap}
	seedPlayer(t, registry, mp, deadConn, aid, aid, 80, 80, "DeadSelf")
	seedPlayer(t, registry, mp, neighborConn, neighborAID, neighborAID, 81, 80, "Neighbor")

	err := svc.HandleChat(context.Background(), aid, "hello")
	require.NoError(t, err, "a dead self socket is a teardown, not an error to the caller")

	_, ok := registry.ByAccount(aid)
	assert.False(t, ok, "originator dropped from the registry after a failed self-write")
	assert.Empty(t, neighborConn.buf.Bytes(), "neighbors are not written to when the self-write failed first")
}
