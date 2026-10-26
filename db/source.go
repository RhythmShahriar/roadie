// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RhythmShahriar/roadie/core"
)

// pageRows is the number of rows one keyset query reads.
const pageRows = 10000

// source streams tables of a live database. Each table is read in
// primary key order, pageRows rows per query, with the last key of a
// page as the start of the next (keyset pagination), so no query holds
// a huge result and no row is read twice. A table without a primary key
// is read in one streamed query. MySQL, MariaDB and SQLite tables are
// preceded by DROP TABLE IF EXISTS and their CREATE TABLE, so a dump
// written from the source loads into an empty database.
type source struct {
	c      *Conn
	tables []*core.Table
	ti     int // the table being read
	t      *tableRead
	queue  []*core.Batch
	seq    uint64
	counts map[string]int64
	bytes  int64
	ended  bool // the trailer is queued
	fp     core.Fingerprint
	err    error
	closed bool
}

// tableRead is the read state of one table.
type tableRead struct {
	tb     *core.Table
	cols   []int            // Table.Columns index of each selected column
	kinds  []core.ValueKind // per selected column; SQLite decides per value
	keys   []int            // positions in the select list of the key columns
	extra  int              // trailing key columns selected but not part of the row (SQLite rowid)
	keyset bool
	last   []any // key values of the last row read
	pageN  int   // rows read by the open query
	rows   *sql.Rows
	head   []byte
	sel    string   // SELECT ... FROM t
	order  []string // the key columns, qualified
	types  []string
}

func (c *Conn) newSource(ctx context.Context, names []string) (core.Source, error) {
	if c.closed {
		return nil, errClosed
	}
	s, err := c.readSchema(ctx)
	if err != nil {
		return nil, err
	}
	if c.busy {
		return nil, errBusy
	}
	var tables []*core.Table
	if names == nil {
		tables = s.Tables
	} else {
		for _, n := range names {
			tb := s.Table(n)
			if tb == nil {
				return nil, fmt.Errorf("db: %s has no table %s", c.ep.Display, n)
			}
			tables = append(tables, tb)
		}
	}
	c.busy = true
	src := &source{c: c, tables: tables, counts: map[string]int64{}}
	c.active = src
	src.header()
	return src, nil
}

func (s *source) Schema() *core.Schema { return s.c.schema }

func (s *source) Fingerprint() core.Fingerprint { return s.fp }

func (s *source) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.c.busy = false
	if s.c.active == s {
		s.c.active = nil
	}
	if s.t != nil && s.t.rows != nil {
		return s.t.rows.Close()
	}
	return nil
}

func (s *source) Next(ctx context.Context) (*core.Batch, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.closed {
		return nil, errClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for {
		if len(s.queue) > 0 {
			b := s.queue[0]
			s.queue = s.queue[1:]
			return b, nil
		}
		if s.t == nil {
			if s.ti >= len(s.tables) {
				if !s.ended {
					s.ended = true
					s.trailer()
					continue
				}
				s.finish()
				s.err = io.EOF
				return nil, io.EOF
			}
			if err := s.start(ctx, s.tables[s.ti]); err != nil {
				return nil, s.fail(err)
			}
			continue
		}
		b, err := s.batch(ctx)
		if err != nil {
			return nil, s.fail(err)
		}
		if b != nil {
			return b, nil
		}
	}
}

// fail keeps err for every later call; a canceled context is returned
// as it is.
func (s *source) fail(err error) error {
	if s.t != nil && s.t.rows != nil {
		_ = s.t.rows.Close()
		s.t.rows = nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		s.err = err
		return err
	}
	s.err = fmt.Errorf("db: read %s from %s: %w", s.tableName(), s.c.ep.Display, err)
	return s.err
}

func (s *source) tableName() string {
	if s.t != nil {
		return s.t.tb.Name
	}
	return "the schema"
}

// pass queues a statement that isn't row data.
func (s *source) pass(kind core.StmtKind, text string) {
	s.seq++
	s.queue = append(s.queue, &core.Batch{Seq: s.seq, Pass: &core.Statement{Kind: kind, Text: []byte(text)}})
}

// header queues the lines a dump of this dialect starts with, so a dump
// written from the source reads back as one of its tool (and is checked
// for its end line) and loads with foreign keys off.
func (s *source) header() {
	switch s.c.engine {
	case core.MySQL:
		s.pass(core.StmtComment, "-- MySQL dump of a live database, written by Roadie\n")
	case core.MariaDB:
		s.pass(core.StmtComment, "-- MariaDB dump of a live database, written by Roadie\n")
	case core.Postgres:
		s.pass(core.StmtComment, "-- PostgreSQL database dump of a live database, written by Roadie\n")
		return
	default:
		return
	}
	s.pass(core.StmtSet, "/*!40101 SET NAMES utf8mb4 */;\n")
	s.pass(core.StmtSet, "/*!40014 SET @OLD_FOREIGN_KEY_CHECKS=@@FOREIGN_KEY_CHECKS, FOREIGN_KEY_CHECKS=0 */;\n")
}

// trailer queues the end line the dump reader checks for.
func (s *source) trailer() {
	switch s.c.engine {
	case core.MySQL, core.MariaDB:
		s.pass(core.StmtSet, "/*!40014 SET FOREIGN_KEY_CHECKS=@OLD_FOREIGN_KEY_CHECKS */;\n")
		s.pass(core.StmtComment, "-- Dump completed\n")
	case core.Postgres:
		s.pass(core.StmtComment, "-- PostgreSQL database dump complete\n")
	}
}

// finish computes the fingerprint: SHA-256 of the sorted "table\trows\n"
// lines of every table read. Bytes is the size of every value read.
func (s *source) finish() {
	lines := make([]string, 0, len(s.counts))
	for t, n := range s.counts {
		lines = append(lines, t+"\t"+strconv.FormatInt(n, 10)+"\n")
	}
	sort.Strings(lines)
	h := sha256.New()
	for _, l := range lines {
		_, _ = io.WriteString(h, l)
	}
	s.fp = core.Fingerprint{Kind: "db-tables", Value: hex.EncodeToString(h.Sum(nil)), Bytes: s.bytes}
}

// start queues the table's DDL and opens its first query.
func (s *source) start(ctx context.Context, tb *core.Table) error {
	d := s.c.engine
	t := &tableRead{tb: tb}
	var names []string
	for i, col := range tb.Columns {
		if col.Generated {
			continue
		}
		t.cols = append(t.cols, i)
		names = append(names, quoteIdent(d, col.Name))
	}
	t.kinds = make([]core.ValueKind, len(t.cols))
	for j, i := range t.cols {
		t.kinds[j] = valueKind(d, tb.Columns[i])
	}
	s.counts[tb.Name] = 0
	s.t = t
	if len(t.cols) == 0 {
		// Nothing to read or write: every column is generated.
		s.t = nil
		s.ti++
		return nil
	}
	t.head = []byte("INSERT INTO " + quoteIdent(d, tb.Name) + " (" + strings.Join(names, ",") + ") VALUES ")
	sel := make([]string, len(names))
	copy(sel, names)
	if d == core.Postgres {
		// Every value as text: the server's own output form, which COPY
		// and INSERT read back unchanged.
		for i := range sel {
			sel[i] += "::text"
		}
	}
	var keyNames []string
	for _, k := range tb.PrimaryKey {
		pos := -1
		for j, i := range t.cols {
			if tb.Columns[i].Name == k {
				pos = j
			}
		}
		if pos < 0 {
			keyNames = nil // a generated key column: read in one query
			break
		}
		t.keys = append(t.keys, pos)
		// Qualified, so ORDER BY names the column and not the select
		// list's "id"::text of the same name, which sorts as text.
		keyNames = append(keyNames, quoteIdent(d, tb.Name)+"."+quoteIdent(d, k))
		t.types = append(t.types, s.c.pgType(ctx, tb.Name, k))
	}
	if len(keyNames) == 0 {
		t.keys = nil
		t.types = nil
		if d == core.SQLite && !s.c.withoutRowid(ctx, tb.Name) {
			sel = append(sel, "_rowid_")
			t.keys, t.extra = []int{len(names)}, 1
			keyNames = []string{quoteIdent(d, tb.Name) + "._rowid_"}
		}
	}
	if d == core.SQLite {
		// likely() returns its argument unchanged with no declared type,
		// so the driver never turns DATETIME text into a time.Time.
		for i := range names {
			sel[i] = "likely(" + sel[i] + ")"
		}
	}
	t.sel = "SELECT " + strings.Join(sel, ",") + " FROM " + quoteIdent(d, tb.Name)
	t.keyset = len(keyNames) > 0
	t.order = keyNames
	if err := s.ddl(ctx, tb.Name); err != nil {
		return err
	}
	return s.query(ctx)
}

// ddl queues DROP TABLE IF EXISTS and the table's CREATE TABLE as the
// server prints it (MySQL, MariaDB and SQLite; PostgreSQL has no such
// statement, so its tables are filled into an existing schema).
func (s *source) ddl(ctx context.Context, name string) error {
	d := s.c.engine
	var create string
	switch d {
	case core.MySQL, core.MariaDB:
		var tn string
		if err := s.c.conn.QueryRowContext(ctx, "SHOW CREATE TABLE "+quoteIdent(d, name)).Scan(&tn, &create); err != nil {
			return err
		}
	case core.SQLite:
		if err := s.c.conn.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&create); err != nil {
			return err
		}
	default:
		return nil
	}
	s.pass(core.StmtDropTable, "DROP TABLE IF EXISTS "+quoteIdent(d, name)+";\n")
	s.pass(core.StmtCreateTable, create+";\n")
	return nil
}

// query opens the next page of the current table.
func (s *source) query(ctx context.Context) error {
	t := s.t
	q, args := t.sel, []any(nil)
	if t.keyset {
		keys := t.order
		order := strings.Join(keys, ",")
		if t.last != nil {
			ph := make([]string, len(keys))
			for i := range keys {
				ph[i] = s.placeholder(i, t)
			}
			if len(keys) == 1 {
				q += " WHERE " + keys[0] + " > " + ph[0]
			} else {
				q += " WHERE (" + order + ") > (" + strings.Join(ph, ",") + ")"
			}
			args = t.last
		}
		q += " ORDER BY " + order + " LIMIT " + strconv.Itoa(pageRows)
	}
	rows, err := s.c.conn.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	t.rows, t.pageN = rows, 0
	return nil
}

// placeholder is the i-th key parameter. PostgreSQL gets the key as text
// cast back to the column's type, so the comparison uses the column's
// own ordering.
func (s *source) placeholder(i int, t *tableRead) string {
	switch s.c.engine {
	case core.Postgres:
		return "$" + strconv.Itoa(i+1) + "::text::" + t.types[i]
	default:
		return "?"
	}
}

// batch reads rows of the current table into one batch of at most
// core.MaxBatchBytes (a larger single row is a batch of its own). It
// returns nil when the table ended without a row to return.
func (s *source) batch(ctx context.Context) (*core.Batch, error) {
	t := s.t
	n := len(t.cols) + t.extra
	dest := make([]any, n)
	ptrs := make([]any, n)
	for i := range dest {
		ptrs[i] = &dest[i]
	}
	var (
		b    *core.Batch
		size int
		ar   arena
	)
	for size < core.MaxBatchBytes {
		if !t.rows.Next() {
			if err := t.rows.Err(); err != nil {
				return nil, err
			}
			if err := t.rows.Close(); err != nil {
				return nil, err
			}
			t.rows = nil
			if t.keyset && t.pageN == pageRows {
				if err := s.query(ctx); err != nil {
					return nil, err
				}
				if b != nil {
					return b, nil
				}
				continue
			}
			s.t = nil
			s.ti++
			return b, nil
		}
		if err := t.rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		t.pageN++
		if b == nil {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			s.seq++
			b = &core.Batch{Seq: s.seq, Table: t.tb, Columns: t.cols, Head: t.head, Tail: []byte(";\n"), First: true, Last: true}
		}
		row := core.Row{Values: make([]core.Value, len(t.cols))}
		if len(b.Rows) > 0 {
			row.Lead = comma
		}
		for j := range t.cols {
			v, err := s.value(&ar, t.kinds[j], dest[j])
			if err != nil {
				return nil, fmt.Errorf("column %s: %w", t.tb.Columns[t.cols[j]].Name, err)
			}
			row.Values[j] = v
			size += len(v.Raw)
			s.bytes += int64(len(v.Raw))
		}
		if t.keyset {
			t.last = t.last[:0]
			for _, k := range t.keys {
				t.last = append(t.last, keyArg(s.c.engine, dest[k]))
			}
		}
		b.Rows = append(b.Rows, row)
		s.counts[t.tb.Name]++
	}
	return b, nil
}

var comma = []byte(",")

// keyArg is a key value to pass back as a parameter: text for MySQL and
// PostgreSQL (cast to the column type by the query), the value as read
// for SQLite.
func keyArg(d core.Dialect, v any) any {
	if d == core.SQLite {
		if b, ok := v.([]byte); ok {
			return append([]byte(nil), b...)
		}
		return v
	}
	switch x := v.(type) {
	case []byte:
		return string(x)
	case nil:
		return nil
	}
	return string(appendAny(nil, v))
}

// value turns one scanned field into a core.Value whose bytes live in
// the batch's arena.
func (s *source) value(ar *arena, kind core.ValueKind, v any) (core.Value, error) {
	if v == nil {
		return core.Value{Kind: core.Null}, nil
	}
	if s.c.engine == core.SQLite {
		switch x := v.(type) {
		case int64, float64:
			raw := appendAny(ar.scratch(), x)
			ar.tmp = raw[:0]
			return core.Value{Kind: core.Number, Raw: ar.add(raw)}, nil
		case []byte:
			return core.Value{Kind: core.BinaryString, Raw: ar.add(x)}, nil
		case string:
			return core.Value{Kind: core.String, Raw: ar.addString(x)}, nil
		}
		return core.Value{Kind: core.String, Raw: ar.add(appendAny(ar.scratch(), v))}, nil
	}
	raw := appendAny(ar.scratch(), v)
	ar.tmp = raw[:0]
	switch kind {
	case core.Bit:
		return core.Value{Kind: core.Bit, Raw: ar.add(bitLiteral(raw))}, nil
	case core.BinaryString:
		if s.c.engine == core.Postgres {
			// bytea as text is \x followed by hex digits.
			h, ok := strings.CutPrefix(string(raw), `\x`)
			if !ok {
				return core.Value{}, errors.New("a bytea value isn't in hex form")
			}
			dec, err := hex.DecodeString(h)
			if err != nil {
				return core.Value{}, errors.New("a bytea value isn't in hex form")
			}
			return core.Value{Kind: core.BinaryString, Raw: ar.add(dec)}, nil
		}
		return core.Value{Kind: core.BinaryString, Raw: ar.add(raw)}, nil
	case core.Number:
		if !reNumber.Match(raw) {
			// NaN, Infinity: kept as text, which the server reads back.
			return core.Value{Kind: core.String, Raw: ar.add(raw)}, nil
		}
	}
	return core.Value{Kind: kind, Raw: ar.add(raw)}, nil
}

// reNumber is a plain decimal number literal.
var reNumber = regexp.MustCompile(`^[-+]?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][-+]?[0-9]+)?$`)

// bitLiteral writes the bytes of a BIT value as b'0101...'.
func bitLiteral(b []byte) []byte {
	out := make([]byte, 0, 3+8*len(b))
	out = append(out, "b'"...)
	for _, c := range b {
		for i := 7; i >= 0; i-- {
			out = append(out, '0'+(c>>uint(i))&1)
		}
	}
	if len(b) == 0 {
		out = append(out, '0')
	}
	return append(out, '\'')
}

// appendAny appends the text form of a scanned value.
func appendAny(out []byte, v any) []byte {
	switch x := v.(type) {
	case []byte:
		return append(out, x...)
	case string:
		return append(out, x...)
	case int64:
		return strconv.AppendInt(out, x, 10)
	case uint64:
		return strconv.AppendUint(out, x, 10)
	case float32:
		return appendFloat(out, float64(x), 32)
	case float64:
		return appendFloat(out, x, 64)
	case bool:
		if x {
			return append(out, '1')
		}
		return append(out, '0')
	case time.Time:
		return x.AppendFormat(out, "2006-01-02 15:04:05.999999999")
	}
	return fmt.Append(out, v)
}

// appendFloat writes the shortest text that reads back as the same
// float; a whole number keeps no exponent below 1e21, as servers print.
func appendFloat(out []byte, f float64, bits int) []byte {
	if f == math.Trunc(f) && math.Abs(f) < 1e21 {
		return strconv.AppendFloat(out, f, 'f', -1, bits)
	}
	return strconv.AppendFloat(out, f, 'g', -1, bits)
}

// valueKind is how values of a column are written: numbers verbatim,
// binary columns as binary strings, BIT as b'...', the rest as strings.
// SQLite types each value on its own, so this is only a default there.
func valueKind(d core.Dialect, c core.Column) core.ValueKind {
	if c.Binary {
		return core.BinaryString
	}
	base := c.Type
	if i := strings.IndexAny(base, "( "); i >= 0 {
		base = base[:i]
	}
	switch d {
	case core.MySQL, core.MariaDB:
		switch base {
		case "tinyint", "smallint", "mediumint", "int", "integer", "bigint", "decimal", "numeric", "float", "double", "real", "year":
			return core.Number
		case "bit":
			return core.Bit
		case "geometry", "point", "linestring", "polygon", "multipoint", "multilinestring", "multipolygon", "geometrycollection", "geomcollection":
			return core.BinaryString
		}
	case core.Postgres:
		switch base {
		case "smallint", "integer", "bigint", "numeric", "real", "double":
			return core.Number
		}
	}
	return core.String
}

// pgType is the exact PostgreSQL type of a column, for the cast of a key
// parameter: the schema keeps types lower-cased, which would break a
// quoted type name such as "Role".
func (c *Conn) pgType(ctx context.Context, table, col string) string {
	if c.engine != core.Postgres {
		return ""
	}
	var t string
	err := c.conn.QueryRowContext(ctx, `SELECT format_type(a.atttypid, a.atttypmod) FROM pg_catalog.pg_attribute a
JOIN pg_catalog.pg_class c ON c.oid = a.attrelid JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = current_schema() AND c.relname = $1 AND a.attname = $2`, table, col).Scan(&t)
	if err != nil || t == "" {
		return "text"
	}
	return t
}

// withoutRowid reports a SQLite table declared WITHOUT ROWID, which has
// no _rowid_ to page by (it always has a primary key, so this is only a
// safety net).
func (c *Conn) withoutRowid(ctx context.Context, table string) bool {
	var n int
	err := c.conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM pragma_index_list(?) WHERE origin = 'pk'", table).Scan(&n)
	return err != nil || n > 0
}

// quoteIdent quotes an identifier for the dialect.
func quoteIdent(d core.Dialect, name string) string {
	if d == core.MySQL || d == core.MariaDB {
		return "`" + strings.ReplaceAll(name, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// arena holds the bytes of a batch's values in a few large blocks, so a
// value costs no allocation of its own. A block is never grown in
// place, so slices handed out stay valid.
type arena struct {
	buf []byte
	tmp []byte
}

const arenaBlock = 64 << 10

func (a *arena) add(b []byte) []byte {
	if cap(a.buf)-len(a.buf) < len(b) {
		a.buf = make([]byte, 0, max(arenaBlock, len(b)))
	}
	start := len(a.buf)
	a.buf = append(a.buf, b...)
	return a.buf[start:len(a.buf):len(a.buf)]
}

func (a *arena) addString(s string) []byte {
	if cap(a.buf)-len(a.buf) < len(s) {
		a.buf = make([]byte, 0, max(arenaBlock, len(s)))
	}
	start := len(a.buf)
	a.buf = append(a.buf, s...)
	return a.buf[start:len(a.buf):len(a.buf)]
}

// scratch is a reusable buffer for text before it is added.
func (a *arena) scratch() []byte { return a.tmp[:0] }
