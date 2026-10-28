// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package core

import "bytes"

// DDLKinds are the statement kinds whose string literals (a column's
// DEFAULT, a COMMENT) are kept in the copy as written: copy scrubs them
// and verify scans them like row values, since a production email can
// sit in a DEFAULT as well as in a row. "alter-table" is MySQL's ALTER
// TABLE other than DISABLE and ENABLE KEYS.
var DDLKinds = []StmtKind{StmtCreateTable, "alter-table"}

// IsDDL reports whether a passthrough statement kind is one of DDLKinds.
func IsDDL(k StmtKind) bool {
	for _, d := range DDLKinds {
		if k == d {
			return true
		}
	}
	return false
}

// DDLLiterals returns the byte ranges of the single-quoted string
// literals of a DDL statement, inside the quotes. Identifiers in
// backticks or double quotes and comments are skipped. A doubled quote
// or a backslash escape stays inside the literal, as MySQL and the E
// strings of PostgreSQL read them.
func DDLLiterals(text []byte) [][2]int {
	var out [][2]int
	for i := 0; i < len(text); i++ {
		switch c := text[i]; {
		case c == '`' || c == '"':
			if j := bytes.IndexByte(text[i+1:], c); j >= 0 {
				i += j + 1
			} else {
				return out
			}
		case c == '-' && i+1 < len(text) && text[i+1] == '-':
			j := bytes.IndexByte(text[i:], '\n')
			if j < 0 {
				return out
			}
			i += j
		case c == '/' && i+1 < len(text) && text[i+1] == '*' && (i+2 >= len(text) || text[i+2] != '!'):
			j := bytes.Index(text[i+2:], []byte("*/"))
			if j < 0 {
				return out
			}
			i += j + 3
		case c == '\'':
			start := i + 1
			j := start
			for j < len(text) {
				if text[j] == '\\' {
					j += 2
					continue
				}
				if text[j] == '\'' {
					if j+1 < len(text) && text[j+1] == '\'' {
						j += 2
						continue
					}
					break
				}
				j++
			}
			if j >= len(text) {
				return out
			}
			out = append(out, [2]int{start, j})
			i = j
		}
	}
	return out
}

// DDLTable is the table a CREATE TABLE or ALTER TABLE statement names,
// without quotes or a schema; "" when it can't tell.
func DDLTable(text []byte) string {
	low := bytes.ToLower(text)
	i := bytes.Index(low, []byte("table"))
	if i < 0 {
		return ""
	}
	rest := bytes.TrimSpace(text[i+5:])
	for _, w := range []string{"if not exists", "if exists", "only"} {
		if len(rest) >= len(w) && bytes.EqualFold(rest[:len(w)], []byte(w)) {
			rest = bytes.TrimSpace(rest[len(w):])
		}
	}
	var name []byte
	for len(rest) > 0 {
		var part []byte
		switch q := rest[0]; q {
		case '`', '"':
			j := bytes.IndexByte(rest[1:], q)
			if j < 0 {
				return ""
			}
			part, rest = rest[1:j+1], rest[j+2:]
		default:
			j := bytes.IndexAny(rest, " \t\r\n(.;")
			if j < 0 {
				j = len(rest)
			}
			part, rest = rest[:j], rest[j:]
		}
		name = part
		if len(rest) == 0 || rest[0] != '.' {
			break
		}
		rest = rest[1:]
	}
	return string(name)
}
