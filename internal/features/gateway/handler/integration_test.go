//go:build integration

package handler_test

import (
	"context"
	"encoding/binary"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/panjf2000/gnet/v2"
	"github.com/rs/zerolog"

	"github.com/bouroo/goAthena/internal/features/gateway/domain"
	"github.com/bouroo/goAthena/internal/features/gateway/handler"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// recordingHandler satisfies domain.PacketHandler and signals each
// invocation on a buffered channel so the test can deterministically
// wait for a real TCP packet to round-trip through gnet.
type recordingHandler struct {
	mu sync.Mutex
	ch chan struct {
		cmd   uint16
		frame []byte
	}
	last struct {
		cmd   uint16
		frame []byte
	}
	count int
}

func newRecordingHandler() *recordingHandler {
	return &recordingHandler{ch: make(chan struct {
		cmd   uint16
		frame []byte
	}, 16)}
}

func (h *recordingHandler) HandlePacket(_ context.Context, info domain.ConnectionInfo, cmd uint16, frame []byte) error {
	cp := make([]byte, len(frame))
	copy(cp, frame)
	h.mu.Lock()
	h.last.cmd = cmd
	h.last.frame = cp
	h.count++
	h.mu.Unlock()
	select {
	case h.ch <- struct {
		cmd   uint16
		frame []byte
	}{cmd, cp}:
	default:
	}
	return nil
}

func TestIntegration_TCPAcceptsAndDecodesCALogin(t *testing.T) {
	// gnet does not support :0 (random port) portably across platforms.
	// Pick a fixed high port; if it is already in use the test is
	// skipped so it never flakes on shared CI hosts.
	const addr = "127.0.0.1:16900"

	db := packet.NewLoginServerDB()
	rec := newRecordingHandler()
	logger := zerolog.New(zerolog.NewTestWriter(nil)).Level(zerolog.Disabled)
	tcpHandler := handler.NewTCPHandler(db, rec, logger)

	engineErrCh := make(chan error, 1)
	go func() {
		engineErrCh <- gnet.Run(tcpHandler, "tcp://"+addr, gnet.WithTicker(false))
	}()

	if err := waitForListen(addr, 3*time.Second); err != nil {
		_ = tcpHandler.Engine().Stop(context.Background())
		t.Skipf("port %s not bindable in this environment: %v", addr, err)
	}

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		_ = tcpHandler.Engine().Stop(context.Background())
		t.Fatalf("dial: %v", err)
	}
	defer func() {
		_ = conn.Close()
		_ = tcpHandler.Engine().Stop(context.Background())
	}()

	frame := make([]byte, 55)
	binary.LittleEndian.PutUint16(frame[0:2], packet.HeaderCALOGIN)
	binary.LittleEndian.PutUint32(frame[2:6], 20130807)
	copy(frame[6:30], "tester")
	copy(frame[30:54], "hunter2")
	frame[54] = 0

	if _, err := conn.Write(frame); err != nil {
		t.Fatalf("write frame: %v", err)
	}

	select {
	case got := <-rec.ch:
		if got.cmd != packet.HeaderCALOGIN {
			t.Fatalf("received cmd = 0x%04x, want 0x%04x", got.cmd, packet.HeaderCALOGIN)
		}
		if len(got.frame) != 55 {
			t.Fatalf("frame len = %d, want 55", len(got.frame))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for HandlePacket invocation")
	}

	select {
	case <-engineErrCh:
	case <-time.After(2 * time.Second):
		t.Log("gnet engine did not return within 2s after Stop")
	}
}

// waitForListen polls the TCP port until it accepts a connection or the
// timeout elapses. The probe connection is closed immediately.
func waitForListen(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = c.Close()
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return context.DeadlineExceeded
}
