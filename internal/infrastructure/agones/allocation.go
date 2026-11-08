package agones

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"agones.dev/agones/pkg/sdk"
)

// GamePortName is the name of the port the shard's map listener is exposed
// under. The transit directory reads Status.Ports["game"] out of the Agones
// API to resolve a shard's client-facing endpoint, so the Fleet's port and
// this constant must agree.
const GamePortName = "game"

// Allocation is the client-facing endpoint Agones assigned to this
// GameServer: the node address and the host port the map listener is
// reachable on.
//
// It is runtime state, not configuration. No environment variable can
// express "the address my node gave me" — the pod's IP is assigned after
// the manifest is written — so a shard that must advertise a reachable
// address has to ask the sidecar.
type Allocation struct {
	// Address is the node's IP address the GameServer is reachable on.
	Address netip.Addr
	// Port is the host port the map listener is bound to.
	Port uint16
}

// ErrNoAllocation is returned when the process has no allocation: it is not
// running as a GameServer.
var ErrNoAllocation = errors.New("agones: no allocation (not running as a GameServer)")

// Alloctor is the optional surface a sidecar-backed process exposes: the
// allocation Agones assigned to it.
//
// It is deliberately not part of Port. A process that does not run as a
// GameServer has no allocation, and the lifecycle surface stays the
// smallest thing every caller needs.
type Alloctor interface {
	// Allocate returns the endpoint Agones assigned to this GameServer.
	Allocate(ctx context.Context) (Allocation, error)
}

// AllocationTimeout bounds the GetGameServer call. The sidecar answers
// from local state, so a slow answer means the sidecar is unhealthy.
const AllocationTimeout = 10 * time.Second

// Allocate reads the GameServer Agones assigned to this pod and returns its
// client-facing endpoint.
//
// The port comes from the "game" port entry in Status.Ports, which is the
// port Agones actually bound on the node — under a Dynamic port policy that
// differs from the container port, and under a Static one it is the same.
// Reading it rather than assuming the container port is what lets a shard
// advertise the address a client can actually dial.
//
// A GameServer with no address or no "game" port cannot accept clients:
// that is an allocation or Fleet misconfiguration, and is reported as such
// rather than as an address that would drop every connection.
func (s *Sidecar) Allocate(ctx context.Context) (Allocation, error) {
	ctx, cancel := context.WithTimeout(ctx, AllocationTimeout)
	defer cancel()
	gs, err := s.client.GetGameServer(ctx, &sdk.Empty{})
	if err != nil {
		return Allocation{}, fmt.Errorf("agones: get gameserver: %w", err)
	}
	if gs.GetStatus() == nil {
		return Allocation{}, fmt.Errorf("agones: gameserver %q has no status", gs.GetObjectMeta().GetName())
	}
	addr, err := netip.ParseAddr(gs.GetStatus().GetAddress())
	if err != nil {
		return Allocation{}, fmt.Errorf("agones: gameserver %q address %q: %w",
			gs.GetObjectMeta().GetName(), gs.GetStatus().GetAddress(), err)
	}
	addr = addr.Unmap()
	if !addr.Is4() {
		return Allocation{}, fmt.Errorf("agones: gameserver %q address %q is not ipv4",
			gs.GetObjectMeta().GetName(), gs.GetStatus().GetAddress())
	}
	for _, p := range gs.GetStatus().GetPorts() {
		if p.GetName() != GamePortName {
			continue
		}
		port := p.GetPort()
		if port <= 0 || port > 65535 {
			return Allocation{}, fmt.Errorf("agones: gameserver %q port %q = %d, out of range",
				gs.GetObjectMeta().GetName(), GamePortName, port)
		}
		return Allocation{Address: addr, Port: uint16(port)}, nil //nolint:gosec // G115: range-checked above.
	}
	return Allocation{}, fmt.Errorf("agones: gameserver %q has no %q port", gs.GetObjectMeta().GetName(), GamePortName)
}

// Allocate is the no-op allocation: a process that does not run as a
// GameServer has nothing to advertise.
func (Noop) Allocate(context.Context) (Allocation, error) {
	return Allocation{}, ErrNoAllocation
}
