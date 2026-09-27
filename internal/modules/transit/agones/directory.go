// Package agones implements the transit MapDirectory port over the Agones
// GameServer API (M13 fleet directory). A GameServer declares the maps it
// serves via the MapsAnnotation CSV (or per-map labels for large map sets —
// LabelPrefix + map name); a Ready GameServer whose declaration contains the
// requested map yields Zone{Status.Address, Status.Ports[name]}.
//
// Discovery goes through the Kubernetes API with in-cluster credentials by
// default. Fleet/Docker-Swarm operators point KUBECONFIG (or the standard
// K8s env contract) at any reachable apiserver — Agones itself only needs
// to schedule the gameservers, not to be the process's own control plane.
package agones

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"

	agonesv1 "agones.dev/agones/pkg/apis/agones/v1"
	"agones.dev/agones/pkg/client/clientset/versioned"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	transitdomain "github.com/bouroo/goAthena/internal/modules/transit/domain"
)

// MapsAnnotation is the GameServer annotation carrying the CSV list of map
// names this GameServer serves, e.g. goathena.dev/maps: "geffen,payon".
const MapsAnnotation = "goathena.dev/maps"

// LabelPrefix is the per-map label prefix for large map sets where a CSV
// annotation would exceed label/annotation size limits:
// goathena.dev/map-<name>: "" present ⇒ <name> served.
const LabelPrefix = "goathena.dev/map-"

// PortName selects which GameServerStatusPort carries the game protocol.
const PortName = "game"

// FleetDirectory resolves map names against Agones GameServer resources.
// Resolve lists GameServers in the configured namespace on every call —
// a portal crossing is cold (one redirect per warp), so a List (~ms against
// an in-cluster apiserver, one RTT against a remote one) needs no informer
// cache in front of it. Callers are handler goroutines; List is safe.
type FleetDirectory struct {
	client    versioned.Interface
	namespace string
	selector  string
	selfName  string // annotation value marking THIS GameServer's maps local
	log       *slog.Logger
}

// NewFleetDirectory dials the Kubernetes API: in-cluster config when the
// process runs inside a pod, the operator's kubeconfig (KUBECONFIG or
// ~/.kube/config) otherwise — the Docker Swarm / workstation path.
func NewFleetDirectory(namespace, selector, selfName string, log *slog.Logger) (*FleetDirectory, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
		cfg, err = clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			loadingRules, &clientcmd.ConfigOverrides{}).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("agones: no in-cluster or kubeconfig credentials: %w", err)
		}
	}
	cs, err := versioned.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("agones: clientset: %w", err)
	}
	if namespace == "" {
		namespace = "default"
	}
	return &FleetDirectory{
		client: cs, namespace: namespace, selector: selector,
		selfName: selfName, log: log,
	}, nil
}

// Resolve returns the zone serving mapName. GameServers not in Ready state
// are skipped (Agones only sends allocations to Ready ones; redirecting a
// client to a not-yet-ready pod would drop it). The configured selector
// narrows candidates. A GameServer whose own-maps declaration contains the
// requested map resolves local (zero Zone) via selfName.
func (d *FleetDirectory) Resolve(mapName string) (transitdomain.Zone, error) {
	list, err := d.client.AgonesV1().GameServers(d.namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: d.selector,
	})
	if err != nil {
		return transitdomain.Zone{}, fmt.Errorf("agones: list gameservers: %w", err)
	}
	for i := range list.Items {
		gs := &list.Items[i]
		if gs.Status.State != agonesv1.GameServerStateReady {
			continue
		}
		if !serves(gs, mapName) {
			continue
		}
		if d.selfName != "" && gs.Name == d.selfName {
			return transitdomain.Zone{}, nil // our own maps: stay local
		}
		return zoneOf(gs)
	}
	return transitdomain.Zone{}, transitdomain.ErrUnknownMap
}

// serves reports whether the GameServer declares mapName: the CSV
// MapsAnnotation or a LabelPrefix+mapName label.
func serves(gs *agonesv1.GameServer, mapName string) bool {
	for _, m := range strings.Split(gs.Annotations[MapsAnnotation], ",") {
		if strings.TrimSpace(m) == mapName {
			return true
		}
	}
	_, ok := gs.Labels[LabelPrefix+mapName]
	return ok
}

// zoneOf converts a Ready GameServer into the wire-order Zone. A GameServer
// without an address or game port cannot accept clients — that is an
// allocation/fleet misconfiguration, reported as unknown rather than a
// redirect to an unroutable ip:port.
func zoneOf(gs *agonesv1.GameServer) (transitdomain.Zone, error) {
	ip, err := netip.ParseAddr(gs.Status.Address)
	if err != nil || !ip.Unmap().Is4() {
		return transitdomain.Zone{}, fmt.Errorf("agones: gameserver %q address %q: %w",
			gs.Name, gs.Status.Address, transitdomain.ErrUnknownMap)
	}
	octets := ip.As4() // Is4/Is4In6 both have a v4 mapping
	for _, p := range gs.Status.Ports {
		if p.Name == PortName {
			return transitdomain.Zone{
				Name: gs.Name,
				IPv4: binary.BigEndian.Uint32(octets[:]),
				Port: swapPort(p.Port),
			}, nil
		}
	}
	return transitdomain.Zone{}, fmt.Errorf("agones: gameserver %q has no %q port: %w",
		gs.Name, PortName, transitdomain.ErrUnknownMap)
}

// swapPort converts an Agones host-order port into the wire value
// ZC_NPCACK_SERVERMOVE carries. Numerically this is the identity: the pinned
// e2e byte evidence is frame [34:36] == 01 14 for port 5121 — the plain
// port number written big-endian (rAthena's ntows(htons()) pair cancels to
// exactly that on the wire). Encode BigEndian-writes the value verbatim.
// Agones types the port int32; ports are ≤ 65535 by IANA.
func swapPort(port int32) uint16 {
	return uint16(port) //nolint:gosec // G115: ports ≤ 65535.
}
