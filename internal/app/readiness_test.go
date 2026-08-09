//go:build unit

package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bouroo/goAthena/internal/config"
)

// TestSchemaVerdict covers the schema-aware readiness decision logic with
// fakes — the live probeSchema query cannot run without a database. It proves
// /readyz reports not-ready when the schema is absent (fresh volume), dirty, or
// at version 0, and ready only once a clean non-zero migration has landed.
func TestSchemaVerdict(t *testing.T) {
	freshVolume := errors.New("Error 1146: Table 'ro.schema_migrations' doesn't exist")
	for _, tc := range []struct {
		name    string
		err     error
		version uint
		dirty   bool
		wantOK  bool
		wantSub string
	}{
		{"applied clean", nil, 3, false, true, ""},
		{"fresh volume missing table", freshVolume, 0, false, false, "schema not applied"},
		{"dirty", nil, 3, true, false, "schema dirty"},
		{"version zero", nil, 0, false, false, "version 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := schemaVerdict(tc.err, tc.version, tc.dirty)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("want nil, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("err = %q, want substring %q", err.Error(), tc.wantSub)
			}
		})
	}
}

// TestReady_DBNil proves readiness fails fast before any DB probe when the
// connection never landed at boot — no live dependency is required.
func TestReady_DBNil(t *testing.T) {
	d := &deps{}
	err := d.ready(context.Background())
	if err == nil {
		t.Fatal("want not-ready when db is nil, got nil")
	}
	if !strings.Contains(err.Error(), "db not connected") {
		t.Fatalf("err = %q, want 'db not connected'", err.Error())
	}
}

// TestOTelStatus covers the #10 honest-gap decision: a default/none exporter is
// not a gap (nothing requested), but otlp is reported as not-wired so the boot
// path logs that telemetry is being dropped rather than silently consuming it.
// TestOTelEnabled covers the pure decision predicate for OTel trace wiring —
// it does not need a live collector. exporter=otlp + a non-empty endpoint means
// initOTel will register a TracerProvider; anything else leaves tracing off.
func TestOTelEnabled(t *testing.T) {
	for _, tc := range []struct {
		name     string
		exporter string
		endpoint string
		want     bool
	}{
		{"none", "none", "", false},
		{"empty default", "", "", false},
		{"otlp no endpoint", "otlp", "", false},
		{"otlp with endpoint", "otlp", "collector:4317", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := otelEnabled(config.OTelConfig{Exporter: tc.exporter, Endpoint: tc.endpoint})
			if got != tc.want {
				t.Fatalf("otelEnabled = %v, want %v", got, tc.want)
			}
		})
	}
}
