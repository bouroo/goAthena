//go:build unit

package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/rs/zerolog"

	"github.com/bouroo/goAthena/internal/features/gateway/domain"
	"github.com/bouroo/goAthena/internal/features/gateway/service"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// wsRecordingHandler is a channel-based PacketHandler sink for the WS unit
// tests. Each HandlePacket call pushes a copy of the frame onto a buffered
// channel so the test can wait for an exact number of dispatches. This
// differs from the mutex-based recordingHandler in tcp_test.go (which is
// better for stateless assertions); the channel variant composes naturally
// with sequential expectations and timeouts.
type wsRecordingHandler struct {
	ch chan recordedPacket
}

func newWSRecordingHandler() *wsRecordingHandler {
	return &wsRecordingHandler{ch: make(chan recordedPacket, 16)}
}

func (h *wsRecordingHandler) HandlePacket(_ context.Context, _ *domain.ConnectionInfo, _ domain.Responder, cmd uint16, frame []byte) error {
	cp := make([]byte, len(frame))
	copy(cp, frame)
	select {
	case h.ch <- recordedPacket{cmd: cmd, frame: cp}:
	default:
	}
	return nil
}

func (h *wsRecordingHandler) wait(t *testing.T, timeout time.Duration) recordedPacket {
	t.Helper()
	select {
	case got := <-h.ch:
		return got
	case <-time.After(timeout):
		t.Fatalf("timed out after %s waiting for HandlePacket", timeout)
		return recordedPacket{}
	}
}

func TestNewWSHandler_StoresConfig(t *testing.T) {
	db := packet.NewLoginServerDB()
	rec := newWSRecordingHandler()
	logger := newTestLogger(t)

	h := NewWSHandler(db, rec, service.NewSessionRegistry(), ":0", "/ws/", logger, nil)

	if h.db != db {
		t.Fatalf("db pointer not stored")
	}
	if h.addr != ":0" {
		t.Fatalf("addr = %q, want :0", h.addr)
	}
	if h.path != "/ws/" {
		t.Fatalf("path = %q, want /ws/", h.path)
	}
}

func TestWSHandler_RejectNonUpgrade_Returns404(t *testing.T) {
	db := packet.NewLoginServerDB()
	rec := newWSRecordingHandler()
	logger := newTestLogger(t)

	mux := http.NewServeMux()
	h := NewWSHandler(db, rec, service.NewSessionRegistry(), "unused", "/ws/", logger, nil)
	mux.HandleFunc(h.path, h.ServeHTTP)
	mux.HandleFunc("/", h.rejectNonUpgrade)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestWSHandler_RejectsPlainUpgradeRequest(t *testing.T) {
	db := packet.NewLoginServerDB()
	rec := newWSRecordingHandler()
	logger := newTestLogger(t)

	mux := http.NewServeMux()
	h := NewWSHandler(db, rec, service.NewSessionRegistry(), "unused", "/ws/", logger, nil)
	mux.HandleFunc(h.path, h.ServeHTTP)
	mux.HandleFunc("/", h.rejectNonUpgrade)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// A plain HTTP POST without WS headers must not be upgraded.
	resp, err := http.Post(srv.URL+h.path, "application/octet-stream", strings.NewReader("nope"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusSwitchingProtocols {
		t.Fatalf("plain POST was upgraded to WS; expected rejection")
	}
}

func TestWSHandler_AcceptsBinaryCALoginAndDispatches(t *testing.T) {
	db := packet.NewLoginServerDB()
	rec := newWSRecordingHandler()
	logger := newTestLogger(t)

	mux := http.NewServeMux()
	h := NewWSHandler(db, rec, service.NewSessionRegistry(), "unused", "/ws/", logger, nil)
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
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer func() { _ = client.Close(websocket.StatusNormalClosure, "") }()

	frame := buildCALogin(t, "roBrowser", "secret")
	if err := client.Write(ctx, websocket.MessageBinary, frame); err != nil {
		t.Fatalf("ws write: %v", err)
	}

	got := rec.wait(t, 3*time.Second)
	if got.cmd != packet.HeaderCALOGIN {
		t.Fatalf("cmd = 0x%04x, want 0x%04x", got.cmd, packet.HeaderCALOGIN)
	}
	if len(got.frame) != len(frame) {
		t.Fatalf("frame len = %d, want %d", len(got.frame), len(frame))
	}
	if !bytesEqual(got.frame, frame) {
		t.Fatalf("frame bytes mismatch:\n got %x\nwant %x", got.frame, frame)
	}
}

func TestWSHandler_MultiplePacketsInOneBinaryMessage(t *testing.T) {
	db := packet.NewLoginServerDB()
	rec := newWSRecordingHandler()
	logger := newTestLogger(t)

	mux := http.NewServeMux()
	h := NewWSHandler(db, rec, service.NewSessionRegistry(), "unused", "/ws/", logger, nil)
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
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer func() { _ = client.Close(websocket.StatusNormalClosure, "") }()

	a := buildCALogin(t, "alice", "pw")
	b := buildCALogin(t, "bob", "pw")
	combined := append(append([]byte{}, a...), b...)

	if err := client.Write(ctx, websocket.MessageBinary, combined); err != nil {
		t.Fatalf("ws write combined: %v", err)
	}

	first := rec.wait(t, 3*time.Second)
	second := rec.wait(t, 3*time.Second)
	if first.cmd != packet.HeaderCALOGIN || second.cmd != packet.HeaderCALOGIN {
		t.Fatalf("commands = 0x%04x, 0x%04x; want 0x0064 twice", first.cmd, second.cmd)
	}
}

func TestWSHandler_PartialPacketAcrossMessages_BufferedUntilComplete(t *testing.T) {
	db := packet.NewLoginServerDB()
	rec := newWSRecordingHandler()
	logger := newTestLogger(t)

	mux := http.NewServeMux()
	h := NewWSHandler(db, rec, service.NewSessionRegistry(), "unused", "/ws/", logger, nil)
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
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer func() { _ = client.Close(websocket.StatusNormalClosure, "") }()

	full := buildCALogin(t, "x", "y")
	splitAt := 30
	if err := client.Write(ctx, websocket.MessageBinary, full[:splitAt]); err != nil {
		t.Fatalf("ws write first half: %v", err)
	}

	// No packet should dispatch yet — assert by ensuring we don't get a
	// frame within a short window. A 150 ms sleep is enough to surface a
	// premature dispatch; the second write will follow immediately.
	select {
	case got := <-rec.ch:
		t.Fatalf("dispatched on partial input: cmd=0x%04x len=%d", got.cmd, len(got.frame))
	case <-time.After(150 * time.Millisecond):
	}

	if err := client.Write(ctx, websocket.MessageBinary, full[splitAt:]); err != nil {
		t.Fatalf("ws write second half: %v", err)
	}

	got := rec.wait(t, 3*time.Second)
	if got.cmd != packet.HeaderCALOGIN {
		t.Fatalf("cmd = 0x%04x, want 0x%04x", got.cmd, packet.HeaderCALOGIN)
	}
	if len(got.frame) != len(full) {
		t.Fatalf("frame len = %d, want %d", len(got.frame), len(full))
	}
}

func TestWSHandler_NonBinaryMessage_ClosesConnection(t *testing.T) {
	db := packet.NewLoginServerDB()
	rec := newWSRecordingHandler()
	logger := newTestLogger(t)

	mux := http.NewServeMux()
	h := NewWSHandler(db, rec, service.NewSessionRegistry(), "unused", "/ws/", logger, nil)
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
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer func() { _ = client.CloseNow() }()

	if err := client.Write(ctx, websocket.MessageText, []byte("hello")); err != nil {
		t.Fatalf("ws write text: %v", err)
	}

	// The server should close the connection. We expect the next Read
	// (or the next dial attempt) to surface a non-nil error.
	_, _, readErr := client.Read(ctx)
	if readErr == nil {
		t.Fatal("expected read error after server closes connection on non-binary message")
	}
}

func TestWSHandler_StopWithoutStart_IsNoop(t *testing.T) {
	db := packet.NewLoginServerDB()
	rec := newWSRecordingHandler()
	logger := newTestLogger(t)
	h := NewWSHandler(db, rec, service.NewSessionRegistry(), "unused", "/ws/", logger, nil)

	if err := h.Stop(context.Background()); err != nil {
		t.Fatalf("Stop without Start err = %v, want nil", err)
	}
}

func TestWSHandler_StartStopRealPort(t *testing.T) {
	// Use a fixed high port; if it is already in use (CI, parallel test
	// runs), skip rather than flake. ":0" cannot be observed after
	// Serve(listener) without exposing the listener.
	const addr = "127.0.0.1:16901"
	db := packet.NewLoginServerDB()
	rec := newWSRecordingHandler()
	logger := zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.Disabled)
	h := NewWSHandler(db, rec, service.NewSessionRegistry(), addr, "/ws/", logger, nil)

	if err := h.Start(context.Background()); err != nil {
		t.Skipf("port %s not bindable in this environment: %v", addr, err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = h.Stop(stopCtx)
	}()

	if err := waitForWS404(addr, 2*time.Second); err != nil {
		t.Fatalf("ws server not responding: %v", err)
	}
}

// TestWSHandler_RejectsDisallowedOrigin exercises the CSWSH origin allowlist:
// a request with an Origin header that does not match any pattern must be
// rejected with HTTP 403.
func TestWSHandler_RejectsDisallowedOrigin(t *testing.T) {
	db := packet.NewLoginServerDB()
	rec := newWSRecordingHandler()
	logger := newTestLogger(t)

	mux := http.NewServeMux()
	h := NewWSHandler(db, rec, service.NewSessionRegistry(), "unused", "/ws/", logger, []string{"https://allowed.example.com"})
	mux.HandleFunc(h.path, h.ServeHTTP)
	mux.HandleFunc("/", h.rejectNonUpgrade)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := wsTestURL(t, srv.URL) + h.path
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPClient: srv.Client(),
		HTTPHeader: http.Header{"Origin": []string{"https://evil.example.com"}},
	})
	if err == nil {
		t.Fatalf("ws dial with disallowed origin succeeded; want 403")
	}
	if resp == nil {
		t.Fatalf("ws dial error = %v with nil response", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("ws dial status = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
}

// TestWSHandler_AcceptsAllowedOrigin verifies that a request with an Origin
// header matching an allowlist entry upgrades successfully and dispatches
// packets.
func TestWSHandler_AcceptsAllowedOrigin(t *testing.T) {
	db := packet.NewLoginServerDB()
	rec := newWSRecordingHandler()
	logger := newTestLogger(t)

	mux := http.NewServeMux()
	h := NewWSHandler(db, rec, service.NewSessionRegistry(), "unused", "/ws/", logger, []string{"https://allowed.example.com"})
	mux.HandleFunc(h.path, h.ServeHTTP)
	mux.HandleFunc("/", h.rejectNonUpgrade)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := wsTestURL(t, srv.URL) + h.path
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	client, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPClient: srv.Client(),
		HTTPHeader: http.Header{"Origin": []string{"https://allowed.example.com"}},
	})
	if err != nil {
		t.Fatalf("ws dial with allowed origin: %v", err)
	}
	defer func() { _ = client.Close(websocket.StatusNormalClosure, "") }()

	frame := buildCALogin(t, "roBrowser", "secret")
	if err := client.Write(ctx, websocket.MessageBinary, frame); err != nil {
		t.Fatalf("ws write: %v", err)
	}

	got := rec.wait(t, 3*time.Second)
	if got.cmd != packet.HeaderCALOGIN {
		t.Fatalf("cmd = 0x%04x, want 0x%04x", got.cmd, packet.HeaderCALOGIN)
	}
}

// TestWSHandler_EmptyOriginsAcceptsAll preserves the dev/default behavior:
// when allowedOrigins is empty the handler must accept connections
// regardless of Origin (this is what existing tests rely on).
func TestWSHandler_EmptyOriginsAcceptsAll(t *testing.T) {
	db := packet.NewLoginServerDB()
	rec := newWSRecordingHandler()
	logger := newTestLogger(t)

	mux := http.NewServeMux()
	h := NewWSHandler(db, rec, service.NewSessionRegistry(), "unused", "/ws/", logger, nil)
	mux.HandleFunc(h.path, h.ServeHTTP)
	mux.HandleFunc("/", h.rejectNonUpgrade)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := wsTestURL(t, srv.URL) + h.path
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	client, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPClient: srv.Client(),
		HTTPHeader: http.Header{"Origin": []string{"https://anywhere.example.com"}},
	})
	if err != nil {
		t.Fatalf("ws dial with no allowlist: %v", err)
	}
	defer func() { _ = client.Close(websocket.StatusNormalClosure, "") }()
}

// wsTestURL converts an http:// test-server URL to its ws:// equivalent.
func wsTestURL(t *testing.T, httpURL string) string {
	t.Helper()
	u, err := url.Parse(httpURL)
	if err != nil {
		t.Fatalf("parse test server url %q: %v", httpURL, err)
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	default:
		t.Fatalf("unexpected test server scheme %q", u.Scheme)
	}
	return u.String()
}

// waitForWS404 polls a plain HTTP GET against the WS listener until it
// receives a 404, which proves the listener is bound and the mux is
// serving. The WS port deliberately returns 404 for any non-upgrade
// request.
func waitForWS404(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + addr + "/nope")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusNotFound {
				return nil
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	return errors.New("timed out waiting for ws listener to respond")
}
