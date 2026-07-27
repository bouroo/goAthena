//go:build unit

package app

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// modulesImportPath is the import-path prefix of every bounded context.
const modulesImportPath = "github.com/bouroo/goAthena/internal/modules"

// appImportPath is the composition root; modules must never import it (that
// would form a composition-root → module → composition-root cycle).
const appImportPath = "github.com/bouroo/goAthena/internal/app"

// persistenceDriverPrefixes name infra drivers a pure domain layer may never
// depend on directly. Domain defines ports; infra provides adapters.
var persistenceDriverPrefixes = []string{
	"gorm.io",
	"github.com/valkey-io",
	"github.com/redis/go-redis",
	"github.com/nats-io",
}

// TestModuleBoundaries enforces the clean-architecture layering contract for
// every Go file under internal/modules. It is a static source walk (parser in
// ImportsOnly mode over non-test files), so it runs in the unit gate with no
// build, network, or exec, and reports a violation the instant a forbidden
// import is added.
//
// Rules, for a file in module M at layer L importing module N at layer K:
//   - domain (L=domain) is pure: it may not import persistence drivers, nor any
//     module's app/infra/di (own or foreign). Cross-module domain imports are OK.
//   - app (L=app) may not import any module's infra/di (own or foreign).
//   - any layer: a module may not import another module's app/infra/di (K in
//     app/infra/di with N != M).
//   - any layer: no module may import the composition root (internal/app).
//
// On the M0 shell the modules hold only doc.go root packages, so nothing
// matches a layer directory yet and the walk passes; the guard is in place to
// catch the first violation as domain/app layers land in M1+.
func TestModuleBoundaries(t *testing.T) {
	t.Parallel()

	root := modulesRoot(t)

	var violations []string
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fmod, flayer := moduleLayerOf(root, path)
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if perr != nil {
			return fmt.Errorf("parse %s: %w", path, perr)
		}

		for _, spec := range file.Imports {
			imp := strings.Trim(spec.Path.Value, `"`)
			for _, v := range boundaryViolations(fmod, flayer, imp) {
				violations = append(violations, fmt.Sprintf("%s (%s): %s", relPath(root, path), flayer, v))
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk modules tree: %v", walkErr)
	}

	if len(violations) > 0 {
		t.Errorf("boundary violations found (%d):\n  %s", len(violations), strings.Join(violations, "\n  "))
	}
}

// boundaryViolations returns the human-readable rule each import breaks. Empty
// means the import is permitted.
func boundaryViolations(fileModule, fileLayer, imp string) []string {
	if imp == appImportPath || strings.HasPrefix(imp, appImportPath+"/") {
		return []string{"imports the composition root (internal/app); modules must not depend on app"}
	}

	if strings.HasPrefix(imp, modulesImportPath+"/") {
		impModule, impLayer := splitModuleLayer(strings.TrimPrefix(imp, modulesImportPath+"/"))
		switch {
		case impModule != fileModule && isImplLayer(impLayer):
			return []string{fmt.Sprintf("imports module %q %s layer (cross-module impl import)", impModule, impLayer)}
		case impModule == fileModule && fileLayer == "domain" && isImplLayer(impLayer):
			return []string{fmt.Sprintf("domain imports own %s layer", impLayer)}
		case impModule == fileModule && fileLayer == "app" && (impLayer == "infra" || impLayer == "di"):
			return []string{fmt.Sprintf("app imports own %s layer", impLayer)}
		}
		return nil
	}

	// External import: only the domain layer is purity-checked.
	if fileLayer == "domain" && isPersistenceDriver(imp) {
		return []string{fmt.Sprintf("domain imports persistence driver %s", imp)}
	}
	return nil
}

// moduleLayerOf derives the (module, layer) identity of a Go file from its path
// under the modules root. Layer is the first directory segment that is a layer
// keyword (domain/app/infra/di); the module is everything before it. A file
// directly under a module root (e.g. account/doc.go) has layer "".
func moduleLayerOf(root, path string) (module, layer string) {
	rel, err := filepath.Rel(root, filepath.Dir(path))
	if err != nil {
		return "", ""
	}
	return splitModuleLayer(filepath.ToSlash(rel))
}

// splitModuleLayer splits a modules-relative path tail into (module, layer) at
// the first layer-keyword segment.
func splitModuleLayer(tail string) (module, layer string) {
	segs := strings.Split(strings.Trim(tail, "/"), "/")
	for i, seg := range segs {
		if isLayer(seg) {
			return strings.Join(segs[:i], "/"), seg
		}
	}
	return strings.Join(segs, "/"), ""
}

func isLayer(seg string) bool {
	switch seg {
	case "domain", "app", "infra", "di":
		return true
	}
	return false
}

// isImplLayer reports whether a layer is an implementation layer that must not
// leak across module boundaries (domain is a value/port layer and may be shared).
func isImplLayer(layer string) bool {
	return layer == "app" || layer == "infra" || layer == "di"
}

func isPersistenceDriver(imp string) bool {
	for _, p := range persistenceDriverPrefixes {
		if strings.HasPrefix(imp, p) {
			return true
		}
	}
	return false
}

// modulesRoot returns the absolute path to internal/modules, located relative
// to this test file so it is correct regardless of the test's working directory.
func modulesRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate test file via runtime.Caller")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "modules")
}

func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}
