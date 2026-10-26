// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package allow

import (
	"bytes"
	"strings"
)

// Token kinds.
const (
	word   = 'w' // a bare word or number, lower-cased in w
	ident  = 'i' // a quoted identifier, unquoted in w
	str    = 's' // a string literal (text holds the quotes)
	punct  = 'p' // one byte of punctuation
	noKind = 0
)

// tok is one token of a statement.
type tok struct {
	kind byte
	w    string // the word lower-cased, the identifier unquoted, the punctuation byte
}

// toks is a statement as tokens, with helpers that read past the end as
// nothing, so the rules can look ahead without bounds checks.
type toks []tok

// w is the word at i, or "".
func (t toks) w(i int) string {
	if i < 0 || i >= len(t) || t[i].kind != word {
		return ""
	}
	return t[i].w
}

// is reports whether the words at i, i+1, ... are ws.
func (t toks) is(i int, ws ...string) bool {
	for k, w := range ws {
		if t.w(i+k) != w {
			return false
		}
	}
	return true
}

// p reports whether t[i] is the punctuation c.
func (t toks) p(i int, c byte) bool {
	return i >= 0 && i < len(t) && t[i].kind == punct && t[i].w[0] == c
}

// kind is the kind of t[i], or noKind past the end.
func (t toks) kind(i int) byte {
	if i < 0 || i >= len(t) {
		return noKind
	}
	return t[i].kind
}

// name reports whether t[i] can be an object name: a word or a quoted
// identifier.
func (t toks) name(i int) bool { k := t.kind(i); return k == word || k == ident }

// seq reports whether the words ws stand next to each other anywhere.
func (t toks) seq(ws ...string) bool {
	for i := range t {
		if t.is(i, ws...) {
			return true
		}
	}
	return false
}

// call reports whether the word fn is called anywhere, fn( or x.fn(.
func (t toks) call(fn string) bool {
	for i := range t {
		if t.w(i) == fn && t.p(i+1, '(') {
			return true
		}
	}
	return false
}

// objName reads a possibly qualified name at i (schema.name or
// db.name) and returns its last part, folded for the dialect and the
// index after it. It returns "" when t[i] isn't a name.
func (t toks) objName(i int, pg bool) (string, int) {
	if !t.name(i) {
		return "", i
	}
	fold := func(k int) string {
		if pg && t[k].kind == word {
			return strings.ToLower(t[k].w)
		}
		return t[k].w
	}
	n := fold(i)
	i++
	for t.p(i, '.') && t.name(i+1) {
		n = fold(i + 1)
		i += 2
	}
	return n, i
}

// syntax are the quoting rules of one dialect.
type syntax struct {
	pg          bool // PostgreSQL: '' only, E'..', $tag$..$tag$, nested /* */ and -- comments
	backslashes bool // MySQL: backslash escapes in strings (no NO_BACKSLASH_ESCAPES)
	ansiQuotes  bool // MySQL: "..." is an identifier (ANSI_QUOTES)
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

func isWordByte(c byte) bool {
	return c == '_' || c == '$' || c >= 0x80 || (c >= '0' && c <= '9') || (c|0x20 >= 'a' && c|0x20 <= 'z')
}

// lex splits a statement into tokens. Comments are skipped; for MySQL
// the caller passes mysql.Body, which has already taken them out and
// opened the versioned comments.
func lex(b []byte, sx syntax) toks {
	var out toks
	n := len(b)
	for i := 0; i < n; {
		c := b[i]
		switch {
		case isSpace(c):
			i++
		case c == '-' && i+1 < n && b[i+1] == '-' && sx.pg:
			j := bytes.IndexByte(b[i:], '\n')
			if j < 0 {
				return out
			}
			i += j + 1
		case c == '/' && i+1 < n && b[i+1] == '*' && sx.pg:
			i = pgComment(b, i)
		case c == '\'' || (c == '"' && !sx.pg && !sx.ansiQuotes):
			j := quoted(b, i, !sx.pg && sx.backslashes)
			out = append(out, tok{str, string(b[i:j])})
			i = j
		case c == '`' || c == '"':
			j := quoted(b, i, false)
			inner := b[i+1 : max(i+1, j-1)]
			out = append(out, tok{ident, strings.ReplaceAll(string(inner), string([]byte{c, c}), string(c))})
			i = j
		case sx.pg && c == '$' && dollarTag(b, i) > 0:
			tag := b[i : i+dollarTag(b, i)]
			end := bytes.Index(b[i+len(tag):], tag)
			j := n
			if end >= 0 {
				j = i + len(tag) + end + len(tag)
			}
			out = append(out, tok{str, string(b[i:j])})
			i = j
		case sx.pg && (c == 'e' || c == 'E') && i+1 < n && b[i+1] == '\'' && (i == 0 || !isWordByte(b[i-1])):
			j := quoted(b, i+1, true)
			out = append(out, tok{str, string(b[i:j])})
			i = j
		case isWordByte(c) && c != '$':
			j := i
			for j < n && isWordByte(b[j]) {
				j++
			}
			out = append(out, tok{word, strings.ToLower(string(b[i:j]))})
			i = j
		default:
			out = append(out, tok{punct, string(c)})
			i++
		}
	}
	return out
}

// quoted returns the index after the quoted run starting at b[i], or
// len(b) when it doesn't close. A doubled quote stays inside.
func quoted(b []byte, i int, esc bool) int {
	q := b[i]
	for j := i + 1; j < len(b); j++ {
		switch {
		case esc && b[j] == '\\':
			j++
		case b[j] == q:
			if j+1 < len(b) && b[j+1] == q {
				j++
				continue
			}
			return j + 1
		}
	}
	return len(b)
}

// dollarTag returns the length of a $tag$ opener at b[i], or 0.
func dollarTag(b []byte, i int) int {
	for j := i + 1; j < len(b); j++ {
		c := b[j]
		switch {
		case c == '$':
			return j - i + 1
		case c == '_' || c >= 0x80 || (c|0x20 >= 'a' && c|0x20 <= 'z') || (j > i+1 && c >= '0' && c <= '9'):
		default:
			return 0
		}
	}
	return 0
}

// pgComment returns the index after the nested /* */ comment at b[i].
func pgComment(b []byte, i int) int {
	depth := 0
	for j := i; j+1 < len(b); j++ {
		switch {
		case b[j] == '/' && b[j+1] == '*':
			depth++
			j++
		case b[j] == '*' && b[j+1] == '/':
			depth--
			j++
			if depth == 0 {
				return j + 1
			}
		}
	}
	return len(b)
}
