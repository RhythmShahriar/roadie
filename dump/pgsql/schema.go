// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pgsql

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
)

// split cuts the tokens between the '(' at from and its ')' at commas of
// depth 0 and returns the parts and the index after the ')'.
func split(t toks, from int) ([]toks, int) {
	var parts []toks
	depth, start := 0, from+1
	for i := from + 1; i < len(t); i++ {
		switch {
		case t[i].punct('(') || t[i].punct('['):
			depth++
		case t[i].punct(']'):
			depth--
		case t[i].punct(')'):
			if depth == 0 {
				if i > start {
					parts = append(parts, t[start:i])
				}
				return parts, i + 1
			}
			depth--
		case t[i].punct(',') && depth == 0:
			parts = append(parts, t[start:i])
			start = i + 1
		}
	}
	return parts, len(t)
}

// ParseCreateTable reads a CREATE TABLE statement: columns in order with
// the lower-cased type as written, MaxLen (characters x 4 for char and
// varchar, since a PostgreSQL character can take four bytes in UTF-8),
// Unique and PrimaryKey from the column and table constraints, Nullable,
// Generated (GENERATED ALWAYS AS ... STORED) and Binary (bytea). pg_dump
// adds keys later with ALTER TABLE, which AlterTable reads. It returns
// nil for anything it can't read as a table with a column list (CREATE
// TABLE ... AS, PARTITION OF, OF type).
func ParseCreateTable(text []byte, m Mode) *core.Table {
	t := tokens(text, m, 0)
	kind, _ := Classify(text)
	if kind != core.StmtCreateTable {
		return nil
	}
	i := 1
	for !t.is(i, "table") && i < len(t) {
		i++
	}
	i++
	if t.is(i, "if") && t.is(i+1, "not") && t.is(i+2, "exists") {
		i += 3
	}
	name, i := t.objName(i)
	if name == "" || !t.punct(i, '(') {
		return nil
	}
	tb := &core.Table{Name: name}
	defs, _ := split(t, i)
	for _, d := range defs {
		if len(d) == 0 {
			continue
		}
		switch {
		case d.is(0, "constraint"), d.is(0, "primary"), d.is(0, "unique"), d.is(0, "check"),
			d.is(0, "foreign"), d.is(0, "exclude"), d.is(0, "like"):
			tableConstraint(tb, d)
			continue
		}
		c, pk, ok := parseColumn(text, d)
		if !ok {
			continue
		}
		tb.Columns = append(tb.Columns, c)
		if pk {
			tb.PrimaryKey = []string{c.Name}
		}
	}
	return tb
}

// tableConstraint applies PRIMARY KEY (a, b) and UNIQUE (a) from a table
// constraint (with or without CONSTRAINT name in front).
func tableConstraint(tb *core.Table, d toks) {
	i := 0
	if d.is(0, "constraint") {
		i = 2
	}
	switch {
	case d.is(i, "primary") && d.is(i+1, "key"):
		cols := keyColumns(d, i+2)
		if len(cols) > 0 {
			tb.PrimaryKey = cols
		}
		if len(cols) == 1 {
			markUnique(tb, cols[0])
		}
	case d.is(i, "unique"):
		j := i + 1
		if d.is(j, "nulls") {
			j += 2
			if d.is(j-1, "not") {
				j++
			}
		}
		if cols := keyColumns(d, j); len(cols) == 1 {
			markUnique(tb, cols[0])
		}
	}
}

// keyColumns reads the (a, b) column list at i.
func keyColumns(d toks, i int) []string {
	if !d.punct(i, '(') {
		return nil
	}
	parts, _ := split(d, i)
	var cols []string
	for _, p := range parts {
		if len(p) != 1 || !p[0].isName() {
			return nil // an expression key
		}
		cols = append(cols, p[0].name())
	}
	return cols
}

func markUnique(tb *core.Table, name string) {
	if j := tb.Col(name); j >= 0 {
		tb.Columns[j].Unique = true
	}
}

// columnStops are the words that end a column's type.
var columnStops = map[string]bool{
	"not": true, "null": true, "default": true, "constraint": true, "primary": true, "unique": true,
	"check": true, "references": true, "generated": true, "collate": true, "compression": true,
	"storage": true,
}

// parseColumn reads one column definition; pk is set for an inline
// PRIMARY KEY.
func parseColumn(text []byte, d toks) (c core.Column, pk, ok bool) {
	if !d[0].isName() || len(d) < 2 {
		return c, false, false
	}
	c.Name = d[0].name()
	c.Nullable = true
	i := 1
	for i < len(d) && (d[i].kind != tokWord || !columnStops[strings.ToLower(string(d[i].text))]) {
		i++
	}
	if i == 1 {
		return c, false, false // no type
	}
	c.Type = normType(text[d[1].start:d[i-1].end])
	for ; i < len(d); i++ {
		switch {
		case d.is(i, "not") && d.is(i+1, "null"):
			c.Nullable = false
			i++
		case d.is(i, "primary") && d.is(i+1, "key"):
			pk, c.Unique, c.Nullable = true, true, false
			i++
		case d.is(i, "unique"):
			c.Unique = true
		case d.is(i, "generated") && d.is(i+1, "always") && d.is(i+2, "as") && d.punct(i+3, '('):
			c.Generated = true
		}
	}
	setLimits(&c)
	return c, pk, true
}

// normType lower-cases a type and folds its blanks: "character
// varying(255)", "timestamp(3) without time zone".
func normType(b []byte) string {
	f := strings.Fields(strings.ToLower(string(b)))
	s := strings.Join(f, " ")
	return strings.ReplaceAll(s, " (", "(")
}

// setLimits sets MaxLen and Binary from the type.
func setLimits(c *core.Column) {
	ty := strings.TrimPrefix(c.Type, "pg_catalog.")
	if strings.HasSuffix(ty, "]") {
		return // an array: no length to keep to
	}
	if ty == "bytea" {
		c.Binary = true
		return
	}
	for _, p := range []string{"character varying(", "varchar(", "character(", "char(", "bpchar("} {
		if rest, ok := strings.CutPrefix(ty, p); ok {
			n, _, _ := strings.Cut(rest, ")")
			if v, err := strconv.Atoi(n); err == nil && v > 0 {
				c.MaxLen = v * 4
			}
			return
		}
	}
}

func cloneTable(t *core.Table) *core.Table {
	c := *t
	c.Columns = append([]core.Column(nil), t.Columns...)
	c.PrimaryKey = append([]string(nil), t.PrimaryKey...)
	return &c
}

// AlterTable applies ALTER TABLE [ONLY] t ADD CONSTRAINT ... PRIMARY KEY
// or UNIQUE, ADD [COLUMN] and DROP [COLUMN] to tb. It returns a changed
// copy, or tb itself when nothing changed, so the batches already read
// keep the table they had.
func AlterTable(tb *core.Table, text []byte, m Mode) *core.Table {
	t := tokens(text, m, 0)
	i := 2
	if t.is(i, "if") && t.is(i+1, "exists") {
		i += 2
	}
	if t.is(i, "only") {
		i++
	}
	_, i = t.objName(i)
	out := cloneTable(tb)
	changed := false
	actions, _ := splitActions(t, i)
	for _, a := range actions {
		switch {
		case a.is(0, "add") && (a.is(1, "constraint") || a.is(1, "primary") || a.is(1, "unique")):
			before := len(out.PrimaryKey)
			u := uniques(out)
			tableConstraint(out, a[1:])
			changed = changed || len(out.PrimaryKey) != before || uniques(out) != u
		case a.is(0, "add"):
			j := 1
			if a.is(j, "column") {
				j++
			}
			if a.is(j, "if") && a.is(j+1, "not") && a.is(j+2, "exists") {
				j += 3
			}
			if c, pk, ok := parseColumn(text, a[j:]); ok && out.Col(c.Name) < 0 {
				out.Columns = append(out.Columns, c)
				if pk {
					out.PrimaryKey = []string{c.Name}
				}
				changed = true
			}
		case a.is(0, "drop"):
			j := 1
			if a.is(j, "column") {
				j++
			} else if !a.at(j).isName() || a.is(j, "constraint") {
				continue
			}
			if a.is(j, "if") && a.is(j+1, "exists") {
				j += 2
			}
			if k := out.Col(a.at(j).name()); k >= 0 && a.at(j).isName() {
				out.Columns = append(out.Columns[:k], out.Columns[k+1:]...)
				changed = true
			}
		}
	}
	if !changed {
		return tb
	}
	return out
}

func uniques(tb *core.Table) int {
	n := 0
	for _, c := range tb.Columns {
		if c.Unique {
			n++
		}
	}
	return n
}

// splitActions cuts the actions of an ALTER TABLE at commas of depth 0
// from i to the ';'.
func splitActions(t toks, i int) ([]toks, int) {
	var out []toks
	depth, start := 0, i
	for ; i < len(t); i++ {
		switch {
		case t[i].punct('('):
			depth++
		case t[i].punct(')'):
			depth--
		case depth == 0 && (t[i].punct(',') || t[i].punct(';')):
			if i > start {
				out = append(out, t[start:i])
			}
			start = i + 1
		}
	}
	if start < len(t) {
		out = append(out, t[start:])
	}
	return out, i
}

// UniqueIndex reads CREATE UNIQUE INDEX name ON [ONLY] t [USING m] (col)
// and returns the table and its one plain column, or "" for an index on
// several columns, an expression or a partial index.
func UniqueIndex(text []byte, m Mode) (table, column string) {
	t := tokens(text, m, 0)
	if !t.is(0, "create") || !t.is(1, "unique") || !t.is(2, "index") {
		return "", ""
	}
	i := 3
	for i < len(t) && !t.is(i, "on") {
		i++
	}
	i++
	if t.is(i, "only") {
		i++
	}
	table, i = t.objName(i)
	if t.is(i, "using") {
		i += 2
	}
	cols := keyColumns(t, i)
	_, end := split(t, i)
	if len(cols) != 1 || t.is(end, "where") {
		return "", ""
	}
	return table, cols[0]
}

// isCopyHead reports whether a batch head is a COPY statement (blank
// lines before it included).
func isCopyHead(head []byte) bool {
	h := bytes.TrimLeft(head, " \t\r\n\f\v")
	return len(h) >= 4 && eqFold(h[:4], "copy")
}
