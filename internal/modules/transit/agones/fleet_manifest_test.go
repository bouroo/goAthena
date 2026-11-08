//go:build unit

package agones

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agones.dev/agones/pkg/apis"
	agonesv1 "agones.dev/agones/pkg/apis/agones/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apimachinery/pkg/util/yaml"

	transitdomain "github.com/bouroo/goAthena/internal/modules/transit/domain"
)

// fleetManifestPath is the committed Agones Fleet manifest. The test walks up
// from this package to the repo root so the path stays correct however the
// package moves.
func fleetManifestPath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := filepath.Join(wd, "..", "..", "..", "..")
	p := filepath.Join(root, "deploy", "agones", "fleet.yaml")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("fleet manifest: %v", err)
	}
	return p
}

// loadFleets decodes every Fleet document from the manifest using the Agones
// API's own YAML decoder, so the test fails on a field Agones would reject or
// ignore rather than on a hand-rolled parse.
func loadFleets(t *testing.T) []agonesv1.Fleet {
	t.Helper()
	f, err := os.Open(fleetManifestPath(t))
	if err != nil {
		t.Fatalf("open manifest: %v", err)
	}
	defer f.Close()

	var fleets []agonesv1.Fleet
	dec := yaml.NewYAMLOrJSONDecoder(f, 4096)
	for {
		var fleet agonesv1.Fleet
		if err := dec.Decode(&fleet); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decode Fleet document: %v", err)
		}
		if fleet.Kind == "" {
			continue // a trailing empty document
		}
		if fleet.Kind != "Fleet" {
			t.Fatalf("manifest document kind = %q, want Fleet", fleet.Kind)
		}
		fleets = append(fleets, fleet)
	}
	if len(fleets) == 0 {
		t.Fatal("no Fleet documents decoded from the manifest")
	}
	return fleets
}

// noopAPIHooks is Agones' generic (non-GKE) cloud-product hook set: it accepts
// every spec. Implemented here rather than imported from agones/pkg/testing so
// the test does not pull that package's k8s.io/apiextensions-apiserver
// dependency into the module graph for a no-op.
type noopAPIHooks struct{}

func (noopAPIHooks) ValidateGameServerSpec(*agonesv1.GameServerSpec, *field.Path) field.ErrorList {
	return nil
}

func (noopAPIHooks) ValidateScheduling(apis.SchedulingStrategy, *field.Path) field.ErrorList {
	return nil
}

func (noopAPIHooks) MutateGameServerPod(*agonesv1.GameServerSpec, *corev1.Pod) error { return nil }

func (noopAPIHooks) SetEviction(*agonesv1.Eviction, *corev1.Pod) error { return nil }

var _ agonesv1.APIHooks = noopAPIHooks{}

// TestFleetManifest_ValidatesAgainstAgones proves every manifest document
// satisfies the Agones API's own validation — a manifest that fails here would
// be rejected by the apiserver at apply time.
func TestFleetManifest_ValidatesAgainstAgones(t *testing.T) {
	fleets := loadFleets(t)
	hooks := noopAPIHooks{}
	for _, fleet := range fleets {
		fleet.Spec.Template.Spec.ApplyDefaults()
		if errs := fleet.Validate(hooks); len(errs) > 0 {
			t.Errorf("Fleet %q failed Agones validation: %v", fleet.Name, errs)
		}
	}
}

// TestFleetManifest_MatchesDirectoryContract proves the manifest agrees with
// what transit/agones reads: the game port is named PortName, each Fleet
// declares its maps through MapsAnnotation, and the API version is the one the
// client resolves.
func TestFleetManifest_MatchesDirectoryContract(t *testing.T) {
	fleets := loadFleets(t)
	for _, fleet := range fleets {
		if fleet.APIVersion != agonesv1.SchemeGroupVersion.String() {
			t.Errorf("Fleet %q apiVersion = %q, want %q",
				fleet.Name, fleet.APIVersion, agonesv1.SchemeGroupVersion.String())
		}

		var haveGamePort bool
		for _, p := range fleet.Spec.Template.Spec.Ports {
			if p.Name == PortName {
				haveGamePort = true
				if p.PortPolicy != agonesv1.Static {
					// The container binds GATEWAY_MAP_PORT at boot and is never
					// told a dynamically assigned host port, so a Dynamic policy
					// would redirect clients to a port nobody listens on.
					t.Errorf("Fleet %q game port policy = %q, want Static (the process binds a fixed port)",
						fleet.Name, p.PortPolicy)
				}
				if p.ContainerPort != 5121 {
					t.Errorf("Fleet %q game containerPort = %d, want 5121 (GATEWAY_MAP_PORT default)",
						fleet.Name, p.ContainerPort)
				}
			}
		}
		if !haveGamePort {
			t.Errorf("Fleet %q has no %q port; the redirect frame carries Status.Ports[%q]",
				fleet.Name, PortName, PortName)
		}

		maps := fleet.Spec.Template.Annotations[MapsAnnotation]
		if maps == "" {
			t.Errorf("Fleet %q declares no %q annotation; the directory cannot resolve any map to it",
				fleet.Name, MapsAnnotation)
		}
		for _, m := range strings.Split(maps, ",") {
			if strings.ContainsAny(m, " \t") || m == "" {
				t.Errorf("Fleet %q %s entry %q is not a clean CSV token", fleet.Name, MapsAnnotation, m)
			}
		}
	}
}

// TestFleetManifest_MapsResolveToZone proves the manifest's declared maps are
// exactly the names the directory would match, and that each Fleet's pod can be
// turned into a wire-order Zone — the same conversion Resolve performs on a
// live GameServer. It uses the manifest's own annotation and port, so a typo in
// either is caught here rather than at a portal crossing.
func TestFleetManifest_MapsResolveToZone(t *testing.T) {
	fleets := loadFleets(t)
	// Fake a Ready GameServer per Fleet, as the apiserver would report it.
	byMap := map[string]string{} // map name -> Fleet name
	for _, fleet := range fleets {
		gs := &agonesv1.GameServer{}
		gs.Name = fleet.Name
		gs.Status.State = agonesv1.GameServerStateReady
		gs.Status.Address = "10.0.0.1"
		spec := fleet.Spec.Template.Spec
		for _, p := range spec.Ports {
			gs.Status.Ports = append(gs.Status.Ports, agonesv1.GameServerStatusPort{
				Name: p.Name,
				Port: p.ContainerPort, // Static: host port == container port
			})
		}
		gs.Annotations = map[string]string{MapsAnnotation: fleet.Spec.Template.Annotations[MapsAnnotation]}
		gs.Labels = fleet.Spec.Template.Labels

		if _, err := zoneOf(gs); err != nil {
			t.Errorf("Fleet %q does not convert to a Zone: %v", fleet.Name, err)
		}
		for _, m := range strings.Split(gs.Annotations[MapsAnnotation], ",") {
			m = strings.TrimSpace(m)
			if prev, dup := byMap[m]; dup {
				t.Errorf("map %q declared by both %q and %q — the directory would pick one nondeterministically",
					m, prev, fleet.Name)
			}
			byMap[m] = fleet.Name
			if !serves(gs, m) {
				t.Errorf("Fleet %q declares map %q but serves() says no", fleet.Name, m)
			}
		}
	}
	// A map that appears in no Fleet must not resolve — a portal to it would
	// otherwise silently local-route.
	if serves(&agonesv1.GameServer{Annotations: map[string]string{MapsAnnotation: "prontera"}}, "aldebaran") {
		t.Error("a map absent from every Fleet resolved as served")
	}
	if len(byMap) == 0 {
		t.Fatal("manifest declares no maps at all")
	}
	_ = transitdomain.Zone{}
}

// gameServerManifestPath is the single-shard GameServer manifest.
func gameServerManifestPath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	p := filepath.Join(wd, "..", "..", "..", "..", "deploy", "agones", "gameserver.yaml")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("gameserver manifest: %v", err)
	}
	return p
}

// TestGameServerManifest_ValidatesAndMatchesContract proves the single-shard
// manifest satisfies the Agones API and honours the same directory contract
// (a `game` port, a maps declaration) as the Fleet.
func TestGameServerManifest_ValidatesAndMatchesContract(t *testing.T) {
	f, err := os.Open(gameServerManifestPath(t))
	if err != nil {
		t.Fatalf("open manifest: %v", err)
	}
	defer f.Close()

	var gs agonesv1.GameServer
	if err := yaml.NewYAMLOrJSONDecoder(f, 4096).Decode(&gs); err != nil {
		t.Fatalf("decode GameServer: %v", err)
	}
	if gs.Kind != "GameServer" {
		t.Fatalf("kind = %q, want GameServer", gs.Kind)
	}
	gs.Spec.ApplyDefaults()
	if errs := gs.Validate(noopAPIHooks{}); len(errs) > 0 {
		t.Errorf("GameServer failed Agones validation: %v", errs)
	}

	var haveGamePort bool
	for _, p := range gs.Spec.Ports {
		if p.Name == PortName {
			haveGamePort = true
			if p.PortPolicy != agonesv1.Static {
				t.Errorf("game port policy = %q, want Static", p.PortPolicy)
			}
			if p.ContainerPort != 5121 {
				t.Errorf("game containerPort = %d, want 5121", p.ContainerPort)
			}
		}
	}
	if !haveGamePort {
		t.Errorf("no %q port declared", PortName)
	}
	if maps := gs.Spec.Template.Annotations[MapsAnnotation]; maps == "" {
		t.Errorf("no %q annotation declared", MapsAnnotation)
	}
}
