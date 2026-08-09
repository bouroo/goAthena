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

// modulePrefix is the shared import-path prefix of every bounded context
// (internal/modules/<name>/...).
const modulePrefix = "github.com/bouroo/goAthena/internal/modules/"

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
// being pulled inside a bounded context, which would form an import cycle. The
// skip guard keeps the test green before internal/modules/ exists.
func TestCompositionRootNotImportedByModules(t *testing.T) {
	if _, err := os.Stat("../../internal/modules"); os.IsNotExist(err) {
		t.Skip("no modules yet (pre-M1)")
	}
	hits := collectImports(t, "../../internal/modules", "github.com/bouroo/goAthena/internal/app")
	for _, h := range hits {
		t.Errorf("module imports composition root: %s", h)
	}
}

// infraViolation records a single cross-context port/adapter leak: an
// application-layer source file that imports another module's infra package.
type infraViolation struct {
	file       string // path relative to modules/, e.g. "gateway/app/login.go"
	line       int    // 1-indexed line of the offending import
	importPath string
}

// scanAppInfraViolations walks modules/ and returns every non-test .go file
// directly under modules/<owner>/app/ that imports modules/<other>/infra. A
// module's app layer may reach another module's domain package (its ports) but
// must never import that module's infra package (its adapters); doing so welds
// one bounded context to another's driver implementation and undoes the seam
// the gateway exists to provide. Own-module infra is out of scope here (the
// composition root di.go is the sanctioned wiring site) and test files are
// excluded because integration tests assemble real adapters by design.
func scanAppInfraViolations(
	t *testing.T,
	fset *token.FileSet,
	modulesDir, infraSuffix string,
) []infraViolation {
	t.Helper()
	var violations []infraViolation
	err := filepath.WalkDir(modulesDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(modulesDir, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		// Only an app-layer file qualifies: modules/<owner>/app/<file>.
		seg := strings.SplitN(rel, "/", 3)
		if len(seg) < 3 || seg[1] != "app" {
			return nil
		}
		ownerModule := seg[0]

		file, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if perr != nil {
			return perr
		}
		for _, spec := range file.Imports {
			imp := strings.Trim(spec.Path.Value, `"`)
			if !strings.HasPrefix(imp, modulePrefix) || !strings.HasSuffix(imp, infraSuffix) {
				continue
			}
			importedModule := strings.TrimSuffix(strings.TrimPrefix(imp, modulePrefix), infraSuffix)
			// A sub-path under /infra is not a direct module/infra import.
			if strings.Contains(importedModule, "/") || importedModule == ownerModule {
				continue
			}
			violations = append(violations, infraViolation{
				file:       rel,
				line:       fset.Position(spec.Pos()).Line,
				importPath: imp,
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", modulesDir, err)
	}
	return violations
}

// TestAppLayerDoesNotImportInfra pins the port/adapter boundary between
// bounded contexts: a module's application layer (modules/<M>/app) may depend
// on another module's domain package -- its ports -- but must never import that
// module's infra package, the adapters. Phase-5 fixed exactly this regression
// (gateway/app -> content/infra); this test fails the moment such an import
// returns, naming the offending file:line so the leak is immediately locatable.
func TestAppLayerDoesNotImportInfra(t *testing.T) {
	if _, err := os.Stat("../../internal/modules"); os.IsNotExist(err) {
		t.Skip("no modules yet (pre-M1)")
	}
	for _, v := range scanAppInfraViolations(t, token.NewFileSet(), "../../internal/modules", "/infra") {
		t.Errorf("app layer imports another module's infra (port/adapter violation): %s:%d -> %s",
			v.file, v.line, v.importPath)
	}
}
