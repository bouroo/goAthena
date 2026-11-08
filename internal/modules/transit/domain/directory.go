// Package domain holds the transit bounded context's ports: the contracts a
// caller (the gateway's warp path) needs to route a client between zone
// processes.
package domain

import "errors"

// Zone is one goathena map-server process: a set of maps served at an
// address the client can reconnect to.
type Zone struct {
	// Name is the operator-assigned zone id (informational in v0).
	Name string
	// IPv4 is the zone's client-facing IPv4 address as the source's struct
	// holds it: htonl of the host-order address, so 127.0.0.1 is
	// 0x7f000001. The packet encoders write the field's little-endian
	// memory image, which is what the source's packed struct puts on the
	// wire (01 00 00 7f).
	IPv4 uint32
	// Port is the zone's map port as the plain number. The source writes
	// ntows(htons(port)) (the two swaps cancel), so the field value is the
	// port itself — 5121.
	Port uint16
}

// ErrUnknownMap is returned when no zone serves the requested map. The
// gateway treats it as a misconfiguration and keeps the player local.
var ErrUnknownMap = errors.New("transit: no zone serves map")

// MapDirectory resolves a map name to the zone process serving it.
// Implementations must be safe for concurrent use from handler goroutines.
//
// v0 scope: the monolith is the only zone, so the production implementation
// (LocalDirectory) serves every map locally and never returns an address. The
// port exists so the gateway already routes through the seam M13's Agones
// fleet will implement (remote zone = (zone, addr) with a real address).
type MapDirectory interface {
	// Resolve returns the zone serving mapName. ErrUnknownMap when none does.
	Resolve(mapName string) (Zone, error)
}

// LocalDirectory is the single-zone v0 directory: every map resolves to
// "this process", expressed as the zero Zone (no address). The gateway's
// redirect branch keys on the zero value, so a resolved-zero and an
// unknown-map behave identically: stay local.
type LocalDirectory struct{}

// NewLocalDirectory builds the single-zone directory.
func NewLocalDirectory() LocalDirectory { return LocalDirectory{} }

// Resolve returns the local zone marker (zero Zone) for any map.
func (LocalDirectory) Resolve(string) (Zone, error) { return Zone{}, nil }
