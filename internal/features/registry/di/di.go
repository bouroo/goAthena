// Package di wires the registry feature (D22) into the DI container.
//
// The registry depends on valkeygo.Client (registered upstream by
// internal/infrastructure/messaging/valkey.Register). It exposes a
// single domain.Registry value that the zone service consumes during
// map-enter / map-leave and during the transit handshake.
package di

import (
	"fmt"

	"github.com/samber/do/v2"
	valkeygo "github.com/valkey-io/valkey-go"

	"github.com/bouroo/goAthena/internal/features/registry/domain"
	"github.com/bouroo/goAthena/internal/features/registry/service"
)

// Register wires the registry into the DI container. It depends on
// valkeygo.Client being already registered (by the valkey infra
// Register).
func Register(c do.Injector) error {
	client, err := do.Invoke[valkeygo.Client](c)
	if err != nil {
		return fmt.Errorf("resolve valkey client: %w", err)
	}

	store := service.NewValkeyStore(client)
	registry := service.NewRegistry(store)

	do.ProvideValue(c, store)
	do.ProvideValue(c, registry)

	return nil
}

// ProvideRegistry resolves the wired Registry. Other features (notably
// zone) call this to look up character locations and to arbitrate the
// zone lock without depending on the valkey client directly.
func ProvideRegistry(c do.Injector) (domain.Registry, error) {
	r, err := do.Invoke[domain.Registry](c)
	if err != nil {
		return nil, fmt.Errorf("resolve registry: %w", err)
	}
	return r, nil
}
