package di

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/bouroo/goAthena/internal/features/zone/domain"
	natsinfra "github.com/bouroo/goAthena/internal/infrastructure/messaging/nats"
)

// natsPublisher adapts the infrastructure/messaging/nats.Client to the
// domain.Publisher port. It mirrors the adapter pattern used by
// internal/features/transit/di/di.go (natsMessenger): keeping the
// transport concern out of the service layer preserves clean-architecture
// layering (service depends on domain, not on natsinfra).
type natsPublisher struct {
	client *natsinfra.Client
}

// NewNATSPublisher returns a domain.Publisher that marshals events as
// protobuf and publishes them to the per-map zone-event subject. The
// returned publisher is safe to use across goroutines — the underlying
// natsinfra.Client wraps a *nats.Conn, which is goroutine-safe per the
// nats.go docs.
func NewNATSPublisher(client *natsinfra.Client) domain.Publisher {
	return &natsPublisher{client: client}
}

// PublishEvent marshals event as protobuf and publishes it to the map's
// zone-event subject. The mapName selects the subject partition (sanitized
// by natsinfra.ZoneEventSubject), so subscribing gateways only receive
// events for the maps they serve.
//
// Errors at any stage are wrapped with zone-prefixed context so the caller
// can identify the failure mode without inspecting error strings.
func (p *natsPublisher) PublishEvent(ctx context.Context, mapName string, event proto.Message) error {
	if p.client == nil {
		return fmt.Errorf("zone: publish event: nats client is nil")
	}
	if event == nil {
		return fmt.Errorf("zone: publish event: nil event for map %q", mapName)
	}
	data, err := proto.Marshal(event)
	if err != nil {
		return fmt.Errorf("zone: marshal event for map %q: %w", mapName, err)
	}
	subject := natsinfra.ZoneEventSubject(mapName)
	if err := p.client.Publish(ctx, subject, data); err != nil {
		return fmt.Errorf("zone: publish to %q: %w", subject, err)
	}
	return nil
}
