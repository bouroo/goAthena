// Package staticdir implements the transit MapDirectory port over a
// deployment-level route table (zone.directory.routes, "map: host:port") —
// the Docker Swarm / fixed-sharding path where no Agones API is reachable.
// Unknown maps resolve local (zero Zone), matching LocalDirectory: a missing
// route means the map lives in this process.
package staticdir

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"strings"

	transitdomain "github.com/bouroo/goAthena/internal/modules/transit/domain"
)

// StaticDirectory resolves maps from an immutable host:port table.
type StaticDirectory struct {
	routes map[string]transitdomain.Zone
}

// New parses routes ("geffen" → "10.0.4.7:5121") into wire-order Zones. A
// malformed route is a fatal configuration error (the process cannot know
// which maps it serves otherwise), so New returns an error instead of
// silently local-routing.
func New(routes map[string]string) (*StaticDirectory, error) {
	parsed := make(map[string]transitdomain.Zone, len(routes))
	for mapName, addr := range routes {
		zone, err := parseAddr(mapName, addr)
		if err != nil {
			return nil, err
		}
		parsed[mapName] = zone
	}
	return &StaticDirectory{routes: parsed}, nil
}

// Resolve returns the zone serving mapName; a map with no route resolves
// local (zero Zone, no error) — the gateway's local-warp branch.
func (d *StaticDirectory) Resolve(mapName string) (transitdomain.Zone, error) {
	if zone, ok := d.routes[mapName]; ok {
		return zone, nil
	}
	return transitdomain.Zone{}, nil
}

// parseAddr converts "host:port" into the wire-order Zone. The host must be
// an IPv4 literal (what the client frame carries); hostnames would need a
// DNS lookup per resolve and a deploy-time address is the whole point of
// the static table.
func parseAddr(mapName, addr string) (transitdomain.Zone, error) {
	ap, err := netip.ParseAddrPort(strings.TrimSpace(addr))
	if err != nil {
		return transitdomain.Zone{}, fmt.Errorf("static directory: %q → %q: %w", mapName, addr, err)
	}
	ip := ap.Addr().Unmap()
	if !ip.Is4() {
		return transitdomain.Zone{}, fmt.Errorf("static directory: %q → %q: want IPv4", mapName, addr)
	}
	octets := ip.As4()
	return transitdomain.Zone{
		Name: mapName,
		IPv4: binary.BigEndian.Uint32(octets[:]),
		Port: ap.Port(),
	}, nil
}
