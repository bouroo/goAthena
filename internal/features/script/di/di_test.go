//go:build unit

package di_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"
	"github.com/samber/do/v2"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/config"
	"github.com/bouroo/goAthena/internal/features/script"
	scriptdi "github.com/bouroo/goAthena/internal/features/script/di"
)

func newInjector(t *testing.T, scriptDir string) do.Injector {
	t.Helper()
	inj := do.New()
	cfg := &config.Config{Zone: config.ZoneConfig{ScriptDir: scriptDir}}
	do.ProvideValue(inj, cfg)
	logger := zerolog.Nop()
	do.ProvideValue(inj, &logger)
	return inj
}

func TestRegister_EmptyScriptDir_DegradesGracefully(t *testing.T) {
	t.Parallel()

	inj := newInjector(t, "")
	require.NoError(t, scriptdi.Register(inj))

	engine, err := scriptdi.ProvideEngine(inj)
	require.NoError(t, err)
	require.NotNil(t, engine)
	require.Equal(t, 0, len(engine.Current().Scripts))
	require.Equal(t, 0, len(engine.Current().Funcs))
	require.Equal(t, 0, len(engine.Current().Warps))
	require.Equal(t, 0, len(engine.Current().Shops))
}

func TestRegister_LoadsFixtureDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	contents := "prontera,150,150,4\tscript\tSampleNPC\t4_M_01,{\n" +
		"\tmes \"Hello, world!\";\n" +
		"\tclose;\n" +
		"\n" +
		"OnInit:\n" +
		"\tset $sampleInit, 1;\n" +
		"\tend;\n" +
		"}\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sample.txt"), []byte(contents), 0o600))

	inj := newInjector(t, dir)
	require.NoError(t, scriptdi.Register(inj))

	engine, err := scriptdi.ProvideEngine(inj)
	require.NoError(t, err)
	require.NotNil(t, engine)

	set := engine.Current()
	require.NotEmpty(t, set.Scripts, "expected at least one compiled NPC script")
	cs, ok := set.Scripts["SampleNPC"]
	require.True(t, ok, "expected SampleNPC in compiled set, got keys: %v", keys(set.Scripts))
	require.NotNil(t, cs)

	// Reload must be idempotent and produce a usable set.
	require.NoError(t, engine.Reload(context.Background()))
	require.NotEmpty(t, engine.Current().Scripts)

	// Sanity: the type returned by ProvideEngine matches the package type.
	var _ *script.Engine = engine
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
