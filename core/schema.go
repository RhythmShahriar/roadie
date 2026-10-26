// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package core

import "strings"

// Dialect is the SQL dialect of a dump or a database.
type Dialect string

// The dialects Roadie reads and writes.
const (
	MySQL    Dialect = "mysql"
	MariaDB  Dialect = "mariadb"
	Postgres Dialect = "postgres"
	SQLite   Dialect = "sqlite"
)

// Column is one column of a table as the dump or the database declares it.
type Column struct {
	Name string
	Type string // as written, lower-cased: "varchar(100)", "longtext", "bigint unsigned"
	// MaxLen is the byte limit for char and varchar (characters x 4 for
	// utf8mb4); 0 means unlimited. Fakes are shortened to fit it.
	MaxLen    int
	Unique    bool
	Nullable  bool
	Generated bool
	Binary    bool
}

// Table is one table with its columns in declaration order.
type Table struct {
	Name         string
	Columns      []Column
	PrimaryKey   []string
	RowsEstimate int64
}

// Schema is every table seen so far in a source.
type Schema struct {
	Dialect Dialect
	Server  string // server version from the dump head or the connection, "8.0.36"
	Prefix  string // the WordPress table prefix when known, "wp_"
	Tables  []*Table
}

// Table returns the table with this exact name, or nil when there is none.
func (s *Schema) Table(name string) *Table {
	if s == nil {
		return nil
	}
	for _, t := range s.Tables {
		if t.Name == name {
			return t
		}
	}
	return nil
}

// Col returns the index of the named column, or -1. An exact match wins;
// otherwise the match ignores case, as MySQL column names do. PostgreSQL
// can hold "Name" and "name" side by side, which the exact pass keeps apart.
func (t *Table) Col(name string) int {
	if t == nil {
		return -1
	}
	for i, c := range t.Columns {
		if c.Name == name {
			return i
		}
	}
	for i, c := range t.Columns {
		if strings.EqualFold(c.Name, name) {
			return i
		}
	}
	return -1
}
