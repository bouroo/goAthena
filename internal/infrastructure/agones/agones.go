// Package agones adapts the Agones GameServer SDK sidecar to a narrow port
// (M13). The monolith stays Agones-ignorant everywhere else: when the
// sidecar's gRPC port is present in the environment (i.e. the process runs
// as a fleet GameServer), the app marks itself Ready once the listeners are
// up, streams health pings while serving, and declares Shutdown when the
// drain begins — Agones then stops sending allocations and reschedules.
// Without the sidecar (hobbyist Podman / bare metal) everything is a no-op.
package agones

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"agones.dev/agones/pkg/sdk"
)

// Port is the lifecycle surface the app needs from the sidecar.
type Port interface {
	// Ready marks the GameServer ready to receive player allocations.
	Ready() error
	// Health streams a liveness ping (Agones evicts on missed pings when its
	// health check is enabled in the GameServer spec).
	Health() error
	// ShutDown marks the GameServer as shutting down.
	ShutDown() error
	// Close releases the sidecar connection.
	Close() error
}

// DefaultHealthInterval bounds how often Health pings stream. Agones's
// default eviction threshold (when the spec enables the health check) is a
// 25s period with tolerance for missed pings — 5s pings sit safely under it.
const DefaultHealthInterval = 5 * time.Second

// Enabled reports whether the process runs inside an Agones GameServer: the
// sidecar injects AGONES_SDK_GRPC_PORT into the pod's environment.
func Enabled() bool {
	return SidecarPort() != ""
}

// SidecarPort returns the sidecar's gRPC port from the environment (""
// when absent).
func SidecarPort() string {
	return envOr("AGONES_SDK_GRPC_PORT", "")
}

// Sidecar is the real Port, backed by the generated SDK gRPC client.
type Sidecar struct {
	client  sdk.SDKClient
	conn    *grpc.ClientConn
	health  sdk.SDK_HealthClient
	stopped bool
}

// New dials the sidecar. It fails only when Agones is expected (the
// environment says the sidecar exists) but unreachable — a fleet pod that
// cannot reach its sidecar cannot serve, so the boot must fail and Agones
// will reschedule it.
func New() (*Sidecar, error) {
	addr := fmt.Sprintf("localhost:%s", SidecarPort())
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("agones: dial sidecar %s: %w", addr, err)
	}
	return &Sidecar{client: sdk.NewSDKClient(conn), conn: conn}, nil
}

// Ready marks the GameServer ready for allocations.
func (s *Sidecar) Ready() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := s.client.Ready(ctx, &sdk.Empty{}); err != nil {
		return fmt.Errorf("agones: ready: %w", err)
	}
	return nil
}

// Health opens the health stream once and streams a ping per call. The
// stream outlives individual pings; a dead stream reopens on the next ping.
func (s *Sidecar) Health() error {
	if s.health == nil {
		hc, err := s.client.Health(context.Background())
		if err != nil {
			return fmt.Errorf("agones: health stream: %w", err)
		}
		s.health = hc
	}
	if err := s.health.Send(&sdk.Empty{}); err != nil {
		s.health = nil // reopen on the next ping
		return fmt.Errorf("agones: health ping: %w", err)
	}
	return nil
}

// ShutDown marks the GameServer as shutting down; Agones stops allocations.
func (s *Sidecar) ShutDown() error {
	if s.stopped {
		return nil
	}
	s.stopped = true
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := s.client.Shutdown(ctx, &sdk.Empty{}); err != nil {
		return fmt.Errorf("agones: shutdown: %w", err)
	}
	return nil
}

// Close releases the sidecar connection.
func (s *Sidecar) Close() error {
	if err := s.conn.Close(); err != nil {
		return fmt.Errorf("agones: close conn: %w", err)
	}
	return nil
}

// Noop is the Agones-absent Port: local Podman / bare-metal runs are no-ops.
type Noop struct{}

// Ready is a no-op.
func (Noop) Ready() error { return nil }

// Health is a no-op.
func (Noop) Health() error { return nil }

// ShutDown is a no-op.
func (Noop) ShutDown() error { return nil }

// Close is a no-op.
func (Noop) Close() error { return nil }

// RunHealthLoop streams Health pings every interval until ctx is done. A
// failed ping is logged, not fatal: Agones's own eviction policy (configured
// on the GameServer spec) decides what missed pings mean, and a transient
// sidecar hiccup should not take the gameserver down from inside.
func RunHealthLoop(ctx context.Context, log *slog.Logger, p Port, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := p.Health(); err != nil {
					log.Warn("agones: health ping failed", "err", err)
				}
			}
		}
	}()
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return fallback
}
