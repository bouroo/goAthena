//go:build unit

package vm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/features/script/compiler"
	"github.com/bouroo/goAthena/internal/features/script/parser"
	"github.com/bouroo/goAthena/pkg/ro/script"
)

func newVM(t *testing.T, code *script.CompiledScript) *VM {
	t.Helper()
	return New(code, NewScopeStore(), defaultBuiltins(t))
}

func defaultBuiltins(t *testing.T) *BuiltinRegistry {
	t.Helper()
	reg := NewBuiltinRegistry()
	reg.RegisterDefaults()
	return reg
}

func mustCompile(t *testing.T, src string) *script.CompiledScript {
	t.Helper()
	tokens, err := script.Lex([]byte(src))
	require.NoError(t, err)
	stmts, err := parser.New(tokens).ParseStmts()
	require.NoError(t, err)
	cs, err := compiler.New().Compile("test", stmts)
	require.NoError(t, err)
	return cs
}

func run(t *testing.T, vm *VM) State {
	t.Helper()
	state, err := vm.Run(context.Background())
	require.NoError(t, err)
	return state
}

func TestStackPushPop(t *testing.T) {
	vm := New(script.NewCompiledScript("empty"), nil, nil)
	vm.push(IntValue(1))
	vm.push(StrValue("hello"))
	assert.Equal(t, 2, len(vm.stack))

	v := vm.pop()
	assert.True(t, v.IsString)
	assert.Equal(t, "hello", v.Str)

	v = vm.pop()
	assert.False(t, v.IsString)
	assert.Equal(t, int64(1), v.Int)

	// Popping an empty stack returns 0.
	v = vm.pop()
	assert.Equal(t, int64(0), v.Int)
}

func TestArithmeticBytecode(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		expected int64
	}{
		{"add", ".@r = 3 + 4;", 7},
		{"sub", ".@r = 10 - 2;", 8},
		{"mul", ".@r = 3 * 4;", 12},
		{"div", ".@r = 20 / 4;", 5},
		{"mod", ".@r = 10 % 3;", 1},
		{"neg", ".@r = -7;", -7},
		{"bitand", ".@r = 5 & 3;", 1},
		{"bitor", ".@r = 5 | 2;", 7},
		{"bitxor", ".@r = 5 ^ 3;", 6},
		{"shl", ".@r = 1 << 3;", 8},
		{"shr", ".@r = 8 >> 1;", 4},
		{"bnot", ".@r = ~0;", -1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cs := mustCompile(t, tc.src)
			vm := newVM(t, cs)
			run(t, vm)
			v, ok := vm.scopes.Get(".@r")
			require.True(t, ok)
			assert.Equal(t, tc.expected, v.AsInt())
		})
	}
}

func TestDivModByZero(t *testing.T) {
	cs := mustCompile(t, ".@r = 10 / 0; .@s = 10 % 0;")
	vm := newVM(t, cs)
	run(t, vm)

	v, _ := vm.scopes.Get(".@r")
	assert.Equal(t, int64(0), v.AsInt())
	v, _ = vm.scopes.Get(".@s")
	assert.Equal(t, int64(0), v.AsInt())
}

func TestComparisonBytecode(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		expected int64
	}{
		{"eq", ".@r = 3 == 3;", 1},
		{"neq", ".@r = 3 != 3;", 0},
		{"lt", ".@r = 2 < 5;", 1},
		{"gt", ".@r = 2 > 5;", 0},
		{"le", ".@r = 5 <= 5;", 1},
		{"ge", ".@r = 4 >= 5;", 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cs := mustCompile(t, tc.src)
			vm := newVM(t, cs)
			run(t, vm)
			v, ok := vm.scopes.Get(".@r")
			require.True(t, ok)
			assert.Equal(t, tc.expected, v.AsInt())
		})
	}
}

func TestLogical(t *testing.T) {
	cs := mustCompile(t, `.@a = 1 && 0; .@b = 1 || 0; .@c = !1; .@d = !0;`)
	vm := newVM(t, cs)
	run(t, vm)

	v, _ := vm.scopes.Get(".@a")
	assert.Equal(t, int64(0), v.AsInt())
	v, _ = vm.scopes.Get(".@b")
	assert.Equal(t, int64(1), v.AsInt())
	v, _ = vm.scopes.Get(".@c")
	assert.Equal(t, int64(0), v.AsInt())
	v, _ = vm.scopes.Get(".@d")
	assert.Equal(t, int64(1), v.AsInt())
}

func TestLogicalShortCircuit(t *testing.T) {
	// These compile to branch instructions; just verify they evaluate to the
	// correct truth values.
	cs := mustCompile(t, `.@x = 0 && 0 || 1;`)
	vm := newVM(t, cs)
	run(t, vm)
	v, _ := vm.scopes.Get(".@x")
	assert.Equal(t, int64(1), v.AsInt())
}

func TestVariableScopes(t *testing.T) {
	cs := mustCompile(t, `
		.@a = 1;
		@b = 2;
		#c = 3;
		$d = 4;
		$@e = 5;
		'f = 6;
		g = 7;
	`)
	vm := newVM(t, cs)
	run(t, vm)

	assertScope(t, vm, ".@a", int64(1))
	assertScope(t, vm, "@b", int64(2))
	assertScope(t, vm, "#c", int64(3))
	assertScope(t, vm, "$d", int64(4))
	assertScope(t, vm, "$@e", int64(5))
	assertScope(t, vm, "'f", int64(6))
	assertScope(t, vm, "g", int64(7))
}

func assertScope(t *testing.T, vm *VM, name string, expected int64) {
	t.Helper()
	v, ok := vm.scopes.Get(name)
	require.True(t, ok, "variable %s not found", name)
	assert.Equal(t, expected, v.AsInt(), "variable %s", name)
}

func TestGoto(t *testing.T) {
	cs := mustCompile(t, `
		goto L_End;
		.@r = 1;
		L_End:
		.@r = 2;
	`)
	vm := newVM(t, cs)
	run(t, vm)
	v, _ := vm.scopes.Get(".@r")
	assert.Equal(t, int64(2), v.AsInt())
}

func TestCallsubReturn(t *testing.T) {
	cs := mustCompile(t, `
		callsub S_Add;
		.@r = .@x;
		end;
		S_Add:
		.@x = 42;
		return;
	`)
	vm := newVM(t, cs)
	run(t, vm)
	v, _ := vm.scopes.Get(".@r")
	assert.Equal(t, int64(42), v.AsInt())
}

func TestCallsubReturnFromBottom(t *testing.T) {
	// A return with an empty call stack ends the script.
	cs := mustCompile(t, `.@x = 1; return; .@x = 2;`)
	vm := newVM(t, cs)
	state := run(t, vm)
	assert.Equal(t, StateEnd, state)
	v, _ := vm.scopes.Get(".@x")
	assert.Equal(t, int64(1), v.AsInt())
}

func TestBuiltinMes(t *testing.T) {
	cs := mustCompile(t, `mes "Hello";`)
	vm := newVM(t, cs)
	state := run(t, vm)
	assert.Equal(t, StateEnd, state)
}

func TestBuiltinNextStopAndResume(t *testing.T) {
	cs := mustCompile(t, `mes "A"; next; mes "B";`)
	vm := newVM(t, cs)
	state, err := vm.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, StateStop, state)

	state, err = vm.Resume(context.Background())
	require.NoError(t, err)
	assert.Equal(t, StateEnd, state)
}

func TestBuiltinCloseEnds(t *testing.T) {
	cs := mustCompile(t, `mes "A"; close; mes "B";`)
	vm := newVM(t, cs)
	state := run(t, vm)
	assert.Equal(t, StateEnd, state)
}

func TestStateTransitions(t *testing.T) {
	cs := mustCompile(t, `mes "A"; next; close;`)
	vm := newVM(t, cs)

	assert.Equal(t, StateRun, vm.State())
	state, err := vm.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, StateStop, state)

	state, err = vm.Resume(context.Background())
	require.NoError(t, err)
	assert.Equal(t, StateEnd, state)
}

func TestInstructionLimit(t *testing.T) {
	// Infinite loop via goto.
	cs := mustCompile(t, `L_Loop: goto L_Loop;`)
	vm := newVM(t, cs)
	vm.SetMaxInstr(10)
	state, err := vm.Run(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "instruction limit exceeded")
	assert.Equal(t, StateRun, state)
}

func TestFullScriptExecution(t *testing.T) {
	cs := mustCompile(t, `mes "Hello"; next; close;`)
	vm := newVM(t, cs)

	state, err := vm.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, StateStop, state)

	state, err = vm.Resume(context.Background())
	require.NoError(t, err)
	assert.Equal(t, StateEnd, state)
}

func TestBuiltinSet(t *testing.T) {
	cs := mustCompile(t, `set .@v, 123;`)
	vm := newVM(t, cs)
	run(t, vm)
	v, ok := vm.scopes.Get(".@v")
	require.True(t, ok)
	assert.Equal(t, int64(123), v.AsInt())
}

func TestCompoundAssignment(t *testing.T) {
	cs := mustCompile(t, `.@x = 5; .@x += 3; .@x -= 1; .@x *= 2; .@x /= 2; .@x %= 3;`)
	vm := newVM(t, cs)
	run(t, vm)
	v, _ := vm.scopes.Get(".@x")
	// 5+3=8, -1=7, *2=14, /2=7, %3=1
	assert.Equal(t, int64(1), v.AsInt())
}

func TestIfElse(t *testing.T) {
	cs := mustCompile(t, `if (1) .@a = 1; else .@a = 2; if (0) .@b = 1; else .@b = 2;`)
	vm := newVM(t, cs)
	run(t, vm)
	a, _ := vm.scopes.Get(".@a")
	b, _ := vm.scopes.Get(".@b")
	assert.Equal(t, int64(1), a.AsInt())
	assert.Equal(t, int64(2), b.AsInt())
}

func TestWhileLoop(t *testing.T) {
	cs := mustCompile(t, `.@i = 0; while (.@i < 5) .@i++;`)
	vm := newVM(t, cs)
	run(t, vm)
	v, _ := vm.scopes.Get(".@i")
	assert.Equal(t, int64(5), v.AsInt())
}

func TestRandBuiltin(t *testing.T) {
	cs := mustCompile(t, `.@r = rand(10);`)
	vm := newVM(t, cs)
	run(t, vm)
	v, _ := vm.scopes.Get(".@r")
	assert.True(t, v.AsInt() >= 0 && v.AsInt() < 10)
}

func TestUnknownBuiltinError(t *testing.T) {
	reg := NewBuiltinRegistry()
	vm := New(script.NewCompiledScript("test"), nil, reg)
	vm.script.Instructions = []script.Instruction{
		{Op: script.OpFunc, Str: "not_a_builtin"},
		{Op: script.OpEnd},
	}
	_, err := vm.Run(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown builtin")
}

func TestVMWithEmptyScript(t *testing.T) {
	vm := New(script.NewCompiledScript("empty"), nil, defaultBuiltins(t))
	state := run(t, vm)
	assert.Equal(t, StateEnd, state)
}
