// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package oracle

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// A minimal SQL dump reader, written for the oracle alone: it reads the
// rows of mysqldump, mariadb-dump, wp db export, phpMyAdmin and pg_dump
// plain output (INSERT and COPY), skips every other statement and knows
// each MySQL table's columns and primary key from its CREATE TABLE. It
// shares nothing with Roadie's dump package on purpose.

// Value is one cell: a string (numbers keep their text) or NULL.
type Value struct {
	S    string
	Null bool
}

// Row is one row of a table.
type Row struct {
	Table string
	// Key names the row: the primary key columns as col=value joined by
	// commas, or #n (its 1-based place in the table) without a key.
	Key  string
	Cols []string
	Vals []Value
}

// Get returns the value of column col.
func (r Row) Get(col string) (Value, bool) {
	for i, c := range r.Cols {
		if c == col && i < len(r.Vals) {
			return r.Vals[i], true
		}
	}
	return Value{}, false
}

// DumpStats is what a full read of a dump found.
type DumpStats struct {
	Rows   map[string]int64 // rows per table
	Tables []string         // tables in the order their first row or CREATE came
}

type tableInfo struct {
	cols []string
	pk   []string
	n    int64
}

type dumpReader struct {
	r      *bufio.Reader
	pg     bool
	delim  string
	tables map[string]*tableInfo
	stats  DumpStats
	fn     func(Row) error
}

// OpenDump opens a dump file, through gzip when it starts with the gzip magic.
func OpenDump(path string) (io.ReadCloser, error) {
	f, err := os.Open(path) // #nosec G304 -- a dump the operator names
	if err != nil {
		return nil, err
	}
	br := bufio.NewReaderSize(f, 1<<20)
	if magic, _ := br.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		zr, err := gzip.NewReader(br)
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		return readCloser{zr, f}, nil
	}
	return readCloser{br, f}, nil
}

type readCloser struct {
	io.Reader
	c io.Closer
}

func (r readCloser) Close() error { return r.c.Close() }

// ReadDump reads every row of the dump in r, calling fn (when not nil)
// for each; it returns the row count per table.
func ReadDump(r io.Reader, fn func(Row) error) (DumpStats, error) {
	d := &dumpReader{r: bufio.NewReaderSize(r, 1<<20), delim: ";", tables: map[string]*tableInfo{}, fn: fn}
	d.stats.Rows = map[string]int64{}
	if head, _ := d.r.Peek(4096); bytes.Contains(head, []byte("PostgreSQL database dump")) {
		d.pg = true
	}
	err := d.run()
	return d.stats, err
}

var errSyntax = errors.New("oracle: dump syntax")

func (d *dumpReader) syntax(what string) error { return fmt.Errorf("%w: %s", errSyntax, what) }

func (d *dumpReader) table(name string) *tableInfo {
	t := d.tables[name]
	if t == nil {
		t = &tableInfo{}
		d.tables[name] = t
		d.stats.Tables = append(d.stats.Tables, name)
		d.stats.Rows[name] += 0
	}
	return t
}

func (d *dumpReader) run() error {
	for {
		if err := d.skipSpace(); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		head, _ := d.r.Peek(16)
		up := strings.ToUpper(string(head))
		switch {
		case bytes.HasPrefix(head, []byte("--")) || bytes.HasPrefix(head, []byte("#")):
			if _, err := d.r.ReadString('\n'); err != nil {
				return nil
			}
		case bytes.HasPrefix(head, []byte("/*")):
			if err := d.skipBlockComment(); err != nil {
				return err
			}
		case bytes.HasPrefix(head, []byte(d.delim)):
			if _, err := d.r.Discard(len(d.delim)); err != nil {
				return err
			}
		case strings.HasPrefix(up, "DELIMITER "):
			line, err := d.r.ReadString('\n')
			if f := strings.Fields(line); len(f) >= 2 {
				d.delim = f[1]
			}
			if err != nil {
				return nil
			}
		case strings.HasPrefix(up, "INSERT ") || strings.HasPrefix(up, "REPLACE "):
			if err := d.insert(); err != nil {
				return err
			}
		case strings.HasPrefix(up, "CREATE TABLE"):
			s, err := d.statement()
			if err != nil {
				return err
			}
			d.create(s)
		case d.pg && strings.HasPrefix(up, "COPY "):
			if err := d.copyRows(); err != nil {
				return err
			}
		default:
			if _, err := d.statement(); err != nil {
				return err
			}
		}
	}
}

func (d *dumpReader) skipSpace() error {
	for {
		c, err := d.r.ReadByte()
		if err != nil {
			return err
		}
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			return d.r.UnreadByte()
		}
	}
}

func (d *dumpReader) skipBlockComment() error {
	var last byte
	for {
		c, err := d.r.ReadByte()
		if err != nil {
			return d.syntax("unterminated comment")
		}
		if last == '*' && c == '/' {
			return nil
		}
		last = c
	}
}

// statement reads up to and including the delimiter, outside quotes; it
// returns the text without it.
func (d *dumpReader) statement() (string, error) {
	var b strings.Builder
	for {
		c, err := d.r.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return b.String(), nil
			}
			return "", err
		}
		switch {
		case c == '\'' || c == '"' || c == '`':
			b.WriteByte(c)
			if err := d.copyQuoted(&b, c); err != nil {
				return "", err
			}
			continue
		case d.pg && c == '$':
			b.WriteByte(c)
			if err := d.copyDollar(&b); err != nil {
				return "", err
			}
			continue
		case c == d.delim[0]:
			if rest, _ := d.r.Peek(len(d.delim) - 1); string(rest) == d.delim[1:] {
				_, _ = d.r.Discard(len(d.delim) - 1)
				return b.String(), nil
			}
		}
		b.WriteByte(c)
	}
}

// copyQuoted copies a quoted string after its opening quote q, through
// the closing one. MySQL escapes with a backslash; both dialects double
// the quote.
func (d *dumpReader) copyQuoted(b *strings.Builder, q byte) error {
	for {
		c, err := d.r.ReadByte()
		if err != nil {
			return d.syntax("unterminated quoted string")
		}
		b.WriteByte(c)
		switch {
		case c == '\\' && !d.pg && q != '`':
			e, err := d.r.ReadByte()
			if err != nil {
				return d.syntax("unterminated escape")
			}
			b.WriteByte(e)
		case c == q:
			if n, _ := d.r.Peek(1); len(n) == 1 && n[0] == q {
				_, _ = d.r.ReadByte()
				b.WriteByte(q)
				continue
			}
			return nil
		}
	}
}

// copyDollar copies a PostgreSQL dollar-quoted string after its first $.
func (d *dumpReader) copyDollar(b *strings.Builder) error {
	tag := "$"
	for {
		c, err := d.r.ReadByte()
		if err != nil {
			return d.syntax("unterminated dollar quote")
		}
		b.WriteByte(c)
		tag += string(c)
		if c == '$' {
			break
		}
		if c != '_' && !isAlnum(rune(c)) {
			return nil // a parameter such as $1, not a quote
		}
	}
	var body []byte
	for {
		c, err := d.r.ReadByte()
		if err != nil {
			return d.syntax("unterminated dollar quote")
		}
		b.WriteByte(c)
		body = append(body, c)
		if bytes.HasSuffix(body, []byte(tag)) {
			return nil
		}
	}
}

var (
	reCreateName = regexp.MustCompile("(?is)^CREATE TABLE\\s+(?:IF NOT EXISTS\\s+)?((?:`[^`]+`|\"[^\"]+\"|[^\\s(`\".]+)(?:\\.(?:`[^`]+`|\"[^\"]+\"|[^\\s(`\".]+))?)")
	reColumn     = regexp.MustCompile("(?m)^\\s*`([^`]+)`\\s")
	rePK         = regexp.MustCompile(`(?i)PRIMARY KEY\s*\(([^)]*)\)`)
)

func unquoteIdent(s string) string {
	parts := strings.Split(s, ".")
	for i, p := range parts {
		parts[i] = strings.Trim(p, "`\"")
	}
	return strings.Join(parts, ".")
}

// create records a MySQL table's columns and primary key.
func (d *dumpReader) create(s string) {
	m := reCreateName.FindStringSubmatch(s)
	if m == nil {
		return
	}
	t := d.table(unquoteIdent(m[1]))
	t.cols = t.cols[:0]
	body := s
	if i := strings.IndexByte(s, '('); i >= 0 {
		body = s[i+1:]
	}
	for _, c := range reColumn.FindAllStringSubmatch(body, -1) {
		t.cols = append(t.cols, c[1])
	}
	t.pk = nil
	if pk := rePK.FindStringSubmatch(body); pk != nil {
		for _, c := range strings.Split(pk[1], ",") {
			c = strings.TrimSpace(c)
			if i := strings.IndexByte(c, '('); i > 0 { // a prefix length: `meta_key`(191)
				c = c[:i]
			}
			t.pk = append(t.pk, unquoteIdent(c))
		}
	}
}

func (d *dumpReader) emit(name string, t *tableInfo, cols []string, vals []Value) error {
	t.n++
	d.stats.Rows[name]++
	if d.fn == nil {
		return nil
	}
	row := Row{Table: name, Cols: cols, Vals: vals}
	var key []string
	for _, k := range t.pk {
		v, ok := row.Get(k)
		if !ok {
			key = nil
			break
		}
		key = append(key, k+"="+v.S)
	}
	if key == nil {
		row.Key = "#" + strconv.FormatInt(t.n, 10)
	} else {
		row.Key = strings.Join(key, ",")
	}
	return d.fn(row)
}

// word reads the next bare word (letters, digits, _ and .) after spaces.
func (d *dumpReader) word() (string, error) {
	if err := d.skipSpace(); err != nil {
		return "", d.syntax("unexpected end")
	}
	var b strings.Builder
	for {
		c, err := d.r.ReadByte()
		if err != nil {
			break
		}
		if c != '_' && c != '.' && !isAlnum(rune(c)) {
			_ = d.r.UnreadByte()
			break
		}
		b.WriteByte(c)
	}
	return b.String(), nil
}

// ident reads a table or column name, quoted or bare, possibly schema-qualified.
func (d *dumpReader) ident() (string, error) {
	if err := d.skipSpace(); err != nil {
		return "", d.syntax("unexpected end")
	}
	var parts []string
	for {
		c, err := d.r.ReadByte()
		if err != nil {
			return "", d.syntax("unexpected end in a name")
		}
		if c == '`' || c == '"' {
			var b strings.Builder
			if err := d.copyQuoted(&b, c); err != nil {
				return "", err
			}
			s := b.String()
			parts = append(parts, strings.ReplaceAll(s[:len(s)-1], string([]byte{c, c}), string(c)))
		} else {
			_ = d.r.UnreadByte()
			w, err := d.word()
			if err != nil || w == "" {
				return "", d.syntax("expected a name")
			}
			parts = append(parts, w)
		}
		if n, _ := d.r.Peek(1); len(n) == 1 && n[0] == '.' {
			_, _ = d.r.ReadByte()
			continue
		}
		return strings.Join(parts, "."), nil
	}
}

func (d *dumpReader) expect(c byte) error {
	if err := d.skipSpace(); err != nil {
		return d.syntax("unexpected end")
	}
	got, _ := d.r.ReadByte()
	if got != c {
		return d.syntax(fmt.Sprintf("expected %q, got %q", c, got))
	}
	return nil
}

func (d *dumpReader) insert() error {
	// INSERT [IGNORE] INTO name, or REPLACE INTO name.
	for {
		w, err := d.word()
		if err != nil {
			return err
		}
		if strings.EqualFold(w, "INTO") {
			break
		}
		if w == "" {
			return d.syntax("expected INTO")
		}
	}
	name, err := d.ident()
	if err != nil {
		return err
	}
	t := d.table(name)
	cols := t.cols
	if err := d.skipSpace(); err != nil {
		return d.syntax("unexpected end")
	}
	if n, _ := d.r.Peek(1); len(n) == 1 && n[0] == '(' {
		_, _ = d.r.ReadByte()
		cols = nil
		for {
			c, err := d.ident()
			if err != nil {
				return err
			}
			cols = append(cols, c)
			if err := d.skipSpace(); err != nil {
				return d.syntax("unexpected end")
			}
			sep, _ := d.r.ReadByte()
			if sep == ')' {
				break
			}
			if sep != ',' {
				return d.syntax("expected , or ) in a column list")
			}
		}
	}
	if w, err := d.word(); err != nil || !strings.EqualFold(w, "VALUES") {
		return d.syntax("expected VALUES")
	}
	for {
		if err := d.expect('('); err != nil {
			return err
		}
		vals, err := d.tuple()
		if err != nil {
			return err
		}
		if err := d.emit(name, t, cols, vals); err != nil {
			return err
		}
		if err := d.skipSpace(); err != nil {
			return nil
		}
		c, _ := d.r.ReadByte()
		if c == ',' {
			continue
		}
		_ = d.r.UnreadByte()
		// The delimiter, or a tail such as ON DUPLICATE KEY UPDATE.
		_, err = d.statement()
		return err
	}
}

// tuple reads values after "(" through ")".
func (d *dumpReader) tuple() ([]Value, error) {
	var vals []Value
	for {
		v, err := d.value()
		if err != nil {
			return nil, err
		}
		vals = append(vals, v)
		if err := d.skipSpace(); err != nil {
			return nil, d.syntax("unexpected end in a row")
		}
		c, _ := d.r.ReadByte()
		if c == ')' {
			return vals, nil
		}
		if c != ',' {
			return nil, d.syntax(fmt.Sprintf("expected , or ) in a row, got %q", c))
		}
	}
}

func (d *dumpReader) value() (Value, error) {
	if err := d.skipSpace(); err != nil {
		return Value{}, d.syntax("unexpected end in a row")
	}
	c, _ := d.r.ReadByte()
	switch {
	case c == '\'':
		s, err := d.stringBody(d.pg)
		return Value{S: s}, err
	case c == '_': // a charset introducer: _binary 'x', _utf8mb4 'x'
		if _, err := d.word(); err != nil {
			return Value{}, err
		}
		return d.value()
	case (c == 'X' || c == 'x') && d.peekIs('\''):
		_, _ = d.r.ReadByte()
		s, err := d.stringBody(true)
		if err != nil {
			return Value{}, err
		}
		b, err := hex.DecodeString(s)
		if err != nil {
			return Value{}, d.syntax("bad hex literal")
		}
		return Value{S: string(b)}, nil
	case (c == 'E' || c == 'e') && d.pg && d.peekIs('\''):
		_, _ = d.r.ReadByte()
		s, err := d.stringBody(false)
		return Value{S: s}, err
	}
	_ = d.r.UnreadByte()
	var b strings.Builder
	for {
		c, err := d.r.ReadByte()
		if err != nil {
			return Value{}, d.syntax("unexpected end in a value")
		}
		if c == ',' || c == ')' || c == ' ' || c == '\n' || c == '\t' || c == '\r' {
			_ = d.r.UnreadByte()
			break
		}
		b.WriteByte(c)
	}
	s := b.String()
	switch {
	case s == "":
		return Value{}, d.syntax("empty value")
	case strings.EqualFold(s, "NULL"):
		return Value{Null: true}, nil
	case len(s) > 2 && (s[:2] == "0x" || s[:2] == "0X"):
		raw, err := hex.DecodeString(s[2:])
		if err != nil {
			return Value{}, d.syntax("bad hex value")
		}
		return Value{S: string(raw)}, nil
	}
	return Value{S: s}, nil
}

func (d *dumpReader) peekIs(c byte) bool {
	n, _ := d.r.Peek(1)
	return len(n) == 1 && n[0] == c
}

// stringBody reads a single-quoted string after its quote. plain strings
// only double the quote (PostgreSQL); otherwise backslash escapes apply.
func (d *dumpReader) stringBody(plain bool) (string, error) {
	var b []byte
	for {
		c, err := d.r.ReadByte()
		if err != nil {
			return "", d.syntax("unterminated string")
		}
		switch {
		case c == '\\' && !plain:
			e, err := d.r.ReadByte()
			if err != nil {
				return "", d.syntax("unterminated escape")
			}
			switch e {
			case '0':
				b = append(b, 0)
			case 'n':
				b = append(b, '\n')
			case 'r':
				b = append(b, '\r')
			case 't':
				b = append(b, '\t')
			case 'b':
				b = append(b, '\b')
			case 'Z':
				b = append(b, 0x1a)
			default:
				b = append(b, e)
			}
		case c == '\'':
			if d.peekIs('\'') {
				_, _ = d.r.ReadByte()
				b = append(b, '\'')
				continue
			}
			return string(b), nil
		default:
			b = append(b, c)
		}
	}
}

// copyRows reads a PostgreSQL COPY ... FROM stdin block.
func (d *dumpReader) copyRows() error {
	header, err := d.statement()
	if err != nil {
		return err
	}
	f := strings.Fields(header)
	if len(f) < 2 {
		return d.syntax("bad COPY")
	}
	name := unquoteIdent(f[1])
	var cols []string
	if i, j := strings.IndexByte(header, '('), strings.IndexByte(header, ')'); i > 0 && j > i {
		for _, c := range strings.Split(header[i+1:j], ",") {
			cols = append(cols, unquoteIdent(strings.TrimSpace(c)))
		}
	}
	if !strings.Contains(strings.ToUpper(header), "FROM STDIN") {
		return nil
	}
	t := d.table(name)
	if _, err := d.r.ReadString('\n'); err != nil { // the rest of the COPY line
		return d.syntax("COPY without data")
	}
	for {
		line, err := d.r.ReadString('\n')
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == `\.` {
			return nil
		}
		if err != nil {
			return d.syntax("COPY data without its end marker")
		}
		var vals []Value
		for _, field := range strings.Split(line, "\t") {
			if field == `\N` {
				vals = append(vals, Value{Null: true})
			} else {
				vals = append(vals, Value{S: copyUnescape(field)})
			}
		}
		if err := d.emit(name, t, cols, vals); err != nil {
			return err
		}
	}
}

func copyUnescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	return strings.NewReplacer(`\\`, `\`, `\n`, "\n", `\r`, "\r", `\t`, "\t", `\b`, "\b", `\f`, "\f", `\v`, "\v").Replace(s)
}
