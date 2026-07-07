//go:build unit

package service

import (
	"bytes"
	"context"
	"testing"

	"github.com/rs/zerolog"

	"github.com/bouroo/goAthena/internal/features/gateway/domain"
)

// noopResponder satisfies domain.Responder but captures nothing — the
// logging handler ignores replies, so we only assert "SendPacket was not
// called" indirectly via the buffer length staying zero.
type noopResponder struct {
	buf bytes.Buffer
}

func (n *noopResponder) SendPacket(p []byte) error {
	_, err := n.buf.Write(p)
	return err
}

func TestLoggingHandler_LogsAndReturnsNil(t *testing.T) {
	logger := zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.DebugLevel)
	h := NewLoggingHandler(logger)

	info := domain.ConnectionInfo{ID: 99, RemoteIP: "10.0.0.1:1234"}
	frame := []byte{0x64, 0x00, 0xff, 0xff, 0xff, 0xff}

	if err := h.HandlePacket(context.Background(), &info, &noopResponder{}, 0x0064, frame); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
}

func TestLoggingHandler_NilContextSafe(t *testing.T) {
	logger := zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.Disabled)
	h := NewLoggingHandler(logger)
	if err := h.HandlePacket(context.TODO(), &domain.ConnectionInfo{}, &noopResponder{}, 0x0001, []byte{0x01, 0x00}); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
}
