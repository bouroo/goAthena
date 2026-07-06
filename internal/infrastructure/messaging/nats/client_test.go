//go:build unit

package nats_test

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	natsgo "github.com/nats-io/nats.go"

	natsinfra "github.com/bouroo/goAthena/internal/infrastructure/messaging/nats"
)

func newTestLogger(t *testing.T) *zerolog.Logger {
	t.Helper()
	l := zerolog.New(zerolog.NewTestWriter(t)).With().Timestamp().Logger()
	return &l
}

func TestNew_NilLogger(t *testing.T) {
	t.Parallel()

	client, err := natsinfra.New(context.Background(), "nats://127.0.0.1:4222", time.Second, nil)
	require.Error(t, err)
	assert.Nil(t, client)
	assert.Contains(t, err.Error(), "logger")
}

func TestNew_EmptyURL(t *testing.T) {
	t.Parallel()

	client, err := natsinfra.New(context.Background(), "", time.Second, newTestLogger(t))
	require.Error(t, err)
	assert.Nil(t, client)
	assert.Contains(t, err.Error(), "url")
}

func TestNew_Unreachable(t *testing.T) {
	t.Parallel()

	// TEST-NET-1 (RFC 5737) — guaranteed not to be reachable.
	client, err := natsinfra.New(context.Background(), "nats://192.0.2.1:4222", 500*time.Millisecond, newTestLogger(t))
	require.Error(t, err, "unreachable nats must error")
	assert.Nil(t, client, "client must be nil on connect failure")
	assert.Contains(t, err.Error(), "nats")
}

func TestNew_ZeroTimeoutFallsBackToDefault(t *testing.T) {
	t.Parallel()

	// Unreachable URL but zero timeout — should still error after the
	// default 5s window. We shrink the deadline by passing a parent
	// ctx so the test does not block for the full 5s.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	client, err := natsinfra.New(ctx, "nats://192.0.2.1:4222", 0, newTestLogger(t))
	require.Error(t, err)
	assert.Nil(t, client)
}

func TestClient_Publish_NilClient(t *testing.T) {
	t.Parallel()

	var c *natsinfra.Client
	err := c.Publish(context.Background(), "foo.bar", []byte("x"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil")
}

func TestClient_PublishRequest_NilClient(t *testing.T) {
	t.Parallel()

	var c *natsinfra.Client
	data, err := c.PublishRequest(context.Background(), "foo.bar", []byte("x"))
	require.Error(t, err)
	assert.Nil(t, data)
	assert.Contains(t, err.Error(), "nil")
}

func TestClient_Publish_CancelledContext(t *testing.T) {
	t.Parallel()

	// A nil client cannot read ctx.Err() before the nil-check, so the
	// preceding guard fires first. Verify that path is preserved.
	var c *natsinfra.Client
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := c.Publish(ctx, "foo.bar", []byte("x"))
	require.Error(t, err)
}

func TestClient_PublishRequest_CancelledContext(t *testing.T) {
	t.Parallel()

	var c *natsinfra.Client
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	data, err := c.PublishRequest(ctx, "foo.bar", []byte("x"))
	require.Error(t, err)
	assert.Nil(t, data)
}

func TestClient_Subscribe_NilClient(t *testing.T) {
	t.Parallel()

	var c *natsinfra.Client
	sub, err := c.Subscribe("foo.bar", func(_ *natsgo.Msg) {})
	require.Error(t, err)
	assert.Nil(t, sub)
	assert.Contains(t, err.Error(), "nil")
}

func TestClient_QueueSubscribe_NilClient(t *testing.T) {
	t.Parallel()

	var c *natsinfra.Client
	sub, err := c.QueueSubscribe("foo.bar", "queue-1", func(_ *natsgo.Msg) {})
	require.Error(t, err)
	assert.Nil(t, sub)
	assert.Contains(t, err.Error(), "nil")
}

func TestClient_Close_NilClient(t *testing.T) {
	t.Parallel()

	var c *natsinfra.Client
	assert.NoError(t, c.Close(), "close on nil client must be a no-op")
}

func TestClient_IsConnected_NilClient(t *testing.T) {
	t.Parallel()

	var c *natsinfra.Client
	assert.False(t, c.IsConnected())
}

func TestClient_Conn_NilClient(t *testing.T) {
	t.Parallel()

	var c *natsinfra.Client
	assert.Nil(t, c.Conn())
}
