//go:build unit

package app_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// collectImports parses every .go file under root and returns a list of
// "file:import" strings for imports whose path begins with one of prefixes.
func collectImports(t *testing.T, root string, prefixes ...string) []string {
	t.Helper()
	fset := token.NewFileSet()
	var hits []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if perr != nil {
			return perr
		}
		for _, spec := range file.Imports {
			imp := strings.Trim(spec.Path.Value, `"`)
			for _, p := range prefixes {
				if strings.HasPrefix(imp, p) {
					rel, _ := filepath.Rel(root, path)
					hits = append(hits, rel+" -> "+imp)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return hits
}

const appLayer = "github.com/bouroo/goAthena/internal/"

// TestKernelHasNoInwardDeps locks the clean-architecture boundary: pkg/ro (the
// shared kernel) must never import the application layer (internal/*). The
// kernel has to stay reusable and testable in isolation, so dependency
// direction is strictly outward-only here. Regressed once (athenaconf imported
// internal/config); this test prevents a return.
func TestKernelHasNoInwardDeps(t *testing.T) {
	hits := collectImports(t, "../../pkg/ro", appLayer)
	for _, h := range hits {
		t.Errorf("kernel imports app layer: %s", h)
	}
}

// TestCompositionRootNotImportedByModules guards the composition root from
// being pulled inside a bounded context, which would form an import cycle. Today
// there are no modules; this activates as soon as internal/modules/ lands.
func TestCompositionRootNotImportedByModules(t *testing.T) {
	if _, err := os.Stat("../../modules"); os.IsNotExist(err) {
		t.Skip("no modules yet (pre-M1)")
	}
	hits := collectImports(t, "../../modules", "github.com/bouroo/goAthena/internal/app")
	for _, h := range hits {
		t.Errorf("module imports composition root: %s", h)
	}
}
