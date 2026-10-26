// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pgsql

import (
	"bytes"
	"unicode/utf8"

	"github.com/RhythmShahriar/roadie/core"
)

// Insert is an INSERT ... VALUES statement split into rows. Head is the
// text before the first row's '(', each Row's Src its exact '(...)' and
// Lead the bytes between it and the row before (empty for the first),
// Tail everything after the last row (";\n" or " ON CONFLICT DO
// NOTHING;\n"). Values point into the statement text or into one
// buffer of decoded strings.
type Insert struct {
	Table   string
	Columns []string // nil when the statement has no column list
	Head    []byte
	Rows    []core.Row
	Tail    []byte
}

// CallError is a row value that calls a function or runs a query. A
// dump's row values are data: pg_dump writes literals only. A call
// such as pg_read_file() would run on the staging server at import.
type CallError struct {
	Line  int64
	Table string
}

func (e *CallError) Error() string { return "a row value calls a function" }

// ParseInsert splits the INSERT statement text (as the Lexer returned
// it) into rows. ok is false for an INSERT that isn't a plain VALUES
// list, such as INSERT ... SELECT, which stays a statement for the
// allowlist to refuse. line is the statement's first line, for errors;
// limit the largest value.
func ParseInsert(text []byte, m Mode, line, limit int64) (*Insert, bool, error) {
	p := &insParser{b: text, m: m, line: line, limit: limit}
	ins, ok := p.head()
	if !ok {
		return nil, false, nil
	}
	if err := p.rows(ins); err != nil {
		return nil, false, err
	}
	if !p.ok {
		return nil, false, nil
	}
	return ins, true, nil
}

type insParser struct {
	b     []byte
	i     int
	m     Mode
	line  int64
	limit int64
	raw   []byte // decoded strings of every row, allocated once
	table string
	ok    bool
}

// gap skips blanks and comments.
func (p *insParser) gap() {
	for p.i < len(p.b) {
		c := p.b[p.i]
		switch {
		case isSpace(c):
			p.i++
		case c == '-' && p.i+1 < len(p.b) && p.b[p.i+1] == '-':
			for p.i < len(p.b) && p.b[p.i] != '\n' {
				p.i++
			}
		case c == '/' && p.i+1 < len(p.b) && p.b[p.i+1] == '*':
			p.i = commentEnd(p.b, p.i)
		default:
			return
		}
	}
}

// word reads the keyword w at i.
func (p *insParser) word(w string) bool {
	p.gap()
	j := p.i
	for j < len(p.b) && isWord(p.b[j]) {
		j++
	}
	if !eqFold(p.b[p.i:j], w) {
		return false
	}
	p.i = j
	return true
}

func (p *insParser) at(c byte) bool {
	p.gap()
	return p.i < len(p.b) && p.b[p.i] == c
}

// ident reads one name part.
func (p *insParser) ident() (string, bool) {
	p.gap()
	if p.i >= len(p.b) {
		return "", false
	}
	if p.b[p.i] == '"' {
		j, name := identEnd(p.b, p.i)
		if j > len(p.b) || p.b[j-1] != '"' {
			return "", false
		}
		p.i = j
		return string(name), true
	}
	j := p.i
	for j < len(p.b) && (isWord(p.b[j]) || p.b[j] == '$') {
		j++
	}
	if j == p.i || p.b[p.i] >= '0' && p.b[p.i] <= '9' {
		return "", false
	}
	t := tok{kind: tokWord, text: p.b[p.i:j]}
	p.i = j
	return t.name(), true
}

// head reads INSERT INTO name [(columns)] [OVERRIDING ... VALUE] VALUES
// up to the first row's '('.
func (p *insParser) head() (*Insert, bool) {
	if !p.word("insert") || !p.word("into") {
		return nil, false
	}
	ins := &Insert{}
	name, ok := p.ident()
	for ok && p.at('.') {
		p.i++
		name, ok = p.ident()
	}
	if !ok {
		return nil, false
	}
	ins.Table, p.table = name, name
	if p.at('(') {
		p.i++
		ins.Columns = []string{}
		for {
			c, ok := p.ident()
			if !ok {
				return nil, false
			}
			ins.Columns = append(ins.Columns, c)
			if p.at(',') {
				p.i++
				continue
			}
			if p.at(')') {
				p.i++
				break
			}
			return nil, false
		}
	}
	if p.word("overriding") {
		if !p.word("system") && !p.word("user") {
			return nil, false
		}
		if !p.word("value") {
			return nil, false
		}
	}
	if !p.word("values") || !p.at('(') {
		return nil, false
	}
	ins.Head = p.b[:p.i:p.i]
	return ins, true
}

// rows reads every row and the tail.
func (p *insParser) rows(ins *Insert) error {
	var vals []core.Value
	type rowSpan struct{ lead, src, src2, v, v2 int }
	var spans []rowSpan
	lead := p.i
	for {
		start := p.i
		p.i++ // '('
		v0 := len(vals)
		var err error
		if vals, err = p.values(vals); err != nil {
			return err
		}
		spans = append(spans, rowSpan{lead, start, p.i, v0, len(vals)})
		lead = p.i
		if !p.at(',') {
			break
		}
		p.i++
		if !p.at('(') {
			return nil // a ',' with no row after it: not ours
		}
	}
	tail := lead
	if p.word("on") {
		if !p.word("conflict") || !p.word("do") || !p.word("nothing") {
			return nil
		}
	}
	if !p.at(';') {
		return nil
	}
	p.ok = true
	ins.Tail = p.b[tail:]
	ins.Rows = make([]core.Row, len(spans))
	for k, s := range spans {
		ins.Rows[k] = core.Row{
			Values: vals[s.v:s.v2:s.v2],
			Src:    p.b[s.src:s.src2:s.src2],
			Lead:   p.b[s.lead:s.src:s.src],
		}
	}
	return nil
}

// values reads the values of one row after its '(' through its ')'.
func (p *insParser) values(vals []core.Value) ([]core.Value, error) {
	if p.at(')') {
		p.i++
		return vals, nil
	}
	for {
		p.gap()
		start := p.i
		v, err := p.value()
		if err != nil {
			return vals, err
		}
		if !p.at(',') && !p.at(')') {
			// More after a literal ('x'::text, 1 + 2): the whole thing is
			// an expression.
			p.i = start
			if v, err = p.expr(); err != nil {
				return vals, err
			}
		}
		vals = append(vals, v)
		if p.i >= len(p.b) {
			return vals, &SyntaxError{Line: p.line, Msg: "a row of the INSERT ends without ')'"}
		}
		c := p.b[p.i]
		p.i++
		if c == ')' {
			return vals, nil
		}
	}
}

// value reads one literal at i.
func (p *insParser) value() (core.Value, error) {
	b, i := p.b, p.i
	if i >= len(b) {
		return core.Value{}, &SyntaxError{Line: p.line, Msg: "a row of the INSERT ends without ')'"}
	}
	c := b[i]
	next := byte(0)
	if i+1 < len(b) {
		next = b[i+1]
	}
	switch {
	case c == '\'':
		return p.str(i, p.m.NoStdStrings)
	case (c == 'e' || c == 'E') && next == '\'':
		return p.str(i+1, true)
	case (c == 'b' || c == 'B' || c == 'x' || c == 'X') && next == '\'':
		j := stringEnd(b, i+1, false)
		p.i = j
		kind := core.Bit
		if c|0x20 == 'x' {
			kind = core.Hex
		}
		return core.Value{Kind: kind, Raw: b[i:j:j]}, nil
	case c >= '0' && c <= '9' || c == '.' || (c == '-' || c == '+') && (next >= '0' && next <= '9' || next == '.'):
		j := numberEnd(b, i+1)
		p.i = j
		return core.Value{Kind: core.Number, Raw: b[i:j:j]}, nil
	case c|0x20 == 'n' && i+4 <= len(b) && eqFold(b[i:i+4], "null") && (i+4 == len(b) || !isWord(b[i+4])):
		p.i = i + 4
		return core.Value{Kind: core.Null}, nil
	}
	return p.expr()
}

// str reads the '...' string whose quote is at q, decoding doubled
// quotes and, with esc, backslash escapes. A string with nothing to
// decode is used where it lies.
func (p *insParser) str(q int, esc bool) (core.Value, error) {
	b := p.b
	j := stringEnd(b, q, esc)
	if j > len(b) || j == q+1 || b[j-1] != '\'' {
		return core.Value{}, &SyntaxError{Line: p.line, Msg: "a string in the INSERT has no end"}
	}
	p.i = j
	body := b[q+1 : j-1]
	if int64(len(body)) > p.limit {
		return core.Value{}, &TooLargeError{Line: p.line, Limit: p.limit}
	}
	plain := !bytes.Contains(body, []byte{'\''})
	if esc && bytes.Contains(body, []byte{'\\'}) {
		plain = false
	}
	if plain {
		return core.Value{Kind: core.String, Raw: body[:len(body):len(body)]}, nil
	}
	if p.raw == nil {
		// A decoded string is never longer than its source, so a buffer
		// the size of the statement never moves and Raw slices of it
		// stay valid.
		p.raw = make([]byte, 0, len(b))
	}
	off := len(p.raw)
	if esc {
		p.raw = appendEUnescape(p.raw, body)
	} else {
		p.raw = appendUndouble(p.raw, body)
	}
	return core.Value{Kind: core.String, Raw: p.raw[off:len(p.raw):len(p.raw)]}, nil
}

// appendUndouble appends a standard string's body with each doubled quote read as one.
func appendUndouble(out, body []byte) []byte {
	for {
		k := bytes.Index(body, []byte("''"))
		if k < 0 {
			return append(out, body...)
		}
		out = append(out, body[:k+1]...)
		body = body[k+2:]
	}
}

// appendEUnescape appends an E'...' string's body decoded: a doubled
// quote and \' are a quote, \b \f \n \r \t, \ooo octal, \xhh hex,
// \uXXXX and \UXXXXXXXX code points; a backslash before anything else
// drops.
func appendEUnescape(out, body []byte) []byte {
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c == '\'' && i+1 < len(body) && body[i+1] == '\'' {
			out = append(out, '\'')
			i++
			continue
		}
		if c != '\\' || i+1 >= len(body) {
			out = append(out, c)
			continue
		}
		i++
		c = body[i]
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
		case '0', '1', '2', '3', '4', '5', '6', '7':
			v := int(c - '0')
			for k := 0; k < 2 && i+1 < len(body) && body[i+1] >= '0' && body[i+1] <= '7'; k++ {
				i++
				v = v*8 + int(body[i]-'0')
			}
			out = append(out, byte(v)) // #nosec G115 -- the low byte, as PostgreSQL
		case 'x':
			if i+1 >= len(body) || unhex(body[i+1]) < 0 {
				out = append(out, 'x')
				continue
			}
			i++
			v := unhex(body[i])
			if i+1 < len(body) && unhex(body[i+1]) >= 0 {
				i++
				v = v*16 + unhex(body[i])
			}
			out = append(out, byte(v)) // #nosec G115 -- two hex digits
		case 'u', 'U':
			n := 4
			if c == 'U' {
				n = 8
			}
			r, ok := 0, i+n < len(body)
			for k := 1; ok && k <= n; k++ {
				d := unhex(body[i+k])
				ok = d >= 0
				r = r*16 + d
			}
			if !ok {
				out = append(out, c)
				continue
			}
			i += n
			out = utf8.AppendRune(out, rune(r)) // #nosec G115 -- at most eight hex digits
		default:
			out = append(out, c)
		}
	}
	return out
}

// expr reads an expression value up to the ',' or ')' that ends it at
// depth 0. A value that calls a function or holds a query is refused:
// only literals, casts (::type) and keywords such as DEFAULT and true
// may stand in a row.
func (p *insParser) expr() (core.Value, error) {
	b := p.b
	start := p.i
	depth := 0
	for p.i < len(b) {
		c := b[p.i]
		switch {
		case c == '\'':
			p.i = stringEnd(b, p.i, p.m.NoStdStrings || ePrefix(b[start:p.i]))
			continue
		case c == '"':
			p.i, _ = identEnd(b, p.i)
			continue
		case c == '$' && (p.i == start || !isWord(b[p.i-1])):
			if j := dollarEnd(b, p.i); j > p.i {
				p.i = j
				continue
			}
		case c == '/' && p.i+1 < len(b) && b[p.i+1] == '*':
			p.i = commentEnd(b, p.i)
			continue
		case c == '(' || c == '[':
			depth++
		case c == ']':
			depth--
		case c == ')':
			if depth == 0 {
				return p.exprValue(start)
			}
			depth--
		case c == ',' && depth == 0:
			return p.exprValue(start)
		case c == ';' && depth == 0:
			return core.Value{}, &SyntaxError{Line: p.line, Msg: "a row of the INSERT ends without ')'"}
		}
		p.i++
	}
	return core.Value{}, &SyntaxError{Line: p.line, Msg: "a row of the INSERT ends without ')'"}
}

func (p *insParser) exprValue(start int) (core.Value, error) {
	end := p.i
	for end > start && isSpace(p.b[end-1]) {
		end--
	}
	text := p.b[start:end:end]
	if !SafeExpr(text, p.m) {
		return core.Value{}, &CallError{Line: p.line, Table: p.table}
	}
	return core.Value{Kind: core.Expr, Raw: text}, nil
}

// typeWords may follow a type name after ::, as in character varying,
// double precision and timestamp(3) without time zone.
var typeWords = map[string]bool{
	"varying": true, "precision": true, "with": true, "without": true, "time": true, "zone": true,
	"to": true, "year": true, "month": true, "day": true, "hour": true, "minute": true, "second": true,
}

// SafeExpr reports whether an expression in a row is data only:
// literals, keywords such as DEFAULT and true, operators and casts
// (::type with a numeric modifier). No function call (a name before
// '(' outside a cast), no qualified name outside a cast and no query
// (SELECT, VALUES, TABLE, WITH).
func SafeExpr(text []byte, m Mode) bool {
	t := tokens(text, m, 0)
	for i := 0; i < len(t); i++ {
		k := t[i]
		switch {
		case k.punct(':') && t.punct(i+1, ':'):
			j, ok := castEnd(t, i+2)
			if !ok {
				return false
			}
			i = j - 1
		case k.kind == tokWord && (k.is("select") || k.is("values") || k.is("table") || k.is("with")):
			return false
		case k.isName() && (t.punct(i+1, '(') || t.punct(i+1, '.')):
			return false
		}
	}
	return true
}

// castEnd reads the type of a cast from i and returns the index after it.
func castEnd(t toks, i int) (int, bool) {
	if !t.at(i).isName() {
		return i, false
	}
	i++
	for {
		switch {
		case t.punct(i, '.') && t.at(i+1).isName():
			i += 2
		case t.at(i).kind == tokWord && typeWords[t[i].name()]:
			i++
		case t.punct(i, '('):
			i++
			for !t.punct(i, ')') {
				if i >= len(t) || t[i].kind != tokNum && !t.punct(i, ',') {
					return i, false
				}
				i++
			}
			i++
		case t.punct(i, '['):
			i++
			if t.at(i).kind == tokNum {
				i++
			}
			if !t.punct(i, ']') {
				return i, false
			}
			i++
		default:
			return i, true
		}
	}
}
