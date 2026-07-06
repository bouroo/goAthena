package nats

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// healthCheckTimeout bounds the readiness probe PING. Keep it short
// because readiness is called on every health-check request.
const healthCheckTimeout = 2 * time.Second

// natsChecker reports NATS connectivity for the readiness probe. It
// uses the lightweight FlushTimeout (PING) instead of an application
// message so the health check never disturbs production traffic.
type natsChecker struct {
	client *Client
}

// Name returns the dependency name reported in health output.
func (natsChecker) Name() string {
	return "nats"
}

// Check verifies NATS is reachable by flushing a PING within
// healthCheckTimeout (or the parent context's remaining deadline,
// whichever is shorter). Returns nil when the server is connected
// and responsive; an error otherwise.
func (c natsChecker) Check(ctx context.Context) error {
	if c.client == nil || c.client.Conn() == nil {
		return errors.New("nats connection is nil")
	}
	nc := c.client.Conn()
	if !nc.IsConnected() {
		return fmt.Errorf("nats not connected (status=%s)", nc.Status())
	}
	timeout := healthCheckTimeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining > 0 && remaining < timeout {
			timeout = remaining
		}
	}
	if err := nc.FlushTimeout(timeout); err != nil {
		return fmt.Errorf("nats flush: %w", err)
	}
	return nil
}
