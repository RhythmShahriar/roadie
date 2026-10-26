// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pgsql

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"

	"github.com/RhythmShahriar/roadie/core"
)

// Writer writes batches back as a plain PostgreSQL dump. A row with no
// changed value is copied from its source bytes; a changed one is
// encoded value by value, by AppendCopyField in a COPY block and by
// AppendValue in an INSERT: the one place Roadie quotes a PostgreSQL
// value.
type Writer struct {
	w       io.Writer
	mode    Mode
	buf     []byte
	started bool
	seq     uint64

	// The open COPY or INSERT.
	open  bool
	copy  bool
	head  []byte
	wrote bool // a row of the open INSERT was written (and so its head)
}

// NewWriter writes to w. The caller buffers w; Writer writes each batch
// in one call.
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w}
}

// Write writes one batch. Batches must come in increasing Seq order; the
// engine may leave some out (a dropped table), never reorder them.
func (w *Writer) Write(b *core.Batch) error {
	if w.started && b.Seq <= w.seq {
		return fmt.Errorf("pgsql: batch %d after batch %d", b.Seq, w.seq)
	}
	w.started, w.seq = true, b.Seq
	w.buf = w.buf[:0]
	if b.Pass != nil {
		if w.open {
			return errors.New("pgsql: a passthrough statement inside an open COPY or INSERT")
		}
		if b.Pass.Kind == core.StmtSet {
			w.mode = SetMode(b.Pass.Text, w.mode)
		}
		w.buf = append(w.buf, b.Pass.Text...)
		return w.flush()
	}
	if err := w.rows(b); err != nil {
		return err
	}
	return w.flush()
}

// Finish fails when a COPY or an INSERT is still open, which means the
// source stopped mid-statement.
func (w *Writer) Finish() error {
	if w.open {
		return errors.New("pgsql: the last COPY or INSERT has no end")
	}
	return nil
}

func (w *Writer) flush() error {
	if len(w.buf) == 0 {
		return nil
	}
	_, err := w.w.Write(w.buf)
	return err
}

// rows appends the rows of a batch. A COPY block is always written whole
// (head and end line), even with every row dropped, as pg_dump writes
// one for an empty table. An INSERT is written from its first row kept,
// with no lead before it; it is left out whole when every row is dropped.
func (w *Writer) rows(b *core.Batch) error {
	if b.First {
		if w.open {
			return errors.New("pgsql: a COPY or INSERT starts inside an open one")
		}
		w.open, w.copy, w.head, w.wrote = true, isCopyHead(b.Head), b.Head, false
		if w.copy {
			w.buf = append(w.buf, b.Head...)
		}
	} else if !w.open {
		return errors.New("pgsql: rows with no COPY or INSERT open")
	}
	for _, r := range b.Rows {
		if r.Drop {
			continue
		}
		if w.copy {
			if err := w.copyRow(r); err != nil {
				return err
			}
			continue
		}
		if !w.wrote {
			w.buf = append(w.buf, w.head...)
			w.wrote = true
		} else {
			w.buf = append(w.buf, r.Lead...)
		}
		if r.Src != nil && !dirty(r) {
			w.buf = append(w.buf, r.Src...)
			continue
		}
		w.buf = append(w.buf, '(')
		for i, v := range r.Values {
			if i > 0 {
				w.buf = append(w.buf, ", "...)
			}
			var err error
			if w.buf, err = AppendValue(w.buf, v, w.mode); err != nil {
				return err
			}
		}
		w.buf = append(w.buf, ')')
	}
	if b.Last {
		if w.copy || w.wrote {
			w.buf = append(w.buf, b.Tail...)
		}
		w.open, w.head = false, nil
	}
	return nil
}

// copyRow appends one COPY data line.
func (w *Writer) copyRow(r core.Row) error {
	if r.Src != nil && !dirty(r) {
		w.buf = append(w.buf, r.Src...)
		return nil
	}
	for i, v := range r.Values {
		if i > 0 {
			w.buf = append(w.buf, '\t')
		}
		var err error
		if w.buf, err = AppendCopyField(w.buf, v); err != nil {
			return err
		}
	}
	w.buf = append(w.buf, '\n')
	return nil
}

func dirty(r core.Row) bool {
	for _, v := range r.Values {
		if v.Dirty {
			return true
		}
	}
	return false
}

// errNUL is a value holding a NUL byte, which no PostgreSQL text type
// can store.
var errNUL = errors.New("pgsql: a text value holds a NUL byte, which PostgreSQL text can't store")

// AppendCopyField appends v as a COPY text field: Null as \N, a
// BinaryString as bytea hex (\\x...), anything else as its bytes with
// backslash, tab, newline, carriage return, backspace, form feed and
// vertical tab escaped.
func AppendCopyField(out []byte, v core.Value) ([]byte, error) {
	switch v.Kind {
	case core.Null:
		return append(out, `\N`...), nil
	case core.BinaryString:
		return appendHex(append(out, `\\x`...), v.Raw), nil
	}
	b := v.Raw
	start := 0
	for i, c := range b {
		var esc byte
		switch c {
		case '\\':
			esc = '\\'
		case '\t':
			esc = 't'
		case '\n':
			esc = 'n'
		case '\r':
			esc = 'r'
		case '\b':
			esc = 'b'
		case '\f':
			esc = 'f'
		case '\v':
			esc = 'v'
		case 0:
			return out, errNUL
		default:
			continue
		}
		out = append(append(out, b[start:i]...), '\\', esc)
		start = i + 1
	}
	return append(out, b[start:]...), nil
}

func appendHex(out, b []byte) []byte {
	const digits = "0123456789abcdef"
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&15])
	}
	return out
}

// Literal shapes a changed Number, Hex or Bit value must have, so a
// value set by a rule can never carry SQL of its own.
var (
	reNumber = regexp.MustCompile(`^[-+]?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][-+]?[0-9]+)?$`)
	reHex    = regexp.MustCompile(`^[xX]'[0-9A-Fa-f]*'$`)
	reBit    = regexp.MustCompile(`^[bB]'[01]*'$`)
)

// AppendValue appends v as a PostgreSQL literal for an INSERT: String
// as '...' with the quote doubled (and backslashes doubled when
// standard_conforming_strings is off), BinaryString as a bytea hex
// string '\x...', Null as NULL, Number, Hex, Bit and Expr as their text.
// A changed (Dirty) Number, Hex or Bit must be a literal of its kind and
// a changed Expr is refused: those are written verbatim, so the check is
// what keeps a rule's value from turning into SQL.
func AppendValue(out []byte, v core.Value, m Mode) ([]byte, error) {
	switch v.Kind {
	case core.Null:
		return append(out, "NULL"...), nil
	case core.String:
		return AppendString(out, v.Raw, m)
	case core.BinaryString:
		out = append(out, '\'')
		if m.NoStdStrings {
			out = append(out, '\\')
		}
		return append(appendHex(append(out, `\x`...), v.Raw), '\''), nil
	}
	if len(v.Raw) == 0 {
		return out, fmt.Errorf("pgsql: a value of kind %d has no text", v.Kind)
	}
	if v.Dirty {
		ok := false
		switch v.Kind {
		case core.Number:
			ok = reNumber.Match(v.Raw)
		case core.Hex:
			ok = reHex.Match(v.Raw)
		case core.Bit:
			ok = reBit.Match(v.Raw)
		}
		if !ok {
			return out, fmt.Errorf("pgsql: a changed value of kind %d isn't a literal of its kind", v.Kind)
		}
	}
	return append(out, v.Raw...), nil
}

// AppendString appends b as a single-quoted PostgreSQL string.
func AppendString(out, b []byte, m Mode) ([]byte, error) {
	if bytes.IndexByte(b, 0) >= 0 {
		return out, errNUL
	}
	out = append(out, '\'')
	start := 0
	for i, c := range b {
		if c == '\'' || c == '\\' && m.NoStdStrings {
			out = append(append(out, b[start:i+1]...), c)
			start = i + 1
		}
	}
	return append(append(out, b[start:]...), '\''), nil
}
