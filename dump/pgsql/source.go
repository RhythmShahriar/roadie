// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pgsql

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
	Server   string        // server version from the dump head
	Progress core.Progress // gets Table on each new table; may be nil
	// NoTrailer is set for a dump without pg_dump's head comment, so a
	// cut dump isn't said to miss pg_dump's end line.
	NoTrailer bool
	// Offset, when set, gives the input offset for messages and
	// Batch.Offset (the raw offset of a compressed file). Without it the
	// lexer's offset is used.
	Offset func() int64
}

// Source turns a plain PostgreSQL dump into batches: the data lines of
// each COPY ... FROM stdin and the rows of each INSERT, cut at row
// boundaries near core.MaxBatchBytes; every other statement comes as a
// passthrough batch in input order.
type Source struct {
	lex    *Lexer
	o      SourceOptions
	schema *core.Schema
	seq    uint64
	err    error
	table  string // the table being read, for messages
	line   int64  // first line of the statement the last batch came from

	// The open COPY or INSERT, when tb is set.
	tb    *core.Table
	cols  []int
	head  []byte
	arena *Arena
	pend  core.Row
	pends bool
	ins   *Insert // the open INSERT; nil for a COPY
	next  int     // its next row
}

// NewSource reads a plain PostgreSQL dump from r, which must already be
// decompressed.
func NewSource(r io.Reader, o SourceOptions) *Source {
	return &Source{
		lex:    NewLexer(r, Options{MaxValue: o.MaxValue}),
		o:      o,
		schema: &core.Schema{Dialect: core.Postgres, Server: o.Server},
	}
}

// Schema is every table seen so far; it is complete at io.EOF.
func (s *Source) Schema() *core.Schema { return s.schema }

// Table is the table the last batch belonged to, or "" before the first.
func (s *Source) Table() string { return s.table }

// Line is the first line of the statement the last batch came from.
func (s *Source) Line() int64 { return s.line }

// Lexer is the lexer under the source.
func (s *Source) Lexer() *Lexer { return s.lex }

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
	switch {
	case s.ins != nil:
		return s.insertRows(false), nil
	case s.tb != nil:
		return s.copyRows(false)
	}
	st, err := s.lex.Next()
	if errors.Is(err, io.EOF) {
		s.err = io.EOF
		return nil, io.EOF
	}
	if err != nil {
		return nil, s.fail(err)
	}
	s.line = st.Line
	if st.Copy {
		if err := s.open(st.Table, st.Columns, st.Line); err != nil {
			return nil, s.fail(err)
		}
		s.head, s.arena = st.Text, newArena()
		return s.copyRows(true)
	}
	if st.Kind == core.StmtInsert {
		ins, ok, err := ParseInsert(st.Text, s.lex.Mode(), st.Line, s.lex.max)
		if err != nil {
			return nil, s.fail(err)
		}
		if ok {
			if err := s.open(ins.Table, ins.Columns, st.Line); err != nil {
				return nil, s.fail(err)
			}
			for _, r := range ins.Rows {
				if err := s.check(r, st.Line); err != nil {
					return nil, s.fail(err)
				}
			}
			s.ins, s.next = ins, 0
			return s.insertRows(true), nil
		}
	}
	s.track(st)
	b := &core.Batch{Seq: s.seq, Pass: &core.Statement{Kind: st.Kind, Text: st.Text, Line: st.Line}, Offset: s.offset()}
	s.seq++
	return b, nil
}

// track keeps the schema up to date with a passthrough statement.
func (s *Source) track(st *Stmt) {
	m := s.lex.Mode()
	switch st.Kind {
	case core.StmtCreateTable:
		if tb := ParseCreateTable(st.Text, m); tb != nil {
			s.put(tb)
			s.enter(tb.Name)
		}
	case KindAlterTable:
		if old := s.schema.Table(st.Table); old != nil {
			if tb := AlterTable(old, st.Text, m); tb != old {
				s.put(tb)
			}
		}
	case core.StmtCreateIndex:
		if t, c := UniqueIndex(st.Text, m); t != "" {
			if old := s.schema.Table(t); old != nil {
				if j := old.Col(c); j >= 0 && !old.Columns[j].Unique {
					tb := cloneTable(old)
					tb.Columns[j].Unique = true
					s.put(tb)
				}
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

// open starts a COPY or INSERT: its table and the column of each value
// position.
func (s *Source) open(name string, columns []string, line int64) error {
	tb := s.lookup(name)
	var cols []int
	switch {
	case columns != nil && tb == nil:
		// A data-only dump with column lists: the list is the schema.
		tb = &core.Table{Name: name}
		for _, c := range columns {
			tb.Columns = append(tb.Columns, core.Column{Name: c, Nullable: true})
		}
		s.schema.Tables = append(s.schema.Tables, tb)
		fallthrough
	case columns != nil:
		for _, c := range columns {
			j := tb.Col(c)
			if j < 0 {
				e := core.Errorf(core.ErrDumpFormat, 2, "dump-format: %s line %s: the rows of %s name column %s, which its CREATE TABLE doesn't have.",
					s.o.Name, size.Count(line), name, c)
				e.Line = line
				return e
			}
			cols = append(cols, j)
		}
	case tb == nil:
		e := core.Errorf(core.ErrDumpFormat, 2, "dump-format: %s: table %s has no CREATE TABLE before line %s; pass --schema.",
			s.o.Name, name, size.Count(line))
		e.Line = line
		return e
	default:
		// No column list: every column in order. pg_dump writes DEFAULT
		// for a generated one in an INSERT (COPY always has a list).
		for i := range tb.Columns {
			cols = append(cols, i)
		}
	}
	s.tb, s.cols = tb, cols
	s.enter(tb.Name)
	return nil
}

func newArena() *Arena {
	return &Arena{Src: make([]byte, 0, 64<<10), Vals: make([]core.Value, 0, 2048)}
}

// copyRows reads the next batch of data lines of the open COPY.
func (s *Source) copyRows(first bool) (*core.Batch, error) {
	b := &core.Batch{Seq: s.seq, Table: s.tb, Columns: s.cols, First: first}
	if first {
		b.Head = s.head
	}
	a := s.arena
	n := 0
	if s.pends {
		b.Rows = append(b.Rows, s.pend)
		n += len(s.pend.Src)
		s.pend, s.pends = core.Row{}, false
	}
	for {
		full := len(b.Rows) > 0 && n >= core.MaxBatchBytes
		if full {
			// Read the next row into a fresh arena to know whether this
			// batch is the COPY's last.
			a = newArena()
		}
		r, ok, err := s.lex.CopyRow(a)
		if err != nil {
			return nil, s.fail(err)
		}
		if !ok {
			tail, err := s.lex.Tail()
			if err != nil {
				return nil, s.fail(err)
			}
			b.Tail, b.Last = tail, true
			s.tb, s.cols, s.head, s.arena = nil, nil, nil, nil
			break
		}
		if len(s.cols) == 0 && len(r.Values) == 1 && len(r.Values[0].Raw) == 0 && r.Values[0].Kind == core.String {
			r.Values = nil // a table with no columns
		}
		if err := s.check(r, s.lex.Line()-1); err != nil {
			return nil, s.fail(err)
		}
		if full {
			s.pend, s.pends, s.arena = r, true, a
			break
		}
		b.Rows = append(b.Rows, r)
		n += len(r.Src)
	}
	b.Offset = s.offset()
	s.seq++
	return b, nil
}

// insertRows returns the next batch of rows of the open INSERT, whose
// rows were all read with the statement.
func (s *Source) insertRows(first bool) *core.Batch {
	ins := s.ins
	b := &core.Batch{Seq: s.seq, Table: s.tb, Columns: s.cols, First: first}
	if first {
		b.Head = ins.Head
	}
	n, i := 0, s.next
	for i < len(ins.Rows) && (i == s.next || n+len(ins.Rows[i].Lead)+len(ins.Rows[i].Src) <= core.MaxBatchBytes) {
		n += len(ins.Rows[i].Lead) + len(ins.Rows[i].Src)
		i++
	}
	b.Rows = ins.Rows[s.next:i:i]
	s.next = i
	if i == len(ins.Rows) {
		b.Tail, b.Last = ins.Tail, true
		s.ins, s.tb, s.cols = nil, nil, nil
	}
	b.Offset = s.offset()
	s.seq++
	return b
}

// check refuses a row whose value count doesn't match the column count:
// PostgreSQL would refuse it too and a misread row would put values in
// the wrong columns.
func (s *Source) check(r core.Row, line int64) error {
	if len(r.Values) == len(s.cols) {
		return nil
	}
	e := core.Errorf(core.ErrDumpFormat, 2, "dump-format: %s line %s: a row of %s has %d values for %d columns.",
		s.o.Name, size.Count(line), s.tb.Name, len(r.Values), len(s.cols))
	e.Line = line
	return e
}

// fail turns a lexer error into the *core.Error of PRD 17 and keeps it.
func (s *Source) fail(err error) error {
	var (
		te *TruncatedError
		le *TooLargeError
		se *SyntaxError
		fe *CallError
		ce *core.Error
	)
	switch {
	case errors.As(err, &te):
		where := "ends inside a statement at line " + size.Count(te.Line)
		if s.table != "" {
			where = "ends inside table " + s.table
		}
		what := `with no "PostgreSQL database dump complete" line`
		if s.o.NoTrailer {
			what = "in the middle of a statement"
		}
		e := core.Errorf(core.ErrDumpTruncated, 3, `dump-truncated: %s %s at %s, %s. Export it again; the target is untouched.`,
			s.o.Name, where, size.Human(s.offset()), what)
		e.Line = te.Line
		err = e
	case errors.As(err, &le):
		what := "a statement"
		if !le.Statement && s.tb != nil {
			what = "a value in " + s.tb.Name
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
	case errors.As(err, &fe):
		e := core.Errorf(core.ErrDumpRefused, 3, "dump-refused: %s line %s holds a function call in a row of %s, which could run code on the staging database. Export the table with pg_dump -Fp, which writes plain values.",
			s.o.Name, size.Count(fe.Line), fe.Table)
		e.Line = fe.Line
		err = e
	case errors.As(err, &ce):
		// Already worded (dump-bomb from the decompressor).
	}
	s.err = err
	return err
}
