//go:build unit

package infra

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	netcodec "github.com/bouroo/goAthena/internal/infrastructure/net"
	"github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// TestWSServer_CALogin_RoundTrip is the M1-2 WebSocket end-to-end proof. A real
// coder/websocket server upgrades a connection, frames a CA_LOGIN carried in a
// binary WS message, the dispatcher routes it, ParseCALogin decodes it, and the
// handler's AC_ACCEPT_LOGIN comes back as a binary WS message. It proves the WS
// adapter feeds the same transport-agnostic core as TCP and that packets
// spanning (or packing into) WS messages are framed correctly.
func TestWSServer_CALogin_RoundTrip(t *testing.T) {
	t.Parallel()

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(freePort(t)))

	got := make(chan string, 1)
	disp := domain.NewDispatcher(domain.PacketHandlerTable{
		packet.HeaderCALOGIN: func(_ context.Context, conn domain.Conn, frame domain.Frame) error {
			parsed, err := packet.ParseCALogin(frame.Raw)
			if err != nil {
				return err
			}
			got <- parsed.Username
			return packet.AcceptLoginResponse{
				LoginID1: 0x11111111, AID: 7, LoginID2: 0x22222222,
			}.Encode(writeAdapter{conn})
		},
	}, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	log := zerolog.Nop()
	srv := NewWSServer(ctx, &log, disp, func() *netcodec.Decoder {
		return netcodec.NewLoginDecoder(packet.NewLoginServerDB())
	}, nil)

	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(addr) }()
	t.Cleanup(func() {
		cancel()
		if err := <-runErr; err != nil {
			t.Errorf("ws server shutdown error: %v", err)
		}
	})

	waitForListen(t, addr, 2*time.Second)

	c, _, err := websocket.Dial(ctx, "ws://"+addr+wsPath, nil)
	require.NoError(t, err)
	defer func() { _ = c.CloseNow() }()

	var req bytes.Buffer
	require.NoError(t, packet.CALoginRequest{
		Version: 20250604, Username: "wsuser", Password: "secret",
	}.Encode(&req))
	require.NoError(t, c.Write(ctx, websocket.MessageBinary, req.Bytes()))

	dialCtx, cancelRead := context.WithTimeout(ctx, 2*time.Second)
	defer cancelRead()
	_, resp, err := c.Read(dialCtx)
	require.NoError(t, err, "no AC_ACCEPT_LOGIN WS message received")

	want := packet.AcceptLoginResponse{}.Size()
	require.Len(t, resp, want, "response frame length")

	assert.Equal(t, packet.HeaderACACCEPTLOGIN, binary.LittleEndian.Uint16(resp[0:2]))
	assert.Equal(t, uint16(want), binary.LittleEndian.Uint16(resp[2:4]))
	assert.Equal(t, uint32(7), binary.LittleEndian.Uint32(resp[8:12]))

	select {
	case u := <-got:
		assert.Equal(t, "wsuser", u)
	case <-time.After(time.Second):
		t.Fatal("handler was not invoked; CA_LOGIN did not round-trip over WS")
	}
}

// TestWSServer_MalformedPacket_Closes proves a decode error (unknown opcode)
// closes the connection with a policy-violation status, mirroring the TCP
// adapter's ErrUnknownPacket → Close behaviour.
func TestWSServer_MalformedPacket_Closes(t *testing.T) {
	t.Parallel()

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(freePort(t)))

	ctx, cancel := context.WithCancel(context.Background())
	log := zerolog.Nop()
	srv := NewWSServer(ctx, &log, domain.NewDispatcher(nil, nil, nil), func() *netcodec.Decoder {
		return netcodec.NewLoginDecoder(packet.NewLoginServerDB())
	}, nil)

	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(addr) }()
	t.Cleanup(func() {
		cancel()
		<-runErr
	})

	waitForListen(t, addr, 2*time.Second)

	c, _, err := websocket.Dial(ctx, "ws://"+addr+wsPath, nil)
	require.NoError(t, err)
	defer func() { _ = c.CloseNow() }()

	// 0xffff is not a registered login opcode → ErrUnknownPacket.
	require.NoError(t, c.Write(ctx, websocket.MessageBinary, []byte{0xff, 0xff}))

	dialCtx, cancelRead := context.WithTimeout(ctx, 2*time.Second)
	defer cancelRead()
	_, _, err = c.Read(dialCtx)
	require.Error(t, err, "expected the server to close the connection after a decode error")
	status := websocket.CloseStatus(err)
	assert.Equal(t, websocket.StatusPolicyViolation, status,
		"want StatusPolicyViolation close, got status %d err %v", status, err)
}
