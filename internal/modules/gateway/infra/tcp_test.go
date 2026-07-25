//go:build unit

package infra

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	netcodec "github.com/bouroo/goAthena/internal/infrastructure/net"
	"github.com/bouroo/goAthena/internal/modules/gateway/domain"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// TestTCPHandler_CALogin_RoundTrip is the M1-2 TCP end-to-end proof. A real gnet
// listener accepts a TCP connection, the codec frames a CA_LOGIN, the dispatcher
// routes it to the handler, ParseCALogin decodes it, and the handler's
// AC_ACCEPT_LOGIN is read back over the same socket. It exercises the full
// transport path the production login server uses: OnBoot → OnOpen →
// OnTraffic → Write, plus clean shutdown via Engine.Stop.
func TestTCPHandler_CALogin_RoundTrip(t *testing.T) {
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
				LoginID1: 0x11111111, AID: 42, LoginID2: 0x22222222,
			}.Encode(writeAdapter{conn})
		},
	}, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	log := zerolog.Nop()
	h := NewTCPHandler(ctx, &log, disp, func() *netcodec.Decoder {
		return netcodec.NewLoginDecoder(packet.NewLoginServerDB())
	})

	runErr := make(chan error, 1)
	go func() { runErr <- h.Run("tcp://" + addr) }()
	t.Cleanup(func() {
		cancel()
		if err := <-runErr; err != nil {
			t.Errorf("tcp server shutdown error: %v", err)
		}
	})

	waitForListen(t, addr, 2*time.Second)

	conn, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer conn.Close()

	var req bytes.Buffer
	require.NoError(t, packet.CALoginRequest{
		Version: 20250604, Username: "tcpuser", Password: "secret",
	}.Encode(&req))
	_, err = conn.Write(req.Bytes())
	require.NoError(t, err)

	resp := make([]byte, packet.AcceptLoginResponse{}.Size())
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, err = io.ReadFull(conn, resp)
	require.NoError(t, err, "no AC_ACCEPT_LOGIN received")

	// [0:2] cmd 0x0ac4, [2:4] length, [8:12] AID — per pkg/ro/packet/encode.go.
	assert.Equal(t, packet.HeaderACACCEPTLOGIN, binary.LittleEndian.Uint16(resp[0:2]))
	assert.Equal(t, uint16(packet.AcceptLoginResponse{}.Size()), binary.LittleEndian.Uint16(resp[2:4]))
	assert.Equal(t, uint32(42), binary.LittleEndian.Uint32(resp[8:12]))

	// The parse happened end-to-end: the username surfaced in the handler.
	select {
	case u := <-got:
		assert.Equal(t, "tcpuser", u)
	case <-time.After(time.Second):
		t.Fatal("handler was not invoked; CA_LOGIN did not round-trip")
	}
}
