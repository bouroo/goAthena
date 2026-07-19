package rathenadb

import (
	"fmt"
	"regexp"
	"strings"
)

// Table is a CREATE TABLE definition parsed from a MySQL DDL document.
type Table struct {
	Name    string
	Columns []Column
}

// Column is one column inside a Table.
type Column struct {
	Name     string // backticks already stripped
	Type     string // lowercased, whitespace-collapsed (e.g. "int(11) unsigned")
	Nullable bool   // true unless the line contains "NOT NULL"
	Default  string // raw DEFAULT clause (quotes preserved), "" if absent
}

// createTablePattern matches a CREATE TABLE [IF NOT EXISTS] `name` ( ... ) ENGINE=...;
// block. Non-greedy so the first closing ") ENGINE=" pair wins. Allows the
// IF NOT EXISTS modifier; flags the case-insensitive flag for the keyword
// (table name itself is always backticked).
var createTablePattern = regexp.MustCompile(`(?is)create\s+table\s+(?:if\s+not\s+exists\s+)?` + "`([A-Za-z0-9_]+)`" + `\s*\((.*?)\)\s*engine\s*=`)

// ParseMainSQL parses a rAthena main.sql document and returns the tables
// declared via CREATE TABLE IF NOT EXISTS. Comments, INSERT statements,
// and other DDL are ignored. Returns an error if the same table name
// appears twice (defensive — rAthena's main.sql does not do this today).
func ParseMainSQL(src string) ([]Table, error) {
	return parseSQL(src, true)
}

// ParseMigrationSQL parses a single goAthena *.up.sql migration file.
// Recognizes CREATE TABLE [IF NOT EXISTS] statements only; ALTER, DROP,
// INSERT, and other statement types are ignored for drift purposes
// (goAthena migrations are additive-only per decision D-001).
func ParseMigrationSQL(src string) ([]Table, error) {
	return parseSQL(src, false)
}

// parseSQL strips line comments, locates every CREATE TABLE block, and
// extracts its columns. When strict is true (ParseMainSQL), duplicate
// table names return an error. When strict is false (ParseMigrationSQL),
// duplicate table names are also an error — goAthena migrations should
// declare each table once, and a duplicate is almost certainly a
// migration bug worth surfacing loudly.
func parseSQL(src string, _ bool) ([]Table, error) {
	stripped := stripLineComments(src)
	tables := make([]Table, 0, 8)
	seen := make(map[string]int, 8)
	for _, m := range createTablePattern.FindAllStringSubmatch(stripped, -1) {
		name := m[1]
		body := m[2]
		t := Table{Name: name, Columns: parseColumns(body)}
		if _, dup := seen[name]; dup {
			return nil, fmt.Errorf("rathenadb: duplicate CREATE TABLE for %q", name)
		}
		seen[name] = 1
		tables = append(tables, t)
	}
	return tables, nil
}

// lineCommentRe matches a SQL "-- ..." line comment to end-of-line.
var lineCommentRe = regexp.MustCompile(`(?m)--[^\n]*`)

// stripLineComments removes "-- ..." line comments. Block comments are
// not stripped because rAthena main.sql does not use them; if a future
// source does, the parser will tolerate the noise.
func stripLineComments(src string) string {
	return lineCommentRe.ReplaceAllString(src, "")
}

// constraintKeywordRe matches a constraint or index directive that
// should be ignored when scanning for column definitions. The match is
// anchored at the start of a (whitespace-trimmed) line.
var constraintKeywordRe = regexp.MustCompile(`(?i)^\s*(primary\s+key|key|unique\s+key|fulltext\s+key|fulltext|spatial\s+key|spatial|constraint|foreign\s+key|index)\b`)

// parseColumns extracts column definitions from the body of a CREATE
// TABLE block (the text between the opening "(" and the closing
// ") ENGINE="). Each column occupies one logical line; constraint
// directives (PRIMARY KEY, KEY, etc.) are skipped.
func parseColumns(body string) []Column {
	out := make([]Column, 0, 16)
	for raw := range strings.SplitSeq(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		// Strip the trailing comma that terminates most lines.
		line = strings.TrimRight(line, ",")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if constraintKeywordRe.MatchString(line) {
			continue
		}
		col, ok := parseColumnLine(line)
		if !ok {
			continue
		}
		out = append(out, col)
	}
	return out
}

// columnNameRe matches the leading backtick-quoted column name.
var columnNameRe = regexp.MustCompile(`^` + "`([A-Za-z0-9_]+)`" + `\s+(.*)$`)

// parseColumnLine parses a single column-definition line such as
//
//	`col_name` int(11) unsigned NOT NULL default '0'
//
// into a Column. Returns ok=false if the line does not start with a
// backtick-quoted identifier (defensive against future DDL constructs
// the parser does not yet understand).
func parseColumnLine(line string) (Column, bool) {
	m := columnNameRe.FindStringSubmatch(line)
	if len(m) != 3 {
		return Column{}, false
	}
	name := m[1]
	rest := strings.TrimSpace(m[2])

	// Peel keywords off the right-hand side of the column definition.
	// Order matters: AUTO_INCREMENT may appear after DEFAULT, so we
	// strip it first; then DEFAULT; then NULL / NOT NULL.
	typeToken, _ := peelAutoIncrement(rest)
	typeToken, defaultVal := peelDefault(typeToken)
	typeToken, nullable := peelNullability(typeToken)

	typ := normalizeType(typeToken)
	if typ == "" {
		return Column{}, false
	}
	return Column{
		Name:     name,
		Type:     typ,
		Nullable: nullable,
		Default:  defaultVal,
	}, true
}

// peelAutoIncrement strips a trailing "auto_increment" (case-insensitive)
// from rest and returns the trimmed remainder.
func peelAutoIncrement(rest string) (string, bool) {
	upper := strings.ToUpper(strings.TrimRight(rest, " "))
	if strings.HasSuffix(upper, "AUTO_INCREMENT") {
		cut := len(rest) - len("AUTO_INCREMENT")
		return strings.TrimRight(rest[:cut], " "), true
	}
	return rest, false
}

// peelDefault extracts the value of the trailing DEFAULT clause (if any)
// and returns the trimmed remainder plus the raw default literal
// (quotes preserved). DEFAULT may appear with or without an explicit
// value; "DEFAULT NULL" yields the literal "" (no value to capture).
func peelDefault(rest string) (string, string) {
	idx := indexKeywordCI(rest, "DEFAULT")
	if idx < 0 {
		return rest, ""
	}
	after := strings.TrimSpace(rest[idx+len("DEFAULT"):])
	if after == "" {
		return strings.TrimSpace(rest[:idx]), ""
	}
	// Stop at the next keyword that signals end of the DEFAULT clause.
	stopAt := len(after)
	for _, kw := range []string{"COMMENT", "ON UPDATE", "COLLATE", "AUTO_INCREMENT"} {
		if i := indexKeywordCI(after, kw); i >= 0 && i < stopAt {
			stopAt = i
		}
	}
	value := strings.TrimSpace(after[:stopAt])
	return strings.TrimSpace(rest[:idx]), value
}

// peelNullability extracts the trailing NOT NULL / NULL (case-insensitive)
// and returns the trimmed remainder plus the nullable flag. The default
// for an absent explicit NULL marker is nullable=true (matches MySQL
// semantics, where NOT NULL is the only modifier that disallows NULL).
func peelNullability(rest string) (string, bool) {
	upper := strings.ToUpper(strings.TrimRight(rest, " "))
	if strings.HasSuffix(upper, "NOT NULL") {
		cut := len(rest) - len("NOT NULL")
		return strings.TrimRight(rest[:cut], " "), false
	}
	if strings.HasSuffix(upper, "NULL") && !strings.HasSuffix(upper, "NOT NULL") {
		cut := len(rest) - len("NULL")
		// Make sure we are not chopping a keyword like "int(11) unsigned null"
		// where the "null" is actually the column nullability marker.
		// Verify the preceding character is whitespace.
		before := strings.TrimRight(rest[:cut], " ")
		if before != "" {
			last := before[len(before)-1]
			if last == ' ' || last == '\t' {
				return before, true
			}
		}
	}
	return rest, true
}

// indexKeywordCI returns the index of kw inside s (case-insensitive) at
// a word boundary (preceded by whitespace, beginning of string, or
// opening paren). Returns -1 if not found.
func indexKeywordCI(s, kw string) int {
	upper := strings.ToUpper(s)
	kwU := strings.ToUpper(kw)
	from := 0
	for {
		i := strings.Index(upper[from:], kwU)
		if i < 0 {
			return -1
		}
		i += from
		if i == 0 {
			return i
		}
		prev := s[i-1]
		if prev == ' ' || prev == '\t' || prev == '(' {
			return i
		}
		from = i + 1
	}
}

// normalizeType lowercases the type and collapses internal whitespace
// runs to a single space. Backticks have already been stripped by the
// caller; trailing whitespace is trimmed.
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
