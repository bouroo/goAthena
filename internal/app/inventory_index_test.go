//go:build unit

package app_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The inventory index convention is the project's highest-risk silent
// divergence, and the e2e suite structurally cannot see it: every test
// transmitted client index 1, which resolves to a valid row under BOTH the
// correct convention (wire = row + 2) and the wrong one (wire = row + 1). The
// packet-level unit test pins the constants; these tests pin the CALL SITES, so
// a new handler cannot reintroduce a raw index arithmetic of its own.
//
// rAthena anchors (third_party/rathenaThailand):
//   src/map/clif.cpp:122-128  client_index(s)=s+2 / server_index(c)=c-2
//   clif.cpp:12121 UseItem, clif.cpp:12063 DropItem, clif.cpp:12139 EquipItem,
//   clif.cpp:12201 takeoff, clif.cpp:12568 AddExchangeItem — all -2.

// inventoryListNames are the identifiers that hold an inventory ROW LIST in
// this codebase (the value LoadByChar returns). The subscript rule is scoped to
// these names so it flags the index convention without firing on unrelated
// `[x-1]` lookups (e.g. a per-level skill-range table).
var inventoryListNames = map[string]bool{
	"items":     true,
	"rows":      true,
	"inv":       true,
	"inventory": true,
	"bag":       true,
}

// indexOperandName returns the identifier a wire-index expression is ultimately
// built from, unwrapping the `int(...)`/`uint16(...)` conversions the handlers
// apply, and reports whether its name reads as an index. It is what makes the
// rule catch `int(req.InventoryIndex) - 1` as well as a bare `req.Index - 1`.
func indexOperandName(e ast.Expr) (string, bool) {
	switch v := e.(type) {
	case *ast.CallExpr:
		if len(v.Args) == 1 {
			return indexOperandName(v.Args[0])
		}
	case *ast.ParenExpr:
		return indexOperandName(v.X)
	case *ast.Ident:
		if strings.Contains(strings.ToLower(v.Name), "index") {
			return v.Name, true
		}
	case *ast.SelectorExpr:
		if strings.Contains(strings.ToLower(v.Sel.Name), "index") {
			return v.Sel.Name, true
		}
	}
	return "", false
}

// indexOffenders walks the given roots and reports non-test .go files under
// modules/*/app/ that perform inventory index arithmetic inline instead of
// routing through ropacket.ServerIndex / ropacket.ClientIndex.
//
// Three shapes are searched for, all of which are how the convention broke or
// could break again:
//   - `[x-1]` on an inventory row list (the 1-based list-position idiom),
//   - `index - 1` / `index - 2` on a wire index (the old -1 resolution), and
//   - `index + 2` on a wire index (double-applying the client offset; rAthena
//     emits client_index(row) at clif.cpp:4315/4346/4484 and never adds 2 to an
//     already-converted value).
func indexOffenders(t *testing.T, root string) []string {
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
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		// Only the ingress app layer is in scope: that is where wire indices are
		// decoded and where the regressions lived.
		seg := strings.SplitN(rel, "/", 3)
		if len(seg) < 3 || seg[1] != "app" {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		ast.Inspect(file, func(n ast.Node) bool {
			// `items[x-1]` — the 1-based list-position idiom the row convention
			// replaced. Only an inventory row list qualifies.
			if idx, ok := n.(*ast.IndexExpr); ok {
				if id, ok := idx.X.(*ast.Ident); ok && inventoryListNames[strings.ToLower(id.Name)] {
					if bin, ok := idx.Index.(*ast.BinaryExpr); ok && bin.Op == token.SUB {
						if lit, ok := bin.Y.(*ast.BasicLit); ok && lit.Kind == token.INT && lit.Value == "1" {
							hits = append(hits, rel+":"+strconv.Itoa(fset.Position(idx.Pos()).Line)+
								" -> `"+id.Name+"[x-1]`; inventory rows are 0-based (use ropacket.ServerIndex)")
						}
					}
				}
			}
			// `index - 1` (old resolution) or `index + 2` (double offset) on a
			// wire index.
			if bin, ok := n.(*ast.BinaryExpr); ok && (bin.Op == token.SUB || bin.Op == token.ADD) {
				if lit, ok := bin.Y.(*ast.BasicLit); ok && lit.Kind == token.INT {
					if name, isIdx := indexOperandName(bin.X); isIdx {
						verb := "use ropacket.ServerIndex"
						if bin.Op == token.ADD {
							verb = "use ropacket.ClientIndex"
						}
						hits = append(hits, rel+":"+strconv.Itoa(fset.Position(bin.Pos()).Line)+
							" -> `"+name+" "+bin.Op.String()+" "+lit.Value+"`; "+verb+" (clif.cpp:122-128)")
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return hits
}

// TestNoInlineInventoryIndexArithmetic fails if a handler re-derives the
// inventory index offset instead of calling ropacket.ServerIndex/ClientIndex.
// Phase 42 fixed a −1/+2 pair of cancelling errors here; this test keeps them
// from coming back one call site at a time.
func TestNoInlineInventoryIndexArithmetic(t *testing.T) {
	if _, err := os.Stat("../../internal/modules"); os.IsNotExist(err) {
		t.Skip("no modules yet (pre-M1)")
	}
	for _, h := range indexOffenders(t, "../../internal/modules") {
		t.Errorf("inline inventory index arithmetic: %s", h)
	}
}
