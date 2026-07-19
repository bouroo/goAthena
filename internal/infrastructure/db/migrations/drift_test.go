//go:build integration

package migrations_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bouroo/goAthena/internal/infrastructure/db/migrations"
	"github.com/bouroo/goAthena/pkg/ro/rathenadb"
)

func rathenaMainSQLPath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	// internal/infrastructure/db/migrations/drift_test.go → 4 dirs up to
	// repo root, then into third_party/rathena/sql-files/main.sql.
	base := filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "third_party", "rathena", "sql-files", "main.sql")
	abs, err := filepath.Abs(base)
	require.NoError(t, err)
	if _, err := os.Stat(abs); err != nil {
		t.Skipf("rAthena main.sql not available at %s: %v", abs, err)
		return ""
	}
	return abs
}

// alterModifyPattern matches ALTER TABLE `name` MODIFY `col` <type> ...
// which is the form D5 uses to converge goAthena's ipbanlist onto the
// rAthena canonical (see decision-log D-001/D-002). We only capture the
// subset needed to re-type a single column.
var alterModifyPattern = regexp.MustCompile(`(?is)alter\s+table\s+` + "`([A-Za-z0-9_]+)`" + `\s+modify\s+` + "`([A-Za-z0-9_]+)`" + `\s+([^,;\n]+)`)

// applyMigrationMutations folds ALTER TABLE ... MODIFY statements into
// the accumulated table slice, replacing each affected column's Type
// with the new (lowercased, whitespace-collapsed) type string. INSERT,
// DROP, ADD CONSTRAINT, etc. are ignored — the drift detector's
// additive-only contract (D-001) means non-CREATE/MODIFY statements
// don't change column-level schema for drift purposes.
//
// This exists solely to handle the single D5 carve-out documented in
// D-001/D-002 of decision-log.md. Once D3 ships and every rAthena
// table has a single canonical CREATE TABLE in goAthena migrations,
// this function becomes unnecessary and can be deleted.
func applyMigrationMutations(tables []rathenadb.Table, src string) []rathenadb.Table {
	for _, m := range alterModifyPattern.FindAllStringSubmatch(src, -1) {
		tblName, colName, typeRaw := m[1], m[2], strings.TrimSpace(m[3])
		idx := -1
		for i, t := range tables {
			if t.Name == tblName {
				idx = i
				break
			}
		}
		if idx < 0 {
			continue
		}
		newType := stripModifiers(normalizeType(typeRaw))
		for j, c := range tables[idx].Columns {
			if c.Name == colName {
				tables[idx].Columns[j].Type = newType
				break
			}
		}
	}
	return tables
}

// normalizeType lowercases the type token and collapses internal
// whitespace. Mirrors pkg/ro/rathenadb.normalizeType but is duplicated
// here so the drift test does not need to export private API.
func normalizeType(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' {
			if !prevSpace {
				b.WriteByte(' ')
			}
			prevSpace = true
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return strings.TrimSpace(b.String())
}

func TestDrift_RAthenaMainSQLVsGoAthenaMigrations(t *testing.T) {
	t.Parallel()

	mainPath := rathenaMainSQLPath(t)
	if mainPath == "" {
		return // t.Skipf already called
	}
	//nolint:gosec // path is hardcoded for the test, not user input
	rawMain, err := os.ReadFile(mainPath)
	require.NoError(t, err, "read rAthena main.sql at %s", mainPath)
	rAthenaTables, err := rathenadb.ParseMainSQL(string(rawMain))
	require.NoError(t, err)

	var goAthenaTables []rathenadb.Table
	entries, err := fs.ReadDir(migrations.FS, ".")
	require.NoError(t, err)
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		b, readErr := fs.ReadFile(migrations.FS, name)
		require.NoError(t, readErr, "read embedded migration %s", name)
		parsed, parseErr := rathenadb.ParseMigrationSQL(string(b))
		require.NoError(t, parseErr, "parse migration %s", name)
		goAthenaTables = append(goAthenaTables, parsed...)
		// Fold ALTER TABLE ... MODIFY statements onto the accumulated
		// table state — see applyMigrationMutations docstring.
		goAthenaTables = applyMigrationMutations(goAthenaTables, string(b))
	}

	report := rathenadb.Diff(rAthenaTables, goAthenaTables)

	// Sanity floor — the rAthena canonical main.sql table count is 56
	// today. If main.sql grows or shrinks, this fails loudly so the
	// reviewer knows to re-baseline.
	assert.Equal(t, 56, report.TotalTablesExpected,
		"rAthena main.sql table count drifted from the R0 baseline (56); re-baseline the drift gate")

	// goAthena implements 6 rAthena tables today (cart_inventory, char,
	// inventory, ipbanlist, login, storage). loginlog is goAthena-specific
	// (lives in rAthena's logs.sql, not main.sql) and is reported in
	// ExtraTables. Bumps as D3 backfills more tables.
	assert.GreaterOrEqual(t, report.TotalTablesImplemented, 6,
		"goAthena implemented-table count fell below the R0 baseline (6); D3 regression?")

	// THE GATE — any column drift on a shared table is a real
	// regression. Log the offending details for diagnosis.
	if len(report.SharedTableDrift) != 0 {
		for _, sd := range report.SharedTableDrift {
			t.Logf("drift on table %q:", sd.Table)
			for _, c := range sd.MissingColumns {
				t.Logf("  MISSING column %q (rAthena type=%q)", c.Name, c.RAthenaType)
			}
			for _, c := range sd.ExtraColumns {
				t.Logf("  extra column %q (goAthena type=%q)", c.Name, c.GoAthenaType)
			}
			for _, c := range sd.RetypedColumns {
				t.Logf("  RETYPED column %q: rAthena=%q goAthena=%q", c.Name, c.RAthenaType, c.GoAthenaType)
			}
		}
	}
	assert.Len(t, report.SharedTableDrift, 0,
		"shared-table column drift is a regression (D-001 violation). If D5 carve-out regressed, restore applyMigrationMutations handling.")

	t.Logf("D8 baseline (2026-07-19): TotalTablesExpected=%d TotalTablesImplemented=%d MissingTables=%d ExtraTables=%d SharedTableDrift=%d",
		report.TotalTablesExpected, report.TotalTablesImplemented,
		len(report.MissingTables), len(report.ExtraTables), len(report.SharedTableDrift))

	if len(report.MissingTables) > 0 {
		top := append([]string(nil), report.MissingTables...)
		sort.Strings(top)
		limit := 5
		if len(top) < limit {
			limit = len(top)
		}
		t.Logf("D8 missing tables (top %d of %d): %v", limit, len(top), top[:limit])
	}
	if len(report.ExtraTables) > 0 {
		t.Logf("D8 extra tables: %v", report.ExtraTables)
	}
}

// stripModifiers removes a trailing NOT NULL / DEFAULT ... clause that
// ALTER TABLE ... MODIFY captures along with the type. Mirrors
// pkg/ro/rathenadb.parseColumnLine's keyword-peeling order so the
// resulting Type matches what ParseMainSQL produces for the same column.
func stripModifiers(s string) string {
	for _, kw := range []string{"NOT NULL", "NULL", "DEFAULT"} {
		if i := strings.Index(strings.ToUpper(s), " "+kw); i >= 0 {
			s = strings.TrimSpace(s[:i])
		}
	}
	return s
}
