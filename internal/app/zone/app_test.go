//go:build unit

package zone

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/config"
)

func TestRun_NilConfig(t *testing.T) {
	t.Parallel()

	err := Run(context.Background(), nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "config is nil")
}

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
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.NoError(t, Run(ctx, cfg))
}
