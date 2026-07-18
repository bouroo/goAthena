//go:build unit

package compiler

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/features/script/parser"
	"github.com/bouroo/goAthena/pkg/ro/script"
)

func mustCompile(t *testing.T, src string) *script.CompiledScript {
	t.Helper()
	tokens, err := script.Lex([]byte(src))
	require.NoError(t, err)
	stmts, err := parser.New(tokens).ParseStmts()
	require.NoError(t, err)
	cs, err := New().Compile("test", stmts)
	require.NoError(t, err)
	return cs
}

func TestCompileMes(t *testing.T) {
	cs := mustCompile(t, `mes "Hello";`)
	require.Len(t, cs.Instructions, 3)
	assert.Equal(t, script.OpStr, cs.Instructions[0].Op)
	assert.Equal(t, "Hello", cs.Instructions[0].Str)
	assert.Equal(t, script.OpFunc, cs.Instructions[1].Op)
	assert.Equal(t, "mes", cs.Instructions[1].Str)
	assert.Equal(t, script.OpEnd, cs.Instructions[2].Op)
}

func TestCompileSet(t *testing.T) {
	cs := mustCompile(t, `set .@var, 42;`)
	require.Len(t, cs.Instructions, 3)
	assert.Equal(t, script.OpInt, cs.Instructions[0].Op)
	assert.Equal(t, int32(42), cs.Instructions[0].Operand)
	assert.Equal(t, script.OpAssign, cs.Instructions[1].Op)
	assert.Equal(t, ".@var", cs.Instructions[1].Str)
}

func TestCompileIf(t *testing.T) {
	cs := mustCompile(t, `if (.@a == 1) mes "yes"; else mes "no";`)
	ops := make([]script.Opcode, len(cs.Instructions))
	for i, ins := range cs.Instructions {
		ops[i] = ins.Op
	}
	assert.Contains(t, ops, script.OpLEq)
	assert.Contains(t, ops, script.OpGoto)
	assert.Contains(t, ops, script.OpLabel)
	assert.Contains(t, ops, script.OpStr)
	assert.Contains(t, ops, script.OpFunc)
}

func TestCompileWhile(t *testing.T) {
	cs := mustCompile(t, `while (.@i < 3) { mes "loop"; .@i++; }`)
	ops := make([]script.Opcode, len(cs.Instructions))
	for i, ins := range cs.Instructions {
		ops[i] = ins.Op
	}
	assert.Contains(t, ops, script.OpLLT)
	assert.Contains(t, ops, script.OpGoto)
	assert.Contains(t, ops, script.OpLabel)
}

func TestCompileExpression(t *testing.T) {
	cs := mustCompile(t, `.@r = a + b * c;`)
	require.GreaterOrEqual(t, len(cs.Instructions), 6)
	// Stack order: a, b, c, MUL, ADD, ASSIGN.
	assert.Equal(t, script.OpVar, cs.Instructions[0].Op)
	assert.Equal(t, script.OpVar, cs.Instructions[1].Op)
	assert.Equal(t, script.OpVar, cs.Instructions[2].Op)
	assert.Equal(t, script.OpMul, cs.Instructions[3].Op)
	assert.Equal(t, script.OpAdd, cs.Instructions[4].Op)
	assert.Equal(t, script.OpAssign, cs.Instructions[5].Op)
}

func TestCompileGotoAndLabel(t *testing.T) {
	cs := mustCompile(t, `goto L_End; mes "skip"; L_End: mes "done";`)
	require.Len(t, cs.Instructions, 7)
	assert.Equal(t, script.OpGoto, cs.Instructions[0].Op)
	assert.Equal(t, "L_End", cs.Instructions[0].Str)
	assert.Equal(t, script.OpStr, cs.Instructions[1].Op)
	assert.Equal(t, script.OpFunc, cs.Instructions[2].Op)
	assert.Equal(t, "mes", cs.Instructions[2].Str)
	assert.Equal(t, script.OpLabel, cs.Instructions[3].Op)
	assert.Equal(t, "L_End", cs.Instructions[3].Str)
	assert.Equal(t, script.OpStr, cs.Instructions[4].Op)
	assert.Equal(t, "done", cs.Instructions[4].Str)
	assert.Equal(t, script.OpFunc, cs.Instructions[5].Op)
	assert.Equal(t, "mes", cs.Instructions[5].Str)
	assert.Equal(t, script.OpEnd, cs.Instructions[6].Op)

	// The goto operand should have been backpatched to the label index.
	idx, ok := cs.Labels["L_End"]
	require.True(t, ok)
	assert.Equal(t, int32(idx), cs.Instructions[0].Operand)
}

func TestCompileCallfunc(t *testing.T) {
	cs := mustCompile(t, `callfunc "F_Kafra", 0, 10;`)
	require.Len(t, cs.Instructions, 5)
	assert.Equal(t, script.OpStr, cs.Instructions[0].Op)
	assert.Equal(t, "F_Kafra", cs.Instructions[0].Str)
	assert.Equal(t, script.OpInt, cs.Instructions[1].Op)
	assert.Equal(t, int32(0), cs.Instructions[1].Operand)
	assert.Equal(t, script.OpInt, cs.Instructions[2].Op)
	assert.Equal(t, int32(10), cs.Instructions[2].Operand)
	assert.Equal(t, script.OpFunc, cs.Instructions[3].Op)
	assert.Equal(t, "callfunc", cs.Instructions[3].Str)
	assert.Equal(t, script.OpEnd, cs.Instructions[4].Op)
}

func TestCompileBreakContinue(t *testing.T) {
	cs := mustCompile(t, `while (1) { if (.@x) break; continue; }`)
	ops := make([]script.Opcode, len(cs.Instructions))
	for i, ins := range cs.Instructions {
		ops[i] = ins.Op
	}
	assert.Contains(t, ops, script.OpGoto)
	assert.Contains(t, ops, script.OpLabel)
}

func TestCompileRealKafraBody(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	path := filepath.Join(filepath.Dir(filename), "..", "parser", "testdata", "kafras.txt")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("kafras.txt fixture not available at %s: %v", path, err)
	}
	src, err := os.ReadFile(path)
	require.NoError(t, err)
	tokens, err := script.Lex(src)
	require.NoError(t, err)
	p := parser.NewWithSource(src, tokens)
	file, err := p.ParseFile()
	require.NoError(t, err)
	cs, err := New().Compile(file.Header().Name, file.Body)
	require.NoError(t, err)
	require.NotEmpty(t, cs.Instructions)
}

func TestCompileUnresolvedLabelError(t *testing.T) {
	tokens, err := script.Lex([]byte(`goto L_Missing;`))
	require.NoError(t, err)
	stmts, err := parser.New(tokens).ParseStmts()
	require.NoError(t, err)
	_, err = New().Compile("test", stmts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unresolved label")
	assert.Contains(t, err.Error(), "L_Missing")
}

func TestCompileSwitch(t *testing.T) {
	cs := mustCompile(t, `switch (.@a) { case 1: mes "one"; break; default: mes "other"; }`)
	ops := make([]script.Opcode, len(cs.Instructions))
	for i, ins := range cs.Instructions {
		ops[i] = ins.Op
	}
	assert.Contains(t, ops, script.OpLEq)
	assert.Contains(t, ops, script.OpGoto)
	assert.Contains(t, ops, script.OpLabel)
	assert.Contains(t, ops, script.OpStr)
}

func TestCompileFor(t *testing.T) {
	cs := mustCompile(t, `for (.@i = 0; .@i < 3; .@i++) mes "loop";`)
	ops := make([]script.Opcode, len(cs.Instructions))
	for i, ins := range cs.Instructions {
		ops[i] = ins.Op
	}
	assert.Contains(t, ops, script.OpAssign)
	assert.Contains(t, ops, script.OpLLT)
	assert.Contains(t, ops, script.OpGoto)
	assert.Contains(t, ops, script.OpLabel)
}

func TestCompileDoWhile(t *testing.T) {
	cs := mustCompile(t, `do { mes "once"; } while (0);`)
	ops := make([]script.Opcode, len(cs.Instructions))
	for i, ins := range cs.Instructions {
		ops[i] = ins.Op
	}
	assert.Contains(t, ops, script.OpStr)
	assert.Contains(t, ops, script.OpFunc)
	assert.Contains(t, ops, script.OpGoto)
	assert.Contains(t, ops, script.OpLabel)
}

func TestCompileReturn(t *testing.T) {
	cs := mustCompile(t, `return .@x;`)
	require.Len(t, cs.Instructions, 3)
	assert.Equal(t, script.OpVar, cs.Instructions[0].Op)
	assert.Equal(t, ".@x", cs.Instructions[0].Str)
	assert.Equal(t, script.OpReturn, cs.Instructions[1].Op)
	assert.Equal(t, script.OpEnd, cs.Instructions[2].Op)
}

func TestCompileCallsub(t *testing.T) {
	cs := mustCompile(t, `callsub S_Sub, 1, 2;`)
	require.Len(t, cs.Instructions, 4)
	assert.Equal(t, script.OpInt, cs.Instructions[0].Op)
	assert.Equal(t, script.OpInt, cs.Instructions[1].Op)
	assert.Equal(t, script.OpCallSub, cs.Instructions[2].Op)
	assert.Equal(t, "S_Sub", cs.Instructions[2].Str)
	assert.Equal(t, script.OpEnd, cs.Instructions[3].Op)
}

func TestCompileNestedIf(t *testing.T) {
	cs := mustCompile(t, `if (1) if (2) mes "x";`)
	require.NotEmpty(t, cs.Instructions)
	assert.Equal(t, script.OpEnd, cs.Instructions[len(cs.Instructions)-1].Op)
}

func TestCompileBlockStmt(t *testing.T) {
	cs := mustCompile(t, `{ mes "a"; next; }`)
	require.NotEmpty(t, cs.Instructions)
	assert.Equal(t, script.OpEnd, cs.Instructions[len(cs.Instructions)-1].Op)
}

func TestCompileArrayAssignment(t *testing.T) {
	cs := mustCompile(t, `.@arr[0] = 42;`)
	require.GreaterOrEqual(t, len(cs.Instructions), 4)
	assert.Equal(t, script.OpInt, cs.Instructions[0].Op)
	assert.Equal(t, int32(42), cs.Instructions[0].Operand)
	assert.Equal(t, script.OpInt, cs.Instructions[1].Op)
	assert.Equal(t, int32(0), cs.Instructions[1].Operand)
	assert.Equal(t, script.OpStr, cs.Instructions[2].Op)
	assert.Equal(t, ".@arr", cs.Instructions[2].Str)
	assert.Equal(t, script.OpIndexSet, cs.Instructions[3].Op)
}

func TestCompileCompoundAssignment(t *testing.T) {
	cs := mustCompile(t, `.@a += 2;`)
	require.Len(t, cs.Instructions, 3)
	assert.Equal(t, script.OpInt, cs.Instructions[0].Op)
	assert.Equal(t, script.OpAssignAdd, cs.Instructions[1].Op)
}

func TestCompileTernary(t *testing.T) {
	cs := mustCompile(t, `.@x = .@a == 1 ? 10 : 20;`)
	ops := make([]script.Opcode, len(cs.Instructions))
	for i, ins := range cs.Instructions {
		ops[i] = ins.Op
	}
	assert.Contains(t, ops, script.OpLEq)
	assert.Contains(t, ops, script.OpGoto)
	assert.Contains(t, ops, script.OpLabel)
}

func TestCompileLogicalShortCircuit(t *testing.T) {
	cs := mustCompile(t, `.@x = .@a && .@b || .@c;`)
	ops := make([]script.Opcode, len(cs.Instructions))
	for i, ins := range cs.Instructions {
		ops[i] = ins.Op
	}
	assert.Contains(t, ops, script.OpLAnd)
	assert.Contains(t, ops, script.OpLOr)
}

func TestCompileUnaryExpressions(t *testing.T) {
	cs := mustCompile(t, `.@x = -.@a; .@y = !.@b; .@z = ~.@c;`)
	ops := make([]script.Opcode, len(cs.Instructions))
	for i, ins := range cs.Instructions {
		ops[i] = ins.Op
	}
	assert.Contains(t, ops, script.OpNeg)
	assert.Contains(t, ops, script.OpNot)
	assert.Contains(t, ops, script.OpBNot)
}

func TestCompileCallExpr(t *testing.T) {
	cs := mustCompile(t, `.@r = foo(1, 2);`)
	ops := make([]script.Opcode, len(cs.Instructions))
	for i, ins := range cs.Instructions {
		ops[i] = ins.Op
	}
	assert.Contains(t, ops, script.OpFunc)
}

func TestCompileFunctionScript(t *testing.T) {
	cs := mustCompile(t, `function script F_Test { mes "hi"; }`)
	require.NotEmpty(t, cs.Instructions)
	idx, ok := cs.Labels["F_Test"]
	require.True(t, ok)
	assert.Equal(t, script.OpLabel, cs.Instructions[idx].Op)
}

func TestCompileBreakOutsideLoopError(t *testing.T) {
	tokens, err := script.Lex([]byte(`break;`))
	require.NoError(t, err)
	stmts, err := parser.New(tokens).ParseStmts()
	require.NoError(t, err)
	_, err = New().Compile("test", stmts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "break outside loop")
}

func TestCompileContinueOutsideLoopError(t *testing.T) {
	tokens, err := script.Lex([]byte(`continue;`))
	require.NoError(t, err)
	stmts, err := parser.New(tokens).ParseStmts()
	require.NoError(t, err)
	_, err = New().Compile("test", stmts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "continue outside loop")
}

func TestCompileInvalidSetTargetError(t *testing.T) {
	stmts := []script.Stmt{
		script.NewCallStmt("set", []script.Expr{
			script.NewIntLit(1, script.Position{Line: 1}),
			script.NewIntLit(2, script.Position{Line: 1}),
		}, script.Position{Line: 1}),
	}
	_, err := New().Compile("test", stmts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid set target")
}

func TestCompileMesStmtNode(t *testing.T) {
	stmts := []script.Stmt{
		script.NewMesStmt(script.NewStrLit("hello", script.Position{Line: 1}), script.Position{Line: 1}),
	}
	cs, err := New().Compile("test", stmts)
	require.NoError(t, err)
	require.Len(t, cs.Instructions, 3)
	assert.Equal(t, script.OpStr, cs.Instructions[0].Op)
	assert.Equal(t, script.OpFunc, cs.Instructions[1].Op)
}

func TestCompileMenuStmtNode(t *testing.T) {
	stmts := []script.Stmt{
		script.NewMenuStmt([]script.MenuOption{
			script.NewMenuOption(script.NewStrLit("A", script.Position{Line: 1}), "L_A", script.Position{Line: 1}),
			script.NewMenuOption(script.NewStrLit("B", script.Position{Line: 1}), "L_B", script.Position{Line: 1}),
		}, script.Position{Line: 1}),
	}
	cs, err := New().Compile("test", stmts)
	require.NoError(t, err)
	require.NotEmpty(t, cs.Instructions)
	assert.Equal(t, script.OpFunc, cs.Instructions[len(cs.Instructions)-2].Op)
}

func TestCompileSelectStmtNode(t *testing.T) {
	stmts := []script.Stmt{
		script.NewSelectStmt([]script.MenuOption{
			script.NewMenuOption(script.NewStrLit("A", script.Position{Line: 1}), "L_A", script.Position{Line: 1}),
		}, script.Position{Line: 1}),
	}
	cs, err := New().Compile("test", stmts)
	require.NoError(t, err)
	require.NotEmpty(t, cs.Instructions)
	assert.Equal(t, script.OpFunc, cs.Instructions[len(cs.Instructions)-2].Op)
}

func TestCompileInputStmtNode(t *testing.T) {
	stmts := []script.Stmt{
		script.NewInputStmt(script.NewIdentExpr("@var", script.Position{Line: 1}), script.Position{Line: 1}),
	}
	cs, err := New().Compile("test", stmts)
	require.NoError(t, err)
	require.NotEmpty(t, cs.Instructions)
	assert.Equal(t, script.OpFunc, cs.Instructions[len(cs.Instructions)-2].Op)
}
