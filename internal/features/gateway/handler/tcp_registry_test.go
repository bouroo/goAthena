//go:build unit

package handler

import (
	"testing"
	"time"

	"github.com/panjf2000/gnet/v2"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/features/gateway/domain"
	"github.com/bouroo/goAthena/internal/features/gateway/service"
	netcodec "github.com/bouroo/goAthena/internal/infrastructure/net"
	"github.com/bouroo/goAthena/pkg/ro/packet"
	"github.com/bouroo/goAthena/pkg/ro/textenc"
)

// fakeGnetConn is a minimal gnet.Conn stub. It only implements the
// methods that TCPHandler.OnClose actually calls (Context). Every
// other method is delegated to a nil interface, so the test will
// panic if the production path tries to read or write — that
// regression mode is the whole point of the stub.
//
// The reason we do not use the full gnet test scaffolding: gnet
// requires a real Engine and listener, which forces the test into
// the integration tier (network ports, OnTraffic threading). The
// OnClose cleanup is a single, branchy contract — it deserves a
// hermetic unit test, not an integration smoke test that the CI
// already exercises.
type fakeGnetConn struct {
	gnet.Conn
	ctx any
}

func (f *fakeGnetConn) Context() any { return f.ctx }

// TestTCPHandler_OnClose_UnregistersSession covers the Phase 1 Step 2c
// contract: a TCP connection that has a cached AccountID (post
// successful CZ_ENTER) must be removed from the session registry
// when gnet fires OnClose, so a future fan-out cannot broadcast to
// a dead connection.
func TestTCPHandler_OnClose_UnregistersSession(t *testing.T) {
	t.Parallel()

	db := packet.NewLoginServerDB()
	registry := service.NewSessionRegistry()
	registry.Register(4242, domain.Session{
		CharID:  9001,
		MapName: "prt_fild08",
	})
	require.Equal(t, 1, registry.Len(), "preconditions: one session installed")

	h := NewTCPHandler(db, &recordingHandler{}, registry, silencedTestLogger(t), textenc.UTF8)

	conn := &fakeGnetConn{
		ctx: &connState{
			info: domain.ConnectionInfo{
				ID:        7,
				AccountID: 4242,
				CharID:    9001,
				RemoteIP:  "127.0.0.1:55555",
				OpenedAt:  time.Now().UnixNano(),
			},
			decoder: netcodec.NewLoginDecoder(db),
		},
	}
	action := h.OnClose(conn, nil)
	assert.Equal(t, gnet.None, action, "OnClose must return gnet.None; gnet owns the conn lifecycle")
	assert.Equal(t, 0, registry.Len(), "registry must be empty after OnClose for a registered account")
	_, ok := registry.Get(4242)
	assert.False(t, ok, "registry.Get(4242) must return ok=false after OnClose")
}

// TestTCPHandler_OnClose_NoAccountID_NoOp asserts the zero-AID path
// never trips: a connection that never reached CZ_ENTER (AccountID
// stays 0) must not call Unregister on the registry. The registry
// is pre-populated with a session for a different account to prove
// the cleanup is keyed correctly (no cross-account wipe).
func TestTCPHandler_OnClose_NoAccountID_NoOp(t *testing.T) {
	t.Parallel()

	db := packet.NewLoginServerDB()
	registry := service.NewSessionRegistry()
	registry.Register(9999, domain.Session{CharID: 1, MapName: "x"})

	h := NewTCPHandler(db, &recordingHandler{}, registry, silencedTestLogger(t), textenc.UTF8)

	conn := &fakeGnetConn{
		ctx: &connState{
			info: domain.ConnectionInfo{
				ID:       7,
				RemoteIP: "127.0.0.1:55556",
				OpenedAt: time.Now().UnixNano(),
				// AccountID deliberately 0
			},
			decoder: netcodec.NewLoginDecoder(db),
		},
	}
	action := h.OnClose(conn, nil)
	assert.Equal(t, gnet.None, action)
	assert.Equal(t, 1, registry.Len(), "registry.Len must be unchanged when AccountID=0")
	_, ok := registry.Get(9999)
	assert.True(t, ok, "an unrelated session must not be removed")
}

// TestTCPHandler_OnClose_NilContext_NoOp asserts the defensive
// guard: a gnet.Conn that was never context-set (state == nil) must
// not panic and must not touch the registry.
func TestTCPHandler_OnClose_NilContext_NoOp(t *testing.T) {
	t.Parallel()

	db := packet.NewLoginServerDB()
	registry := service.NewSessionRegistry()
	registry.Register(4242, domain.Session{CharID: 9001})

	h := NewTCPHandler(db, &recordingHandler{}, registry, silencedTestLogger(t), textenc.UTF8)

	conn := &fakeGnetConn{ctx: nil} // no state at all
	action := h.OnClose(conn, nil)
	assert.Equal(t, gnet.None, action)
	assert.Equal(t, 1, registry.Len(), "registry.Len must be unchanged when conn.Context() is nil")
}

// silencedTestLogger returns a zerolog logger that drops every event.
// The OnClose / OnOpen logs are debug-level; silencing keeps the
// test output clean.
func silencedTestLogger(t *testing.T) zerolog.Logger {
	t.Helper()
	return zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.Disabled)
}
