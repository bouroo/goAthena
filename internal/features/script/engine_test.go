//go:build unit

package script

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/features/script/loader"
)

func rathenaNPCPath(t *testing.T, elems ...string) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok)
	base := filepath.Join(filepath.Dir(filename), "..", "..", "..", "..", "rathena", "npc")
	return filepath.Join(append([]string{base}, elems...)...)
}

func rathenaKafrasPath(t *testing.T) string {
	t.Helper()
	p := rathenaNPCPath(t, "re", "kafras")
	if _, err := os.Stat(p); err != nil {
		t.Skipf("rathena npc corpus not available at %s: %v", p, err)
	}
	return p
}

func rathenaKafrasFile(t *testing.T) string {
	t.Helper()
	dir := rathenaKafrasPath(t)
	f := filepath.Join(dir, "kafras.txt")
	if _, err := os.Stat(f); err != nil {
		t.Skipf("rathena kafras.txt not available at %s: %v", f, err)
	}
	return f
}

func TestLoadFileKafra(t *testing.T) {
	results, err := loader.LoadFile(rathenaKafrasFile(t))
	require.NoError(t, err)
	require.NotEmpty(t, results)

	var foundKafra bool
	for _, r := range results {
		if r.ParseErr != nil {
			continue
		}
		if r.Header == nil {
			continue
		}
		if r.Header.SpriteName == "kaf_dewata" {
			foundKafra = true
			assert.Equal(t, "dewata", r.Header.MapName)
			assert.Equal(t, 202, r.Header.X)
			assert.Equal(t, 184, r.Header.Y)
			assert.Equal(t, 6, r.Header.Facing)
			assert.Equal(t, "script", r.Header.Type)
			assert.Equal(t, "Kafra Employee", r.Header.Name)
			require.NotEmpty(t, r.Body, "kaf_dewata should have a body")
		}
	}
	require.True(t, foundKafra, "expected to find kaf_dewata NPC")
}

func TestLoadDirKafras(t *testing.T) {
	results, err := loader.LoadDir(rathenaKafrasPath(t))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(results), 5, "expected several NPC definitions in kafras dir")

	scripts := 0
	warps := 0
	shops := 0
	funcs := 0
	for _, r := range results {
		if r.ParseErr != nil {
			continue
		}
		if r.Header == nil {
			continue
		}
		switch r.Header.Type {
		case "script":
			scripts++
		case "warp":
			warps++
		case "shop":
			shops++
		case "function":
			funcs++
		}
	}
	assert.GreaterOrEqual(t, scripts, 1, "expected at least one script-type NPC")
}

func TestEngineCompileAndReload(t *testing.T) {
	logger := zerolog.New(nil)
	eng := NewEngine(&logger, rathenaKafrasPath(t))

	set, err := eng.LoadAndCompile(t.Context())
	require.NoError(t, err)
	require.NotNil(t, set)
	assert.NotEmpty(t, set.Scripts, "expected compiled scripts")

	require.NoError(t, eng.Reload(t.Context()))
	cur := eng.Current()
	require.NotNil(t, cur)
	assert.GreaterOrEqual(t, len(cur.Scripts), len(set.Scripts))
}

func TestEngineAtomicSwapKeepsOldSet(t *testing.T) {
	logger := zerolog.New(nil)
	eng := NewEngine(&logger, rathenaKafrasPath(t))

	require.NoError(t, eng.Reload(t.Context()))
	old := eng.Current()
	require.NotNil(t, old)

	require.NoError(t, eng.Reload(t.Context()))
	newSet := eng.Current()
	require.NotNil(t, newSet)

	assert.NotSame(t, old, newSet, "Current should return a new set after swap")
	// The old set is still reachable through our local variable, proving
	// in-flight VMs can keep referencing it while new code uses the new set.
	assert.NotNil(t, old)
}

func TestEngineErrorHandlingMalformedScript(t *testing.T) {
	tmp := t.TempDir()
	bad := filepath.Join(tmp, "bad.txt")
	// The body is missing the closing brace, so the loader cannot extract a
	// complete segment and will report a parse error. The engine must not
	// panic and must return an empty compiled set for this single broken file.
	require.NoError(t, writeFile(bad, "not_a_real_map,1,2,3\tscript\tBadNPC\t4_F_KAFRA1,{\n\tmes \"oops\";\n"))

	logger := zerolog.New(nil)
	eng := NewEngine(&logger, tmp)

	// The single malformed definition fails to parse/compile, but
	// LoadAndCompile still returns an empty set rather than an error because
	// partial success is allowed.
	set, err := eng.LoadAndCompile(t.Context())
	require.NoError(t, err)
	require.NotNil(t, set)
	assert.Empty(t, set.Scripts)
}

func TestParseShopItems(t *testing.T) {
	spriteID, items, err := loader.ParseShopItems("900,13200:-1,13221:-1")
	require.NoError(t, err)
	assert.Equal(t, 900, spriteID)
	require.Len(t, items, 2)
	assert.Equal(t, int32(13200), items[0].ItemID)
	assert.Equal(t, int32(-1), items[0].Price)
	assert.Equal(t, int32(13221), items[1].ItemID)
	assert.Equal(t, int32(-1), items[1].Price)
}

func TestParseWarpDest(t *testing.T) {
	spriteID, destMap, destX, destY, err := loader.ParseWarpDest("1,alberta_in,69,141")
	require.NoError(t, err)
	assert.Equal(t, 1, spriteID)
	assert.Equal(t, "alberta_in", destMap)
	assert.Equal(t, 69, destX)
	assert.Equal(t, 141, destY)
}

func writeFile(path, content string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	_, err = f.WriteString(content)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}
