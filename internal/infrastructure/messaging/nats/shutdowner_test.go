//go:build unit

package nats_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	natsinfra "github.com/bouroo/goAthena/internal/infrastructure/messaging/nats"
)

func TestShutdowner_NilClientIsSafe(t *testing.T) {
	t.Parallel()

	s := natsinfra.NewShutdowner(nil)
	require.NotNil(t, s, "shutdowner constructor must return non-nil")

	assert.NoError(t, s.Shutdown(context.Background()), "shutdown with nil client must return nil")
}

func TestShutdowner_Idempotent(t *testing.T) {
	t.Parallel()

	s := natsinfra.NewShutdowner(nil)
	require.NotNil(t, s)

	assert.NoError(t, s.Shutdown(context.Background()), "first shutdown on nil client must return nil")
	assert.NoError(t, s.Shutdown(context.Background()), "second shutdown must remain a no-op")
}
