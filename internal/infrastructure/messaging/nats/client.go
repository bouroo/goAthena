// Package nats wires the NATS pub/sub client into the DI container
// for inter-service communication (transit, social, broadcast).
package nats

import (
	"context"
	"fmt"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/rs/zerolog"

	"github.com/bouroo/goAthena/internal/config"
)

// Default tuning constants. The values match the Scout findings
// (D21, §1.4). They are constants (not configurable knobs) because
// changing them mid-flight risks breaking the reconnect semantics
// other services rely on.
const (
	// defaultConnectTimeout is the fallback for an unset or zero
	// cfg.NATS.ConnectTimeout.
	defaultConnectTimeout = 5 * time.Second

	// defaultReconnectWait is the backoff between reconnect attempts
	// after a dropped connection.
	defaultReconnectWait = 2 * time.Second

	// defaultPingInterval is the keep-alive PING period. Two outstanding
	// PINGs before the server considers the connection dead.
	defaultPingInterval = 30 * time.Second

	// defaultMaxPingsOutstanding caps the number of in-flight PINGs before
	// the client declares the connection dead and triggers reconnect.
	defaultMaxPingsOutstanding = 2

	// defaultRequestTimeout is the timeout for PublishRequest when the
	// caller does not set its own. It must be long enough to cover a
	// cross-zone transit handshake end-to-end.
	defaultRequestTimeout = 2 * time.Second
)

// Client wraps a NATS connection for type-safe pub/sub. The wrapper is
// a thin adapter — the underlying *nats.Conn is goroutine-safe (see
// nats.go docs), so Client itself does not need its own locking.
//
// JetStream support is intentionally absent in Phase 5: hot paths
// use core NATS for synchronous request/reply and pub/sub fan-out, and
// JetStream persistence is deferred until we observe message loss that
// requires replay (see Scout findings §1.1).
type Client struct {
	nc     *natsgo.Conn
	logger *zerolog.Logger
}

// New creates a new NATS client and connects to the server. It returns
// an error if the connection cannot be established within timeout, or
// if the server does not respond to a flush within the same window
// (liveness ping). The returned Client must be closed with Close.
func New(ctx context.Context, url string, timeout time.Duration, logger *zerolog.Logger) (*Client, error) {
	if logger == nil {
		return nil, fmt.Errorf("nats: logger is nil")
	}
	if url == "" {
		return nil, fmt.Errorf("nats: url is empty")
	}
	if timeout <= 0 {
		timeout = defaultConnectTimeout
	}

	nc, err := natsgo.Connect(url,
		natsgo.Name("goathena"),
		natsgo.Timeout(timeout),
		natsgo.ReconnectWait(defaultReconnectWait),
		natsgo.MaxReconnects(-1),
		natsgo.PingInterval(defaultPingInterval),
		natsgo.MaxPingsOutstanding(defaultMaxPingsOutstanding),
		natsgo.ReconnectHandler(func(nc *natsgo.Conn) {
			logger.Info().Str("url", nc.ConnectedUrl()).Msg("nats reconnected")
		}),
		natsgo.DisconnectErrHandler(func(_ *natsgo.Conn, err error) {
			logger.Warn().Err(err).Msg("nats disconnected")
		}),
		natsgo.ErrorHandler(func(_ *natsgo.Conn, sub *natsgo.Subscription, err error) {
			evt := logger.Error().Err(err)
			if sub != nil {
				evt = evt.Str("subject", sub.Subject).Str("queue", sub.Queue)
			}
			evt.Msg("nats async error")
		}),
		natsgo.RetryOnFailedConnect(true),
	)
	if err != nil {
		return nil, fmt.Errorf("nats connect %q: %w", url, err)
	}

	flushTimeout := timeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 && remaining < flushTimeout {
			flushTimeout = remaining
		}
	}
	if err := nc.FlushTimeout(flushTimeout); err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats flush %q: %w", url, err)
	}

	return &Client{nc: nc, logger: logger}, nil
}

// NewClientFromConfig is the DI-friendly constructor used by Register.
// It mirrors NewClient signatures in other infrastructure packages
// (e.g. valkey.NewClient).
func NewClientFromConfig(ctx context.Context, cfg *config.Config, logger *zerolog.Logger) (*Client, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nats: config is nil")
	}
	timeout := cfg.NATS.ConnectTimeout
	if timeout <= 0 {
		timeout = defaultConnectTimeout
	}
	return New(ctx, cfg.NATS.URL, timeout, logger)
}

// Conn returns the underlying *nats.Conn for callers that need direct
// access (e.g. JetStream context, raw Conn.Stats). New code should
// prefer the typed methods on Client.
func (c *Client) Conn() *natsgo.Conn {
	if c == nil {
		return nil
	}
	return c.nc
}

// Publish publishes data to a subject (fire-and-forget). It respects
// ctx.Done() so a cancelled context aborts the publish without blocking.
func (c *Client) Publish(ctx context.Context, subject string, data []byte) error {
	if c == nil || c.nc == nil {
		return fmt.Errorf("nats: publish %q: client is nil", subject)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("nats: publish %q: %w", subject, err)
	}
	if err := c.nc.Publish(subject, data); err != nil {
		return fmt.Errorf("nats: publish %q: %w", subject, err)
	}
	return nil
}

// PublishRequest publishes a request and waits for a reply. The reply
// timeout is taken from ctx if it carries a deadline; otherwise the
// default (defaultRequestTimeout) is used. It returns the reply payload
// or an error wrapping nats.ErrTimeout on timeout.
func (c *Client) PublishRequest(ctx context.Context, subject string, data []byte) ([]byte, error) {
	if c == nil || c.nc == nil {
		return nil, fmt.Errorf("nats: request %q: client is nil", subject)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("nats: request %q: %w", subject, err)
	}
	timeout := defaultRequestTimeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 && remaining < timeout {
			timeout = remaining
		}
	}
	msg, err := c.nc.Request(subject, data, timeout)
	if err != nil {
		return nil, fmt.Errorf("nats: request %q: %w", subject, err)
	}
	if msg == nil {
		return nil, fmt.Errorf("nats: request %q: nil reply", subject)
	}
	return msg.Data, nil
}

// Subscribe subscribes to a subject with a handler. The handler runs on
// the NATS client's internal goroutines — keep it non-blocking.
func (c *Client) Subscribe(subject string, handler func(msg *natsgo.Msg)) (*natsgo.Subscription, error) {
	if c == nil || c.nc == nil {
		return nil, fmt.Errorf("nats: subscribe %q: client is nil", subject)
	}
	sub, err := c.nc.Subscribe(subject, handler)
	if err != nil {
		return nil, fmt.Errorf("nats: subscribe %q: %w", subject, err)
	}
	return sub, nil
}

// QueueSubscribe subscribes to a queue group (competing consumers).
// Only one subscriber in the queue receives each message.
func (c *Client) QueueSubscribe(subject, queue string, handler func(msg *natsgo.Msg)) (*natsgo.Subscription, error) {
	if c == nil || c.nc == nil {
		return nil, fmt.Errorf("nats: queue subscribe %q queue %q: client is nil", subject, queue)
	}
	sub, err := c.nc.QueueSubscribe(subject, queue, handler)
	if err != nil {
		return nil, fmt.Errorf("nats: queue subscribe %q queue %q: %w", subject, queue, err)
	}
	return sub, nil
}

// Close drains pending publishes, unsubscribes, and closes the
// connection. Drain (not raw Close) is used so in-flight messages
// reach the server before the connection goes away. After Close the
// Client must not be reused.
func (c *Client) Close() error {
	if c == nil || c.nc == nil {
		return nil
	}
	if err := c.nc.Drain(); err != nil {
		return fmt.Errorf("nats: drain: %w", err)
	}
	return nil
}

// IsConnected reports whether the underlying connection is currently in
// the CONNECTED state. It does not guarantee that a specific publish or
// subscribe will succeed.
func (c *Client) IsConnected() bool {
	if c == nil || c.nc == nil {
		return false
	}
	return c.nc.IsConnected()
}
