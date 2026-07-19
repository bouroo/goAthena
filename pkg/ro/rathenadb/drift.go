package rathenadb

import "sort"

// DriftReport summarizes the schema-comparison result between a set of
// expected (rAthena) tables and actual (goAthena) tables. See the
// subplan phase-r0-db-drift-test.md for the full rationale of each
// field's failure semantics.
type DriftReport struct {
	// MissingTables names rAthena tables that goAthena does not yet
	// implement. Expected to be non-empty today (50+ missing); tracked
	// as the D3 backlog. NOT a failure per D-001.
	MissingTables []string

	// ExtraTables names goAthena-specific tables that are not in
	// rAthena main.sql. Allowed per D-001. NOT a failure.
	ExtraTables []string

	// SharedTableDrift records column-level drift on tables present
	// in both rAthena and goAthena. A SharedTableDrift entry appears
	// only when at least one column is missing, extra, or retyped.
	SharedTableDrift []SharedTableDrift

	// TotalTablesExpected counts the tables in rAthena main.sql.
	TotalTablesExpected int

	// TotalTablesImplemented counts the tables goAthena implements
	// that are also in rAthena (the intersection).
	TotalTablesImplemented int
}

// SharedTableDrift is the column-level diff for one table present in
// both rAthena and goAthena.
type SharedTableDrift struct {
	Table          string
	MissingColumns []ColumnDiff // in rAthena but not in goAthena
	ExtraColumns   []ColumnDiff // in goAthena but not in rAthena
	RetypedColumns []ColumnDiff // same name, different Type
}

// ColumnDiff is a single column discrepancy.
type ColumnDiff struct {
	Name         string
	RAthenaType  string // "" if column is entirely missing on rAthena side
	GoAthenaType string // "" if column is entirely missing on goAthena side
}

// Diff returns a DriftReport comparing expected (rAthena) tables to
// actual (goAthena) tables. Both slices may be unsorted; Diff sorts
// internally for deterministic output.
func Diff(expected, actual []Table) *DriftReport {
	r := &DriftReport{
		TotalTablesExpected: len(expected),
	}
	expByName := indexByName(expected)
	actByName := indexByName(actual)

	for name := range expByName {
		if _, ok := actByName[name]; !ok {
			r.MissingTables = append(r.MissingTables, name)
		}
	}
	for name := range actByName {
		if _, ok := expByName[name]; !ok {
			r.ExtraTables = append(r.ExtraTables, name)
		}
	}
	sort.Strings(r.MissingTables)
	sort.Strings(r.ExtraTables)

	for name, expTbl := range expByName {
		actTbl, ok := actByName[name]
		if !ok {
			continue
		}
		r.TotalTablesImplemented++
		diff := diffTable(name, expTbl, actTbl)
		if len(diff.MissingColumns) > 0 || len(diff.RetypedColumns) > 0 {
			r.SharedTableDrift = append(r.SharedTableDrift, diff)
		}
	}
	sort.Slice(r.SharedTableDrift, func(i, j int) bool {
		return r.SharedTableDrift[i].Table < r.SharedTableDrift[j].Table
	})
	return r
}

func indexByName(tables []Table) map[string]Table {
	m := make(map[string]Table, len(tables))
	for _, t := range tables {
		m[t.Name] = t
	}
	return m
}

func diffTable(name string, expected, actual Table) SharedTableDrift {
	d := SharedTableDrift{Table: name}
	expCols := indexColsByName(expected.Columns)
	actCols := indexColsByName(actual.Columns)

	for cn, ec := range expCols {
		ac, ok := actCols[cn]
		if !ok {
			d.MissingColumns = append(d.MissingColumns, ColumnDiff{
				Name:        cn,
				RAthenaType: ec.Type,
			})
			continue
		}
		if ac.Type != ec.Type {
			d.RetypedColumns = append(d.RetypedColumns, ColumnDiff{
				Name:         cn,
				RAthenaType:  ec.Type,
				GoAthenaType: ac.Type,
			})
		}
	}
	for cn, ac := range actCols {
		if _, ok := expCols[cn]; !ok {
			d.ExtraColumns = append(d.ExtraColumns, ColumnDiff{
				Name:         cn,
				GoAthenaType: ac.Type,
			})
		}
	}
	sort.Slice(d.MissingColumns, func(i, j int) bool {
		return d.MissingColumns[i].Name < d.MissingColumns[j].Name
	})
	sort.Slice(d.ExtraColumns, func(i, j int) bool {
		return d.ExtraColumns[i].Name < d.ExtraColumns[j].Name
	})
	sort.Slice(d.RetypedColumns, func(i, j int) bool {
		return d.RetypedColumns[i].Name < d.RetypedColumns[j].Name
	})
	return d
}

func indexColsByName(cols []Column) map[string]Column {
	m := make(map[string]Column, len(cols))
	for _, c := range cols {
		m[c.Name] = c
	}
	return m
}
