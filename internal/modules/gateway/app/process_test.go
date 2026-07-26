//go:build unit

package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	netcodec "github.com/bouroo/goAthena/internal/infrastructure/net"
	"github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// fakeConn is an in-memory domain.Conn: it records written bytes, role
// transitions, and auth cache writes. No real socket is involved.
type fakeConn struct {
	role    domain.Role
	auth    domain.ConnAuth
	written bytes.Buffer
	closed  bool
}

func (c *fakeConn) Role() domain.Role         { return c.role }
func (c *fakeConn) SetRole(r domain.Role)     { c.role = r }
func (c *fakeConn) Auth() domain.ConnAuth     { return c.auth }
func (c *fakeConn) SetAuth(a domain.ConnAuth) { c.auth = a }
func (c *fakeConn) RemoteAddr() string        { return "test:0" }
func (c *fakeConn) Write(p []byte) error {
	_, err := c.written.Write(p)
	return err
}

func (c *fakeConn) Close() error {
	c.closed = true
	return nil
}

// writeAdapter bridges a domain.Conn (Write returns only error) to io.Writer
// (Write returns (int, error)) so the kernel's packet encoders can target a
// Conn directly. The transport adapters do the same at runtime.
type writeAdapter struct{ c domain.Conn }

func (w writeAdapter) Write(p []byte) (int, error) {
	if err := w.c.Write(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// TestProcessBytes_CALogin_HappyPath is the M1 ingress end-to-end proof: a raw
// CA_LOGIN byte stream is framed by the codec, routed by the dispatcher to the
// login handler, parsed, and answered with an AC_ACCEPT_LOGIN response — all
// with no real socket. This exercises codec.Next → Dispatcher.Dispatch →
// handler → Conn.Write, the path every later handler reuses.
func TestProcessBytes_CALogin_HappyPath(t *testing.T) {
	t.Parallel()

	// Build a real 55-byte CA_LOGIN frame via the kernel encoder.
	var req bytes.Buffer
	require.NoError(t, packet.CALoginRequest{
		Version:    20250604,
		Username:   "testuser",
		Password:   "secret",
		ClientType: 0x0,
	}.Encode(&req))

	var (
		gotUsername string
		gotVersion  uint32
		conn        = &fakeConn{role: domain.RoleLogin}
	)
	handler := func(_ context.Context, _ domain.Conn, frame domain.Frame) error {
		parsed, err := packet.ParseCALogin(frame.Raw)
		if err != nil {
			return err
		}
		gotUsername = parsed.Username
		gotVersion = parsed.Version
		// Respond with a minimal AC_ACCEPT_LOGIN (no char servers).
		return packet.AcceptLoginResponse{
			LoginID1: 0x11111111,
			AID:      42,
			LoginID2: 0x22222222,
		}.Encode(writeAdapter{conn})
	}
	d := domain.NewDispatcher(
		domain.PacketHandlerTable{packet.HeaderCALOGIN: handler}, nil, nil,
	)

	dec := netcodec.NewLoginDecoder(packet.NewLoginServerDB())
	log := zerolog.Nop()

	err := ProcessBytes(context.Background(), &log, conn, dec, req.Bytes(), d)
	require.NoError(t, err)

	// The request was parsed correctly by the handler.
	assert.Equal(t, "testuser", gotUsername)
	assert.Equal(t, uint32(20250604), gotVersion)

	// A full AC_ACCEPT_LOGIN response was written. Layout per
	// pkg/ro/packet/encode.go: [0:2] cmd 0x0ac4, [2:4] length, [4:8] login_id1,
	// [8:12] AID, [12:16] login_id2.
	out := conn.written.Bytes()
	assert.Equal(t, packet.AcceptLoginResponse{}.Size(), len(out))
	assert.Equal(t, packet.HeaderACACCEPTLOGIN, binary.LittleEndian.Uint16(out[0:2]))
	assert.Equal(t, uint32(42), binary.LittleEndian.Uint32(out[8:12]))
}

// TestProcessBytes_PartialFrame_WaitsForMore proves ErrIncomplete is tolerated:
// a partial CA_LOGIN (fewer than 55 bytes) yields no dispatch and no error —
// the decoder retains the bytes for the next chunk.
func TestProcessBytes_PartialFrame_WaitsForMore(t *testing.T) {
	t.Parallel()

	// Build a full CA_LOGIN, then feed only its first 10 bytes. The decoder
	// sees the known cmd 0x0064 (length 55) but has only 10 bytes buffered →
	// ErrIncomplete. The session must stay open for the remaining 45 bytes.
	var full bytes.Buffer
	require.NoError(t, packet.CALoginRequest{Username: "partial"}.Encode(&full))

	conn := &fakeConn{role: domain.RoleLogin}
	dec := netcodec.NewLoginDecoder(packet.NewLoginServerDB())
	log := zerolog.Nop()
	d := domain.NewDispatcher(nil, nil, nil)

	err := ProcessBytes(context.Background(), &log, conn, dec, full.Bytes()[:10], d)

	require.NoError(t, err)
	assert.Equal(t, 0, conn.written.Len(), "partial frame must not trigger a write")
	assert.False(t, conn.closed, "connection must stay open for the next chunk")
}

// TestProcessBytes_UnknownOpcode_Disconnects proves an opcode absent from the
// packet DB is treated as an untrusted stream: the error propagates so the
// transport closes the connection (rAthena clif.cpp behaviour for unknown
// commands).
func TestProcessBytes_UnknownOpcode_Disconnects(t *testing.T) {
	t.Parallel()

	conn := &fakeConn{role: domain.RoleLogin}
	dec := netcodec.NewLoginDecoder(packet.NewLoginServerDB())
	log := zerolog.Nop()
	d := domain.NewDispatcher(nil, nil, nil)

	// 0xffff is not a registered login opcode.
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf[0:2], 0xffff)

	err := ProcessBytes(context.Background(), &log, conn, dec, buf, d)

	require.Error(t, err)
	assert.True(t, errors.Is(err, netcodec.ErrUnknownPacket), "want ErrUnknownPacket, got %v", err)
}

// TestProcessBytes_NoHandler_Tolerated proves a registered DB opcode with no
// handler is logged-and-continued (ErrNoHandler), not a disconnect. The session
// survives an unimplemented opcode.
func TestProcessBytes_NoHandler_Tolerated(t *testing.T) {
	t.Parallel()

	conn := &fakeConn{role: domain.RoleLogin}
	dec := netcodec.NewLoginDecoder(packet.NewLoginServerDB())
	log := zerolog.Nop()
	// Empty handler set: CA_LOGIN is a known opcode but has no handler.
	d := domain.NewDispatcher(nil, nil, nil)

	var req bytes.Buffer
	require.NoError(t, packet.CALoginRequest{Username: "x"}.Encode(&req))

	err := ProcessBytes(context.Background(), &log, conn, dec, req.Bytes(), d)

	require.NoError(t, err, "ErrNoHandler must not surface as a transport error")
	assert.False(t, conn.closed, "session must survive an unhandled opcode")
}
