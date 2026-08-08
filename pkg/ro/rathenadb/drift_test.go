//go:build unit

package rathenadb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiff_NoDriftWhenIdentical(t *testing.T) {
	t.Parallel()
	in := []Table{
		{Name: "login", Columns: []Column{
			{Name: "account_id", Type: "int(11) unsigned"},
			{Name: "userid", Type: "varchar(23)"},
		}},
	}
	r := Diff(in, clone(in))
	assert.Equal(t, 0, len(r.MissingTables))
	assert.Equal(t, 0, len(r.ExtraTables))
	assert.Equal(t, 0, len(r.SharedTableDrift))
	assert.Equal(t, len(in), r.TotalTablesExpected)
	assert.Equal(t, len(in), r.TotalTablesImplemented)
}

func TestDiff_MissingTableRecordedNotFailed(t *testing.T) {
	t.Parallel()
	rA := []Table{{Name: "login"}, {Name: "char"}}
	gA := []Table{{Name: "login"}}
	r := Diff(rA, gA)
	require.Len(t, r.MissingTables, 1)
	assert.Equal(t, "char", r.MissingTables[0])
	assert.Equal(t, 0, len(r.SharedTableDrift))
	assert.Equal(t, 1, r.TotalTablesImplemented)
}

func TestDiff_ExtraTableRecordedNotFailed(t *testing.T) {
	t.Parallel()
	rA := []Table{{Name: "login"}}
	gA := []Table{{Name: "login"}, {Name: "goathena_only"}}
	r := Diff(rA, gA)
	require.Len(t, r.ExtraTables, 1)
	assert.Equal(t, "goathena_only", r.ExtraTables[0])
	assert.Equal(t, 0, len(r.SharedTableDrift))
}

func TestDiff_MissingColumnOnSharedTableIsFailure(t *testing.T) {
	t.Parallel()
	rA := []Table{{Name: "char", Columns: []Column{{Name: "char_id", Type: "int"}}}}
	gA := []Table{{Name: "char"}}
	r := Diff(rA, gA)
	require.Len(t, r.SharedTableDrift, 1)
	d := r.SharedTableDrift[0]
	assert.Equal(t, "char", d.Table)
	require.Len(t, d.MissingColumns, 1)
	assert.Equal(t, "char_id", d.MissingColumns[0].Name)
	assert.Equal(t, "int", d.MissingColumns[0].RAthenaType)
	assert.Equal(t, "", d.MissingColumns[0].GoAthenaType)
}

func TestDiff_ExtraColumnOnSharedTableIsNotFailure(t *testing.T) {
	t.Parallel()
	rA := []Table{{Name: "char"}}
	gA := []Table{{Name: "char", Columns: []Column{{Name: "goathena_extra", Type: "int"}}}}
	r := Diff(rA, gA)
	assert.Equal(t, 0, len(r.SharedTableDrift), "extra columns on shared tables must not gate")
}

func TestDiff_RetypedColumnOnSharedTableIsFailure(t *testing.T) {
	t.Parallel()
	rA := []Table{{Name: "char", Columns: []Column{{Name: "amount", Type: "int(11)"}}}}
	gA := []Table{{Name: "char", Columns: []Column{{Name: "amount", Type: "int(11) unsigned"}}}}
	r := Diff(rA, gA)
	require.Len(t, r.SharedTableDrift, 1)
	d := r.SharedTableDrift[0]
	require.Len(t, d.RetypedColumns, 1)
	assert.Equal(t, "amount", d.RetypedColumns[0].Name)
	assert.Equal(t, "int(11)", d.RetypedColumns[0].RAthenaType)
	assert.Equal(t, "int(11) unsigned", d.RetypedColumns[0].GoAthenaType)
}

func TestDiff_DeterministicOrdering(t *testing.T) {
	t.Parallel()
	rA := []Table{
		{Name: "zeta", Columns: []Column{{Name: "z", Type: "int"}}},
		{Name: "alpha", Columns: []Column{{Name: "a", Type: "int"}}},
		{Name: "mid", Columns: []Column{{Name: "shared_col", Type: "int(11) unsigned"}}},
	}
	gA := []Table{
		{Name: "alpha", Columns: []Column{{Name: "a", Type: "int"}}},
		{Name: "zeta"},
		{Name: "mid", Columns: []Column{{Name: "shared_col", Type: "int(11)"}}},
		{Name: "beta"},
	}
	r := Diff(rA, gA)
	assert.Equal(t, []string{"beta"}, r.ExtraTables, "ExtraTables must be sorted")
	assert.Equal(t, 0, len(r.MissingTables))
	require.Len(t, r.SharedTableDrift, 2, "two shared tables have column drift (mid retyped, zeta missing z)")
	assert.Equal(t, "mid", r.SharedTableDrift[0].Table, "SharedTableDrift must be sorted by table name")
	assert.Equal(t, "zeta", r.SharedTableDrift[1].Table)
	assert.Equal(t, []string{"shared_col"}, columnNames(r.SharedTableDrift[0].RetypedColumns))
	assert.Equal(t, []string{"z"}, columnNames(r.SharedTableDrift[1].MissingColumns))
}

func columnNames(diffs []ColumnDiff) []string {
	out := make([]string, len(diffs))
	for i, d := range diffs {
		out[i] = d.Name
	}
	return out
}

func clone(in []Table) []Table {
	out := make([]Table, len(in))
	for i, t := range in {
		cc := make([]Column, len(t.Columns))
		copy(cc, t.Columns)
		out[i] = Table{Name: t.Name, Columns: cc}
	}
	return out
}
