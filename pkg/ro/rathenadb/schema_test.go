//go:build unit

package rathenadb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMainSQL_SingleTable(t *testing.T) {
	t.Parallel()
	src := `
CREATE TABLE IF NOT EXISTS ` + "`login`" + ` (
  ` + "`account_id`" + ` int(11) unsigned NOT NULL auto_increment,
  ` + "`userid`" + ` varchar(23) NOT NULL default '',
  PRIMARY KEY (` + "`account_id`" + `)
) ENGINE=InnoDB;
`
	tables, err := ParseMainSQL(src)
	require.NoError(t, err)
	require.Len(t, tables, 1)
	assert.Equal(t, "login", tables[0].Name)
	require.Len(t, tables[0].Columns, 2)
	assert.Equal(t, Column{Name: "account_id", Type: "int(11) unsigned", Nullable: false, Default: ""}, tables[0].Columns[0])
	assert.Equal(t, Column{Name: "userid", Type: "varchar(23)", Nullable: false, Default: "''"}, tables[0].Columns[1])
}

func TestParseMainSQL_MultipleTables(t *testing.T) {
	t.Parallel()
	src := `
CREATE TABLE IF NOT EXISTS ` + "`a`" + ` (
  ` + "`x`" + ` int(11) NOT NULL default '0',
  KEY ` + "`x`" + ` (` + "`x`" + `)
) ENGINE=MyISAM;

CREATE TABLE IF NOT EXISTS ` + "`b`" + ` (
  ` + "`y`" + ` varchar(10) NOT NULL default ''
) ENGINE=InnoDB;
`
	tables, err := ParseMainSQL(src)
	require.NoError(t, err)
	require.Len(t, tables, 2)
	assert.Equal(t, "a", tables[0].Name)
	assert.Equal(t, "b", tables[1].Name)
}

func TestParseMainSQL_IgnoresCommentsAndOtherStatements(t *testing.T) {
	t.Parallel()
	src := `
-- this is a comment
/* block comment */
INSERT INTO ` + "`login`" + ` VALUES (1, 'x');
DROP TABLE IF EXISTS ` + "`foo`" + `;
CREATE TABLE IF NOT EXISTS ` + "`only`" + ` (
  ` + "`k`" + ` int(11) NOT NULL default '0'
) ENGINE=InnoDB;
`
	tables, err := ParseMainSQL(src)
	require.NoError(t, err)
	require.Len(t, tables, 1)
	assert.Equal(t, "only", tables[0].Name)
	require.Len(t, tables[0].Columns, 1)
	assert.Equal(t, "k", tables[0].Columns[0].Name)
}

func TestParseMainSQL_DuplicateTableNameReturnsError(t *testing.T) {
	t.Parallel()
	src := `
CREATE TABLE IF NOT EXISTS ` + "`dup`" + ` (` + "`a`" + ` int) ENGINE=InnoDB;
CREATE TABLE IF NOT EXISTS ` + "`dup`" + ` (` + "`b`" + ` int) ENGINE=InnoDB;
`
	_, err := ParseMainSQL(src)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}

func TestParseMainSQL_EmptyInputReturnsEmptyNoError(t *testing.T) {
	t.Parallel()
	tables, err := ParseMainSQL("")
	require.NoError(t, err)
	assert.Empty(t, tables)
}

func TestParseMigrationSQL_CreateTable(t *testing.T) {
	t.Parallel()
	src := "CREATE TABLE `foo` (`k` int(11) unsigned NOT NULL default '0') ENGINE=InnoDB;"
	tables, err := ParseMigrationSQL(src)
	require.NoError(t, err)
	require.Len(t, tables, 1)
	assert.Equal(t, "foo", tables[0].Name)
	require.Len(t, tables[0].Columns, 1)
	assert.Equal(t, "int(11) unsigned", tables[0].Columns[0].Type)
}

func TestParseMigrationSQL_IgnoresAlterAndDrop(t *testing.T) {
	t.Parallel()
	src := `
ALTER TABLE ` + "`ipbanlist`" + ` MODIFY ` + "`list`" + ` varchar(15) NOT NULL default '';
ALTER TABLE ` + "`ipbanlist`" + ` ADD PRIMARY KEY (` + "`list`" + `);
DROP TABLE IF EXISTS ` + "`legacy`" + `;
INSERT INTO ` + "`login`" + ` VALUES (1, 'x');
CREATE TABLE IF NOT EXISTS ` + "`fresh`" + ` (
  ` + "`id`" + ` int(11) unsigned NOT NULL auto_increment
) ENGINE=InnoDB;
`
	tables, err := ParseMigrationSQL(src)
	require.NoError(t, err)
	require.Len(t, tables, 1)
	assert.Equal(t, "fresh", tables[0].Name)
}

func TestIndexKeywordCI_SucceedingBoundary(t *testing.T) {
	t.Parallel()

	// DEFAULT inside DEFAULT_VAL must NOT match — without the
	// succeeding-boundary check the buggy implementation would
	// return the position of DEFAULT inside DEFAULT_VAL.
	assert.Equal(t, -1, indexKeywordCI("DEFAULT_VAL foo", "DEFAULT"),
		"indexKeywordCI must not match a keyword that is a strict prefix of an identifier")

	// Smoke test: a real DEFAULT with a following space still matches.
	assert.Equal(t, 0, indexKeywordCI("DEFAULT '0'", "DEFAULT"))

	// Succeeding boundary via end-of-string: a trailing keyword with
	// nothing after it still matches.
	assert.Equal(t, 4, indexKeywordCI("foo DEFAULT", "DEFAULT"))
}

func TestParseMainSQL_TableWithoutEngineClause(t *testing.T) {
	t.Parallel()
	src := `
CREATE TABLE IF NOT EXISTS ` + "`legacy`" + ` (
  ` + "`k`" + ` int(11) NOT NULL default '0'
) DEFAULT CHARSET=utf8mb4;
`
	tables, err := ParseMainSQL(src)
	require.NoError(t, err)
	require.Len(t, tables, 1)
	assert.Equal(t, "legacy", tables[0].Name)
	require.Len(t, tables[0].Columns, 1)
	assert.Equal(t, Column{Name: "k", Type: "int(11)", Nullable: false, Default: "'0'"}, tables[0].Columns[0])
}
