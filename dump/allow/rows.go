// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package allow

import (
	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump/mysql"
)

// whyQualified is the danger of a table name with a database in front.
// The dump's own database counts too: it is the production database's
// name, while the copy goes to the one --to names.
var whyQualified = why{"would send the import to a database --to doesn't name",
	"Export the dump again with mysqldump or mariadb-dump, which write table names without their database."}

// qualified reports whether the name at t[i] has a database in front of
// it (db.t). mysqldump, mariadb-dump, phpMyAdmin and Adminer never write
// one, so any qualified table name in a MySQL dump is refused.
func qualified(t toks, i int) bool {
	return t.name(i) && t.p(i+1, '.') && t.name(i+2)
}

// otherDB refuses a statement of kind that names a table in a database.
func otherDB(kind string) Decision {
	return refuse(article(kind)+" "+kind+" statement that names a table with its database", whyQualified)
}

// onTable refuses a statement whose first ON after t[i] names a table
// with its database (CREATE INDEX ... ON, DROP INDEX ... ON, CREATE
// TRIGGER ... ON) and keeps it otherwise.
func onTable(t toks, i int, kind string) Decision {
	for k := i; k < len(t); k++ {
		if t.w(k) == "on" {
			if qualified(t, k+1) {
				return otherDB(kind)
			}
			break
		}
	}
	return keep()
}

// renamesTable reports whether the ALTER TABLE clause c (RENAME [TO|AS]
// x) moves the table to a name with a database. RENAME COLUMN, INDEX and
// KEY rename parts of the table and never move it.
func renamesTable(c toks) bool {
	k := 1
	switch c.w(1) {
	case "column", "index", "key":
		return false
	case "to", "as":
		k = 2
	}
	return qualified(c, k)
}

// mergeUnion reports whether t[k] starts a MERGE table's UNION=(...)
// option that lists a table with its database: the MERGE table would
// read and write that table.
func mergeUnion(t toks, k int) bool {
	if t.w(k) != "union" || !t.p(k+1, '=') && !t.p(k+1, '(') {
		return false
	}
	for m := k + 1; m < len(t) && !t.p(m, ')'); m++ {
		if qualified(t, m) {
			return true
		}
	}
	return false
}

// insertWords may stand between INSERT or REPLACE and the table name.
var insertWords = map[string]bool{"low_priority": true, "delayed": true, "high_priority": true, "ignore": true, "into": true}

// rowsHead judges the head of a MySQL row batch: INSERT or REPLACE, its
// modifiers, the table, an optional column list and VALUES, nothing
// more. The reader only reads that shape as rows; this keeps it so if
// the reader changes. It also refuses a table with its database.
func rowsHead(t toks, verb string) Decision {
	if hasBackslash(t) {
		return refuse("a mysql client command", whyClient)
	}
	bad := refuse(article(verb)+" "+verb+" statement that isn't a list of rows", whyRows)
	i := 1
	for insertWords[t.w(i)] {
		i++
	}
	if qualified(t, i) {
		return otherDB(verb)
	}
	if !t.name(i) {
		return bad
	}
	i++
	if t.p(i, '(') {
		i++
		for t.name(i) {
			i++
			if !t.p(i, ',') {
				break
			}
			i++
		}
		if !t.p(i, ')') {
			return bad
		}
		i++
	}
	if (t.w(i) != "values" && t.w(i) != "value") || i != len(t)-1 {
		return bad
	}
	return keep()
}

// rowsTail judges what follows the last row of a MySQL row batch. Only
// the delimiter and the ON DUPLICATE KEY UPDATE col = VALUES(col) forms
// that phpMyAdmin and mysqldump-style tools write are kept, with MySQL
// 8's row alias (AS new ... col = new.col). Anything else there would
// run unread on the machine or the server that loads the copy: a client
// command, a function call or a subquery.
func rowsTail(t toks, verb string) Decision {
	t = trimDelims(t)
	if len(t) == 0 {
		return keep()
	}
	if hasBackslash(t) {
		return refuse("a mysql client command", whyClient)
	}
	if d, ok := globalChecks(t); ok {
		return d
	}
	bad := refuse(article(verb)+" "+verb+" statement with more after its rows than ON DUPLICATE KEY UPDATE col = VALUES(col)", whyRows)
	i := 0
	if t.w(0) == "as" {
		if !t.name(1) {
			return bad
		}
		i = 2
		if t.p(i, '(') {
			i++
			for t.name(i) && (t.p(i+1, ',') || t.p(i+1, ')')) {
				i += 2
				if t.p(i-1, ')') {
					break
				}
			}
			if !t.p(i-1, ')') {
				return bad
			}
		}
	}
	if !t.is(i, "on", "duplicate", "key", "update") {
		return bad
	}
	for _, c := range clauses(t[i+4:]) {
		if !copyAssign(c) {
			return bad
		}
	}
	return keep()
}

// copyAssign reports whether c is col = VALUES(col) or col = alias.col:
// an assignment that copies a value of the row being inserted.
func copyAssign(c toks) bool {
	k, ok := colRef(c, 0)
	if !ok || !c.p(k, '=') {
		return false
	}
	k++
	if c.w(k) == "values" && c.p(k+1, '(') {
		if k, ok = colRef(c, k+2); !ok || !c.p(k, ')') {
			return false
		}
		return k+1 == len(c)
	}
	k, ok = colRef(c, k)
	return ok && k == len(c)
}

// colRef reads col or x.col at c[i] and returns the index after it.
func colRef(c toks, i int) (int, bool) {
	if !c.name(i) {
		return i, false
	}
	i++
	if c.p(i, '.') {
		if !c.name(i + 1) {
			return i, false
		}
		i += 2
	}
	return i, true
}

// Rows judges the head and tail of a MySQL row batch: the text of the
// INSERT or REPLACE before its first row (when First) and after its last
// row (when Last). The rows themselves are checked by the reader. line
// is the statement's first line, for the message. A row batch can't be
// dropped without its rows, so anything but Keep is refused. PostgreSQL
// batches pass: their COPY heads go through Head and their INSERT rows
// through the reader's own checks.
func (f *Filter) Rows(b *core.Batch, line int64) error {
	if b.Pass != nil || f.o.Dialect == core.Postgres {
		return nil
	}
	o := f.o
	o.Mode = f.mode
	sx := mysqlSyntax(o.Mode)
	st := core.Statement{Kind: core.StmtInsert, Line: line}
	if b.First {
		t := lex(mysql.Body(b.Head, o.Mode), sx)
		f.verb = "INSERT"
		if t.w(0) == "replace" {
			f.verb = "REPLACE"
		}
		if d := rowsHead(t, f.verb); d.Verdict != Keep {
			return refusal(st, o, d)
		}
	}
	if b.Last {
		verb := f.verb
		if verb == "" {
			verb = "INSERT"
		}
		if d := rowsTail(lex(mysql.Body(b.Tail, o.Mode), sx), verb); d.Verdict != Keep {
			return refusal(st, o, d)
		}
	}
	return nil
}
