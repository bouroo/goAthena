// Package infra binds the transport-agnostic dispatch core (ProcessBytes) to
// real network transports. Each adapter is a thin reader that supplies inbound
// bytes and a domain.Conn; the app layer does the framing and routing. New
// transports are a new adapter in this package — never a new abstraction.
package infra

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"github.com/panjf2000/gnet/v2"
	"github.com/rs/zerolog"

	netcodec "github.com/bouroo/goAthena/internal/infrastructure/net"
	"github.com/bouroo/goAthena/internal/modules/gateway/app"
	"github.com/bouroo/goAthena/internal/modules/gateway/domain"
)

// stopTimeout bounds graceful shutdown so a stuck peer cannot hang the process.
// rAthena drops clients on SIGTERM without waiting for in-flight frames.
const stopTimeout = 3 * time.Second

// DecoderFactory builds a fresh per-connection login Decoder. The codec is not
// concurrency-safe, so every connection owns its own; the factory is invoked
// once per accepted connection.
type DecoderFactory func() *netcodec.Decoder

// tcpConn adapts a gnet v2 connection to the gateway domain.Conn. It carries
// the connection's dispatch role and per-connection Decoder in the gnet Conn's
// user context.
//
// Write uses gnet's synchronous io.Writer, which is safe because the M1 model
// is one goroutine per connection (the event-loop owns both read and write).
// Cross-connection broadcast writes arrive at M4 and switch to AsyncWrite.
type tcpConn struct {
	raw    gnet.Conn
	role   domain.Role
	remote string
	dec    *netcodec.Decoder
	ctx    context.Context
	cancel context.CancelFunc
}

func (c *tcpConn) Role() domain.Role     { return c.role }
func (c *tcpConn) SetRole(r domain.Role) { c.role = r }
func (c *tcpConn) RemoteAddr() string    { return c.remote }
func (c *tcpConn) Write(p []byte) error {
	if _, err := c.raw.Write(p); err != nil {
		return fmt.Errorf("tcp write: %w", err)
	}
	return nil
}

func (c *tcpConn) Close() error {
	if err := c.raw.Close(); err != nil {
		return fmt.Errorf("tcp close: %w", err)
	}
	return nil
}

// TCPHandler is a gnet v2 EventHandler that drives the gateway dispatch loop.
// Embedding *gnet.BuiltinEventEngine supplies no-op defaults for the lifecycle
// hooks we do not override (OnShutdown, OnTick).
type TCPHandler struct {
	*gnet.BuiltinEventEngine

	baseCtx context.Context
	log     *zerolog.Logger
	disp    *domain.Dispatcher
	newDec  DecoderFactory

	eng    atomic.Pointer[gnet.Engine]
	booted chan struct{}
}

// NewTCPHandler builds a login-server TCP handler. baseCtx governs the server
// lifetime: when it is cancelled, Run stops the engine.
func NewTCPHandler(baseCtx context.Context, log *zerolog.Logger, disp *domain.Dispatcher, newDec DecoderFactory) *TCPHandler {
	return &TCPHandler{
		BuiltinEventEngine: &gnet.BuiltinEventEngine{},
		baseCtx:            baseCtx,
		log:                log,
		disp:               disp,
		newDec:             newDec,
		booted:             make(chan struct{}),
	}
}

// OnBoot captures the engine so Run can stop it on shutdown. Closing booted
// unblocks Run exactly once the listener is accepting.
func (h *TCPHandler) OnBoot(eng gnet.Engine) gnet.Action {
	engCopy := eng
	h.eng.Store(&engCopy)
	close(h.booted)
	return gnet.None
}

// OnOpen allocates the per-connection state: a fresh Decoder, a cancelled-on
// close context derived from the server lifetime, and the cached peer address
// (gnet's RemoteAddr is unsafe to read off the event-loop, so we snapshot it).
func (h *TCPHandler) OnOpen(c gnet.Conn) ([]byte, gnet.Action) {
	ctx, cancel := context.WithCancel(h.baseCtx)
	conn := &tcpConn{
		raw:    c,
		remote: c.RemoteAddr().String(),
		dec:    h.newDec(),
		ctx:    ctx,
		cancel: cancel,
	}
	c.SetContext(conn)
	return nil, gnet.None
}

// OnTraffic drains every buffered inbound byte into ProcessBytes. A decode
// error means the byte stream is untrusted (unknown opcode or malformed
// length); the connection is closed, mirroring rAthena's clif.cpp. Handler
// errors are logged-and-continued inside ProcessBytes and never reach here.
func (h *TCPHandler) OnTraffic(c gnet.Conn) gnet.Action {
	conn, ok := c.Context().(*tcpConn)
	if !ok || conn == nil {
		// Context should always be set by OnOpen; if it is not, the
		// connection is in an unrecoverable state — close it.
		return gnet.Close
	}
	var buf bytes.Buffer
	buf.Grow(c.InboundBuffered())
	if _, err := c.WriteTo(&buf); err != nil {
		h.log.Error().Err(err).Str("peer", conn.remote).Msg("read inbound bytes")
		return gnet.Close
	}
	if err := app.ProcessBytes(conn.ctx, h.log, conn, conn.dec, buf.Bytes(), h.disp); err != nil {
		h.log.Error().Err(err).Str("peer", conn.remote).Msg("closing connection after decode error")
		return gnet.Close
	}
	return gnet.None
}

// OnClose releases the per-connection context. gnet guarantees OnClose fires
// exactly once per connection, even on error-driven Close.
func (h *TCPHandler) OnClose(c gnet.Conn, err error) gnet.Action {
	if conn, _ := c.Context().(*tcpConn); conn != nil {
		conn.cancel()
	}
	if err != nil && !errors.Is(err, net.ErrClosed) {
		h.log.Debug().Err(err).Msg("connection closed")
	}
	return gnet.None
}

// Run starts the gnet listener on addr (e.g. "tcp://:6900") and blocks until
// baseCtx is cancelled, then stops the engine and returns. The return is nil
// for a clean shutdown, the gnet.Run error otherwise.
func (h *TCPHandler) Run(addr string) error {
	runErr := make(chan error, 1)
	go func() { runErr <- gnet.Run(h, addr) }()

	// Wait until either boot completes (engine captured) or Run fails first.
	select {
	case <-h.booted:
	case err := <-runErr:
		return err
	}

	select {
	case <-h.baseCtx.Done():
		stopCtx, cancel := context.WithTimeout(context.Background(), stopTimeout)
		defer cancel()
		if eng := h.eng.Load(); eng != nil {
			if stopErr := eng.Stop(stopCtx); stopErr != nil {
				return fmt.Errorf("stop tcp engine: %w", stopErr)
			}
		}
		return <-runErr
	case err := <-runErr:
		return err
	}
}
