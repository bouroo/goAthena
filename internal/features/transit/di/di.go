// Package di wires the transit feature (D23) into the DI container.
//
// The transit service depends on:
//   - natsinfra.Client (registered by internal/infrastructure/messaging/nats.Register)
//   - a TransitConfigSource for this zone's TCP endpoint (the zone
//     service derives its own IP/Port from environment config and
//     exposes them via the static endpoint source below).
package di

import (
	"context"
	"fmt"

	natsgo "github.com/nats-io/nats.go"
	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/features/transit/domain"
	"github.com/bouroo/goAthena/internal/features/transit/service"
	natsinfra "github.com/bouroo/goAthena/internal/infrastructure/messaging/nats"
)

// Register wires the transit use cases into the DI container. It
// depends on *config.Config (for the zone id), natsinfra.Client, and
// (optionally) a TransitConfigSource being registered.
func Register(c do.Injector) error {
	cfg, err := do.Invoke[*config.Config](c)
	if err != nil {
		return fmt.Errorf("resolve config: %w", err)
	}
	nc, err := do.Invoke[*natsinfra.Client](c)
	if err != nil {
		return fmt.Errorf("resolve nats client: %w", err)
	}

	endpoints, err := do.Invoke[domain.TransitConfigSource](c)
	if err != nil {
		endpoints = service.NewStaticEndpointSource(domain.TransitEndpoint{
			IP:   cfg.GRPC.Host,
			Port: cfg.GRPC.Port,
		})
	}

	transitSvc := service.NewTransitService(
		newNatsMessenger(nc),
		service.NewRandomLoginIDGenerator(),
		endpoints,
	)

	do.ProvideValue(c, transitSvc)

	return nil
}

// ProvideTransitService resolves the wired TransitService. The zone
// service uses this to drive the source-side InitiateTransit call and
// to install the inbound subscription on startup.
func ProvideTransitService(c do.Injector) (domain.TransitService, error) {
	svc, err := do.Invoke[domain.TransitService](c)
	if err != nil {
		return nil, fmt.Errorf("resolve transit service: %w", err)
	}
	return svc, nil
}

// natsMessenger adapts the production *natsinfra.Client to the narrow
// domain.TransitMessenger port. It uses the underlying *nats.Conn for
// Subscribe because request/reply needs msg.Respond, which is not on
// the typed wrapper.
type natsMessenger struct {
	nc *natsinfra.Client
}

func newNatsMessenger(nc *natsinfra.Client) domain.TransitMessenger {
	return &natsMessenger{nc: nc}
}

func (n *natsMessenger) PublishRequest(ctx context.Context, subject string, data []byte) ([]byte, error) {
	reply, err := n.nc.PublishRequest(ctx, subject, data)
	if err != nil {
		return nil, fmt.Errorf("nats: transit publish request %q: %w", subject, err)
	}
	return reply, nil
}

func (n *natsMessenger) Subscribe(subject string, handler func(ctx context.Context, data []byte) ([]byte, error)) (domain.UnsubscribeFunc, error) {
	conn := n.nc.Conn()
	if conn == nil {
		return func() {}, fmt.Errorf("nats: subscribe %q: client is nil", subject)
	}
	sub, err := conn.Subscribe(subject, func(msg *natsgo.Msg) {
		reply, err := handler(context.Background(), msg.Data)
		if err != nil {
			return
		}
		if err := msg.Respond(reply); err != nil {
			_ = err
		}
	})
	if err != nil {
		return func() {}, fmt.Errorf("nats: subscribe %q: %w", subject, err)
	}
	return func() {
		if sub != nil {
			_ = sub.Unsubscribe()
		}
	}, nil
}
