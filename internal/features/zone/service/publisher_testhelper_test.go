//go:build unit

package service

import (
	"context"

	"google.golang.org/protobuf/proto"

	"github.com/bouroo/goAthena/internal/features/zone/domain"
)

// nopPublisher is a no-op domain.Publisher used by unit tests in this
// package that do not exercise the broadcast path. It satisfies
// domain.Publisher so NewTickLoop's signature is type-checked at the
// call site. Tests that DO need to assert on publish calls use the
// generated mock.Publisher from internal/features/zone/domain/mock.
type nopPublisher struct{}

// PublishEvent discards the event and returns nil. Used by tick,
// physics, and pathfinding tests where the broadcast is incidental.
func (nopPublisher) PublishEvent(_ context.Context, _ string, _ proto.Message) error {
	return nil
}

// compile-time assertion: nopPublisher must satisfy the same port the
// production NewNATSPublisher satisfies. If domain.Publisher ever
// drifts, this test helper stops compiling before the rest of the test
// suite does.
var _ domain.Publisher = nopPublisher{}
