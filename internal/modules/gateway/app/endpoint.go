package app

import "context"

// MapScopedEndpointResolver resolves the map-server endpoint for a specific
// map.
//
// It is the port that lets a multi-process deployment tell each client where
// its character's map actually is. A character whose last map is served by
// another shard must be handed that shard's address, not the one this
// process happens to be: the client dials the address it is given, and a
// shard that does not serve the map would admit the character into the
// wrong world.
//
// Implementations must be safe for concurrent use.
type MapScopedEndpointResolver interface {
	// ResolveMapEndpointFor returns the endpoint serving mapName.
	ResolveMapEndpointFor(ctx context.Context, mapName string) (MapEndpoint, error)
}

// MapEndpoint is a map-server address as the client will dial it.
//
// The Host is resolved to an IPv4 when the endpoint is built, because the
// handshake carries an IPv4 and not a hostname.
type MapEndpoint struct {
	// Host is the map server's address as the client will dial it.
	Host string
	// Port is the map server's port.
	Port uint16
}
