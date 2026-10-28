// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mysql

import (
	"context"
	"errors"
	"io"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump/internal/size"
)

// SourceOptions configures NewSource.
type SourceOptions struct {
	Name     string        // the file name, for messages
	MaxValue int64         // 0 = DefaultMaxValue
	Schema   *core.Schema  // from --schema, for a data-only dump; may be nil
	Dialect  core.Dialect  // MySQL or MariaDB, for Schema().Dialect
	Server   string        // server version from the dump head
	Progress core.Progress // gets Table on each new table; may be nil
	// NoTrailer is set for a dump whose tool writes no end line
	// (phpMyAdmin, Adminer), so a cut dump isn't said to miss one.
	NoTrailer bool
	// Offset, when set, gives the input offset for messages and
	// Batch.Offset (the raw offset of a compressed file). Without it the
	// lexer's offset is used.
	Offset func() int64
}

// Source turns a MySQL dump into batches: rows of INSERT and REPLACE
// statements, cut at row boundaries near core.MaxBatchBytes and every
// other statement as a passthrough batch in input order.
type Source struct {
	lex    *Lexer
	o      SourceOptions
	schema *core.Schema
	seq    uint64
	err    error
	table  string // the table being read, for messages

	// The open INSERT, when ins is set.
	ins     *core.Table
	cols    []int
	stmt    *Stmt
	line    int64 // the first line of the last INSERT or REPLACE
	arena   *Arena
	pending core.Row
	hasPend bool

	// hint is the size of the largest batch so far, so a new batch's
	// arena and row slice start at the size they will need instead of
	// growing by doubling (which copies and allocates about twice over).
	hint struct{ src, raw, vals, rows int }
}

// NewSource reads a MySQL or MariaDB dump from r, which must already be
// decompressed.
func NewSource(r io.Reader, o SourceOptions) *Source {
	d := o.Dialect
	if d == "" {
		d = core.MySQL
	}
	return &Source{
		lex:    NewLexer(r, Options{MaxValue: o.MaxValue}),
		o:      o,
		schema: &core.Schema{Dialect: d, Server: o.Server},
	}
}

// Schema is every table seen so far; it is complete at io.EOF.
func (s *Source) Schema() *core.Schema { return s.schema }

// Lexer is the lexer under the source, for the sql_mode and the sandbox
// line state.
func (s *Source) Lexer() *Lexer { return s.lex }

// InsertLine is the first line of the INSERT or REPLACE the last row
// batch came from, for the allowlist's messages.
func (s *Source) InsertLine() int64 { return s.line }

// Table is the table the last batch belonged to, or "" before the first.
func (s *Source) Table() string { return s.table }

func (s *Source) offset() int64 {
	if s.o.Offset != nil {
		return s.o.Offset()
	}
	return s.lex.Offset()
}

// Next returns the next batch, or io.EOF at the end of the dump.
func (s *Source) Next(ctx context.Context) (*core.Batch, error) {
	if s.err != nil {
		return nil, s.err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.ins != nil {
		return s.rows(false)
	}
	st, err := s.lex.Next()
	if errors.Is(err, io.EOF) {
		s.err = io.EOF
		return nil, io.EOF
	}
	if err != nil {
		return nil, s.fail(err)
	}
	if st.Rows {
		if err := s.open(st); err != nil {
			return nil, s.fail(err)
		}
		return s.rows(true)
	}
	s.track(st)
	b := &core.Batch{Seq: s.seq, Pass: &core.Statement{Kind: st.Kind, Text: st.Text, Line: st.Line}, Offset: s.offset()}
	s.seq++
	return b, nil
}

// track keeps the schema up to date with a passthrough statement.
func (s *Source) track(st *Stmt) {
	switch st.Kind {
	case core.StmtCreateTable:
		tb := ParseCreateTable(st.Text, s.lex.Mode(), s.lookup)
		if tb == nil {
			return
		}
		s.put(tb)
		s.enter(tb.Name)
	case KindAlterTable:
		if old := s.schema.Table(st.Table); old != nil {
			if tb := AlterTable(old, st.Text, s.lex.Mode()); tb != old {
				s.put(tb)
			}
		}
	}
}

// put adds tb to the schema or replaces the table of the same name. The
// old *Table stays as it was for the batches that point at it.
func (s *Source) put(tb *core.Table) {
	for i, t := range s.schema.Tables {
		if t.Name == tb.Name {
			s.schema.Tables[i] = tb
			return
		}
	}
	s.schema.Tables = append(s.schema.Tables, tb)
}

// lookup finds a table in the dump so far, then in --schema; one from
// --schema joins the dump's schema so the engine sees it too.
func (s *Source) lookup(name string) *core.Table {
	if t := s.schema.Table(name); t != nil {
		return t
	}
	if t := s.o.Schema.Table(name); t != nil {
		c := cloneTable(t)
		s.schema.Tables = append(s.schema.Tables, c)
		return c
	}
	return nil
}

// enter notes the table being read and tells the progress.
func (s *Source) enter(name string) {
	if name == s.table {
		return
	}
	s.table = name
	if s.o.Progress != nil {
		s.o.Progress.Table(name, 0)
	}
}

// open starts an INSERT: its table and the column of each value position.
func (s *Source) open(st *Stmt) error {
	tb := s.lookup(st.Table)
	var cols []int
	switch {
	case st.Columns != nil && tb == nil:
		// A data-only dump with column lists: the list is the schema.
		tb = &core.Table{Name: st.Table}
		for _, c := range st.Columns {
			tb.Columns = append(tb.Columns, core.Column{Name: c, Nullable: true})
		}
		s.schema.Tables = append(s.schema.Tables, tb)
		fallthrough
	case st.Columns != nil:
		for _, c := range st.Columns {
			j := tb.Col(c)
			if j < 0 {
				e := core.Errorf(core.ErrDumpFormat, 2, "dump-format: %s line %s: the INSERT into %s names column %s, which its CREATE TABLE doesn't have.",
					s.o.Name, size.Count(st.Line), st.Table, c)
				e.Line = st.Line
				return e
			}
			cols = append(cols, j)
		}
	case tb == nil:
		e := core.Errorf(core.ErrDumpFormat, 2, "dump-format: %s: table %s has no CREATE TABLE before line %s; pass --schema.",
			s.o.Name, st.Table, size.Count(st.Line))
		e.Line = st.Line
		return e
	default:
		cols = make([]int, len(tb.Columns))
		for i := range cols {
			cols[i] = i
		}
	}
	s.ins, s.cols, s.stmt, s.arena = tb, cols, st, s.newArena()
	s.line = st.Line
	s.enter(tb.Name)
	return nil
}

// rows reads the next batch of rows of the open INSERT.
func (s *Source) rows(first bool) (*core.Batch, error) {
	b := &core.Batch{Seq: s.seq, Table: s.ins, Columns: s.cols, First: first}
	if first {
		b.Head = s.stmt.Text
	}
	b.Rows = make([]core.Row, 0, s.hint.rows)
	a := s.arena
	size := 0
	if s.hasPend {
		b.Rows = append(b.Rows, s.pending)
		size += rowSize(s.pending)
		s.pending, s.hasPend = core.Row{}, false
	}
	defer s.learn(b, a)
	for {
		full := len(b.Rows) > 0 && size >= core.MaxBatchBytes
		if full {
			// Read the next row into a fresh arena to know whether this
			// batch is the statement's last.
			a = s.newArena()
		}
		r, ok, err := s.lex.Row(a)
		if err != nil {
			return nil, s.fail(err)
		}
		if !ok {
			if err := s.close(b); err != nil {
				return nil, s.fail(err)
			}
			break
		}
		if err := s.check(r); err != nil {
			return nil, s.fail(err)
		}
		if full || len(b.Rows) > 0 && size+rowSize(r) > core.MaxBatchBytes {
			// The row starts the next batch. When it was read into this
			// batch's arena it stays there; the slices keep it alive.
			if !full {
				a = s.newArena()
			}
			s.pending, s.hasPend, s.arena = r, true, a
			break
		}
		b.Rows = append(b.Rows, r)
		size += rowSize(r)
	}
	b.Offset = s.offset()
	s.seq++
	return b, nil
}

// Arena sizes for the first batch, before there is a hint and the most
// any hint asks for.
const (
	firstSrc  = 64 << 10
	firstVals = 2048
	firstRows = 256
	maxHint   = core.MaxBatchBytes + core.MaxBatchBytes/4
)

// newArena returns an arena sized from the hint.
func (s *Source) newArena() *Arena {
	h := s.hint
	return &Arena{
		Src:  make([]byte, 0, max(h.src, firstSrc)),
		Raw:  make([]byte, 0, h.raw),
		Vals: make([]core.Value, 0, max(h.vals, firstVals)),
	}
}

// learn grows the hint from a finished batch. a is the arena the batch's
// last rows went into.
func (s *Source) learn(b *core.Batch, a *Arena) {
	h := &s.hint
	h.src = min(max(h.src, len(a.Src)), maxHint)
	h.raw = min(max(h.raw, len(a.Raw)), maxHint)
	h.vals = min(max(h.vals, len(a.Vals)), maxHint/8)
	h.rows = min(max(h.rows, len(b.Rows), firstRows), maxHint/16)
}

// rowSize is what a row adds to a batch.
func rowSize(r core.Row) int { return len(r.Lead) + len(r.Src) }

// close ends the open INSERT with its tail.
func (s *Source) close(b *core.Batch) error {
	tail, err := s.lex.Tail()
	if err != nil {
		return err
	}
	b.Tail, b.Last = tail, true
	s.ins, s.cols, s.stmt, s.arena = nil, nil, nil, nil
	return nil
}

// check refuses a row with an expression that isn't data only (SafeExpr)
// and a row whose value count doesn't match the column count: MySQL
// would refuse it too and a misread row would put values in the wrong
// columns.
func (s *Source) check(r core.Row) error {
	for _, v := range r.Values {
		if v.Kind == core.Expr && !SafeExpr(v.Raw, s.lex.Mode()) {
			line := s.stmt.Line
			e := core.Errorf(core.ErrDumpRefused, 3, "dump-refused: %s line %s holds a function call or a name in a row of %s, which could run code on the staging database. Export the table with mysqldump, which writes plain values.",
				s.o.Name, size.Count(line), s.ins.Name)
			e.Line = line
			return e
		}
	}
	if len(r.Values) == len(s.cols) {
		return nil
	}
	line := s.lex.Line()
	e := core.Errorf(core.ErrDumpFormat, 2, "dump-format: %s line %s: a row of %s has %d values for %d columns.",
		s.o.Name, size.Count(line), s.ins.Name, len(r.Values), len(s.cols))
	e.Line = line
	return e
}

// fail turns a lexer error into the *core.Error of PRD 17 and keeps it.
func (s *Source) fail(err error) error {
	var (
		te *TruncatedError
		le *TooLargeError
		se *SyntaxError
		ce *core.Error
	)
	switch {
	case errors.As(err, &te):
		where := "ends inside a statement at line " + size.Count(te.Line)
		if s.table != "" {
			where = "ends inside table " + s.table
		}
		what := `with no "Dump completed" line`
		if s.o.NoTrailer {
			what = "in the middle of a statement"
		}
		e := core.Errorf(core.ErrDumpTruncated, 3, `dump-truncated: %s %s at %s, %s. Export it again; the target is untouched.`,
			s.o.Name, where, size.Human(s.offset()), what)
		e.Line = te.Line
		err = e
	case errors.As(err, &le):
		what := "a statement"
		if !le.Statement && s.ins != nil {
			what = "a value in " + s.ins.Name
		} else if !le.Statement {
			what = "a value"
		}
		e := core.Errorf(core.ErrValueTooLarge, 3, "value-too-large: %s at line %s is over %s. Pass --max-value %s if it's real; the target is untouched.",
			what, size.Count(le.Line), size.Binary(le.Limit), size.Binary(le.Limit*4))
		e.Line = le.Line
		err = e
	case errors.As(err, &se):
		e := core.Errorf(core.ErrDumpFormat, 2, "dump-format: %s line %s: %s.", s.o.Name, size.Count(se.Line), se.Msg)
		e.Line = se.Line
		err = e
	case errors.As(err, &ce):
		// Already worded (dump-bomb from the decompressor).
	}
	s.err = err
	return err
}
