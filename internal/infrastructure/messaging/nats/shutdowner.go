package nats

import (
	"context"
	"sync"
)

// Shutdowner adapts a *Client to samber/do's
// ShutdownerWithContextAndError interface so the DI container owns the
// connection lifecycle. This guarantees the connection is drained by
// injector.Shutdown() even when the application-level shutdown never
// runs (partial startup failure, tests that call Build then
// injector.Shutdown).
//
// Drain is guarded by sync.Once so repeated shutdown calls are
// idempotent. The wrapped Close is itself safe to call twice, but the
// guard keeps shutdown deterministic and silent in the happy path
// where both the application and the DI container close the same
// connection.
type Shutdowner struct {
	client *Client
	once   sync.Once
	err    error
}

// NewShutdowner wraps client so the DI container can close it.
func NewShutdowner(client *Client) *Shutdowner {
	return &Shutdowner{client: client}
}

// Shutdown implements do.ShutdownerWithContextAndError. It calls
// Client.Close (which drains pending publishes) and caches the first
// error for inspection.
func (s *Shutdowner) Shutdown(_ context.Context) error {
	s.once.Do(func() {
		if s.client == nil {
			return
		}
		s.err = s.client.Close()
	})
	return s.err
}
