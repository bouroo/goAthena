// Package valkey opens the process-wide Valkey (Redis-compatible) client used
// for session keys and hot cache. valkey-go is a non-blocking, pipelined client.
package valkey

import (
	"context"
	"fmt"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/bouroo/goAthena/internal/config"
)

// New dials a Valkey client for the configured address. The client is safe for
// concurrent use; a single process-wide instance is the intended scope.
func New(ctx context.Context, cfg config.ValkeyConfig) (valkey.Client, error) {
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	c, err := valkey.NewClient(valkey.ClientOption{
		InitAddress: []string{addr},
		Password:    cfg.Password,
		SelectDB:    cfg.DB,
	})
	if err != nil {
		return nil, fmt.Errorf("valkey dial %s: %w", addr, err)
	}
	// Fail fast if the broker is unreachable so startup is honest.
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := c.Do(pingCtx, c.B().Ping().Build()).Error(); err != nil {
		c.Close()
		return nil, fmt.Errorf("valkey ping %s: %w", addr, err)
	}
	return c, nil
}

// Ping verifies the broker responds within a short deadline.
func Ping(ctx context.Context, c valkey.Client) error {
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := c.Do(pingCtx, c.B().Ping().Build()).Error(); err != nil {
		return fmt.Errorf("valkey ping: %w", err)
	}
	return nil
}
