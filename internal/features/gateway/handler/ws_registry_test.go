//go:build unit

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/features/gateway/domain"
	"github.com/bouroo/goAthena/internal/features/gateway/service"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// registryProbeHandler is a PacketHandler that simulates the
// dispatch handler's CZ_ENTER effect by mutating conn.AccountID on
// the first packet. It records every packet it sees on ch so the
// test can wait for the dispatch to land before closing the
// connection (which triggers the WS serve loop's defer, which
// performs the registry Unregister).
type registryProbeHandler struct {
	ch   chan struct{}
	once bool
}

func (h *registryProbeHandler) HandlePacket(_ context.Context, conn *domain.ConnectionInfo, _ domain.Responder, cmd uint16, frame []byte) error {
	if !h.once {
		h.once = true
		// Pretend a CZ_ENTER happened. The exact value of MapName
		// and View are not under test here — only the existence
		// of an installed session and its removal on disconnect.
		conn.AccountID = 4242
		conn.CharID = 9001
		conn.MapName = "prt_fild08"
	}
	select {
	case h.ch <- struct{}{}:
	default:
	}
	return nil
}

// TestWSHandler_DisconnectUnregistersSession asserts the Phase 1
// Step 2c contract: a WebSocket connection that has had its
// AccountID set (simulated by the probe handler) is removed from
// the session registry when the client disconnects, so a future
// fan-out cannot broadcast to a dead connection.
func TestWSHandler_DisconnectUnregistersSession(t *testing.T) {
	t.Parallel()

	db := packet.NewLoginServerDB()
	registry := service.NewSessionRegistry()
	probe := &registryProbeHandler{ch: make(chan struct{}, 1)}
	logger := zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.Disabled)

	mux := http.NewServeMux()
	h := NewWSHandler(db, probe, registry, "unused", "/ws/", logger, nil)
	mux.HandleFunc(h.path, h.ServeHTTP)
	mux.HandleFunc("/", h.rejectNonUpgrade)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := wsTestURL(t, srv.URL) + h.path
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	client, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPClient: srv.Client(),
	})
	require.NoError(t, err, "ws dial")

	// Send a packet so the probe handler fires and sets AccountID.
	frame := buildCALogin(t, "roBrowser", "secret")
	require.NoError(t, client.Write(ctx, websocket.MessageBinary, frame))

	// Wait for the server-side dispatch.
	select {
	case <-probe.ch:
	case <-time.After(2 * time.Second):
		t.Fatal("server did not dispatch the packet within 2s")
	}

	// We can't observe the registry from inside ServeHTTP, but we
	// can: install the same account before the disconnect fires
	// (the probe handler did not register — it only mutated
	// conn). Pre-install here so the test is independent of any
	// future wire-up to the dispatch handler. This isolates the
	// Unregister-on-disconnect contract.
	registry.Register(4242, domain.Session{
		CharID:  9001,
		MapName: "prt_fild08",
	})
	require.Equal(t, 1, registry.Len(), "preconditions: one session installed")

	// Close the client. The server's read loop returns with an
	// error, the defer runs, and the registry must be cleaned.
	require.NoError(t, client.Close(websocket.StatusNormalClosure, ""))

	// Poll briefly for the cleanup; the WS serve loop runs in a
	// goroutine.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if registry.Len() == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.Equal(t, 0, registry.Len(), "registry must be empty after WS disconnect")
	_, ok := registry.Get(4242)
	assert.False(t, ok, "registry.Get(4242) must return ok=false after WS disconnect")
}
