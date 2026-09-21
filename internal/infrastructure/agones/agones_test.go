package agones

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func TestEnabledFollowsSidecarEnv(t *testing.T) {
	t.Setenv("AGONES_SDK_GRPC_PORT", "9357")
	if !Enabled() {
		t.Fatal("Enabled = false with AGONES_SDK_GRPC_PORT set, want true")
	}
	if got := SidecarPort(); got != "9357" {
		t.Fatalf("SidecarPort = %q, want 9357", got)
	}
	t.Setenv("AGONES_SDK_GRPC_PORT", "")
	if Enabled() {
		t.Fatal("Enabled = true with the sidecar port unset, want false")
	}
}

// countingPort tallies Health pings so RunHealthLoop's cadence and teardown
// are observable without a sidecar.
type countingPort struct {
	pings atomic.Int64
}

func (c *countingPort) Ready() error    { return nil }
func (c *countingPort) Health() error   { c.pings.Add(1); return nil }
func (c *countingPort) ShutDown() error { return nil }
func (c *countingPort) Close() error    { return nil }

func TestRunHealthLoopPingsUntilContextDone(t *testing.T) {
	p := &countingPort{}
	ctx, cancel := context.WithCancel(context.Background())
	RunHealthLoop(ctx, discardLogger(), p, 10*time.Millisecond)

	// A few intervals must produce pings.
	deadline := time.Now().Add(2 * time.Second)
	for p.pings.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := p.pings.Load(); got < 3 {
		t.Fatalf("health pings = %d after ~30ms, want >= 3", got)
	}

	// Cancellation stops the loop: the count must freeze.
	cancel()
	frozen := p.pings.Load()
	time.Sleep(60 * time.Millisecond)
	if got := p.pings.Load(); got != frozen {
		t.Fatalf("health pings after cancel = %d, want frozen at %d", got, frozen)
	}
}

func TestNoopPortIsInert(t *testing.T) {
	var p Noop
	if err := p.Ready(); err != nil {
		t.Fatalf("noop Ready: %v", err)
	}
	if err := p.Health(); err != nil {
		t.Fatalf("noop Health: %v", err)
	}
	if err := p.ShutDown(); err != nil {
		t.Fatalf("noop ShutDown: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("noop Close: %v", err)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
