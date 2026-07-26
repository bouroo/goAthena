package infra

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/rs/zerolog"

	netcodec "github.com/bouroo/goAthena/internal/infrastructure/net"
	"github.com/bouroo/goAthena/internal/modules/gateway/app"
	"github.com/bouroo/goAthena/internal/modules/gateway/domain"
)

// wsPath is the WebSocket upgrade route roBrowser dials. A dedicated listener
// owns this server; the echo HTTP server hosts health on its own port.
const wsPath = "/ws/"

// wsReadHeaderTimeout bounds the HTTP upgrade handshake, closing the slowloris
// vector (gosec G112). It does not bound the upgraded connection, which
// coder/websocket owns for the session lifetime.
const wsReadHeaderTimeout = 10 * time.Second

// wsConn adapts a coder/websocket connection to the gateway domain.Conn. Its
// context is connection-scoped (cancelled on close or server shutdown) so that
// Write deadlines follow the session lifetime rather than blocking forever.
type wsConn struct {
	c      *websocket.Conn
	role   domain.Role
	auth   domain.ConnAuth
	remote string
	dec    *netcodec.Decoder
	ctx    context.Context
}

func (w *wsConn) Role() domain.Role         { return w.role }
func (w *wsConn) SetRole(r domain.Role)     { w.role = r }
func (w *wsConn) Auth() domain.ConnAuth     { return w.auth }
func (w *wsConn) SetAuth(a domain.ConnAuth) { w.auth = a }
func (w *wsConn) RemoteAddr() string        { return w.remote }

// Write sends raw response bytes as a single binary WS message. roBrowser
// carries the RO protocol over binary frames; the Write serializes with the
// read loop, so the single-reader-single-writer M1 model holds.
func (w *wsConn) Write(p []byte) error {
	if err := w.c.Write(w.ctx, websocket.MessageBinary, p); err != nil {
		return fmt.Errorf("ws write: %w", err)
	}
	return nil
}

func (w *wsConn) Close() error {
	if err := w.c.Close(websocket.StatusNormalClosure, ""); err != nil {
		return fmt.Errorf("ws close: %w", err)
	}
	return nil
}

// WSServer serves the roBrowser WebSocket game endpoint. Each accepted
// connection runs a read loop that feeds whole WS messages into the same
// ProcessBytes core as TCP. RO packets do not align to WS message boundaries,
// so the codec's buffered framing handles packets spanning messages exactly as
// it handles TCP segmentation.
//
// String encoding (UTF-8 for roBrowser vs TextCodepage for the native client)
// is applied at the parse layer, not here: this adapter is byte-neutral.
type WSServer struct {
	baseCtx context.Context
	log     *zerolog.Logger
	disp    *domain.Dispatcher
	// newDec is the underlying func type so both DecoderFactory (login/char)
	// and MapDecoderFactory (map) are assignable without conversion.
	newDec      func() *netcodec.Decoder
	initialRole domain.Role
	opts        *websocket.AcceptOptions
	server      *http.Server
}

// NewWSServer builds the login/char WebSocket endpoint: every accepted
// connection starts at the login role. allowedOrigins configures
// AcceptOptions.OriginPatterns; an empty list disables origin verification
// (InsecureSkipVerify) — the documented way to permit any origin for local
// development.
func NewWSServer(
	baseCtx context.Context,
	log *zerolog.Logger,
	disp *domain.Dispatcher,
	newDec DecoderFactory,
	allowedOrigins []string,
) *WSServer {
	opts := &websocket.AcceptOptions{OriginPatterns: allowedOrigins}
	if len(allowedOrigins) == 0 {
		opts = &websocket.AcceptOptions{InsecureSkipVerify: true}
	}
	return &WSServer{
		baseCtx:     baseCtx,
		log:         log,
		disp:        disp,
		newDec:      newDec,
		initialRole: domain.RoleLogin,
		opts:        opts,
	}
}

// NewMapWSServer builds the map-server WebSocket endpoint for roBrowser: every
// accepted connection starts at the map role with the map decoder. The roBrowser
// client reaches this endpoint only after HC_NOTIFY_ZONESVR redirects it from
// char-select, mirroring NewMapTCPHandler on the WebSocket transport. The map
// listener registration and its config land with the dual-client e2e (M7); the
// constructor is map-capable now so that wiring is a one-liner then.
func NewMapWSServer(
	baseCtx context.Context,
	log *zerolog.Logger,
	disp *domain.Dispatcher,
	newDec MapDecoderFactory,
	allowedOrigins []string,
) *WSServer {
	opts := &websocket.AcceptOptions{OriginPatterns: allowedOrigins}
	if len(allowedOrigins) == 0 {
		opts = &websocket.AcceptOptions{InsecureSkipVerify: true}
	}
	return &WSServer{
		baseCtx:     baseCtx,
		log:         log,
		disp:        disp,
		newDec:      newDec,
		initialRole: domain.RoleMap,
		opts:        opts,
	}
}

// Run binds the WebSocket listener on addr and serves until baseCtx is
// cancelled, then shuts the server down gracefully. It owns its own
// http.Server serving the upgrade at wsPath.
func (s *WSServer) Run(addr string) error {
	mux := http.NewServeMux()
	mux.HandleFunc(wsPath, s.handle)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("ws listen %s: %w", addr, err)
	}
	s.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: wsReadHeaderTimeout,
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- s.server.Serve(ln) }()

	select {
	case <-s.baseCtx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), stopTimeout)
		defer cancel()
		if err := s.server.Shutdown(shutCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("ws server shutdown: %w", err)
		}
		return nil
	case err := <-serveErr:
		return err
	}
}

// handle upgrades one HTTP request to WebSocket and runs the read loop. It is
// the per-connection entry point.
func (s *WSServer) handle(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, s.opts)
	if err != nil {
		s.log.Debug().Err(err).Str("peer", r.RemoteAddr).Msg("ws accept failed")
		return
	}
	ctx, cancel := context.WithCancel(s.baseCtx)
	conn := &wsConn{
		c:      c,
		role:   s.initialRole,
		remote: r.RemoteAddr,
		dec:    s.newDec(),
		ctx:    ctx,
	}
	defer cancel()
	defer func() { _ = c.CloseNow() }()
	s.serveConn(ctx, conn)
}

// serveConn is the connection read loop. It returns when the client closes,
// the context is cancelled (server shutdown), or a decode error forces a close.
func (s *WSServer) serveConn(ctx context.Context, conn *wsConn) {
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		_, msg, err := conn.c.Read(ctx)
		if err != nil {
			// A clean client close and server-driven shutdown are expected.
			if !isExpectedClose(err) {
				s.log.Debug().Err(err).Str("peer", conn.remote).Msg("ws read ended")
			}
			return
		}
		if err := app.ProcessBytes(ctx, s.log, conn, conn.dec, msg, s.disp); err != nil {
			s.log.Error().Err(err).Str("peer", conn.remote).Msg("ws decode error; closing connection")
			_ = conn.c.Close(websocket.StatusPolicyViolation, "malformed packet")
			return
		}
	}
}

// isExpectedClose reports whether an error from Read is a normal session end
// (client-initiated close or server shutdown) and therefore not worth logging.
// Anything else — abrupt network resets, abnormal close codes — is logged at
// debug. It branches on the typed close status, never on error strings.
func isExpectedClose(err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	status := websocket.CloseStatus(err)
	return status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway
}
