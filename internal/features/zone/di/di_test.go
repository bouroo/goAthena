//go:build unit

package di_test

import (
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/samber/do/v2"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/features/zone/di"
	"github.com/bouroo/goAthena/internal/infrastructure/agones"
	natsinfra "github.com/bouroo/goAthena/internal/infrastructure/messaging/nats"
)

func TestRegister_ReturnsNil(t *testing.T) {
	t.Parallel()
	c := do.New()
	cfg := &config.Config{
		App: config.AppConfig{
			Name: "test", Environment: "test", Host: "127.0.0.1", Port: 8080,
			ShutdownTimeout: 15 * time.Second,
		},
		HTTP: config.HTTPConfig{
			Host: "127.0.0.1", Port: 8080,
			ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second,
			IdleTimeout: 60 * time.Second, BodyLimit: "1M",
			HealthProbeTimeout: 5 * time.Second,
		},
		GRPC: config.GRPCConfig{Host: "127.0.0.1", Port: 50051},
		DB: config.DBConfig{
			Driver: "mariadb", Host: "127.0.0.1", Port: 3306, Name: "x", User: "u", Password: "p",
			SSLMode: "false", MaxConns: 10, MaxIdleConns: 2,
			MaxConnIdle: 30 * time.Minute, MaxConnLife: time.Hour,
			ConnectTimeout: 5 * time.Second,
		},
		Valkey: config.ValkeyConfig{
			Host: "127.0.0.1", Port: 6379, DB: 0,
			ConnectTimeout: 5 * time.Second,
		},
		NATS: config.NATSConfig{URL: "nats://127.0.0.1:4222", ConnectTimeout: 5 * time.Second},
		Zone: config.ZoneConfig{
			TickRate: 50 * time.Millisecond, MapDir: "./data/maps",
			DefaultMap: "prontera", MoveSpeed: 150,
			ShutdownGrace: 30 * time.Second,
		},
		OTel: config.OTelConfig{Exporter: "none", ServiceName: "test", Sampling: 1.0},
		Log:  config.LogConfig{Level: "info", Format: "json"},
	}
	do.ProvideValue(c, cfg)
	l := zerolog.Nop()
	do.ProvideValue(c, &l)
	var ag agones.Lifecycle = agones.NewLocal(&l)
	do.ProvideValue(c, ag)

	// The zone DI requires *natsinfra.Client because it wires a NATS
	// Publisher; this unit test does not exercise the broadcast path,
	// so a nil client is fine — the natsPublisher adapter short-circuits
	// with a wrapped error if Publish is ever called with c==nil. No tick
	// loop is started in this test, so no publish ever fires.
	var nc *natsinfra.Client
	do.ProvideValue(c, nc)

	require.NoError(t, di.Register(c))
}
