// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package pgsql reads and writes plain PostgreSQL dumps (pg_dump -Fp):
// statements, COPY ... FROM stdin data and the INSERT statements of
// pg_dump --inserts and --column-inserts. Like dump/mysql it keeps the
// exact source bytes of every row, so an untouched row is written back
// byte for byte; the Writer is the one place a value is quoted.
package pgsql

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/RhythmShahriar/roadie/core"
)

// The statement kinds this package adds to core's. They have the same
// values as dump/mysql's, so code that follows both reads them alike.
const (
	// KindMeta is a psql meta-command such as \connect or \restrict.
	KindMeta core.StmtKind = "client-command"
	// KindAlterTable is an ALTER TABLE, such as the ADD CONSTRAINT lines
	// pg_dump writes after the data.
	KindAlterTable core.StmtKind = "alter-table"
)

// DefaultMaxValue is the largest single value, COPY line and statement
// when Options leaves MaxValue at 0 (PRD 10.2).
const DefaultMaxValue = 256 << 20

const bufSize = 256 << 10

// Options configures a Lexer.
type Options struct {
	MaxValue int64 // 0 = DefaultMaxValue
}

// Mode is the quoting state a dump sets for itself.
type Mode struct {
	// NoStdStrings is standard_conforming_strings = off: backslashes in
	// '...' are escapes. pg_dump has written "on" since 9.1.
	NoStdStrings bool
}

// Stmt is one statement. For COPY ... FROM stdin (Copy set) Text is the
// COPY statement through its line end; the caller then reads every data
// line with CopyRow and the end line with Tail.
type Stmt struct {
	Kind    core.StmtKind
	Line    int64  // line of the statement's first non-blank byte, from 1
	Text    []byte // the statement as written, blank lines before it included
	Copy    bool
	Table   string   // the table a create, copy, insert, drop or alter names
	Columns []string // the column list of a copy or insert; nil when it has none
}

// Arena holds the bytes and values of the rows of one batch, so a batch
// costs a few growing slices instead of allocations per value.
type Arena struct {
	Src  []byte       // row sources, back to back
	Raw  []byte       // decoded values that held escapes
	Vals []core.Value // every row's values
}

// TruncatedError is input that ended inside a statement or a COPY block.
type TruncatedError struct {
	Line int64 // line where the cut statement starts
}

func (e *TruncatedError) Error() string {
	return fmt.Sprintf("input ends inside the statement at line %d", e.Line)
}

// TooLargeError is a value, a COPY line or a statement over MaxValue.
type TooLargeError struct {
	Line      int64
	Limit     int64
	Statement bool // a whole statement, not one value
}

func (e *TooLargeError) Error() string {
	return fmt.Sprintf("a value at line %d is over %d bytes", e.Line, e.Limit)
}

// SyntaxError is a row or a COPY the reader can't follow. It never quotes
// the input, which may hold personal data.
type SyntaxError struct {
	Line int64
	Msg  string
}

func (e *SyntaxError) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Msg) }

// Lexer splits a plain PostgreSQL dump into statements and COPY rows.
type Lexer struct {
	r    *bufio.Reader
	max  int64
	line int64 // line of the next byte, from 1
	off  int64 // bytes read
	mode Mode
	text []byte
	err  error // the reader's error, other than io.EOF

	inCopy   bool
	stmtLine int64
	tail     []byte
}

// NewLexer reads a decompressed dump from r.
func NewLexer(r io.Reader, o Options) *Lexer {
	m := o.MaxValue
	if m <= 0 {
		m = DefaultMaxValue
	}
	return &Lexer{r: bufio.NewReaderSize(r, bufSize), max: m, line: 1}
}

// Mode is the quoting state after the last statement.
func (l *Lexer) Mode() Mode { return l.mode }

// Offset is the number of decompressed bytes read.
func (l *Lexer) Offset() int64 { return l.off }

// Line is the line of the next byte.
func (l *Lexer) Line() int64 { return l.line }

// window returns the buffered bytes, filling the buffer when it is empty;
// nil at the end of the input.
func (l *Lexer) window() []byte {
	n := l.r.Buffered()
	if n == 0 {
		if _, err := l.r.Peek(1); err != nil {
			if !errors.Is(err, io.EOF) {
				l.err = err
			}
			return nil
		}
		n = l.r.Buffered()
	}
	b, _ := l.r.Peek(n)
	return b
}

// peek returns up to n next bytes without reading them.
func (l *Lexer) peek(n int) []byte {
	b, err := l.r.Peek(n)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		l.err = err
	}
	return b
}

// take moves the next n buffered bytes into the text.
func (l *Lexer) take(n int) {
	b, _ := l.r.Peek(n)
	l.text = append(l.text, b...)
	l.line += int64(bytes.Count(b, nl))
	l.off += int64(n)
	_, _ = l.r.Discard(n)
}

var nl = []byte{'\n'}

// next reads one byte into the text; false at the end of the input.
func (l *Lexer) next() (byte, bool) {
	b := l.window()
	if len(b) == 0 {
		return 0, false
	}
	c := b[0]
	l.take(1)
	return c, true
}

// until reads bytes into the text up to the first one in set; false
// when the input ends first.
func (l *Lexer) until(set *[256]bool) bool {
	for {
		b := l.window()
		if len(b) == 0 {
			return false
		}
		i := 0
		for i < len(b) && !set[b[i]] {
			i++
		}
		l.take(i)
		if i < len(b) {
			return true
		}
		if int64(len(l.text)) > l.max {
			return true // the caller's size check stops it
		}
	}
}

func byteSet(s string) *[256]bool {
	var t [256]bool
	for i := range len(s) {
		t[s[i]] = true
	}
	return &t
}

var (
	stmtSpecial = byteSet(";'\"$-/\\")
	eolSet      = byteSet("\n")
	dollarSet   = byteSet("$")
	commentSet  = byteSet("*/")
	// quoteSets are the bytes that stop a quoted run, by quote byte; the
	// quote with its high bit set is the set with backslash escapes.
	quoteSets = map[byte]*[256]bool{
		'\'': byteSet("'"), '\'' | 0x80: byteSet("'\\"),
		'"': byteSet(`"`), '"' | 0x80: byteSet(`"\`),
	}
)

func (l *Lexer) truncated() error {
	if l.err != nil {
		return l.err
	}
	return &TruncatedError{Line: l.stmtLine}
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

func isWord(c byte) bool {
	return c == '_' || c >= 0x80 || (c >= '0' && c <= '9') || (c|0x20 >= 'a' && c|0x20 <= 'z')
}

// Next returns the next statement, or io.EOF at the end of the input.
// After a statement with Copy set, the caller reads every row with
// CopyRow and then Tail before calling Next again.
func (l *Lexer) Next() (*Stmt, error) {
	if l.inCopy {
		return nil, fmt.Errorf("pgsql: Next called before the rows of the COPY at line %d were read", l.stmtLine)
	}
	l.text = l.text[:0]
	// Blank lines before a statement belong to it.
	for {
		b := l.window()
		i := 0
		for i < len(b) && isSpace(b[i]) {
			i++
		}
		l.take(i)
		if i < len(b) || len(b) == 0 {
			break
		}
		if int64(len(l.text)) > l.max {
			return nil, &TooLargeError{Line: l.line, Limit: l.max, Statement: true}
		}
	}
	if len(l.peek(1)) == 0 {
		if l.err != nil {
			return nil, l.err
		}
		if len(l.text) == 0 {
			return nil, io.EOF
		}
		return l.done(&Stmt{Kind: core.StmtComment, Line: l.line})
	}
	l.stmtLine = l.line
	st := &Stmt{Line: l.line}
	head := l.peek(2)
	switch {
	case bytes.HasPrefix(head, []byte("--")):
		st.Kind = core.StmtComment
		l.toEOL()
	case bytes.HasPrefix(head, []byte("/*")):
		st.Kind = core.StmtComment
		l.take(2)
		if !l.comment() {
			return nil, l.truncated()
		}
		l.eatEOL()
	case head[0] == '\\':
		// psql runs a meta-command to the end of its line.
		st.Kind = KindMeta
		l.toEOL()
	default:
		meta, err := l.statement()
		if err != nil {
			return nil, err
		}
		l.eatEOL()
		st.Kind, st.Table = Classify(l.text)
		if meta {
			st.Kind = KindMeta
		}
		if st.Kind == core.StmtCopy {
			if c, ok, opts := ParseCopy(l.text); ok {
				if opts {
					return nil, &SyntaxError{Line: st.Line, Msg: "a COPY ... FROM stdin with options; Roadie reads the text format pg_dump writes"}
				}
				if !bytes.HasSuffix(l.text, nl) {
					if len(l.peek(1)) == 0 {
						return nil, l.truncated()
					}
					return nil, &SyntaxError{Line: st.Line, Msg: "text follows COPY ... FROM stdin on its line"}
				}
				st.Copy, st.Table, st.Columns = true, c.Table, c.Columns
				l.inCopy = true
			}
		}
	}
	return l.done(st)
}

// done finishes a statement: its size limit, its text and the
// standard_conforming_strings change it makes.
func (l *Lexer) done(st *Stmt) (*Stmt, error) {
	if int64(len(l.text)) > l.max {
		return nil, &TooLargeError{Line: st.Line, Limit: l.max, Statement: true}
	}
	if l.err != nil {
		return nil, l.err
	}
	st.Text = append([]byte(nil), l.text...)
	if st.Kind == core.StmtSet {
		l.mode = SetMode(st.Text, l.mode)
	}
	return st, nil
}

// toEOL reads through the end of the line.
func (l *Lexer) toEOL() {
	if l.until(eolSet) {
		l.take(1)
	}
}

// eatEOL reads blanks after a statement through the end of the line, so
// the line break stays with the statement it ends.
func (l *Lexer) eatEOL() {
	for {
		b := l.peek(1)
		if len(b) == 0 {
			return
		}
		switch b[0] {
		case ' ', '\t', '\r':
			l.take(1)
		case '\n':
			l.take(1)
			return
		default:
			return
		}
	}
}

// comment reads the rest of a /* comment whose opener was read. Comments
// nest in PostgreSQL. False when it doesn't close.
func (l *Lexer) comment() bool {
	depth := 1
	for {
		if int64(len(l.text)) > l.max {
			return true // the caller's size check stops it
		}
		if !l.until(commentSet) {
			return false
		}
		c, _ := l.next()
		n := l.peek(1)
		switch {
		case c == '/' && len(n) == 1 && n[0] == '*':
			l.take(1)
			depth++
		case c == '*' && len(n) == 1 && n[0] == '/':
			l.take(1)
			depth--
			if depth == 0 {
				return true
			}
		}
	}
}

// statement reads a statement through its ';' outside quotes and
// comments. meta reports a backslash outside quotes and comments, which
// psql reads as a meta-command.
func (l *Lexer) statement() (meta bool, err error) {
	for {
		if int64(len(l.text)) > l.max {
			return meta, &TooLargeError{Line: l.stmtLine, Limit: l.max, Statement: true}
		}
		if !l.until(stmtSpecial) {
			return meta, l.truncated()
		}
		if int64(len(l.text)) > l.max {
			continue
		}
		before := len(l.text)
		c, _ := l.next()
		switch c {
		case ';':
			return meta, nil
		case '\'':
			esc := l.mode.NoStdStrings || ePrefix(l.text[:before])
			if !l.quoted('\'', esc) {
				return meta, l.truncated()
			}
		case '"':
			if !l.quoted('"', false) {
				return meta, l.truncated()
			}
		case '$':
			if before > 0 && isWord(l.text[before-1]) {
				continue // part of a name such as a$b
			}
			if tag := l.dollarTag(); tag != nil && !l.dollarBody(tag) {
				return meta, l.truncated()
			}
		case '-':
			if n := l.peek(1); len(n) == 1 && n[0] == '-' {
				l.toEOL()
			}
		case '/':
			if n := l.peek(1); len(n) == 1 && n[0] == '*' {
				l.take(1)
				if !l.comment() {
					return meta, l.truncated()
				}
			}
		case '\\':
			meta = true
		}
	}
}

// ePrefix reports whether text ends with the E of an E'...' string: an e
// or E with no word byte before it.
func ePrefix(text []byte) bool {
	n := len(text)
	if n == 0 || text[n-1]|0x20 != 'e' {
		return false
	}
	return n == 1 || !isWord(text[n-2])
}

// quoted reads the rest of a quoted run whose opening quote was read; a
// doubled quote stays inside. esc makes backslash an escape.
func (l *Lexer) quoted(q byte, esc bool) bool {
	set := quoteSets[q]
	if esc {
		set = quoteSets[q|0x80]
	}
	for {
		if int64(len(l.text)) > l.max {
			return true // the caller's size check stops it
		}
		if !l.until(set) {
			return false
		}
		c, _ := l.next()
		if c == '\\' {
			if _, ok := l.next(); !ok {
				return false
			}
			continue
		}
		if n := l.peek(1); len(n) == 1 && n[0] == q {
			l.take(1)
			continue
		}
		return true
	}
}

// dollarTag reads the rest of a $tag$ opener after its first '$' and
// returns the whole tag, or nil (reading nothing) when the '$' isn't one,
// such as $1.
func (l *Lexer) dollarTag() []byte {
	b := l.peek(64)
	for i, c := range b {
		switch {
		case c == '$':
			tag := append([]byte{'$'}, b[:i+1]...)
			l.take(i + 1)
			return tag
		case c == '_' || c >= 0x80 || (c|0x20 >= 'a' && c|0x20 <= 'z') || (i > 0 && c >= '0' && c <= '9'):
		default:
			return nil
		}
	}
	return nil
}

// dollarBody reads through the closing tag.
func (l *Lexer) dollarBody(tag []byte) bool {
	for {
		if int64(len(l.text)) > l.max {
			return true // the caller's size check stops it
		}
		if !l.until(dollarSet) {
			return false
		}
		if bytes.Equal(l.peek(len(tag)), tag) {
			l.take(len(tag))
			return true
		}
		l.take(1)
	}
}

// CopyRow reads the next data line of the open COPY into a. It returns
// false at the end line ('\.'); Tail then returns that line. Src is the
// line with its line end; each tab-separated field is a value: \N is
// Null, anything else a String decoded from COPY's escapes.
func (l *Lexer) CopyRow(a *Arena) (core.Row, bool, error) {
	if !l.inCopy {
		return core.Row{}, false, errors.New("pgsql: CopyRow called with no COPY open")
	}
	start := len(a.Src)
	line := l.line
	for {
		chunk, err := l.r.ReadSlice('\n')
		a.Src = append(a.Src, chunk...)
		l.off += int64(len(chunk))
		if int64(len(a.Src)-start) > l.max {
			return core.Row{}, false, &TooLargeError{Line: line, Limit: l.max}
		}
		if err == nil {
			l.line++
			break
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if !errors.Is(err, io.EOF) {
			return core.Row{}, false, err
		}
		// The input ended without a line end: only the end line may.
		if bytes.Equal(a.Src[start:], []byte(`\.`)) {
			break
		}
		return core.Row{}, false, &TruncatedError{Line: l.stmtLine}
	}
	src := a.Src[start:]
	body := bytes.TrimSuffix(bytes.TrimSuffix(src, nl), []byte{'\r'})
	if bytes.Equal(body, []byte(`\.`)) {
		l.tail = append(l.tail[:0], src...)
		a.Src = a.Src[:start]
		l.inCopy = false
		return core.Row{}, false, nil
	}
	vs := len(a.Vals)
	for {
		i := bytes.IndexByte(body, '\t')
		f := body
		if i >= 0 {
			f = body[:i]
		}
		v, err := copyField(a, f, line, l.max)
		if err != nil {
			return core.Row{}, false, err
		}
		a.Vals = append(a.Vals, v)
		if i < 0 {
			break
		}
		body = body[i+1:]
	}
	end := len(a.Src)
	return core.Row{
		Values: a.Vals[vs:len(a.Vals):len(a.Vals)],
		Src:    a.Src[start:end:end],
	}, true, nil
}

// copyField decodes one COPY text field. A field with no backslash is
// used where it lies; one with escapes is decoded into a.Raw.
func copyField(a *Arena, f []byte, line, limit int64) (core.Value, error) {
	if len(f) == 2 && f[0] == '\\' && f[1] == 'N' {
		return core.Value{Kind: core.Null}, nil
	}
	i := bytes.IndexByte(f, '\\')
	if i < 0 {
		return core.Value{Kind: core.String, Raw: f[:len(f):len(f)]}, nil
	}
	start := len(a.Raw)
	a.Raw = AppendCopyUnescape(a.Raw, f)
	if int64(len(a.Raw)-start) > limit {
		return core.Value{}, &TooLargeError{Line: line, Limit: limit}
	}
	return core.Value{Kind: core.String, Raw: a.Raw[start:len(a.Raw):len(a.Raw)]}, nil
}

// AppendCopyUnescape appends the bytes a COPY text field stands for: \b
// \f \n \r \t \v and \\, \ooo octal (one to three digits), \xhh hex (one
// or two digits); a backslash before any other byte is dropped, as
// PostgreSQL does.
func AppendCopyUnescape(out, f []byte) []byte {
	for {
		i := bytes.IndexByte(f, '\\')
		if i < 0 {
			return append(out, f...)
		}
		out = append(out, f[:i]...)
		f = f[i+1:]
		if len(f) == 0 {
			return append(out, '\\') // a lone backslash at the end stays
		}
		c := f[0]
		f = f[1:]
		switch c {
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'v':
			out = append(out, '\v')
		case '0', '1', '2', '3', '4', '5', '6', '7':
			v := int(c - '0')
			for k := 0; k < 2 && len(f) > 0 && f[0] >= '0' && f[0] <= '7'; k++ {
				v = v*8 + int(f[0]-'0')
				f = f[1:]
			}
			out = append(out, byte(v)) // #nosec G115 -- PostgreSQL keeps the low byte too
		case 'x':
			if len(f) == 0 || unhex(f[0]) < 0 {
				out = append(out, 'x')
				continue
			}
			v := unhex(f[0])
			f = f[1:]
			if len(f) > 0 && unhex(f[0]) >= 0 {
				v = v*16 + unhex(f[0])
				f = f[1:]
			}
			out = append(out, byte(v)) // #nosec G115 -- two hex digits
		default:
			out = append(out, c)
		}
	}
}

func unhex(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// Tail returns the end line of the COPY after CopyRow returned false.
func (l *Lexer) Tail() ([]byte, error) {
	if l.inCopy {
		return nil, errors.New("pgsql: Tail called before the last row was read")
	}
	return append([]byte(nil), l.tail...), nil
}
