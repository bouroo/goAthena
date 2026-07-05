//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/rs/zerolog"

	"github.com/bouroo/goAthena/internal/features/gateway/domain"
)

func TestLoggingHandler_LogsAndReturnsNil(t *testing.T) {
	logger := zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.DebugLevel)
	h := NewLoggingHandler(logger)

	info := domain.ConnectionInfo{ID: 99, RemoteIP: "10.0.0.1:1234"}
	frame := []byte{0x64, 0x00, 0xff, 0xff, 0xff, 0xff}

	if err := h.HandlePacket(context.Background(), info, 0x0064, frame); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
}

func TestLoggingHandler_NilContextSafe(t *testing.T) {
	logger := zerolog.New(zerolog.NewTestWriter(t)).Level(zerolog.Disabled)
	h := NewLoggingHandler(logger)
	if err := h.HandlePacket(context.TODO(), domain.ConnectionInfo{}, 0x0001, []byte{0x01, 0x00}); err != nil {
		t.Fatalf("HandlePacket err = %v, want nil", err)
	}
}
