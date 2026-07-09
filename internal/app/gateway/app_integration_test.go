//go:build integration

// Package gateway integration tests — these require a live NATS broker
// (and the upstream identity/zone services, exercised indirectly via
// the gateway composition root). The unit build tag is used for the
// rest of the gateway tests; these integration cases only run under
// `task test-integration`.
//
// TestRun_ShutdownOnContextCancel boots the full gateway composition
// root (telemetry, NATS subscriber, TCP/WS handlers) and asserts that
// Run returns cleanly when its context is cancelled mid-startup.
package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/config"
)

func TestRun_ShutdownOnContextCancel(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		App: config.AppConfig{
			Name:            "test",
			Environment:     "test",
			Host:            "127.0.0.1",
			Port:            8080,
			ShutdownTimeout: 15 * time.Second,
		},
		HTTP: config.HTTPConfig{
			Host:               "127.0.0.1",
			Port:               8080,
			ReadTimeout:        15 * time.Second,
			WriteTimeout:       15 * time.Second,
			IdleTimeout:        60 * time.Second,
			BodyLimit:          "1M",
			HealthProbeTimeout: 5 * time.Second,
		},
		GRPC: config.GRPCConfig{Host: "127.0.0.1", Port: 50051},
		DB: config.DBConfig{
			Driver:         "mariadb",
			Host:           "127.0.0.1",
			Port:           3306,
			Name:           "app",
			User:           "u",
			Password:       "p",
			SSLMode:        "false",
			MaxConns:       10,
			MaxIdleConns:   2,
			MaxConnIdle:    30 * time.Minute,
			MaxConnLife:    1 * time.Hour,
			ConnectTimeout: 5 * time.Second,
		},
		Valkey: config.ValkeyConfig{Host: "127.0.0.1", Port: 6379, DB: 0},
		NATS:   config.NATSConfig{URL: "nats://127.0.0.1:4222", ConnectTimeout: 5 * time.Second},
		OTel:   config.OTelConfig{Exporter: "none", ServiceName: "test", Sampling: 1.0},
		Log:    config.LogConfig{Level: "info", Format: "json"},
		Gateway: config.GatewayConfig{
			TCP:          config.TCPConfig{Addr: "127.0.0.1:16910"},
			WS:           config.WSConfig{Addr: "127.0.0.1:16911", Path: "/ws/"},
			Packetver:    20250604,
			IdentityAddr: "127.0.0.1:50051",
			ZoneAddr:     "127.0.0.1:50052",
			MapAddr:      "127.0.0.1:5121",
		},
		Assets: config.AssetsConfig{
			Enabled:    false,
			GRFDir:     "./data/grf",
			MaxCacheMB: 256,
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg) }()

	// Allow boot to complete (telemetry + NATS subscriber + TCP/WS).
	time.Sleep(200 * time.Millisecond)

	cancel()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return within 10s after context cancel")
	}
}
