// Package nats opens the process-wide NATS connection used by the module
// extraction seam (M13). Modules reachable over the bus speak request/reply
// through it; the connection itself reconnects forever so a broker restart
// heals without a process restart — a call made while disconnected fails with
// a per-call error, matching the best-effort dependency posture compose uses
// for the DB and Valkey.
package nats

import (
	"fmt"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bouroo/goAthena/internal/config"
)

// DrainWait bounds how long Close lets in-flight request/reply exchanges
// finish before force-closing the socket.
const DrainWait = 2 * time.Second

// New dials the bus. The dial itself does not block on the broker:
// RetryOnFailedConnect queues the connection attempt in the background, so a
// zone process may boot before its bus (or before its economy host) is up,
// and calls fail individually until the route exists.
func New(cfg config.NATSConfig) (*nats.Conn, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("nats: empty url")
	}
	opts := []nats.Option{
		nats.Name("goathena"),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
	}
	if cfg.User != "" {
		opts = append(opts, nats.UserInfo(cfg.User, cfg.Password))
	}
	nc, err := nats.Connect(cfg.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("nats dial %s: %w", cfg.URL, err)
	}
	return nc, nil
}

// Close drains the connection — in-flight handler invocations and pending
// publishes finish within DrainWait — then releases it. nats.Conn.Drain is
// asynchronous, so the wait is ours: without it a shutdown path would return
// immediately and cut request handlers mid-DB-call. A hung drain degrades to
// a forced close after the bound.
func Close(nc *nats.Conn) error {
	if nc == nil {
		return nil
	}
	if err := nc.Drain(); err != nil {
		nc.Close()
		return fmt.Errorf("nats drain: %w", err)
	}
	deadline := time.Now().Add(DrainWait)
	for !nc.IsClosed() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !nc.IsClosed() {
		nc.Close()
		return fmt.Errorf("nats drain timed out after %s; connection force-closed", DrainWait)
	}
	return nil
}
