//go:build integration

package repository_test

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/features/identity/repository"
)

// rathenaCharTableSQL returns the rAthena `char` table DDL extracted from
// third_party/rathena/sql-files/main.sql. The path is resolved relative to
// this test file: 4 dirs up to repo root, then third_party/rathena. If the
// reference SQL is absent (e.g. CI without the rAthena submodule), the
// test skips cleanly.
func rathenaCharTableSQL(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	// internal/features/identity/repository/model_test.go → 4 dirs up
	// to repo root, then into third_party/rathena/sql-files/main.sql.
	base := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "third_party", "rathena", "sql-files", "main.sql")
	abs, err := filepath.Abs(base)
	require.NoError(t, err)
	if _, err := os.Stat(abs); err != nil {
		t.Skipf("rAthena main.sql not available at %s: %v", abs, err)
		return ""
	}
	//nolint:gosec // path is hardcoded for the test, not user input
	raw, err := os.ReadFile(abs)
	require.NoError(t, err, "read rAthena main.sql at %s", abs)
	return extractCharTableDDL(string(raw))
}

// charTableDDLPattern captures everything between the opening
// CREATE TABLE IF NOT EXISTS `char` ( and its matching closing
// `) ENGINE=...` line. Non-greedy so the first `)` with `ENGINE=` wins.
var charTableDDLPattern = regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS ` + "`char`" + ` \((.*?)\)\s*ENGINE=`)

func extractCharTableDDL(sql string) string {
	m := charTableDDLPattern.FindStringSubmatch(sql)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// columnLinePattern matches a single column-definition line: it must
// start with a backtick-quoted column name at the line's beginning (after
// optional leading whitespace). This deliberately rejects KEY/PRIMARY
// KEY/UNIQUE KEY constraint lines whose identifier appears later on
// the line.
var columnLinePattern = regexp.MustCompile(`(?m)^\s*\` + "`" + `([A-Za-z0-9_]+)` + "`" + `\s`)

func rathenaCharColumns(t *testing.T) []string {
	t.Helper()
	body := rathenaCharTableSQL(t)
	if body == "" {
		t.Skip("rAthena `char` table DDL unavailable (reference SQL missing)")
		return nil
	}
	require.NotEmpty(t, body, "could not locate rAthena `char` table DDL in main.sql")
	matches := columnLinePattern.FindAllStringSubmatch(body, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	require.NotEmpty(t, out, "no columns parsed from rAthena `char` table DDL")
	return out
}

// gormColumnTag extracts the `column:<name>;` token from a struct tag.
// If no `column:` tag is present, falls back to snake_case of the field
// name (the GORM default). Every CharModel field declares `column:`
// today so the fallback is unreachable, but the helper keeps the test
// robust if a future field forgets the tag.
var columnTagPattern = regexp.MustCompile(`column:([A-Za-z0-9_]+)`)

func charModelColumns() []string {
	t := reflect.TypeFor[repository.CharModel]()
	cols := make([]string, 0, t.NumField())
	for f := range t.Fields() {
		tag := f.Tag.Get("gorm")
		m := columnTagPattern.FindStringSubmatch(tag)
		if len(m) >= 2 {
			cols = append(cols, m[1])
			continue
		}
		cols = append(cols, toSnakeCase(f.Name))
	}
	return cols
}

func toSnakeCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && r >= 'A' && r <= 'Z' {
			prev := s[i-1]
			if !(prev >= 'A' && prev <= 'Z') {
				b.WriteByte('_')
			}
		}
		if r >= 'A' && r <= 'Z' {
			b.WriteRune(r + ('a' - 'A'))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func diffStrings(want, got []string) string {
	wset := map[string]int{}
	for _, s := range want {
		wset[s]++
	}
	gset := map[string]int{}
	for _, s := range got {
		gset[s]++
	}
	var missing, extra []string
	for k := range wset {
		if _, ok := gset[k]; !ok {
			missing = append(missing, k)
		}
	}
	for k := range gset {
		if _, ok := wset[k]; !ok {
			extra = append(extra, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	var b strings.Builder
	if len(missing) > 0 {
		b.WriteString("missing in CharModel: [")
		b.WriteString(strings.Join(missing, ", "))
		b.WriteString("]")
	}
	if len(extra) > 0 {
		if b.Len() > 0 {
			b.WriteString("; ")
		}
		b.WriteString("extra in CharModel: [")
		b.WriteString(strings.Join(extra, ", "))
		b.WriteString("]")
	}
	return b.String()
}

// TestCharModel_ColumnParityWithRAthena asserts that every rAthena
// `char` column (sql-files/main.sql:209-296) has a matching GORM field
// on CharModel, and that CharModel introduces no extra columns not
// present in rAthena. Skips cleanly if the rAthena reference SQL is
// unavailable (e.g. CI without the third_party/rathena submodule).
func TestCharModel_ColumnParityWithRAthena(t *testing.T) {
	t.Parallel()

	want := sortedCopy(rathenaCharColumns(t))
	got := sortedCopy(charModelColumns())

	if !assert.Len(t, got, len(want), "column count mismatch (rAthena=%d, CharModel=%d)", len(want), len(got)) {
		t.Logf("rAthena `char` columns (%d): %v", len(want), want)
		t.Logf("CharModel columns      (%d): %v", len(got), got)
		t.Logf("diff: %s", diffStrings(want, got))
	}
	if !assert.Equal(t, want, got, "column set mismatch:\n%s", diffStrings(want, got)) {
		t.Logf("rAthena `char` columns (%d): %v", len(want), want)
		t.Logf("CharModel columns      (%d): %v", len(got), got)
	}
}
