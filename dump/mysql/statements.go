// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mysql

import (
	"bytes"

	"github.com/RhythmShahriar/roadie/core"
)

// Body returns what the server runs of a statement: versioned comment
// markers (/*!50003 and /*M!100100 and their closing */) removed with
// their contents kept, plain comments removed, strings untouched. The
// allowlist and the classifier judge this, so /*!50000 GRANT ... */ reads
// as GRANT whatever version it names.
func Body(text []byte, m Mode) []byte {
	out := make([]byte, 0, len(text))
	depth := 0
	n := len(text)
	for i := 0; i < n; {
		c := text[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			j := quotedEnd(text, i, m)
			out = append(out, text[i:j]...)
			i = j
		case c == '/' && i+1 < n && text[i+1] == '*':
			switch {
			case i+2 < n && text[i+2] == '!':
				i = skipDigits(text, i+3)
				depth++
			case i+3 < n && text[i+2] == 'M' && text[i+3] == '!':
				i = skipDigits(text, i+4)
				depth++
			default:
				end := bytes.Index(text[i+2:], []byte("*/"))
				if end < 0 {
					i = n
				} else {
					i += 2 + end + 2
				}
			}
			out = append(out, ' ')
		case c == '*' && depth > 0 && i+1 < n && text[i+1] == '/':
			depth--
			out = append(out, ' ')
			i += 2
		case c == '-' && i+1 < n && text[i+1] == '-' && (i+2 == n || text[i+2] <= ' '), c == '#':
			end := bytes.IndexByte(text[i:], '\n')
			if end < 0 {
				i = n
			} else {
				i += end
			}
		default:
			out = append(out, c)
			i++
		}
	}
	return bytes.TrimSpace(out)
}

func skipDigits(b []byte, i int) int {
	for i < len(b) && b[i] >= '0' && b[i] <= '9' {
		i++
	}
	return i
}

// quotedEnd returns the index after the quoted run that starts at i, or
// len(b) when it doesn't close.
func quotedEnd(b []byte, i int, m Mode) int {
	q := b[i]
	esc := q != '`' && !m.NoBackslashEscapes && (q != '"' || !m.ANSIQuotes)
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

// Token kinds.
const (
	tokWord   = 'w' // a bare word or number
	tokIdent  = 'i' // a `quoted` identifier, unquoted in text
	tokString = 's' // a quoted string, with its quotes
	tokPunct  = 'p' // one byte of punctuation
)

// tok is one token of a statement body.
type tok struct {
	kind byte
	text []byte
}

func (t tok) is(w string) bool { return t.kind == tokWord && eqFold(t.text, w) }

func (t tok) punct(c byte) bool { return t.kind == tokPunct && t.text[0] == c }

// name is the token as an identifier: unquoted for `x`, as is for a word.
func (t tok) name() string { return string(t.text) }

// tokens splits a body into at most max tokens (0 = all).
func tokens(b []byte, m Mode, max int) []tok {
	var out []tok
	for i := 0; i < len(b) && (max == 0 || len(out) < max); {
		c := b[i]
		switch {
		case isSpace(c):
			i++
		case c == '`' || (c == '"' && m.ANSIQuotes):
			j := quotedEnd(b, i, m)
			inner := b[i+1 : max0(i+1, j-1)]
			out = append(out, tok{tokIdent, bytes.ReplaceAll(inner, []byte{c, c}, []byte{c})})
			i = j
		case c == '\'' || c == '"':
			j := quotedEnd(b, i, m)
			out = append(out, tok{tokString, b[i:j]})
			i = j
		case isWord(c):
			j := i
			for j < len(b) && isWord(b[j]) {
				j++
			}
			out = append(out, tok{tokWord, b[i:j]})
			i = j
		default:
			out = append(out, tok{tokPunct, b[i : i+1]})
			i++
		}
	}
	return out
}

func max0(a, b int) int {
	if b < a {
		return a
	}
	return b
}

// Classify names the kind of a statement that isn't row data and the
// table it names when it is about one. An INSERT or REPLACE reaching
// Classify is one the lexer couldn't stream (inside a versioned comment,
// INSERT ... SELECT or SET), so it is StmtOther: its values would pass
// unread and the allowlist refuses other.
func Classify(text []byte, m Mode) (core.StmtKind, string) {
	t := tokens(Body(text, m), m, 32)
	if len(t) == 0 {
		return core.StmtComment, ""
	}
	at := func(i int, w string) bool { return i < len(t) && t[i].is(w) }
	switch {
	case at(0, "create"):
		i := skipCreateModifiers(t, 1)
		switch {
		case at(i, "table"):
			return core.StmtCreateTable, tableAfter(t, i+1)
		case at(i, "view"), at(i, "trigger"), at(i, "procedure"), at(i, "function"), at(i, "event"):
			return core.StmtRoutine, ""
		case at(i, "index"), at(i, "unique"), at(i, "fulltext"), at(i, "spatial"):
			return core.StmtCreateIndex, ""
		case at(i, "sequence"):
			return core.StmtSequence, ""
		}
	case at(0, "drop") && at(1, "table"):
		return core.StmtDropTable, tableAfter(t, 2)
	case at(0, "drop") && at(1, "temporary") && at(2, "table"):
		return core.StmtDropTable, tableAfter(t, 3)
	case at(0, "lock") && (at(1, "tables") || at(1, "table")):
		return core.StmtLock, tableAfter(t, 2)
	case at(0, "unlock") && (at(1, "tables") || at(1, "table")):
		return core.StmtUnlock, ""
	case at(0, "set"):
		return core.StmtSet, ""
	case at(0, "start") && at(1, "transaction"), at(0, "begin"), at(0, "commit"), at(0, "rollback"):
		return core.StmtTransaction, ""
	case at(0, "alter") && at(1, "table"):
		name := tableAfter(t, 2)
		j := 3
		for j < len(t) && t[j].punct('.') {
			j += 2
		}
		if (at(j, "disable") || at(j, "enable")) && at(j+1, "keys") {
			return core.StmtAlterKeys, name
		}
		return KindAlterTable, name
	case at(0, "use"):
		return KindUse, ""
	}
	return core.StmtOther, ""
}

// skipCreateModifiers skips what may stand between CREATE and the kind of
// object: OR REPLACE, TEMPORARY, ALGORITHM=x, DEFINER=user@host, SQL
// SECURITY x, AGGREGATE.
func skipCreateModifiers(t []tok, i int) int {
	for i < len(t) {
		switch {
		case t[i].is("or") && i+1 < len(t) && t[i+1].is("replace"):
			i += 2
		case t[i].is("temporary"), t[i].is("aggregate"), t[i].is("online"), t[i].is("offline"):
			i++
		case t[i].is("algorithm") && i+2 < len(t) && t[i+1].punct('='):
			i += 3
		case t[i].is("sql") && i+2 < len(t) && t[i+1].is("security"):
			i += 3
		case t[i].is("definer"):
			i = skipDefiner(t, i)
		default:
			return i
		}
	}
	return i
}

// skipDefiner skips DEFINER = user[@host] or DEFINER = CURRENT_USER[()]
// from t[i] (the DEFINER word) and returns the index after it.
func skipDefiner(t []tok, i int) int {
	i++
	if i < len(t) && t[i].punct('=') {
		i++
	}
	if i < len(t) {
		i++ // the user
	}
	if i+1 < len(t) && t[i].punct('(') && t[i+1].punct(')') {
		return i + 2
	}
	if i+1 < len(t) && t[i].punct('@') {
		i += 2
	}
	return i
}

// tableAfter is the table name at t[i], after IF [NOT] EXISTS, with any
// schema qualifier dropped.
func tableAfter(t []tok, i int) string {
	if i < len(t) && t[i].is("if") {
		i++
		if i < len(t) && t[i].is("not") {
			i++
		}
		if i < len(t) && t[i].is("exists") {
			i++
		}
	}
	if i >= len(t) || (t[i].kind != tokWord && t[i].kind != tokIdent) {
		return ""
	}
	name := t[i].name()
	for i+2 < len(t) && t[i+1].punct('.') && (t[i+2].kind == tokWord || t[i+2].kind == tokIdent) {
		name = t[i+2].name()
		i += 2
	}
	return name
}
