//go:build unit

package nats

import (
	"context"
	"testing"
	"time"

	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/shared/telemetry"
)

func TestNatsChecker_Name(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "nats", natsChecker{}.Name())
}

func TestNatsChecker_NilClient(t *testing.T) {
	t.Parallel()

	c := natsChecker{}
	err := c.Check(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestRegister_ConfigResolveFails(t *testing.T) {
	t.Parallel()

	injector := do.New()
	err := Register(context.Background(), injector)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolve config")
}

func TestRegister_HealthRegistryResolveFails(t *testing.T) {
	t.Parallel()

	injector := do.New()
	do.ProvideValue(injector, &config.Config{
		NATS: config.NATSConfig{URL: "nats://127.0.0.1:4222", ConnectTimeout: time.Second},
	})

	err := Register(context.Background(), injector)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolve health registry")
}

func TestRegister_LoggerResolveFails(t *testing.T) {
	t.Parallel()

	injector := do.New()
	do.ProvideValue(injector, &config.Config{
		NATS: config.NATSConfig{URL: "nats://127.0.0.1:4222", ConnectTimeout: time.Second},
	})
	do.ProvideValue(injector, telemetry.NewRegistry())

	err := Register(context.Background(), injector)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolve logger")
}

func TestRegister_UnreachableClient(t *testing.T) {
	t.Parallel()

	injector := do.New()
	do.ProvideValue(injector, &config.Config{
		NATS: config.NATSConfig{
			URL:            "nats://192.0.2.1:4222", // TEST-NET-1
			ConnectTimeout: 500 * time.Millisecond,
		},
	})
	do.ProvideValue(injector, telemetry.NewRegistry())

	err := Register(context.Background(), injector)
	require.Error(t, err, "register must fail when nats is unreachable")
}

func TestProvideClient_ConfigResolveFails(t *testing.T) {
	t.Parallel()

	injector := do.New()
	client, err := ProvideClient(injector)
	require.Error(t, err)
	assert.Nil(t, client)
	assert.Contains(t, err.Error(), "resolve config")
}

func TestProvideClient_LoggerResolveFails(t *testing.T) {
	t.Parallel()

	injector := do.New()
	do.ProvideValue(injector, &config.Config{
		NATS: config.NATSConfig{URL: "nats://127.0.0.1:4222", ConnectTimeout: time.Second},
	})
	client, err := ProvideClient(injector)
	require.Error(t, err)
	assert.Nil(t, client)
	assert.Contains(t, err.Error(), "resolve logger")
}
