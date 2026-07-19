//go:build unit

package athenaconf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestParse_BasicKeyValue(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.conf", "foo: bar\nbaz: 42\n")

	f, err := NewParser(dir).Parse(path)
	require.NoError(t, err)

	require.Len(t, f.Keys, 2)
	v, ok := f.Keys["foo"]
	require.True(t, ok)
	assert.Equal(t, KindString, v.Kind)
	assert.Equal(t, "bar", v.Str)

	v, ok = f.Keys["baz"]
	require.True(t, ok)
	assert.Equal(t, KindInt, v.Kind)
	assert.Equal(t, int64(42), v.Int)
}

func TestParse_QuotedString(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.conf", `key: "hello world"`+"\n")

	f, err := NewParser(dir).Parse(path)
	require.NoError(t, err)
	v := f.Keys["key"]
	assert.Equal(t, KindString, v.Kind)
	assert.Equal(t, "hello world", v.Str)
}

func TestParse_BoolVariants(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"yes", true}, {"YES", true}, {"Yes", true},
		{"no", false}, {"NO", false},
		{"true", true}, {"True", true},
		{"false", false},
		{"on", true}, {"On", true},
		{"off", false}, {"OFF", false},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			dir := t.TempDir()
			path := writeFile(t, dir, "a.conf", "k: "+tc.raw+"\n")
			f, err := NewParser(dir).Parse(path)
			require.NoError(t, err)
			v := f.Keys["k"]
			assert.Equal(t, KindBool, v.Kind, "raw=%s", tc.raw)
			assert.Equal(t, tc.want, v.Bool, "raw=%s", tc.raw)
		})
	}
}

func TestParse_IntFloatFallback(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.conf", "i: 100\nf: 1.5\ns: hello\n")
	f, err := NewParser(dir).Parse(path)
	require.NoError(t, err)
	assert.Equal(t, KindInt, f.Keys["i"].Kind)
	assert.Equal(t, int64(100), f.Keys["i"].Int)
	assert.Equal(t, KindFloat, f.Keys["f"].Kind)
	assert.Equal(t, 1.5, f.Keys["f"].Flt)
	assert.Equal(t, KindString, f.Keys["s"].Kind)
}

func TestParse_InlineCommentStripped(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.conf", "k: 42 // inline note\n")
	f, err := NewParser(dir).Parse(path)
	require.NoError(t, err)
	assert.Equal(t, int64(42), f.Keys["k"].Int)
}

func TestParse_EmptyValue(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.conf", "k:\n")
	f, err := NewParser(dir).Parse(path)
	require.NoError(t, err)
	v, ok := f.Keys["k"]
	require.True(t, ok)
	assert.Equal(t, KindString, v.Kind)
	assert.Equal(t, "", v.Str)
}

func TestParse_CommentedKey(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "a.conf", "// ignored: 1\nreal: 2\n")
	f, err := NewParser(dir).Parse(path)
	require.NoError(t, err)
	_, present := f.Keys["ignored"]
	assert.False(t, present)
	assert.Equal(t, int64(2), f.Keys["real"].Int)
}

func TestParseImport_Basic(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.conf", "k: 1\nimport: b.conf\n")
	writeFile(t, dir, "b.conf", "k2: 2\n")

	f, err := NewParser(dir).ParseWithImports(filepath.Join(dir, "a.conf"))
	require.NoError(t, err)
	assert.Equal(t, int64(1), f.Keys["k"].Int)
	assert.Equal(t, int64(2), f.Keys["k2"].Int)
}

func TestParseImport_RelativePath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.conf", "import: sub/b.conf\n")
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub"), 0o700))
	writeFile(t, dir, "sub/b.conf", "k: 7\n")

	f, err := NewParser(dir).ParseWithImports(filepath.Join(dir, "a.conf"))
	require.NoError(t, err)
	assert.Equal(t, int64(7), f.Keys["k"].Int)
}

func TestParseImport_Cycle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.conf", "import: b.conf\n")
	writeFile(t, dir, "b.conf", "import: a.conf\n")

	_, err := NewParser(dir).ParseWithImports(filepath.Join(dir, "a.conf"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cycle")
}

func TestParseDir_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.conf", "k1: 1\n")
	require.NoError(t, os.Mkdir(filepath.Join(dir, "import"), 0o700))
	writeFile(t, dir, "import/b.txt", "k2: 2\n")
	writeFile(t, dir, "ignored.txt", "kignored: 9\n")

	p := NewParser(dir)
	f, m, err := p.ParseDir(dir)
	require.NoError(t, err)
	assert.Len(t, f.Keys, 2)
	_, present := f.Keys["kignored"]
	assert.False(t, present, "non-import *.txt files must be skipped")
	assert.Len(t, m.Sources, 2)
	assert.Contains(t, m.KeyOrigin["k1"], "a.conf")
}

func TestApplyToConfig_KnownKeys(t *testing.T) {
	f := &File{
		Keys: map[string]Value{
			"use_MD5_passwords": {Kind: KindBool, Bool: true},
			"chars_per_account": {Kind: KindInt, Int: 9},
		},
	}
	m := &Manifest{KeyOrigin: map[string]string{}}
	applyKnownKeys(t, f, m)
}

func applyKnownKeys(t *testing.T, f *File, m *Manifest) {
	t.Helper()
	cfg := newTestConfig()
	err := ApplyToConfig(cfg, f, m)
	require.NoError(t, err)
	assert.True(t, cfg.Identity.UseMD5Passwords)
	assert.Equal(t, 9, cfg.Identity.MaxChars)
}

func TestApplyToConfig_UnknownKeysRecorded(t *testing.T) {
	f := &File{
		Keys: map[string]Value{
			"unmapped_a":        {Kind: KindString, Str: "x"},
			"unmapped_b":        {Kind: KindInt, Int: 3},
			"use_MD5_passwords": {Kind: KindBool, Bool: false},
		},
	}
	m := &Manifest{KeyOrigin: map[string]string{}}
	cfg := newTestConfig()
	require.NoError(t, ApplyToConfig(cfg, f, m))
	assert.Contains(t, m.Unmapped, "unmapped_a")
	assert.Contains(t, m.Unmapped, "unmapped_b")
	assert.NotContains(t, m.Unmapped, "use_MD5_passwords")
	assert.False(t, cfg.Identity.UseMD5Passwords)
}

func TestApplyToConfig_AltKeyAlias(t *testing.T) {
	f := &File{
		Keys: map[string]Value{
			"use_md5_passwds": {Kind: KindBool, Bool: true},
			"max_char":        {Kind: KindInt, Int: 12},
		},
	}
	m := &Manifest{KeyOrigin: map[string]string{}}
	cfg := newTestConfig()
	require.NoError(t, ApplyToConfig(cfg, f, m))
	assert.True(t, cfg.Identity.UseMD5Passwords)
	assert.Equal(t, 12, cfg.Identity.MaxChars)
}

func TestApplyToConfig_TypeMismatch(t *testing.T) {
	f := &File{
		Keys: map[string]Value{
			"use_MD5_passwords": {Kind: KindString, Str: "true"},
		},
	}
	m := &Manifest{KeyOrigin: map[string]string{}}
	cfg := newTestConfig()
	err := ApplyToConfig(cfg, f, m)
	require.Error(t, err)
}

func TestDefaultRootDir_RathenaConfLayout(t *testing.T) {
	root := t.TempDir()
	confDir := filepath.Join(root, "conf")
	require.NoError(t, os.Mkdir(confDir, 0o700))
	got := DefaultRootDir(confDir)
	// The implementation returns srcDir+"/.."; semantically this must
	// resolve back to the parent directory on disk so callers can pass
	// it to NewParser.
	assert.Equal(t, root, filepath.Clean(got),
		"DefaultRootDir(<srcDir>/conf) must resolve to the parent of srcDir")
}

func TestDefaultRootDir_NonConfSrcReturnsItself(t *testing.T) {
	root := t.TempDir()
	other := filepath.Join(root, "elsewhere")
	require.NoError(t, os.Mkdir(other, 0o700))
	got := DefaultRootDir(other)
	assert.Equal(t, other, got,
		"DefaultRootDir must return srcDir unchanged when no sibling /conf exists")
}

func TestDefaultRootDir_Deterministic(t *testing.T) {
	root := t.TempDir()
	confDir := root + "/conf"
	require.NoError(t, os.Mkdir(confDir, 0o700))
	first := DefaultRootDir(confDir)
	second := DefaultRootDir(confDir)
	assert.Equal(t, first, second,
		"DefaultRootDir must be deterministic for the same input")
}

func TestValue_AsString(t *testing.T) {
	t.Run("string returns Str verbatim", func(t *testing.T) {
		v := Value{Kind: KindString, Str: "hello"}
		assert.Equal(t, "hello", v.AsString())
	})
	t.Run("int formats as base-10", func(t *testing.T) {
		v := Value{Kind: KindInt, Int: 42}
		assert.Equal(t, "42", v.AsString())
	})
	t.Run("float formats canonically", func(t *testing.T) {
		v := Value{Kind: KindFloat, Flt: 1.5}
		assert.Equal(t, "1.5", v.AsString())
	})
	t.Run("bool true -> true", func(t *testing.T) {
		v := Value{Kind: KindBool, Bool: true}
		assert.Equal(t, "true", v.AsString())
	})
	t.Run("bool false -> false", func(t *testing.T) {
		v := Value{Kind: KindBool, Bool: false}
		assert.Equal(t, "false", v.AsString())
	})
}

// TestApplyToConfig_MappedKeysAndAliases builds a synthetic .conf per
// mapped key and asserts ApplyToConfig lands the value on the correct
// Config field. It also exercises both rAthena-key aliases for each
// mapped field, so the test would catch a regression where one alias
// is dropped from the Initial key map.
//
// This test does NOT depend on third_party/rathena and lives under the
// integration tag only for proximity with TestRoundTrip_RathenaConfDir
// (the two tests share setup and assert on the same Config fields).
func TestApplyToConfig_MappedKeysAndAliases(t *testing.T) {
	cases := []struct {
		name       string
		rathenaKey string
		body       string
		check      func(t *testing.T, cfg interface{})
	}{
		{
			name:       "use_MD5_passwords=true (canonical)",
			rathenaKey: "use_MD5_passwords",
			body:       "use_MD5_passwords: yes\n",
		},
		{
			name:       "use_md5_passwds=false (alias)",
			rathenaKey: "use_md5_passwds",
			body:       "use_md5_passwds: no\n",
		},
		{
			name:       "chars_per_account=9 (canonical)",
			rathenaKey: "chars_per_account",
			body:       "chars_per_account: 9\n",
		},
		{
			name:       "max_char=12 (alias)",
			rathenaKey: "max_char",
			body:       "max_char: 12\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "inter.conf")
			require.NoError(t, os.WriteFile(path, []byte(tc.body), 0o600))

			merged, manifest, err := NewParser(dir).ParseDir(dir)
			require.NoError(t, err)

			cfg := newTestConfig()
			require.NoError(t, ApplyToConfig(cfg, merged, manifest))

			assert.NotContains(t, manifest.Unmapped, tc.rathenaKey,
				"mapped key %q must not be flagged unmapped", tc.rathenaKey)

			switch tc.rathenaKey {
			case "use_MD5_passwords":
				assert.True(t, cfg.Identity.UseMD5Passwords,
					"Identity.UseMD5Passwords must be true after parsing %q", tc.rathenaKey)
			case "use_md5_passwds":
				assert.False(t, cfg.Identity.UseMD5Passwords,
					"Identity.UseMD5Passwords must be false after parsing %q", tc.rathenaKey)
			case "chars_per_account":
				assert.Equal(t, 9, cfg.Identity.MaxChars,
					"Identity.MaxChars must equal 9 after parsing %q", tc.rathenaKey)
			case "max_char":
				assert.Equal(t, 12, cfg.Identity.MaxChars,
					"Identity.MaxChars must equal 12 after parsing %q", tc.rathenaKey)
			}
		})
	}
}
