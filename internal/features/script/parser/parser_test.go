//go:build unit

package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/pkg/ro/script"
)

func mustLex(t *testing.T, src string) []script.Token {
	t.Helper()
	tokens, err := script.Lex([]byte(src))
	require.NoError(t, err)
	return tokens
}

func parseStmts(t *testing.T, src string) []script.Stmt {
	t.Helper()
	p := New(mustLex(t, src))
	stmts, err := p.ParseStmts()
	require.NoError(t, err)
	return stmts
}

func parseBody(t *testing.T, src string) []script.Stmt {
	t.Helper()
	p := New(mustLex(t, src))
	stmts, err := p.ParseBody()
	require.NoError(t, err)
	return stmts
}

func TestParseBodyRequiresBraces(t *testing.T) {
	p := New(mustLex(t, `mes "hello";`))
	_, err := p.ParseBody()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected `{`")
}

func TestParseStmtsSimpleCalls(t *testing.T) {
	stmts := parseStmts(t, `mes "hello"; next; close;`)
	require.Len(t, stmts, 3)
	assert.Equal(t, "mes", asCall(stmts[0]).Name)
	assert.Equal(t, "hello", asStr(asCall(stmts[0]).Args[0]))
	assert.Equal(t, "next", asCall(stmts[1]).Name)
	assert.Equal(t, "close", asCall(stmts[2]).Name)
}

func TestParseBodySimpleCalls(t *testing.T) {
	stmts := parseBody(t, `{ mes "hello"; next; close; }`)
	require.Len(t, stmts, 3)
	assert.Equal(t, "mes", asCall(stmts[0]).Name)
	assert.Equal(t, "next", asCall(stmts[1]).Name)
	assert.Equal(t, "close", asCall(stmts[2]).Name)
}

func TestParseBlockStmt(t *testing.T) {
	stmts := parseStmts(t, `{ mes "a"; next; }`)
	require.Len(t, stmts, 1)
	blk, ok := stmts[0].(*script.BlockStmt)
	require.True(t, ok, "expected *BlockStmt, got %T", stmts[0])
	require.Len(t, blk.Body, 2)
	assert.Equal(t, "mes", asCall(blk.Body[0]).Name)
	assert.Equal(t, "next", asCall(blk.Body[1]).Name)
}

func TestParseNestedBlockStmt(t *testing.T) {
	stmts := parseStmts(t, `{ { mes "a"; } }`)
	require.Len(t, stmts, 1)
	outer, ok := stmts[0].(*script.BlockStmt)
	require.True(t, ok)
	require.Len(t, outer.Body, 1)
	inner, ok := outer.Body[0].(*script.BlockStmt)
	require.True(t, ok)
	require.Len(t, inner.Body, 1)
	assert.Equal(t, "mes", asCall(inner.Body[0]).Name)
}

func TestParseAssignment(t *testing.T) {
	stmts := parseStmts(t, `.@a = 1; .@b += 2;`)
	require.Len(t, stmts, 2)
	assign := asAssign(stmts[0])
	assert.Equal(t, "=", assign.Op)
	assert.Equal(t, ".@a", asIdent(assign.Lhs).Name)
	assert.Equal(t, int64(1), asInt(assign.Rhs))

	assign2 := asAssign(stmts[1])
	assert.Equal(t, "+=", assign2.Op)
}

func TestParseArrayAssignment(t *testing.T) {
	stmts := parseStmts(t, `.@arr[0] = 42;`)
	require.Len(t, stmts, 1)
	assign := asAssign(stmts[0])
	idx, ok := assign.Lhs.(*script.IndexExpr)
	require.True(t, ok)
	assert.Equal(t, ".@arr", asIdent(idx.Target).Name)
	assert.Equal(t, int64(0), asInt(idx.Index))
	assert.Equal(t, int64(42), asInt(assign.Rhs))
}

func TestParseExpressionStatement(t *testing.T) {
	stmts := parseStmts(t, `a == b; (a + b) * c;`)
	require.Len(t, stmts, 2)
	expr := asCall(stmts[0])
	assert.Empty(t, expr.Name)
	require.Len(t, expr.Args, 1)
	bin, ok := expr.Args[0].(*script.BinExpr)
	require.True(t, ok)
	assert.Equal(t, "==", bin.Op)

	expr2 := asCall(stmts[1])
	require.Len(t, expr2.Args, 1)
	assert.IsType(t, &script.BinExpr{}, expr2.Args[0])
}

func TestParseTernary(t *testing.T) {
	stmts := parseStmts(t, `.@x = .@a == 1 ? 10 : 20;`)
	require.Len(t, stmts, 1)
	assign := asAssign(stmts[0])
	ter, ok := assign.Rhs.(*script.TernaryExpr)
	require.True(t, ok)
	assert.Equal(t, int64(10), asInt(ter.Then))
	assert.Equal(t, int64(20), asInt(ter.Else))
}

func TestParsePostfixIncDec(t *testing.T) {
	stmts := parseStmts(t, `.@i++; .@j--;`)
	require.Len(t, stmts, 2)
	for i, op := range []string{"post++", "post--"} {
		exprStmt := asCall(stmts[i])
		require.Len(t, exprStmt.Args, 1)
		unary, ok := exprStmt.Args[0].(*script.UnaryExpr)
		require.True(t, ok)
		assert.Equal(t, op, unary.Op)
	}
}

func TestParseIf(t *testing.T) {
	stmts := parseStmts(t, `if (.@a == 1) mes "yes"; else mes "no";`)
	require.Len(t, stmts, 1)
	ifStmt := asIf(stmts[0])
	require.Len(t, ifStmt.Then, 1)
	require.Len(t, ifStmt.Else, 1)
	assert.Equal(t, "yes", asStr(asCall(ifStmt.Then[0]).Args[0]))
	assert.Equal(t, "no", asStr(asCall(ifStmt.Else[0]).Args[0]))
}

func TestParseIfBlock(t *testing.T) {
	stmts := parseStmts(t, `if (.@a == 1) { mes "yes"; next; }`)
	require.Len(t, stmts, 1)
	ifStmt := asIf(stmts[0])
	require.Len(t, ifStmt.Then, 2)
}

func TestParseWhile(t *testing.T) {
	stmts := parseStmts(t, `while (.@i < 3) { mes "loop"; .@i++; }`)
	require.Len(t, stmts, 1)
	w := asWhile(stmts[0])
	require.Len(t, w.Body, 2)
	assert.Equal(t, "mes", asCall(w.Body[0]).Name)
}

func TestParseDoWhile(t *testing.T) {
	stmts := parseStmts(t, `do { mes "once"; } while (0);`)
	require.Len(t, stmts, 1)
	dw := asDoWhile(stmts[0])
	require.Len(t, dw.Body, 1)
}

func TestParseFor(t *testing.T) {
	stmts := parseStmts(t, `for (.@i = 0; .@i < 3; .@i++) mes "loop";`)
	require.Len(t, stmts, 1)
	f := asFor(stmts[0])
	require.Len(t, f.Init, 1)
	require.Len(t, f.Post, 1)
	require.Len(t, f.Body, 1)
	init := asAssign(f.Init[0])
	assert.Equal(t, ".@i", asIdent(init.Lhs).Name)
}

func TestParseSwitch(t *testing.T) {
	stmts := parseStmts(t, `switch (.@a) { case 1: mes "one"; break; default: mes "other"; }`)
	require.Len(t, stmts, 1)
	sw := asSwitch(stmts[0])
	require.Len(t, sw.Cases, 2)
	assert.Len(t, sw.Cases[0].Values, 1)
	require.Len(t, sw.Cases[0].Body, 2)  // mes + break
	assert.Len(t, sw.Cases[1].Values, 0) // default
	require.Len(t, sw.Cases[1].Body, 1)
}

func TestParseBreakContinueReturn(t *testing.T) {
	stmts := parseStmts(t, `break; continue; return .@x; return;`)
	require.Len(t, stmts, 4)
	assert.IsType(t, &script.BreakStmt{}, stmts[0])
	assert.IsType(t, &script.ContinueStmt{}, stmts[1])
	ret := stmts[2].(*script.ReturnStmt)
	require.NotNil(t, ret.Value)
	assert.Nil(t, stmts[3].(*script.ReturnStmt).Value)
}

func TestParseGoto(t *testing.T) {
	stmts := parseStmts(t, `goto L_End;`)
	require.Len(t, stmts, 1)
	g := stmts[0].(*script.GotoStmt)
	assert.Equal(t, "L_End", g.Label)
}

func TestParseCallSub(t *testing.T) {
	stmts := parseStmts(t, `callsub S_Sub, 1, 2;`)
	require.Len(t, stmts, 1)
	cs := stmts[0].(*script.CallSubStmt)
	assert.Equal(t, "S_Sub", cs.Label)
	require.Len(t, cs.Args, 2)
}

func TestParseLabel(t *testing.T) {
	stmts := parseStmts(t, `L_Label: mes "labeled";`)
	require.Len(t, stmts, 2)
	lbl := stmts[0].(*script.LabelDecl)
	assert.Equal(t, "L_Label", lbl.Name)
	assert.Equal(t, "mes", asCall(stmts[1]).Name)
}

func TestParseFunctionScript(t *testing.T) {
	stmts := parseStmts(t, `function script F_Test { mes "hi"; }`)
	require.Len(t, stmts, 1)
	fd := stmts[0].(*script.FuncDecl)
	assert.Equal(t, "F_Test", fd.Name)
	require.Len(t, fd.Body, 1)
}

func TestParseFunctionScriptEntryPoint(t *testing.T) {
	src := `function script F_Test { mes "hi"; }`
	p := New(mustLex(t, src))
	fd, err := p.ParseFunctionScript()
	require.NoError(t, err)
	assert.Equal(t, "F_Test", fd.Name)
}

func TestParseMenu(t *testing.T) {
	stmts := parseStmts(t, `menu "A",L_A,"B",L_B;`)
	require.Len(t, stmts, 1)
	menu := asMenu(stmts[0])
	require.Len(t, menu.Options, 2)
	assert.Equal(t, "L_A", menu.Options[0].Label)
	assert.Equal(t, "L_B", menu.Options[1].Label)
}

func TestParseFileNPC(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	path := filepath.Join(filepath.Dir(filename), "testdata", "kafras.txt")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("kafras.txt fixture not available at %s: %v", path, err)
	}
	src, err := os.ReadFile(path)
	require.NoError(t, err)
	tokens, err := script.Lex(src)
	require.NoError(t, err)
	p := NewWithSource(src, tokens)
	file, err := p.ParseFile()
	require.NoError(t, err)
	h := file.Header()
	require.NotNil(t, h)
	assert.Equal(t, "dewata", h.MapName)
	assert.Equal(t, "Kafra Employee", h.Name)
	assert.Equal(t, "kaf_dewata", h.SpriteName)
	require.Len(t, file.Body, 4)
	assert.Equal(t, "cutin", asCall(file.Body[0]).Name)
	assert.Equal(t, "callfunc", asCall(file.Body[1]).Name)
	assert.Equal(t, "savepoint", asCall(file.Body[2]).Name)
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr string
	}{
		{"missing semicolon", `mes "hello"`, "expected `;`"},
		{"unclosed brace", `{ mes "hello";`, "unexpected end of file"},
		{"bad if cond", `if mes "x";`, "expected `(`"},
		{"expected expression", `= 1;`, "expected statement"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := New(mustLex(t, tc.src))
			_, err := p.ParseStmts()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestMaxDepth(t *testing.T) {
	src := "if (1) "
	for i := 0; i < 110; i++ {
		src += "if (1) "
	}
	src += "mes \"x\";"
	p := New(mustLex(t, src))
	_, err := p.ParseStmts()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maximum nesting depth")
}

func TestParseStmtsEOF(t *testing.T) {
	stmts := parseStmts(t, ``)
	assert.Empty(t, stmts)
}

// ----- AST helpers -----

func asMenu(s script.Stmt) *script.MenuStmt {
	m, ok := s.(*script.MenuStmt)
	if !ok {
		panicf("expected *MenuStmt, got %T", s)
	}
	return m
}

func asCall(s script.Stmt) *script.CallStmt {
	c, ok := s.(*script.CallStmt)
	if !ok {
		panicf("expected *CallStmt, got %T", s)
	}
	return c
}

func asAssign(s script.Stmt) *script.AssignStmt {
	a, ok := s.(*script.AssignStmt)
	if !ok {
		panicf("expected *AssignStmt, got %T", s)
	}
	return a
}

func asIf(s script.Stmt) *script.IfStmt {
	i, ok := s.(*script.IfStmt)
	if !ok {
		panicf("expected *IfStmt, got %T", s)
	}
	return i
}

func asWhile(s script.Stmt) *script.WhileStmt {
	w, ok := s.(*script.WhileStmt)
	if !ok {
		panicf("expected *WhileStmt, got %T", s)
	}
	return w
}

func asDoWhile(s script.Stmt) *script.DoWhileStmt {
	d, ok := s.(*script.DoWhileStmt)
	if !ok {
		panicf("expected *DoWhileStmt, got %T", s)
	}
	return d
}

func asFor(s script.Stmt) *script.ForStmt {
	f, ok := s.(*script.ForStmt)
	if !ok {
		panicf("expected *ForStmt, got %T", s)
	}
	return f
}

func asSwitch(s script.Stmt) *script.SwitchStmt {
	sw, ok := s.(*script.SwitchStmt)
	if !ok {
		panicf("expected *SwitchStmt, got %T", s)
	}
	return sw
}

func asIdent(e script.Expr) *script.IdentExpr {
	i, ok := e.(*script.IdentExpr)
	if !ok {
		panicf("expected *IdentExpr, got %T", e)
	}
	return i
}

func asInt(e script.Expr) int64 {
	switch v := e.(type) {
	case *script.IntLit:
		return v.Value
	case *script.IdentExpr:
		panicf("expected integer literal, got ident %q", v.Name)
	default:
		panicf("expected *IntLit, got %T", e)
	}
	panic("unreachable")
}

func asStr(e script.Expr) string {
	s, ok := e.(*script.StrLit)
	if !ok {
		panicf("expected *StrLit, got %T", e)
	}
	return s.Value
}

func panicf(format string, args ...any) {
	panic(fmt.Sprintf(format, args...))
}
