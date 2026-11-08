package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/bouroo/goAthena/internal/infrastructure/agones"
	"github.com/bouroo/goAthena/internal/modules/gateway"
)

// AgonesMapEndpoint adapts the Agones allocation to the gateway's
// MapEndpointResolver.
//
// Inside a fleet, the map server's client-facing address is runtime state:
// Agones assigns the node's address and binds the host port, and neither is
// knowable from configuration — the pod's IP is assigned after the manifest
// is written. A shard that advertises gateway.map_host instead would tell
// the client to dial an address that is not this shard's.
//
// The allocation is read once, at build time, and cached: the node address
// and the bound port do not change over a GameServer's life.
type AgonesMapEndpoint struct {
	// Alloctor reads the allocation from the Agones sidecar.
	Alloctor agones.Alloctor
	// Log records the resolved endpoint once.
	Log *slog.Logger

	endpoint gateway.MapEndpoint
}

// NewAgonesMapEndpoint reads the allocation and returns the resolver that
// advertises it.
//
// It fails when the sidecar reports no usable endpoint: a fleet pod that
// cannot say where it is must not start, because every client it hands a
// character to would be sent to an unroutable address.
func NewAgonesMapEndpoint(ctx context.Context, alloctor agones.Alloctor, log *slog.Logger) (*AgonesMapEndpoint, error) {
	allocation, err := alloctor.Allocate(ctx)
	if err != nil {
		return nil, fmt.Errorf("agones allocation: %w", err)
	}
	endpoint := gateway.MapEndpoint{
		Host: allocation.Address.String(),
		Port: allocation.Port,
	}
	log.Info("agones: advertising the allocated map endpoint",
		"host", endpoint.Host, "port", endpoint.Port)
	return &AgonesMapEndpoint{Alloctor: alloctor, Log: log, endpoint: endpoint}, nil
}

// ResolveMapEndpoint returns the cached allocation.
func (a *AgonesMapEndpoint) ResolveMapEndpoint(context.Context) (gateway.MapEndpoint, error) {
	return a.endpoint, nil
}
