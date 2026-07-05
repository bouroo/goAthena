//go:build unit

package valkey

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

func TestValkeyChecker_Name(t *testing.T) {
	t.Parallel()
	c := valkeyChecker{}
	assert.Equal(t, "valkey", c.Name())
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
		Valkey: config.ValkeyConfig{Host: "127.0.0.1", Port: 6379, DB: 0},
	})

	err := Register(context.Background(), injector)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolve health registry")
}

func TestRegister_UnreachableClient(t *testing.T) {
	t.Parallel()

	injector := do.New()
	do.ProvideValue(injector, &config.Config{
		Valkey: config.ValkeyConfig{
			Host:           "192.0.2.1", // TEST-NET-1, unreachable
			Port:           6379,
			DB:             0,
			ConnectTimeout: 1 * time.Second,
		},
	})
	do.ProvideValue(injector, telemetry.NewRegistry())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := Register(ctx, injector)

	require.Error(t, err)
}
