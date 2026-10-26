// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mysql

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"

	"github.com/RhythmShahriar/roadie/core"
)

// WriterOptions configures a Writer.
type WriterOptions struct {
	// Target is the server the output is for. MariaDB output always starts
	// with the sandbox line; MySQL output never holds it, since a MySQL
	// client fails on it. "" keeps whatever the input had.
	Target core.Dialect
	// FixCollation rewrites utf8mb4_0900_* collations, which MariaDB and
	// MySQL 5.7 don't know, to utf8mb4_unicode_520_ci.
	FixCollation bool
}

// FixedCollation is what FixCollation writes for a utf8mb4_0900_* collation.
const FixedCollation = "utf8mb4_unicode_520_ci"

var reCollation0900 = regexp.MustCompile(`utf8mb4_0900_[A-Za-z0-9_]+`)

// Writer writes batches back as MySQL text. A row with no changed value
// is copied from its source bytes; a changed one is encoded value by
// value by AppendValue, the one place Roadie quotes a value.
type Writer struct {
	w       io.Writer
	o       WriterOptions
	mode    Mode
	vars    Vars // the user variables the dump has set, for sql_mode
	buf     []byte
	started bool
	seq     uint64

	// The open INSERT.
	open  bool
	head  []byte
	wrote bool // a row of the open INSERT was written (and so its head)
}

// NewWriter writes to w. The caller buffers w; Writer writes each batch
// in a few calls.
func NewWriter(w io.Writer, o WriterOptions) *Writer {
	return &Writer{w: w, o: o}
}

// Write writes one batch. Batches must come in increasing Seq order; the
// engine may leave some out (a dropped table), never reorder them.
func (w *Writer) Write(b *core.Batch) error {
	if w.started && b.Seq <= w.seq {
		return fmt.Errorf("mysql: batch %d after batch %d", b.Seq, w.seq)
	}
	w.buf = w.buf[:0]
	if !w.started {
		w.started = true
		if w.o.Target == core.MariaDB && !isSandbox(b) {
			w.buf = append(w.buf, SandboxLine+"\n"...)
		}
	} else if isSandbox(b) && w.o.Target != "" {
		// Written once at the top for MariaDB, never for MySQL.
		w.seq = b.Seq
		return nil
	}
	w.seq = b.Seq
	if b.Pass != nil {
		if w.open {
			return errors.New("mysql: a passthrough statement inside an open INSERT")
		}
		if isSandbox(b) && w.o.Target == core.MySQL {
			return nil
		}
		w.buf = w.pass(w.buf, b.Pass)
		return w.flush()
	}
	if err := w.rows(b); err != nil {
		return err
	}
	return w.flush()
}

// Finish writes what an output needs even when no batch came: the
// sandbox line of an empty MariaDB dump. It fails when an INSERT is still
// open, which means the source stopped mid-statement.
func (w *Writer) Finish() error {
	if w.open {
		return errors.New("mysql: the last INSERT has no end")
	}
	if !w.started && w.o.Target == core.MariaDB {
		w.started = true
		w.buf = append(w.buf[:0], SandboxLine+"\n"...)
		return w.flush()
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

// isSandbox reports whether b is the MariaDB sandbox line.
func isSandbox(b *core.Batch) bool {
	return b.Pass != nil && b.Pass.Kind == core.StmtComment && bytes.HasPrefix(b.Pass.Text, []byte(SandboxLine))
}

// pass appends a passthrough statement with the collation fix and
// follows its sql_mode changes for the encoder.
func (w *Writer) pass(out []byte, st *core.Statement) []byte {
	text := st.Text
	if w.o.FixCollation && (st.Kind == core.StmtCreateTable || st.Kind == core.StmtSet) {
		text = FixCollation(text, w.mode)
	}
	if st.Kind == core.StmtSet {
		w.mode = SQLMode(text, w.mode, &w.vars)
	}
	return append(out, text...)
}

// FixCollation replaces every utf8mb4_0900_* collation outside quoted
// strings with utf8mb4_unicode_520_ci.
func FixCollation(text []byte, m Mode) []byte {
	if !bytes.Contains(text, []byte("utf8mb4_0900_")) {
		return text
	}
	out := make([]byte, 0, len(text))
	for i := 0; i < len(text); {
		c := text[i]
		if c == '\'' || c == '"' && !m.ANSIQuotes {
			j := quotedEnd(text, i, m)
			out = append(out, text[i:j]...)
			i = j
			continue
		}
		j := i
		for j < len(text) && text[j] != '\'' && (text[j] != '"' || m.ANSIQuotes) {
			j++
		}
		out = append(out, reCollation0900.ReplaceAll(text[i:j], []byte(FixedCollation))...)
		i = j
	}
	return out
}

// rows appends the rows of a batch: the head before the first row
// written, no lead before it (so dropping the statement's first row
// leaves no leading comma) and the tail after the last batch only when
// some row was written. A statement whose rows are all dropped is left
// out whole.
func (w *Writer) rows(b *core.Batch) error {
	if b.First {
		if w.open {
			return errors.New("mysql: an INSERT starts inside an open one")
		}
		w.open, w.head, w.wrote = true, b.Head, false
	} else if !w.open {
		return errors.New("mysql: rows with no INSERT open")
	}
	for _, r := range b.Rows {
		if r.Drop {
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
				w.buf = append(w.buf, ',')
			}
			var err error
			if w.buf, err = AppendValue(w.buf, v, w.mode); err != nil {
				return err
			}
		}
		w.buf = append(w.buf, ')')
	}
	if b.Last {
		if w.wrote {
			w.buf = append(w.buf, b.Tail...)
		}
		w.open, w.head = false, nil
	}
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

// Literal shapes a changed Number, Hex or Bit value must have, so a
// value set by a rule can never carry SQL of its own.
var (
	reNumber = regexp.MustCompile(`^[-+]?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][-+]?[0-9]+)?$`)
	reHex    = regexp.MustCompile(`^(0x[0-9A-Fa-f]+|[xX]'[0-9A-Fa-f]*')$`)
	reBit    = regexp.MustCompile(`^(0b[01]+|[bB]'[01]*')$`)
)

// AppendValue appends v as a MySQL literal: String quoted with \0 \' \\
// \n \r and \Z escaped (or only the quote doubled under
// NO_BACKSLASH_ESCAPES), BinaryString the same after _binary, Null as
// NULL, Number, Hex, Bit and Expr as their text. A changed (Dirty) Number,
// Hex or Bit must be a literal of its kind and a changed Expr is refused:
// those are written verbatim, so the check is what keeps a rule's value
// from turning into SQL.
func AppendValue(out []byte, v core.Value, m Mode) ([]byte, error) {
	switch v.Kind {
	case core.Null:
		return append(out, "NULL"...), nil
	case core.String:
		return AppendString(out, v.Raw, m), nil
	case core.BinaryString:
		return AppendString(append(out, "_binary "...), v.Raw, m), nil
	}
	if len(v.Raw) == 0 {
		return out, fmt.Errorf("mysql: a value of kind %d has no text", v.Kind)
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
			return out, fmt.Errorf("mysql: a changed value of kind %d isn't a literal of its kind", v.Kind)
		}
	}
	return append(out, v.Raw...), nil
}

// HexValue is b as a hex literal (0x...), for a rule that changes a
// value the input wrote in hex (--hex-blob), so the output keeps the form.
func HexValue(b []byte) core.Value {
	const digits = "0123456789ABCDEF"
	if len(b) == 0 {
		return core.Value{Kind: core.Hex, Raw: []byte("X''"), Dirty: true}
	}
	raw := make([]byte, 2, 2+2*len(b))
	copy(raw, "0x")
	for _, c := range b {
		raw = append(raw, digits[c>>4], digits[c&15])
	}
	return core.Value{Kind: core.Hex, Raw: raw, Dirty: true}
}

// AppendString appends b as a single-quoted MySQL string.
func AppendString(out, b []byte, m Mode) []byte {
	out = append(out, '\'')
	if m.NoBackslashEscapes {
		for {
			i := bytes.IndexByte(b, '\'')
			if i < 0 {
				break
			}
			out = append(append(out, b[:i+1]...), '\'')
			b = b[i+1:]
		}
		return append(append(out, b...), '\'')
	}
	start := 0
	for i, c := range b {
		var esc byte
		switch c {
		case 0:
			esc = '0'
		case '\'':
			esc = '\''
		case '\\':
			esc = '\\'
		case '\n':
			esc = 'n'
		case '\r':
			esc = 'r'
		case 0x1a:
			esc = 'Z'
		default:
			continue
		}
		out = append(append(out, b[start:i]...), '\\', esc)
		start = i + 1
	}
	return append(append(out, b[start:]...), '\'')
}
