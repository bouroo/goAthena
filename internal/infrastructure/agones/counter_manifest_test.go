package agones

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	agonesv1 "agones.dev/agones/pkg/apis/agones/v1"
	autoscalingv1 "agones.dev/agones/pkg/apis/autoscaling/v1"
	"agones.dev/agones/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// The player counter is a contract between three places, and a typo in
// any one of them leaves the autoscaler scaling on a counter that does
// not exist — which Agones reports as an error, and the fleet then stays
// at whatever size it was.
//
//   * the Fleet declares the counter: spec.template.spec.counters.players
//   * the shard publishes it under the same name (PlayersCounterName)
//   * the FleetAutoscaler's policy reads it: spec.policy.counter.key
//
// These tests read the committed manifests and assert the three agree.

// repoRoot walks up from this package to the repo root.
func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Join(wd, "..", "..", "..")
}

// decodeManifest reads a multi-document YAML manifest into the values
// the caller decodes, using the Agones API's own decoder so a field
// Agones would reject fails here rather than silently.
func decodeManifest[T any](t *testing.T, path string) []T {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	var out []T
	dec := yaml.NewYAMLOrJSONDecoder(f, 4096)
	for {
		var v T
		if err := dec.Decode(&v); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("decode %s: %v", path, err)
		}
		out = append(out, v)
	}
	return out
}

// loadCounterFleets decodes the Fleets from the deployed manifest.
func loadCounterFleets(t *testing.T) []agonesv1.Fleet {
	t.Helper()
	path := filepath.Join(repoRoot(t), "deploy", "k3s", "base", "zone-fleet.yaml")
	var out []agonesv1.Fleet
	for _, f := range decodeManifest[agonesv1.Fleet](t, path) {
		if f.Kind == "Fleet" {
			out = append(out, f)
		}
	}
	return out
}

// loadCounterAutoscalers decodes the FleetAutoscalers from the deployed
// manifest.
func loadCounterAutoscalers(t *testing.T) []autoscalingv1.FleetAutoscaler {
	t.Helper()
	path := filepath.Join(repoRoot(t), "deploy", "k3s", "base", "fleet-autoscaler.yaml")
	var out []autoscalingv1.FleetAutoscaler
	for _, fas := range decodeManifest[autoscalingv1.FleetAutoscaler](t, path) {
		if fas.Kind == "FleetAutoscaler" {
			out = append(out, fas)
		}
	}
	return out
}

// TestFleetManifest_DeclaresThePlayerCounter proves every Fleet declares
// the counter the shard publishes, under the name the code uses.
//
// The name is the contract: the shard publishes "players", and a Fleet
// that declares a different name leaves the autoscaler reading a counter
// that does not exist.
func TestFleetManifest_DeclaresThePlayerCounter(t *testing.T) {
	fleets := loadCounterFleets(t)
	if len(fleets) == 0 {
		t.Fatal("the manifest declares no Fleets")
	}
	for _, fleet := range fleets {
		counter, ok := fleet.Spec.Template.Spec.Counters[PlayersCounterName]
		if !ok {
			t.Errorf("Fleet %q declares no %q counter; the shard publishes one, and the autoscaler reads it",
				fleet.Name, PlayersCounterName)
			continue
		}
		if counter.Capacity <= 0 {
			t.Errorf("Fleet %q counter %q capacity = %d, want > 0",
				fleet.Name, PlayersCounterName, counter.Capacity)
		}
	}
}

// TestFleetManifest_CounterCapacityMatchesTheShard proves the Fleet's
// counter capacity is the capacity the shard reports.
//
// The shard reports its player count with the capacity baked into the
// code (PlayerCounterCapacity). The Fleet declares the same capacity in
// its counter spec. If they disagree, Agones aggregates a capacity the
// shard never claimed, and the autoscaler sizes the fleet against a
// number that is not the world's.
func TestFleetManifest_CounterCapacityMatchesTheShard(t *testing.T) {
	for _, fleet := range loadCounterFleets(t) {
		counter, ok := fleet.Spec.Template.Spec.Counters[PlayersCounterName]
		if !ok {
			continue // reported by TestFleetManifest_DeclaresThePlayerCounter
		}
		if counter.Capacity != PlayerCounterCapacity {
			t.Errorf("Fleet %q counter %q capacity = %d, want %d (the capacity the shard reports)",
				fleet.Name, PlayersCounterName, counter.Capacity, PlayerCounterCapacity)
		}
	}
}

// TestFleetAutoscaler_CounterKeyMatchesTheFleet proves every Counter
// policy names a counter the Fleet it scales actually declares.
//
// This is the check that catches a typo on either side: the policy's key
// and the Fleet's counter name have to be the same string.
func TestFleetAutoscaler_CounterKeyMatchesTheFleet(t *testing.T) {
	fleets := loadCounterFleets(t)
	byName := make(map[string]agonesv1.Fleet, len(fleets))
	for _, f := range fleets {
		byName[f.Name] = f
	}
	autoscalers := loadCounterAutoscalers(t)
	if len(autoscalers) == 0 {
		t.Fatal("the manifest declares no FleetAutoscalers")
	}
	sawCounterPolicy := false
	for _, fas := range autoscalers {
		if fas.Spec.Policy.Type != autoscalingv1.CounterPolicyType {
			continue
		}
		sawCounterPolicy = true
		fleet, ok := byName[fas.Spec.FleetName]
		if !ok {
			t.Errorf("FleetAutoscaler %q scales fleet %q, which the manifest does not declare",
				fas.Name, fas.Spec.FleetName)
			continue
		}
		key := fas.Spec.Policy.Counter.Key
		if key != PlayersCounterName {
			t.Errorf("FleetAutoscaler %q counter key = %q, want %q (the name the shard publishes)",
				fas.Name, key, PlayersCounterName)
		}
		if _, ok := fleet.Spec.Template.Spec.Counters[key]; !ok {
			t.Errorf("FleetAutoscaler %q reads counter %q, which Fleet %q does not declare",
				fas.Name, key, fleet.Name)
		}
	}
	if !sawCounterPolicy {
		t.Error("no FleetAutoscaler uses a Counter policy; the player count would scale nothing")
	}
}

// TestFleetAutoscaler_ValidatesAgainstAgones runs Agones' own validator
// over the autoscalers, so a policy Agones would reject fails here rather
func TestFleetAutoscaler_ValidatesAgainstAgones(t *testing.T) {
	// The Agones feature gates default to empty until the controller
	// parses them at startup (runtime.ParseFeaturesFromEnv). The
	// validator reads them, so the test has to apply the same defaults
	// the controller does — otherwise every gated policy (the counter
	// and the schedule) is rejected for a reason that does not exist in
	// a real cluster.
	if err := runtime.ParseFeatures(""); err != nil {
		t.Fatalf("parse the Agones feature defaults: %v", err)
	}
	for _, fas := range loadCounterAutoscalers(t) {
		fas.ApplyDefaults()
		if errs := fas.Validate(); len(errs) > 0 {
			t.Errorf("FleetAutoscaler %q failed Agones validation: %v", fas.Name, errs)
		}
	}
}

// TestFleetAutoscaler_CounterPolicyCapacityIsOrdered proves the counter
// policy's capacities are in the order Agones requires: the minimum must
// be below the maximum, and the buffer must be positive.
//
// A policy that breaks the order is rejected by Agones at apply time, and
// the fleet never scales.
func TestFleetAutoscaler_CounterPolicyCapacityIsOrdered(t *testing.T) {
	for _, fas := range loadCounterAutoscalers(t) {
		if fas.Spec.Policy.Type != autoscalingv1.CounterPolicyType {
			continue
		}
		c := fas.Spec.Policy.Counter
		if c.MaxCapacity <= c.MinCapacity {
			t.Errorf("FleetAutoscaler %q maxCapacity = %d, want > minCapacity = %d",
				fas.Name, c.MaxCapacity, c.MinCapacity)
		}
		if c.BufferSize.IntValue() <= 0 && c.BufferSize.StrVal == "" {
			t.Errorf("FleetAutoscaler %q bufferSize = %v, want a positive size",
				fas.Name, c.BufferSize)
		}
	}
}
