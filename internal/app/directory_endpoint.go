package app

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net/netip"

	"github.com/bouroo/goAthena/internal/modules/gateway"
	transitdomain "github.com/bouroo/goAthena/internal/modules/transit/domain"
)

// DirectoryMapEndpoints adapts the transit MapDirectory to the gateway's
// MapScopedEndpointResolver.
//
// The directory answers "which zone serves this map"; the char server needs
// "what address does the client dial to reach that zone". The two differ
// in one important way: the directory's zero Zone means "this process" —
// no address — while the char server always has to hand the client
// something dialable.
//
// So a map the directory resolves locally is answered with this process's
// own advertised endpoint, and a map it resolves remotely is answered with
// the zone's address.
type DirectoryMapEndpoints struct {
	// Directory resolves a map name to the zone serving it.
	Directory transitdomain.MapDirectory
	// Local is the endpoint this process advertises for the maps it
	// serves itself.
	Local gateway.MapEndpoint
	// Log records the resolved endpoints.
	Log *slog.Logger
}

// ResolveMapEndpointFor returns the endpoint serving mapName.
func (d *DirectoryMapEndpoints) ResolveMapEndpointFor(_ context.Context, mapName string) (gateway.MapEndpoint, error) {
	if d.Directory == nil {
		return d.Local, nil
	}
	zone, err := d.Directory.Resolve(mapName)
	if err != nil {
		return gateway.MapEndpoint{}, fmt.Errorf("resolve map %q: %w", mapName, err)
	}
	if zone == (transitdomain.Zone{}) {
		// Local: the directory says this process serves the map.
		return d.Local, nil
	}
	host, err := zoneHost(zone)
	if err != nil {
		return gateway.MapEndpoint{}, fmt.Errorf("resolve map %q: %w", mapName, err)
	}
	endpoint := gateway.MapEndpoint{Host: host, Port: zone.Port}
	d.Log.Info("char: map served by another shard",
		"map", mapName, "host", endpoint.Host, "port", endpoint.Port)
	return endpoint, nil
}

// zoneHost renders the zone's wire-order IPv4 as a dotted quad.
//
// Zone.IPv4 is the source's field value — htonl of the host-order address
// (0x7f000001 for 127.0.0.1) — so the octets come back out in the order
// the client dials them.
func zoneHost(zone transitdomain.Zone) (string, error) {
	octets := make([]byte, 4)
	binary.BigEndian.PutUint32(octets, zone.IPv4)
	addr, ok := netip.AddrFromSlice(octets)
	if !ok {
		return "", fmt.Errorf("zone %q: malformed IPv4", zone.Name)
	}
	return addr.String(), nil
}
