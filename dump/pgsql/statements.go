// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pgsql

import (
	"bytes"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
)

// Token kinds of a statement.
const (
	tokWord  = iota // an unquoted keyword or name
	tokIdent        // a "quoted" name, unquoted in text
	tokStr          // a string: '...', E'...', $tag$...$tag$
	tokNum
	tokPunct // one byte
)

// tok is one token of a statement. For tokIdent, text is the name with
// its quotes and doubled quotes removed; for the others it is as written.
type tok struct {
	kind       int
	text       []byte
	start, end int // offsets in the statement
}

func (t tok) is(w string) bool { return t.kind == tokWord && eqFold(t.text, w) }

func (t tok) punct(c byte) bool { return t.kind == tokPunct && t.text[0] == c }

// name is the token as a PostgreSQL name: an unquoted name folds to
// lower case, a quoted one stays as written.
func (t tok) name() string {
	if t.kind == tokWord {
		return strings.ToLower(string(t.text))
	}
	return string(t.text)
}

func (t tok) isName() bool { return t.kind == tokWord || t.kind == tokIdent }

func eqFold(b []byte, w string) bool { return len(b) == len(w) && bytes.EqualFold(b, []byte(w)) }

// toks is a statement's tokens; out-of-range indexes read as nothing.
type toks []tok

func (t toks) at(i int) tok {
	if i < 0 || i >= len(t) {
		return tok{kind: -1}
	}
	return t[i]
}

func (t toks) is(i int, w string) bool { return t.at(i).is(w) }

func (t toks) punct(i int, c byte) bool { return i >= 0 && i < len(t) && t[i].punct(c) }

// tokens splits a statement into tokens, leaving out blanks and
// comments. max stops after that many tokens (0 = all).
func tokens(b []byte, m Mode, limit int) toks {
	var out toks
	for i := 0; i < len(b); {
		if limit > 0 && len(out) >= limit {
			break
		}
		c := b[i]
		switch {
		case isSpace(c):
			i++
		case c == '-' && i+1 < len(b) && b[i+1] == '-':
			for i < len(b) && b[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			i = commentEnd(b, i)
		case c == '\'':
			j := stringEnd(b, i, m.NoStdStrings)
			out = append(out, tok{kind: tokStr, text: b[i:j], start: i, end: j})
			i = j
		case (c == 'e' || c == 'E') && i+1 < len(b) && b[i+1] == '\'' && (i == 0 || !isWord(b[i-1])):
			j := stringEnd(b, i+1, true)
			out = append(out, tok{kind: tokStr, text: b[i:j], start: i, end: j})
			i = j
		case c == '"':
			j, name := identEnd(b, i)
			out = append(out, tok{kind: tokIdent, text: name, start: i, end: j})
			i = j
		case c == '$' && (i == 0 || !isWord(b[i-1])):
			if j := dollarEnd(b, i); j > i {
				out = append(out, tok{kind: tokStr, text: b[i:j], start: i, end: j})
				i = j
				continue
			}
			out = append(out, tok{kind: tokPunct, text: b[i : i+1], start: i, end: i + 1})
			i++
		case c >= '0' && c <= '9':
			j := numberEnd(b, i)
			out = append(out, tok{kind: tokNum, text: b[i:j], start: i, end: j})
			i = j
		case isWord(c):
			j := i
			for j < len(b) && (isWord(b[j]) || b[j] == '$') {
				j++
			}
			out = append(out, tok{kind: tokWord, text: b[i:j], start: i, end: j})
			i = j
		default:
			out = append(out, tok{kind: tokPunct, text: b[i : i+1], start: i, end: i + 1})
			i++
		}
	}
	return out
}

// commentEnd is the offset after the nested /* */ comment at i, or the
// end of b.
func commentEnd(b []byte, i int) int {
	depth := 0
	for i < len(b) {
		switch {
		case b[i] == '/' && i+1 < len(b) && b[i+1] == '*':
			depth++
			i += 2
		case b[i] == '*' && i+1 < len(b) && b[i+1] == '/':
			depth--
			i += 2
			if depth == 0 {
				return i
			}
		default:
			i++
		}
	}
	return i
}

// stringEnd is the offset after the '...' string whose quote is at i.
func stringEnd(b []byte, i int, esc bool) int {
	for j := i + 1; j < len(b); j++ {
		switch {
		case esc && b[j] == '\\':
			j++
		case b[j] == '\'':
			if j+1 < len(b) && b[j+1] == '\'' {
				j++
				continue
			}
			return j + 1
		}
	}
	return len(b)
}

// identEnd is the offset after the "..." name at i and the name itself.
func identEnd(b []byte, i int) (int, []byte) {
	var name []byte
	for j := i + 1; j < len(b); j++ {
		if b[j] == '"' {
			if j+1 < len(b) && b[j+1] == '"' {
				name = append(name, '"')
				j++
				continue
			}
			return j + 1, name
		}
		name = append(name, b[j])
	}
	return len(b), name
}

// dollarEnd is the offset after the $tag$...$tag$ string at i, or i when
// the '$' doesn't open one.
func dollarEnd(b []byte, i int) int {
	j := i + 1
	for j < len(b) && b[j] != '$' {
		c := b[j]
		if !isTagByte(c, j > i+1) {
			return i
		}
		j++
	}
	if j >= len(b) {
		return i
	}
	tag := b[i : j+1]
	k := bytes.Index(b[j+1:], tag)
	if k < 0 {
		return len(b)
	}
	return j + 1 + k + len(tag)
}

// isTagByte reports whether c may stand in a dollar-quote tag. A digit
// may only when it isn't first.
func isTagByte(c byte, notFirst bool) bool {
	return c == '_' || c >= 0x80 || (c|0x20 >= 'a' && c|0x20 <= 'z') || (notFirst && c >= '0' && c <= '9')
}

// numberEnd is the offset after the number at i.
func numberEnd(b []byte, i int) int {
	j := i
	for j < len(b) {
		c := b[j]
		switch {
		case c >= '0' && c <= '9' || c == '.' || c == '_':
		case (c == 'e' || c == 'E') && j+1 < len(b):
			if b[j+1] == '-' || b[j+1] == '+' {
				j++
			}
		default:
			return j
		}
		j++
	}
	return j
}

// objName reads a possibly schema-qualified name at i and returns its
// last part (the table name without "public.") and the index after it.
func (t toks) objName(i int) (string, int) {
	if !t.at(i).isName() {
		return "", i
	}
	name := t[i].name()
	i++
	for t.punct(i, '.') && t.at(i+1).isName() {
		name = t[i+1].name()
		i += 2
	}
	return name, i
}

// Classify names the kind of a statement and the table it is about.
func Classify(text []byte) (core.StmtKind, string) {
	t := tokens(text, Mode{}, 12)
	if len(t) == 0 {
		return core.StmtComment, ""
	}
	switch {
	case t.is(0, "create"):
		i := 1
		if t.is(i, "or") && t.is(i+1, "replace") {
			i += 2
		}
		for t.is(i, "unlogged") || t.is(i, "temporary") || t.is(i, "temp") || t.is(i, "global") || t.is(i, "local") {
			i++
		}
		switch {
		case t.is(i, "table"):
			i++
			if t.is(i, "if") && t.is(i+1, "not") && t.is(i+2, "exists") {
				i += 3
			}
			name, _ := t.objName(i)
			return core.StmtCreateTable, name
		case t.is(i, "index"), t.is(i, "unique") && t.is(i+1, "index"):
			return core.StmtCreateIndex, ""
		case t.is(i, "sequence"):
			return core.StmtSequence, ""
		case t.is(i, "function"), t.is(i, "procedure"), t.is(i, "trigger"), t.is(i, "view"),
			t.is(i, "materialized"), t.is(i, "rule"), t.is(i, "aggregate"), t.is(i, "constraint") && t.is(i+1, "trigger"),
			t.is(i, "event") && t.is(i+1, "trigger"):
			return core.StmtRoutine, ""
		}
	case t.is(0, "alter"):
		switch {
		case t.is(1, "table"):
			i := 2
			if t.is(i, "if") && t.is(i+1, "exists") {
				i += 2
			}
			if t.is(i, "only") {
				i++
			}
			name, _ := t.objName(i)
			return KindAlterTable, name
		case t.is(1, "sequence"):
			return core.StmtSequence, ""
		}
	case t.is(0, "drop") && t.is(1, "table"):
		i := 2
		if t.is(i, "if") && t.is(i+1, "exists") {
			i += 2
		}
		name, _ := t.objName(i)
		return core.StmtDropTable, name
	case t.is(0, "copy"):
		name, _ := t.objName(1)
		return core.StmtCopy, name
	case t.is(0, "insert") && t.is(1, "into"):
		name, _ := t.objName(2)
		return core.StmtInsert, name
	case t.is(0, "set"), t.is(0, "reset"):
		return core.StmtSet, ""
	case t.is(0, "select"):
		fn, _ := t.objName(1)
		switch fn {
		case "set_config":
			return core.StmtSet, ""
		case "setval":
			return core.StmtSequence, ""
		}
	case t.is(0, "begin"), t.is(0, "start"), t.is(0, "commit"), t.is(0, "end"), t.is(0, "rollback"), t.is(0, "abort"):
		return core.StmtTransaction, ""
	case t.is(0, "comment") && t.is(1, "on"):
		return core.StmtCommentOn, core.DDLTable(text)
	case t.is(0, "lock"):
		name, _ := t.objName(1)
		if t.is(1, "table") {
			name, _ = t.objName(2)
		}
		return core.StmtLock, name
	}
	return core.StmtOther, ""
}

// SetMode follows standard_conforming_strings through a SET, RESET or
// SELECT pg_catalog.set_config(...) statement.
func SetMode(text []byte, cur Mode) Mode {
	t := tokens(text, cur, 16)
	i := 1
	switch {
	case t.is(0, "reset"):
		if t.is(1, "standard_conforming_strings") || t.is(1, "all") {
			cur.NoStdStrings = false
		}
		return cur
	case t.is(0, "set"):
		if t.is(i, "session") || t.is(i, "local") {
			i++
		}
		if !t.is(i, "standard_conforming_strings") {
			return cur
		}
		i++
		if !t.punct(i, '=') && !t.is(i, "to") {
			return cur
		}
		i++
	case t.is(0, "select"):
		fn, j := t.objName(1)
		if fn != "set_config" || !t.punct(j, '(') || !strEq(t.at(j+1), "standard_conforming_strings") || !t.punct(j+2, ',') {
			return cur
		}
		i = j + 3
	default:
		return cur
	}
	switch v := t.at(i); {
	case v.is("off") || v.is("false") || strEq(v, "off") || strEq(v, "false"):
		cur.NoStdStrings = true
	case v.is("on") || v.is("true") || v.is("default") || strEq(v, "on") || strEq(v, "true"):
		cur.NoStdStrings = false
	}
	return cur
}

// strEq reports whether t is the plain string literal 'w', in any case.
func strEq(t tok, w string) bool {
	return t.kind == tokStr && len(t.text) == len(w)+2 && t.text[0] == '\'' && eqFold(t.text[1:len(t.text)-1], w)
}

// Copy is what ParseCopy reads from a COPY ... FROM stdin statement.
type Copy struct {
	Table   string
	Columns []string // nil when the statement has no column list
}

// ParseCopy reads COPY name [(columns)] FROM stdin, the form pg_dump
// writes. ok is false for any other COPY (to a file, from a program, a
// query), which stays a statement for the allowlist to judge.
// Options after stdin (another format, delimiter or null string) give
// ok true with opts set: Roadie reads only pg_dump's text format.
func ParseCopy(text []byte) (c Copy, ok, opts bool) {
	t := tokens(text, Mode{}, 0)
	if !t.is(0, "copy") {
		return c, false, false
	}
	name, i := t.objName(1)
	if name == "" {
		return c, false, false
	}
	c.Table = name
	if t.punct(i, '(') {
		c.Columns = []string{}
		i++
		for !t.punct(i, ')') {
			if !t.at(i).isName() {
				return c, false, false
			}
			c.Columns = append(c.Columns, t[i].name())
			i++
			if t.punct(i, ',') {
				i++
			} else if !t.punct(i, ')') {
				return c, false, false
			}
		}
		i++
	}
	if !t.is(i, "from") || !t.is(i+1, "stdin") {
		return c, false, false
	}
	i += 2
	if !t.punct(i, ';') || i+1 != len(t) {
		return c, true, true
	}
	return c, true, false
}
