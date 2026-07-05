//go:build unit

package handler

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"testing"

	"github.com/rs/zerolog"

	"github.com/bouroo/goAthena/internal/features/gateway/domain"
	netcodec "github.com/bouroo/goAthena/internal/infrastructure/net"
	"github.com/bouroo/goAthena/pkg/ro/packet"
)

// recordingHandler captures every packet dispatched by processBytes so
// tests can assert the decode+dispatch contract without touching gnet.
type recordingHandler struct {
	mu      sync.Mutex
	packets []recordedPacket
	err     error // returned from HandlePacket if non-nil
}

type recordedPacket struct {
	cmd   uint16
	frame []byte
}

func (h *recordingHandler) HandlePacket(_ context.Context, _ domain.ConnectionInfo, cmd uint16, frame []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.err != nil {
		return h.err
	}
	// Copy the frame so the test is independent of the decoder's
	// buffer reuse after Next returns.
	cp := make([]byte, len(frame))
	copy(cp, frame)
	h.packets = append(h.packets, recordedPacket{cmd: cmd, frame: cp})
	return nil
}

func (h *recordingHandler) calls() []recordedPacket {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]recordedPacket, len(h.packets))
	copy(out, h.packets)
	return out
}

// buildCALogin crafts a complete CA_LOGIN packet (0x0064, 55 bytes):
//
//	cmd[2] + version[4] + username[24] + password[24] + clienttype[1] = 55
func buildCALogin(t *testing.T, username, password string) []byte {
	t.Helper()
	const size = 55
	frame := make([]byte, size)
	binary.LittleEndian.PutUint16(frame[0:2], packet.HeaderCALOGIN)
	binary.LittleEndian.PutUint32(frame[2:6], 20130807)
	copy(frame[6:30], username)
	copy(frame[30:54], password)
	frame[54] = 0 // clienttype
	return frame
}

func newTestLogger(t *testing.T) zerolog.Logger {
	t.Helper()
	return zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.Disabled)
}

func TestProcessBytes_CompleteCALogin_DispatchesOnce(t *testing.T) {
	db := packet.NewLoginServerDB()
	dec := netcodec.NewLoginDecoder(db)
	h := &recordingHandler{}
	info := domain.ConnectionInfo{ID: 42, RemoteIP: "127.0.0.1:1234"}

	frame := buildCALogin(t, "tester", "hunter2")
	if err := processBytes(context.Background(), dec, frame, info, h); err != nil {
		t.Fatalf("processBytes err = %v", err)
	}

	calls := h.calls()
	if len(calls) != 1 {
		t.Fatalf("HandlePacket calls = %d, want 1", len(calls))
	}
	if calls[0].cmd != packet.HeaderCALOGIN {
		t.Fatalf("cmd = 0x%04x, want 0x%04x", calls[0].cmd, packet.HeaderCALOGIN)
	}
	if len(calls[0].frame) != len(frame) {
		t.Fatalf("frame len = %d, want %d", len(calls[0].frame), len(frame))
	}
	if !bytesEqual(calls[0].frame, frame) {
		t.Fatalf("frame bytes mismatch:\n got %x\nwant %x", calls[0].frame, frame)
	}
	if dec.Buffered() != 0 {
		t.Fatalf("decoder buffered %d bytes after complete packet, want 0", dec.Buffered())
	}
}

func TestProcessBytes_PartialBytes_DoesNotDispatch(t *testing.T) {
	db := packet.NewLoginServerDB()
	dec := netcodec.NewLoginDecoder(db)
	h := &recordingHandler{}
	info := domain.ConnectionInfo{ID: 1}

	full := buildCALogin(t, "u", "p")
	partial := full[:30] // only cmd + version + 24-byte username

	if err := processBytes(context.Background(), dec, partial, info, h); err != nil {
		t.Fatalf("processBytes(partial) err = %v", err)
	}
	if calls := h.calls(); len(calls) != 0 {
		t.Fatalf("HandlePacket calls = %d on partial input, want 0", len(calls))
	}
	if dec.Buffered() != len(partial) {
		t.Fatalf("decoder buffered = %d, want %d", dec.Buffered(), len(partial))
	}

	// Feed the remainder — the decoder must now yield the full packet
	// without any loss or duplication.
	rest := full[len(partial):]
	if err := processBytes(context.Background(), dec, rest, info, h); err != nil {
		t.Fatalf("processBytes(rest) err = %v", err)
	}
	if calls := h.calls(); len(calls) != 1 {
		t.Fatalf("HandlePacket calls = %d after feeding remainder, want 1", len(calls))
	}
}

func TestProcessBytes_UnknownCmd_ReturnsErrUnknownPacket(t *testing.T) {
	db := packet.NewLoginServerDB()
	dec := netcodec.NewLoginDecoder(db)
	h := &recordingHandler{}
	info := domain.ConnectionInfo{ID: 1}

	bad := []byte{0x00, 0xFE, 0, 0, 0, 0} // 0xFE00 is not registered

	err := processBytes(context.Background(), dec, bad, info, h)
	if err == nil {
		t.Fatal("processBytes err = nil, want error")
	}
	if !errors.Is(err, netcodec.ErrUnknownPacket) {
		t.Fatalf("err = %v, want wraps ErrUnknownPacket", err)
	}
	if calls := h.calls(); len(calls) != 0 {
		t.Fatalf("HandlePacket calls = %d on unknown cmd, want 0", len(calls))
	}
}

func TestProcessBytes_HandlerError_Propagates(t *testing.T) {
	db := packet.NewLoginServerDB()
	dec := netcodec.NewLoginDecoder(db)
	handlerErr := errors.New("downstream boom")
	h := &recordingHandler{err: handlerErr}
	info := domain.ConnectionInfo{ID: 7}

	frame := buildCALogin(t, "u", "p")
	err := processBytes(context.Background(), dec, frame, info, h)
	if !errors.Is(err, handlerErr) {
		t.Fatalf("processBytes err = %v, want wraps %v", err, handlerErr)
	}
}

func TestProcessBytes_MultiplePacketsInOneFeed(t *testing.T) {
	db := packet.NewLoginServerDB()
	dec := netcodec.NewLoginDecoder(db)
	h := &recordingHandler{}
	info := domain.ConnectionInfo{ID: 9}

	a := buildCALogin(t, "alice", "pw")
	b := buildCALogin(t, "bob", "pw")

	combined := append(append([]byte{}, a...), b...)
	if err := processBytes(context.Background(), dec, combined, info, h); err != nil {
		t.Fatalf("processBytes err = %v", err)
	}
	calls := h.calls()
	if len(calls) != 2 {
		t.Fatalf("HandlePacket calls = %d, want 2", len(calls))
	}
	if calls[0].cmd != packet.HeaderCALOGIN || calls[1].cmd != packet.HeaderCALOGIN {
		t.Fatalf("unexpected cmds: 0x%04x, 0x%04x", calls[0].cmd, calls[1].cmd)
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
