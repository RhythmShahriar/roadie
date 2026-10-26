// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package mysql reads and writes MySQL and MariaDB dumps: mysqldump,
// mariadb-dump, wp db export, phpMyAdmin and Adminer exports.
//
// The lexer is a byte-level state machine over a fixed buffer, with no
// line limit and no regexp on the hot path. Statement boundaries follow
// the mysql command-line client, since that is what imports the output:
// ';' (or the current DELIMITER) ends a statement outside quotes, '--'
// and '#' comments and plain '/* */' comments. Versioned comments
// (/*!50003 ... */) are code to the client, so a delimiter inside one ends
// the statement, exactly as the client would split it.
//
// INSERT and REPLACE statements with a VALUES list are never held whole:
// Next returns their head and Row reads one row at a time, so an INSERT
// of any size streams through a few MiB.
package mysql

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/RhythmShahriar/roadie/core"
)

// The statement kinds this package adds to core's. The allowlist judges
// them; none of them carries row data.
const (
	// KindDelimiter is a DELIMITER line of the mysql client.
	KindDelimiter core.StmtKind = "delimiter"
	// KindClient is a mysql client command (\!, system, source, \. and
	// the other named commands) or a statement holding a backslash
	// command outside quotes.
	KindClient core.StmtKind = "client-command"
	// KindUse is USE, a client command and a statement at once.
	KindUse core.StmtKind = "use"
	// KindAlterTable is an ALTER TABLE that isn't DISABLE or ENABLE KEYS,
	// such as the ADD PRIMARY KEY lines phpMyAdmin writes after the data.
	KindAlterTable core.StmtKind = "alter-table"
)

// SandboxLine is the first line mariadb-dump writes since the fix for
// CVE-2024-21096. The MariaDB client reads it as "enable the sandbox
// mode"; a MySQL client fails on it with Unknown command '\-'.
const SandboxLine = `/*M!999999\- enable the sandbox mode */`

// DefaultMaxValue is the largest single value when Options leaves
// MaxValue at 0 (PRD 10.2).
const DefaultMaxValue = 256 << 20

const bufSize = 256 << 10

// Options configures a Lexer.
type Options struct {
	MaxValue int64 // 0 = DefaultMaxValue
}

// Stmt is one statement. For an INSERT or REPLACE with a VALUES list
// (Rows set), Text is the head up to the first row's '(' and the caller
// reads the rows with Row and the rest with Tail.
type Stmt struct {
	Kind    core.StmtKind
	Line    int64    // line of the statement's first non-blank byte, from 1
	Text    []byte   // the statement as written, or the head when Rows
	Rows    bool     // an INSERT or REPLACE whose rows follow
	Table   string   // the table an insert, create, drop, lock or alter names
	Columns []string // the column list of an insert; nil when it has none
	Sandbox bool     // the MariaDB sandbox line
}

// Arena holds the bytes and values of the rows of one batch. Row appends
// to it and the rows it returns point into it, so a batch of untouched
// rows costs a few growing slices instead of allocations per value.
type Arena struct {
	Src  []byte       // leads and row sources, back to back
	Raw  []byte       // decoded strings that held escapes
	Vals []core.Value // every row's values
}

// TruncatedError is input that ended inside a statement.
type TruncatedError struct {
	Line   int64 // line where the cut statement starts
	Offset int64 // decompressed bytes read
}

func (e *TruncatedError) Error() string {
	return fmt.Sprintf("input ends inside the statement at line %d", e.Line)
}

// TooLargeError is a value, or a statement passed through whole, over
// the MaxValue limit.
type TooLargeError struct {
	Line      int64
	Limit     int64
	Statement bool // a whole statement, not one value
}

func (e *TooLargeError) Error() string {
	return fmt.Sprintf("a value at line %d is over %d bytes", e.Line, e.Limit)
}

// SyntaxError is a row the lexer can't read. It never quotes the input,
// which may hold personal data.
type SyntaxError struct {
	Line int64
	Msg  string
}

func (e *SyntaxError) Error() string { return fmt.Sprintf("line %d: %s", e.Line, e.Msg) }

// scanner is a refillable window on the input. Bytes between mark and
// pos are consumed but not yet appended to *dst; flush appends them and
// counts their lines.
type scanner struct {
	r     io.Reader
	buf   []byte
	pos   int
	end   int
	mark  int
	base  int64 // input offset of buf[0]
	dst   *[]byte
	line  int64 // line of buf[mark]
	err   error
	qAt   int64 // input offset of the next quote nextQuote found, or -1
	qUpto int64 // input offset nextQuote searched up to
}

var newline = []byte{'\n'}

func (s *scanner) flush() {
	if s.pos > s.mark {
		seg := s.buf[s.mark:s.pos]
		if s.dst != nil {
			*s.dst = append(*s.dst, seg...)
		}
		s.line += int64(bytes.Count(seg, newline))
		s.mark = s.pos
	}
}

// setDst sends the bytes consumed from now on to d.
func (s *scanner) setDst(d *[]byte) {
	s.flush()
	s.dst = d
}

// at is the offset in *dst the byte at pos will have once flushed.
func (s *scanner) at() int { return len(*s.dst) + s.pos - s.mark }

// appendSince appends the consumed bytes from dst offset from up to pos.
func (s *scanner) appendSince(out []byte, from int) []byte {
	d := *s.dst
	if from < len(d) {
		out = append(out, d[from:]...)
		from = len(d)
	}
	return append(out, s.buf[s.mark+from-len(d):s.pos]...)
}

// lineAt is the line of the byte at pos.
func (s *scanner) lineAt() int64 { return s.line + int64(bytes.Count(s.buf[s.mark:s.pos], newline)) }

// offset is the input offset of pos.
func (s *scanner) offset() int64 { return s.base + int64(s.pos) }

// ensure makes n bytes from pos available, reading as needed. It returns
// false at the end of the input or on a read error (in s.err).
func (s *scanner) ensure(n int) bool {
	if s.end-s.pos >= n {
		return true
	}
	if s.err != nil {
		return false
	}
	s.flush()
	copy(s.buf, s.buf[s.pos:s.end])
	s.base += int64(s.pos)
	s.end -= s.pos
	s.pos, s.mark = 0, 0
	empty := 0
	for s.end < n && s.err == nil {
		m, err := s.r.Read(s.buf[s.end:])
		s.end += m
		switch {
		case err != nil:
			s.err = err
		case m == 0:
			if empty++; empty > 100 {
				s.err = io.ErrNoProgress
			}
		}
	}
	return s.end-s.pos >= n
}

// peek returns up to n bytes from pos without consuming them.
func (s *scanner) peek(n int) []byte {
	s.ensure(n)
	return s.buf[s.pos:min(s.end, s.pos+n)]
}

// resetQuote forgets where the last quote search ended, for a new string.
func (s *scanner) resetQuote() { s.qAt, s.qUpto = -1, 0 }

// nextQuote is the index from pos of the next q in the window, or -1. It
// remembers how far it searched, in input offsets, so a long string with
// many escapes isn't searched again from each escape to the window end.
func (s *scanner) nextQuote(q byte) int {
	abs := s.base + int64(s.pos)
	if s.qAt >= abs {
		if s.qAt < s.base+int64(s.end) {
			return int(s.qAt - abs)
		}
	}
	from := max(abs, s.qUpto)
	if from >= s.base+int64(s.end) {
		return -1
	}
	i := bytes.IndexByte(s.buf[from-s.base:s.end], q)
	if i < 0 {
		s.qAt, s.qUpto = -1, s.base+int64(s.end)
		return -1
	}
	s.qAt = from + int64(i)
	s.qUpto = s.qAt
	return int(s.qAt - abs)
}

// readErr is the read error to report, or nil at a clean end of input.
func (s *scanner) readErr() error {
	if s.err == nil || errors.Is(s.err, io.EOF) {
		return nil
	}
	return s.err
}

// Lexer splits a MySQL dump into statements and rows.
type Lexer struct {
	s        scanner
	max      int64
	delim    []byte
	plain    [256]bool // bytes the statement scan can skip over
	mode     Mode
	sandbox  bool
	text     []byte
	stmtLine int64
	rows     int // 0 = no rows open, 1 = before the first row, 2 = after a row
	tailPre  []byte
	spans    []span
	client   bool // the statement being scanned holds a backslash command
}

// Mode is the part of sql_mode that changes how a dump is read.
type Mode struct {
	NoBackslashEscapes bool // backslashes in strings are plain bytes
	ANSIQuotes         bool // "..." is an identifier, not a string
}

// NewLexer reads a dump from r.
func NewLexer(r io.Reader, o Options) *Lexer {
	l := &Lexer{s: scanner{r: r, buf: make([]byte, bufSize), line: 1}, max: o.MaxValue}
	if l.max <= 0 {
		l.max = DefaultMaxValue
	}
	l.setDelim([]byte{';'})
	return l
}

func (l *Lexer) setDelim(d []byte) {
	l.delim = d
	for i := range l.plain {
		l.plain[i] = true
	}
	for _, c := range []byte("'\"`-#/\\") {
		l.plain[c] = false
	}
	l.plain[d[0]] = false
}

// Mode is the sql_mode state at the current position.
func (l *Lexer) Mode() Mode { return l.mode }

// Sandbox reports whether the input held the MariaDB sandbox line.
func (l *Lexer) Sandbox() bool { return l.sandbox }

// Offset is the number of input bytes consumed.
func (l *Lexer) Offset() int64 { return l.s.offset() }

// Line is the line of the next byte to read.
func (l *Lexer) Line() int64 { return l.s.lineAt() }

func (l *Lexer) truncated() error {
	if err := l.s.readErr(); err != nil {
		return err
	}
	return &TruncatedError{Line: l.stmtLine, Offset: l.s.offset()}
}

// isSpace is SQL whitespace.
func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

func isWord(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '$' || c >= 0x80
}

// lower is ASCII lower case of one byte.
func lower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

// eqFold compares b with the lower-case ASCII word w.
func eqFold(b []byte, w string) bool {
	if len(b) != len(w) {
		return false
	}
	for i := range b {
		if lower(b[i]) != w[i] {
			return false
		}
	}
	return true
}

// clientCommands are the mysql and mariadb client's named commands. At
// the start of a statement the client runs them up to the end of the
// line, with no delimiter (mysql.cc find_command).
var clientCommands = map[string]bool{
	"?": true, "clear": true, "connect": true, "edit": true, "ego": true, "exit": true, "go": true,
	"help": true, "nopager": true, "notee": true, "pager": true, "print": true, "prompt": true,
	"quit": true, "rehash": true, "source": true, "status": true, "system": true, "tee": true,
	"charset": true, "warnings": true, "nowarning": true, "resetconnection": true,
	"query_attributes": true, "ssl_session_data_print": true, "sandbox": true,
}

// Next returns the next statement, or io.EOF at the end of the input.
// After a statement with Rows set, the caller reads every row with Row
// and then Tail before calling Next again.
func (l *Lexer) Next() (*Stmt, error) {
	if l.rows != 0 {
		return nil, fmt.Errorf("mysql: Next called before the rows of the statement at line %d were read", l.stmtLine)
	}
	s := &l.s
	l.text = l.text[:0]
	s.setDst(&l.text)
	l.client = false
	// Blank lines before a statement belong to it.
	for s.ensure(1) && isSpace(s.buf[s.pos]) {
		s.pos++
	}
	if !s.ensure(1) {
		if err := s.readErr(); err != nil {
			return nil, err
		}
		if len(l.text) == 0 && s.pos == s.mark {
			return nil, io.EOF
		}
		return l.done(&Stmt{Kind: core.StmtComment, Line: s.lineAt()})
	}
	l.stmtLine = s.lineAt()
	st := &Stmt{Line: l.stmtLine}
	head := s.peek(len(SandboxLine) + 1)
	c := head[0]
	switch {
	case bytes.HasPrefix(head, []byte(SandboxLine)):
		l.sandbox = true
		st.Kind, st.Sandbox = core.StmtComment, true
		l.toEOL()
	case l.lineComment():
		st.Kind = core.StmtComment
		l.toEOL()
	case c == '/' && len(head) > 1 && head[1] == '*' && !versioned(head):
		st.Kind = core.StmtComment
		if !l.blockComment() {
			return nil, l.truncated()
		}
		l.eatEOL()
	case c == '\\':
		st.Kind = KindClient
		l.toEOL()
	default:
		word := l.peekWord()
		switch {
		case eqFold(word, "delimiter") && l.delimiterLine(st):
		case clientCommands[string(bytes.ToLower(word))] && len(word) > 0:
			st.Kind = KindClient
			l.toEOL()
		case eqFold(word, "use"):
			st.Kind = KindUse
			l.toEOL()
		case eqFold(word, "insert"), eqFold(word, "replace"):
			if err := l.insert(st); err != nil {
				return nil, err
			}
			if st.Rows {
				l.rows = 1
				st.Text = append([]byte(nil), l.text...)
				return st, nil
			}
		default:
			if err := l.scanToDelim(); err != nil {
				return nil, err
			}
			s.flush()
			st.Kind, st.Table = Classify(l.text, l.mode)
		}
	}
	return l.done(st)
}

// done finishes a statement: its text, the client-command override and
// the sql_mode and DELIMITER changes it makes.
func (l *Lexer) done(st *Stmt) (*Stmt, error) {
	l.s.flush()
	if int64(len(l.text)) > l.max {
		return nil, &TooLargeError{Line: st.Line, Limit: l.max, Statement: true}
	}
	st.Text = append([]byte(nil), l.text...)
	if l.client && st.Kind != core.StmtComment {
		st.Kind = KindClient
	}
	if st.Kind == core.StmtSet {
		l.mode = SQLMode(Body(st.Text, l.mode), l.mode)
	}
	return st, nil
}

// lineComment reports whether a '--' or '#' comment starts at pos. The
// client wants whitespace or a control character after '--'.
func (l *Lexer) lineComment() bool {
	b := l.s.peek(3)
	switch {
	case len(b) >= 1 && b[0] == '#':
		return true
	case len(b) >= 2 && b[0] == '-' && b[1] == '-':
		return len(b) == 2 || b[2] <= ' '
	}
	return false
}

// versioned reports whether b starts a /*! or /*M! comment, which the
// client treats as code.
func versioned(b []byte) bool {
	return len(b) >= 3 && b[2] == '!' || len(b) >= 4 && b[2] == 'M' && b[3] == '!'
}

// toEOL consumes up to and including the next line feed, or the input.
func (l *Lexer) toEOL() {
	s := &l.s
	for s.ensure(1) {
		if i := bytes.IndexByte(s.buf[s.pos:s.end], '\n'); i >= 0 {
			s.pos += i + 1
			return
		}
		s.pos = s.end
	}
}

// blockComment consumes a '/* ... */' comment starting at pos.
func (l *Lexer) blockComment() bool {
	s := &l.s
	s.pos += 2
	for {
		if !s.ensure(2) {
			s.pos = s.end
			return false
		}
		i := bytes.Index(s.buf[s.pos:s.end], []byte("*/"))
		if i >= 0 {
			s.pos += i + 2
			return true
		}
		// Keep a trailing '*' for the next round.
		s.pos = s.end - 1
	}
}

// eatEOL consumes spaces and one line end after a statement, when the
// line holds nothing else.
func (l *Lexer) eatEOL() {
	b := l.s.peek(256)
	i := 0
	for i < len(b) && (b[i] == ' ' || b[i] == '\t') {
		i++
	}
	switch {
	case i < len(b) && b[i] == '\n':
		l.s.pos += i + 1
	case i+1 < len(b) && b[i] == '\r' && b[i+1] == '\n':
		l.s.pos += i + 2
	}
}

// peekWord returns the word at pos without consuming it.
func (l *Lexer) peekWord() []byte {
	b := l.s.peek(64)
	i := 0
	for i < len(b) && isWord(b[i]) {
		i++
	}
	if i == 0 && len(b) > 0 && b[0] == '?' {
		i = 1
	}
	return b[:i]
}

// delimiterLine reads "DELIMITER x" up to the end of the line.
func (l *Lexer) delimiterLine(st *Stmt) bool {
	b := l.s.peek(len("delimiter") + 1)
	if len(b) <= len("delimiter") || (b[len("delimiter")] != ' ' && b[len("delimiter")] != '\t') {
		return false
	}
	start := l.s.at()
	l.toEOL()
	l.s.flush()
	arg := bytes.TrimSpace(l.text[start+len("delimiter"):])
	if i := bytes.IndexAny(arg, " \t"); i >= 0 {
		arg = arg[:i]
	}
	st.Kind = KindDelimiter
	if len(arg) > 0 {
		l.setDelim(append([]byte(nil), arg...))
	}
	return true
}

// atDelim reports whether the delimiter starts at pos.
func (l *Lexer) atDelim() bool {
	if len(l.delim) == 1 {
		return true // the caller saw delim[0]
	}
	return bytes.Equal(l.s.peek(len(l.delim)), l.delim)
}

// scanToDelim consumes code up to and including the delimiter and the
// line end after it.
func (l *Lexer) scanToDelim() error {
	s := &l.s
	d0 := l.delim[0]
	for {
		for s.pos < s.end && l.plain[s.buf[s.pos]] {
			s.pos++
		}
		if s.pos == s.end {
			if !s.ensure(1) {
				return l.truncated()
			}
			continue
		}
		c := s.buf[s.pos]
		switch {
		case c == d0 && l.atDelim():
			s.pos += len(l.delim)
			l.eatEOL()
			return nil
		case c == '\'' || c == '"' || c == '`':
			s.pos++
			if !l.skipQuoted(c) {
				return l.truncated()
			}
		case (c == '-' || c == '#') && l.lineComment():
			l.toEOL()
		case c == '/' && bytes.Equal(s.peek(2), []byte("/*")) && !versioned(s.peek(4)):
			if !l.blockComment() {
				return l.truncated()
			}
		case c == '\\':
			l.client = true
			s.pos++
		default:
			s.pos++
		}
	}
}

// escapes reports whether a backslash escapes the next byte inside quote
// q, as the client and server read it.
func (l *Lexer) escapes(q byte) bool {
	return q != '`' && !l.mode.NoBackslashEscapes && (q != '"' || !l.mode.ANSIQuotes)
}

// firstBackslash is the index of the first backslash in b before the
// quote at i (all of b when i < 0), or -1. Searching only up to the quote
// keeps a short string from scanning the whole buffer for a backslash.
func firstBackslash(b []byte, i int) int {
	if i >= 0 {
		b = b[:i]
	}
	return bytes.IndexByte(b, '\\')
}

// skipQuoted consumes the rest of a quoted string or identifier opened by
// q. A doubled q is part of it.
func (l *Lexer) skipQuoted(q byte) bool {
	s := &l.s
	esc := l.escapes(q)
	s.resetQuote()
	for {
		if !s.ensure(1) {
			return false
		}
		b := s.buf[s.pos:s.end]
		i := s.nextQuote(q)
		if esc {
			if j := firstBackslash(b, i); j >= 0 {
				s.pos += j
				if !s.ensure(2) {
					s.pos = s.end
					return false
				}
				s.pos += 2
				continue
			}
		}
		if i < 0 {
			s.pos = s.end
			continue
		}
		s.pos += i + 1
		if !s.ensure(1) {
			return true // the input ends right after the closing quote
		}
		if s.buf[s.pos] == q {
			s.pos++ // a doubled quote: still inside
			continue
		}
		return true
	}
}

// skipGap consumes whitespace and comments inside a statement.
func (l *Lexer) skipGap() bool {
	s := &l.s
	for s.ensure(1) {
		c := s.buf[s.pos]
		switch {
		case isSpace(c):
			s.pos++
		case (c == '-' || c == '#') && l.lineComment():
			l.toEOL()
		case c == '/' && bytes.Equal(s.peek(2), []byte("/*")) && !versioned(s.peek(4)):
			if !l.blockComment() {
				return false
			}
		default:
			return true
		}
	}
	return false
}

// word consumes a word at pos and returns it (valid until the next read).
func (l *Lexer) word() []byte {
	s := &l.s
	b := s.peek(128)
	i := 0
	for i < len(b) && isWord(b[i]) {
		i++
	}
	w := append([]byte(nil), b[:i]...)
	s.pos += i
	return w
}

// ident consumes a bare or backquoted identifier and returns its name.
func (l *Lexer) ident() (string, bool) {
	s := &l.s
	if !s.ensure(1) {
		return "", false
	}
	q := s.buf[s.pos]
	if q == '`' || (q == '"' && l.mode.ANSIQuotes) {
		start := s.at() + 1
		s.pos++
		if !l.skipQuoted(q) {
			return "", false
		}
		name := s.appendSince(nil, start)
		name = name[:len(name)-1]
		return string(bytes.ReplaceAll(name, []byte{q, q}, []byte{q})), true
	}
	w := l.word()
	return string(w), len(w) > 0
}

// insert reads the head of an INSERT or REPLACE. When it has a VALUES
// list it stops before the first row's '(' and sets Rows; otherwise (INSERT
// ... SELECT, INSERT ... SET, anything it doesn't follow) it reads the
// statement through as a passthrough of kind other, which the allowlist
// refuses: its values would reach the copy unread.
func (l *Lexer) insert(st *Stmt) error {
	s := &l.s
	w := l.word()
	st.Kind = core.StmtInsert
	if eqFold(w, "replace") {
		st.Kind = core.StmtReplace
	}
	other := func() error {
		st.Kind, st.Rows = core.StmtOther, false
		return l.scanToDelim()
	}
	for {
		if !l.skipGap() {
			return l.truncated()
		}
		w := l.peekWord()
		if eqFold(w, "low_priority") || eqFold(w, "delayed") || eqFold(w, "high_priority") || eqFold(w, "ignore") || eqFold(w, "into") {
			l.word()
			continue
		}
		break
	}
	name, ok := l.ident()
	if !ok {
		return other()
	}
	if s.ensure(1) && s.buf[s.pos] == '.' {
		s.pos++
		if name, ok = l.ident(); !ok {
			return other()
		}
	}
	st.Table = name
	if !l.skipGap() {
		return l.truncated()
	}
	if s.buf[s.pos] == '(' {
		s.pos++
		cols := []string{}
		for {
			if !l.skipGap() {
				return l.truncated()
			}
			if s.buf[s.pos] == ')' && len(cols) == 0 {
				s.pos++
				break
			}
			c, ok := l.ident()
			if !ok {
				return other()
			}
			cols = append(cols, c)
			if !l.skipGap() {
				return l.truncated()
			}
			if s.buf[s.pos] == ',' {
				s.pos++
				continue
			}
			if s.buf[s.pos] == ')' {
				s.pos++
				break
			}
			return other()
		}
		st.Columns = cols
		if !l.skipGap() {
			return l.truncated()
		}
	}
	w = l.peekWord()
	if !eqFold(w, "values") && !eqFold(w, "value") {
		return other()
	}
	l.word()
	if !l.skipGap() {
		return l.truncated()
	}
	if s.buf[s.pos] != '(' {
		return other()
	}
	s.flush()
	st.Rows = true
	return nil
}

// span is a value of the row being read, before its bytes settle.
type span struct {
	kind       core.ValueKind
	raw        bool // Raw is in Arena.Raw, not Arena.Src
	start, end int
}

// Row reads the next row of the open INSERT into a. It returns false
// after the last row; Tail then reads the rest of the statement.
func (l *Lexer) Row(a *Arena) (core.Row, bool, error) {
	s := &l.s
	if l.rows == 0 {
		return core.Row{}, false, errors.New("mysql: Row called with no INSERT open")
	}
	s.setDst(&a.Src)
	leadStart := len(a.Src)
	if l.rows == 2 {
		if !l.skipGap() {
			return core.Row{}, false, l.truncated()
		}
		if s.buf[s.pos] != ',' {
			// The rows ended; what was read belongs to the tail.
			s.flush()
			l.tailPre = append(l.tailPre[:0], a.Src[leadStart:]...)
			a.Src = a.Src[:leadStart]
			l.rows = 0
			return core.Row{}, false, nil
		}
		s.pos++
		if !l.skipGap() {
			return core.Row{}, false, l.truncated()
		}
		if s.buf[s.pos] != '(' {
			return core.Row{}, false, &SyntaxError{Line: s.lineAt(), Msg: "a row of the INSERT doesn't start with '('"}
		}
	}
	srcStart := s.at()
	s.pos++ // '('
	l.spans = l.spans[:0]
	for {
		if !l.skipRowSpace() {
			return core.Row{}, false, l.truncated()
		}
		if s.buf[s.pos] == ')' && len(l.spans) == 0 {
			break
		}
		vs, err := l.value(a)
		if err != nil {
			return core.Row{}, false, err
		}
		if !l.skipRowSpace() {
			return core.Row{}, false, l.truncated()
		}
		if c := s.buf[s.pos]; c != ',' && c != ')' {
			// More after a literal ("'a' 'b'", "1 + 2", a comment): the
			// whole thing is an expression.
			if vs.raw {
				a.Raw = a.Raw[:vs.start]
			}
			if err := l.expr(); err != nil {
				return core.Row{}, false, err
			}
			vs.span = span{kind: core.Expr, start: vs.exprStart, end: trimEnd(s, s.at(), vs.exprStart)}
		}
		l.spans = append(l.spans, vs.span)
		if s.buf[s.pos] == ')' {
			break
		}
		s.pos++ // ','
	}
	s.pos++ // ')'
	s.flush()
	l.rows = 2
	vs := len(a.Vals)
	for _, sp := range l.spans {
		v := core.Value{Kind: sp.kind}
		switch {
		case sp.kind == core.Null:
		case sp.raw:
			v.Raw = a.Raw[sp.start:sp.end:sp.end]
		default:
			v.Raw = a.Src[sp.start:sp.end:sp.end]
		}
		a.Vals = append(a.Vals, v)
	}
	end := len(a.Src)
	return core.Row{
		Values: a.Vals[vs:len(a.Vals):len(a.Vals)],
		Src:    a.Src[srcStart:end:end],
		Lead:   a.Src[leadStart:srcStart:srcStart],
	}, true, nil
}

// trimEnd backs off trailing whitespace from the dst offset at, never
// past floor (where the value started).
func trimEnd(s *scanner, at, floor int) int {
	for at > floor {
		var c byte
		if d := len(*s.dst); at <= d {
			c = (*s.dst)[at-1]
		} else {
			c = s.buf[s.mark+at-1-d]
		}
		if !isSpace(c) {
			break
		}
		at--
	}
	return at
}

// skipRowSpace consumes whitespace inside a row.
func (l *Lexer) skipRowSpace() bool {
	s := &l.s
	for s.ensure(1) {
		if !isSpace(s.buf[s.pos]) {
			return true
		}
		s.pos++
	}
	return false
}

// valueSpan is a span plus where its text started, for the expression
// fallback.
type valueSpan struct {
	span
	exprStart int
}

// value reads one value at pos.
func (l *Lexer) value(a *Arena) (valueSpan, error) {
	s := &l.s
	start := s.at()
	vs := valueSpan{exprStart: start}
	b := s.peek(10)
	c := b[0]
	switch {
	case c == '\'' || (c == '"' && !l.mode.ANSIQuotes):
		sp, err := l.str(a, core.String)
		vs.span = sp
		return vs, err
	case (c == 'x' || c == 'X') && len(b) > 1 && b[1] == '\'':
		s.pos++
		vs.span = l.quotedLiteral(start, core.Hex)
	case (c == 'b' || c == 'B') && len(b) > 1 && b[1] == '\'':
		s.pos++
		vs.span = l.quotedLiteral(start, core.Bit)
	case (c == 'n' || c == 'N') && len(b) > 1 && b[1] == '\'':
		s.pos++
		sp, err := l.str(a, core.String)
		vs.span = sp
		return vs, err
	case c == '0' && len(b) > 2 && (b[1] == 'x' || b[1] == 'b') && isWord(b[2]):
		kind := core.Hex
		if b[1] == 'b' {
			kind = core.Bit
		}
		l.wordRun()
		vs.span = span{kind: kind, start: start, end: s.at()}
	case c >= '0' && c <= '9' || c == '.' || ((c == '-' || c == '+') && len(b) > 1 && (b[1] >= '0' && b[1] <= '9' || b[1] == '.')):
		s.pos++
		l.numberRun()
		vs.span = span{kind: core.Number, start: start, end: s.at()}
	case c == '_':
		return l.introducer(a, start)
	case (c == 'n' || c == 'N') && len(b) >= 4 && eqFold(b[:4], "null") && (len(b) == 4 || !isWord(b[4])):
		s.pos += 4
		vs.span = span{kind: core.Null}
	default:
		if err := l.expr(); err != nil {
			return vs, err
		}
		vs.span = span{kind: core.Expr, start: start, end: trimEnd(s, s.at(), start)}
	}
	if s.err != nil && !errors.Is(s.err, io.EOF) {
		return vs, s.err
	}
	return vs, nil
}

// wordRun consumes word bytes.
func (l *Lexer) wordRun() {
	s := &l.s
	for s.ensure(1) && isWord(s.buf[s.pos]) {
		s.pos++
	}
}

// numberRun consumes the rest of a number: digits, '.', and an exponent.
func (l *Lexer) numberRun() {
	s := &l.s
	for s.ensure(1) {
		c := s.buf[s.pos]
		switch {
		case c >= '0' && c <= '9' || c == '.':
		case c == 'e' || c == 'E':
			if b := s.peek(2); len(b) == 2 && (b[1] == '-' || b[1] == '+') {
				s.pos++
			}
		default:
			return
		}
		s.pos++
	}
}

// quotedLiteral consumes X'..' or b'..' from its quote; Raw is the text.
func (l *Lexer) quotedLiteral(start int, kind core.ValueKind) span {
	l.s.pos++
	l.skipQuoted('\'')
	return span{kind: kind, start: start, end: l.s.at()}
}

// introducer reads _binary '...', _utf8mb4'...' and _latin1 X'..'.
func (l *Lexer) introducer(a *Arena, start int) (valueSpan, error) {
	s := &l.s
	vs := valueSpan{exprStart: start}
	l.wordRun()
	name := s.appendSince(nil, start)
	if !l.skipRowSpace() {
		return vs, l.truncated()
	}
	b := s.peek(3)
	switch {
	case b[0] == '\'' || (b[0] == '"' && !l.mode.ANSIQuotes):
		kind := core.String
		if eqFold(name, "_binary") {
			kind = core.BinaryString
		}
		sp, err := l.str(a, kind)
		vs.span = sp
		return vs, err
	case (b[0] == 'x' || b[0] == 'X') && len(b) > 1 && b[1] == '\'':
		s.pos++
		vs.span = l.quotedLiteral(start, core.Hex)
		return vs, nil
	case b[0] == '0' && len(b) > 1 && b[1] == 'x':
		l.wordRun()
		vs.span = span{kind: core.Hex, start: start, end: s.at()}
		return vs, nil
	}
	if err := l.expr(); err != nil {
		return vs, err
	}
	vs.span = span{kind: core.Expr, start: start, end: trimEnd(s, s.at(), start)}
	return vs, nil
}

// str reads a quoted string at pos and decodes it. A string with no
// escape is used where it lies in Src; one with escapes is decoded into
// Raw.
func (l *Lexer) str(a *Arena, kind core.ValueKind) (span, error) {
	s := &l.s
	q := s.buf[s.pos]
	s.pos++
	esc := l.escapes(q)
	contentStart := s.at()
	copied := contentStart
	rawStart := len(a.Raw)
	escaped := false
	s.resetQuote()
	for {
		if !s.ensure(1) {
			return span{}, l.truncated()
		}
		if int64(s.at()-contentStart) > l.max {
			return span{}, &TooLargeError{Line: s.lineAt(), Limit: l.max}
		}
		b := s.buf[s.pos:s.end]
		i := s.nextQuote(q)
		j := -1
		if esc {
			j = firstBackslash(b, i)
		}
		if j >= 0 {
			s.pos += j
			a.Raw = s.appendSince(a.Raw, copied)
			escaped = true
			if !s.ensure(2) {
				return span{}, l.truncated()
			}
			a.Raw = appendUnescape(a.Raw, s.buf[s.pos+1])
			s.pos += 2
			copied = s.at()
			continue
		}
		if i < 0 {
			s.pos = s.end
			continue
		}
		s.pos += i
		// A doubled quote is one quote of content.
		if b := s.peek(2); len(b) == 2 && b[1] == q {
			a.Raw = s.appendSince(a.Raw, copied)
			a.Raw = append(a.Raw, q)
			escaped = true
			s.pos += 2
			copied = s.at()
			continue
		}
		var sp span
		if escaped {
			a.Raw = s.appendSince(a.Raw, copied)
			sp = span{kind: kind, raw: true, start: rawStart, end: len(a.Raw)}
		} else {
			sp = span{kind: kind, start: contentStart, end: s.at()}
		}
		if int64(sp.end-sp.start) > l.max {
			return span{}, &TooLargeError{Line: s.lineAt(), Limit: l.max}
		}
		s.pos++
		return sp, nil
	}
}

// appendUnescape appends what the escape '\c' stands for in a MySQL
// string. \% and \_ keep their backslash, as MySQL does outside LIKE.
func appendUnescape(out []byte, c byte) []byte {
	switch c {
	case '0':
		return append(out, 0)
	case 'b':
		return append(out, '\b')
	case 'n':
		return append(out, '\n')
	case 'r':
		return append(out, '\r')
	case 't':
		return append(out, '\t')
	case 'Z':
		return append(out, 0x1a)
	case '%', '_':
		return append(out, '\\', c)
	}
	return append(out, c)
}

// expr consumes an expression value up to the ',' or ')' that ends it at
// depth 0, honoring quotes and parentheses.
func (l *Lexer) expr() error {
	s := &l.s
	depth := 0
	for {
		if !s.ensure(1) {
			return l.truncated()
		}
		c := s.buf[s.pos]
		switch {
		case c == '\'' || c == '"' || c == '`':
			s.pos++
			if !l.skipQuoted(c) {
				return l.truncated()
			}
			continue
		case c == '(':
			depth++
		case c == ')':
			if depth == 0 {
				return nil
			}
			depth--
		case c == ',' && depth == 0:
			return nil
		case c == '/' && bytes.Equal(s.peek(2), []byte("/*")):
			if !l.blockComment() {
				return l.truncated()
			}
			continue
		case c == l.delim[0] && depth == 0 && l.atDelim():
			return &SyntaxError{Line: s.lineAt(), Msg: "a row of the INSERT ends without ')'"}
		}
		s.pos++
	}
}

// Tail reads the rest of the INSERT after its last row, through the
// delimiter and the line end: ";\n", or " ON DUPLICATE KEY UPDATE ...;\n".
func (l *Lexer) Tail() ([]byte, error) {
	if l.rows != 0 {
		return nil, errors.New("mysql: Tail called before the last row was read")
	}
	l.text = append(l.text[:0], l.tailPre...)
	l.s.setDst(&l.text)
	if err := l.scanToDelim(); err != nil {
		return nil, err
	}
	l.s.flush()
	return append([]byte(nil), l.text...), nil
}
