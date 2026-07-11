//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/features/script/compiler"
	"github.com/bouroo/goAthena/internal/features/script/parser"
	"github.com/bouroo/goAthena/internal/features/script/vm"
	"github.com/bouroo/goAthena/pkg/ro/script"
)

func compileScript(t *testing.T, name, src string) *script.CompiledScript {
	t.Helper()
	tokens, err := script.Lex([]byte(src))
	require.NoError(t, err)
	stmts, err := parser.New(tokens).ParseStmts()
	require.NoError(t, err)
	cs, err := compiler.New().Compile(name, stmts)
	require.NoError(t, err)
	return cs
}

func newSet(t *testing.T, scripts map[string]string) *script.CompiledScriptSet {
	t.Helper()
	set := script.NewCompiledScriptSet()
	for name, src := range scripts {
		set.Scripts[name] = compileScript(t, name, src)
	}
	return set
}

func nopLogger() *zerolog.Logger {
	l := zerolog.Nop()
	return &l
}

func TestRunOnInit_NilSet(t *testing.T) {
	scopes, ran, errs := RunOnInit(context.Background(), nil, nopLogger())
	require.NotNil(t, scopes, "ScopeStore is always returned so callers can persist it")
	assert.Equal(t, 0, ran)
	assert.Nil(t, errs)
}

func TestRunOnInit_RunsOnlyOnInitScripts(t *testing.T) {
	set := newSet(t, map[string]string{
		"A": `OnInit:
			set $aRan, 1;
			end;`,
		"B": `mes "hi";
			close;`,
	})

	scopes := vm.NewScopeStore()
	ran, errs := runOnInitWithScope(context.Background(), set, scopes, nopLogger())

	require.Empty(t, errs)
	assert.Equal(t, 1, ran, "only script A defines OnInit")

	v, ok := scopes.Get("$aRan")
	require.True(t, ok, "A's OnInit should have set $aRan")
	assert.Equal(t, int64(1), v.AsInt())
}

func TestRunOnInit_CollectsErrorsNonFatally(t *testing.T) {
	// An unknown builtin compiles fine (compiler emits OpFunc with the name
	// verbatim) but the VM errors at runtime with "unknown builtin". This is
	// the cleanest reproducible failure for the error-collection path.
	set := newSet(t, map[string]string{
		"Bad": `OnInit:
			unknownfunc();
			end;`,
		"Good": `OnInit:
			set $goodRan, 1;
			end;`,
	})

	scopes := vm.NewScopeStore()
	ran, errs := runOnInitWithScope(context.Background(), set, scopes, nopLogger())

	assert.Equal(t, 2, ran, "both OnInit scripts are attempted")
	require.Len(t, errs, 1, "exactly one script should fail")
	assert.Contains(t, errs[0].Error(), "unknown builtin")
	assert.Contains(t, errs[0].Error(), "OnInit Bad")

	v, ok := scopes.Get("$goodRan")
	require.True(t, ok, "Good's OnInit should still have run after Bad failed")
	assert.Equal(t, int64(1), v.AsInt())
}

func TestRunOnInit_SharedScopeVisibleAcrossScripts(t *testing.T) {
	// Two independent writes from two OnInit scripts both end up in the
	// same shared ScopeStore. This proves the runner threads a single store
	// through every VM (without needing cross-script ordering).
	set := newSet(t, map[string]string{
		"A": `OnInit:
			set $aRan, 1;
			end;`,
		"B": `OnInit:
			set $bRan, 1;
			end;`,
	})

	scopes := vm.NewScopeStore()
	ran, errs := runOnInitWithScope(context.Background(), set, scopes, nopLogger())

	require.Empty(t, errs)
	assert.Equal(t, 2, ran)

	a, ok := scopes.Get("$aRan")
	require.True(t, ok)
	assert.Equal(t, int64(1), a.AsInt())

	b, ok := scopes.Get("$bRan")
	require.True(t, ok)
	assert.Equal(t, int64(1), b.AsInt())
}
