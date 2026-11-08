// Package gateway is the ingress module root. It builds the login and char TCP
// listeners, resolving the account Authenticator, character CharService, and
// SessionStore ports from the injector.
//
// The map endpoint the char server advertises is a port of this module
// (MapEndpointResolver); the adapter that fills it — an Agones allocation, or
// the static configuration — lives outside, so the gateway stays ignorant of
// Agones.
package gateway

import (
	"context"
	"fmt"

	"github.com/samber/do/v2"

	"github.com/bouroo/goAthena/internal/config"
)

// MapEndpoint is the map-server address the char server hands the client.
//
// It is a port of this module: the shape is what the gateway needs, and
// the adapter that fills it lives outside the module.
type MapEndpoint struct {
	// Host is the map server's address as the client will dial it. A
	// hostname is resolved to an IPv4 when the endpoint is built.
	Host string
	// Port is the map server's port.
	Port uint16
}

// MapEndpointResolver resolves the map-server endpoint the char server
// advertises.
//
// Implementations must be safe for concurrent use.
type MapEndpointResolver interface {
	// ResolveMapEndpoint returns the endpoint to advertise.
	ResolveMapEndpoint(ctx context.Context) (MapEndpoint, error)
}

// StaticMapEndpoint is the configured endpoint: gateway.map_host and
// gateway.map_port.
//
// It is correct wherever the configured address is dialable by the client
// — a workstation, or a client inside the cluster reaching a Service.
type StaticMapEndpoint struct {
	Endpoint MapEndpoint
}

// ResolveMapEndpoint returns the configured endpoint.
func (s StaticMapEndpoint) ResolveMapEndpoint(context.Context) (MapEndpoint, error) {
	return s.Endpoint, nil
}

// RegisterMapEndpoint provisions the MapEndpointResolver the char server
// advertises.
//
// The composition root calls this with the resolver the deployment needs:
// an Agones allocation when the process runs as a fleet GameServer (the
// node address and the host port Agones bound are runtime state that no
// configuration can express), and the configured endpoint otherwise.
//
// The resolver is optional. Without one the char server advertises the
// configured endpoint directly, which is what a local run wants.
func RegisterMapEndpoint(inj do.Injector, resolver MapEndpointResolver) {
	if resolver != nil {
		do.ProvideValue(inj, resolver)
	}
}

// resolveMapEndpoint returns the endpoint to advertise: the injected
// resolver's answer when one is registered, and the configured endpoint
// otherwise.
func resolveMapEndpoint(ctx context.Context, inj do.Injector, cfg config.Config) (MapEndpoint, error) {
	resolver, err := do.Invoke[MapEndpointResolver](inj)
	if err != nil {
		// No resolver registered: the configured endpoint is the answer.
		return MapEndpoint{
			Host: cfg.Gateway.MapHost,
			Port: uint16(cfg.Gateway.MapPort), //nolint:gosec // G115: MapPort operator-set (default 5121).
		}, nil
	}
	endpoint, err := resolver.ResolveMapEndpoint(ctx)
	if err != nil {
		return MapEndpoint{}, fmt.Errorf("resolve map endpoint: %w", err)
	}
	if endpoint.Host == "" {
		return MapEndpoint{}, fmt.Errorf("resolve map endpoint: host is empty")
	}
	if endpoint.Port == 0 {
		return MapEndpoint{}, fmt.Errorf("resolve map endpoint: port is zero")
	}
	return endpoint, nil
}
