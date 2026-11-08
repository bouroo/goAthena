//go:build unit

package agones

import (
	"errors"
	"log/slog"
	"testing"

	agonesv1 "agones.dev/agones/pkg/apis/agones/v1"
	versioned "agones.dev/agones/pkg/client/clientset/versioned"
	"agones.dev/agones/pkg/client/clientset/versioned/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	transitdomain "github.com/bouroo/goAthena/internal/modules/transit/domain"
)

func gs(name, state, address, maps string, port int32) *agonesv1.GameServer {
	g := &agonesv1.GameServer{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   "default",
			Annotations: map[string]string{MapsAnnotation: maps},
		},
	}
	if state != "" {
		g.Status.State = agonesv1.GameServerState(state)
	}
	g.Status.Address = address
	if port != 0 {
		g.Status.Ports = []agonesv1.GameServerStatusPort{{Name: PortName, Port: port}}
	}
	return g
}

func newFake(t *testing.T, objs ...runtime.Object) *FleetDirectory {
	t.Helper()
	var cs versioned.Interface = fake.NewSimpleClientset(objs...)
	d := &FleetDirectory{client: cs, namespace: "default", log: slog.Default()}
	return d
}

func TestFleetDirectory_ResolvesReadyServingZone(t *testing.T) {
	d := newFake(t,
		gs("zone-b", "Ready", "192.168.4.7", "geffen,payon", 5121),
	)
	z, err := d.Resolve("geffen")
	if err != nil {
		t.Fatalf("resolve geffen: %v", err)
	}
	// IPv4 arrives as the big-endian wire value the SERVERMOVE frame carries.
	if got, want := z.IPv4, uint32(0xc0a80407); got != want {
		t.Errorf("ip = %#x, want %#x (big-endian 192.168.4.7)", got, want)
	}
	// Port arrives as the plain port number — the wire "swap" is Encode's
	// big-endian write (5121 = 0x1401; the pinned e2e reads exactly that).
	if got, want := z.Port, uint16(0x1401); got != want {
		t.Errorf("port = %#x, want %#x (5121 written big-endian)", got, want)
	}
	if z.Name != "zone-b" {
		t.Errorf("zone name = %q, want zone-b", z.Name)
	}
}

func TestFleetDirectory_SkipsNotReadyAndNonServing(t *testing.T) {
	d := newFake(t,
		gs("starting", "Creating", "10.0.0.1", "geffen", 5121),
		gs("draining", "Shutdown", "10.0.0.2", "geffen", 5121),
		gs("other", "Ready", "10.0.0.3", "alberta", 5121),
	)
	if _, err := d.Resolve("geffen"); !errors.Is(err, transitdomain.ErrUnknownMap) {
		t.Errorf("geffen with only not-ready/other servers: want ErrUnknownMap, got %v", err)
	}
}

func TestFleetDirectory_SelfNameResolvesLocal(t *testing.T) {
	d := newFake(t, gs("me", "Ready", "10.0.0.9", "geffen", 5121))
	d.selfName = "me"
	z, err := d.Resolve("geffen")
	if err != nil {
		t.Fatalf("self resolve: %v", err)
	}
	if z != (transitdomain.Zone{}) {
		t.Errorf("self map should resolve local zero Zone, got %+v", z)
	}
}

func TestFleetDirectory_LabelDeclarationAndSelector(t *testing.T) {
	labeled := gs("zone-c", "Ready", "10.1.2.3", "", 7777)
	labeled.Labels = map[string]string{LabelPrefix + "izlude": "", "shard": "west"}
	d := newFake(t, labeled)
	d.selector = "shard=west"
	if z, err := d.Resolve("izlude"); err != nil {
		t.Errorf("label-declared izlude: %v", err)
	} else if got, want := z.IPv4, uint32(0x0a010203); got != want {
		t.Errorf("ip = %#x, want %#x", got, want)
	}
	// The selector excludes this server when it does not match.
	d.selector = "shard=east"
	if _, err := d.Resolve("izlude"); !errors.Is(err, transitdomain.ErrUnknownMap) {
		t.Errorf("selector mismatch: want ErrUnknownMap, got %v", err)
	}
}

func TestFleetDirectory_MisconfiguredGameServerIsUnknown(t *testing.T) {
	noPort := gs("noport", "Ready", "10.0.0.4", "geffen", 0)
	badAddr := gs("badaddr", "Ready", "not-an-ip", "geffen", 5121)
	d := newFake(t, noPort)
	if _, err := d.Resolve("geffen"); !errors.Is(err, transitdomain.ErrUnknownMap) {
		t.Errorf("server without game port: want ErrUnknownMap, got %v", err)
	}
	d2 := newFake(t, badAddr)
	if _, err := d2.Resolve("geffen"); !errors.Is(err, transitdomain.ErrUnknownMap) {
		t.Errorf("server with bad address: want ErrUnknownMap, got %v", err)
	}
}

func TestFleetDirectory_InClusterConfigPreferred(t *testing.T) {
	// Outside a pod, InClusterConfig fails and NewFleetDirectory falls back
	// to the kubeconfig path; with neither present construction must fail
	// loudly rather than produce a directory that answers everything local.
	t.Setenv("KUBECONFIG", "/nonexistent/kubeconfig")
	if _, err := NewFleetDirectory("default", "", "", slog.Default()); err == nil {
		t.Errorf("construction without any credentials should fail")
	}
}

func TestSwapPort(t *testing.T) {
	for _, tc := range []struct {
		in   int32
		want uint16
	}{
		{5121, 0x1401}, {7777, 0x1e61}, {6900, 0x1af4},
	} {
		if got := swapPort(tc.in); got != tc.want {
			t.Errorf("swapPort(%d) = %#04x, want %#04x", tc.in, got, tc.want)
		}
	}
}
