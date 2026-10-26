// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package db

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump/pgsql"
)

// stmtText is a statement without its trailing semicolons and space: the
// drivers take one statement with no terminator.
func stmtText(b []byte) string {
	return strings.TrimRight(string(b), "; \t\r\n")
}

// delimiterArg is the delimiter a mysql client "DELIMITER x" line sets,
// or "" when it sets ';' or names none.
func delimiterArg(b []byte) string {
	f := strings.Fields(string(b))
	if len(f) < 2 || f[1] == ";" {
		return ""
	}
	return f[1]
}

// trimDelim cuts a custom delimiter (";;" or "$$") off the end of a
// statement, after any trailing space.
func trimDelim(b []byte, delim string) []byte {
	if delim == "" {
		return b
	}
	t := bytes.TrimRight(b, " \t\r\n")
	if bytes.HasSuffix(t, []byte(delim)) {
		return t[:len(t)-len(delim)]
	}
	return b
}

// hasWordPrefix reports whether s starts with the word w, any case.
func hasWordPrefix(s, w string) bool {
	if len(s) < len(w) || !strings.EqualFold(s[:len(w)], w) {
		return false
	}
	return len(s) == len(w) || !isIdentByte(s[len(w)])
}

func isIdentByte(c byte) bool {
	return c == '_' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

// objectName finds the first of the keywords outside quotes and
// comments and reads the (possibly schema-qualified) name after it,
// past IF [NOT] EXISTS and ONLY. raw is the name as written, quotes
// included, ready to put back into SQL of the same dialect; name is its
// last part unquoted. Both are "" when there is no such name. A MySQL
// versioned comment (/*!40101 ... */) is read as its body.
func objectName(text []byte, keywords ...string) (raw, name string) {
	s := string(text)
	i := 0
	found := false
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		case c == '(' || c == ',' || c == ';':
			if found {
				return "", ""
			}
			i++
		case strings.HasPrefix(s[i:], "--") || c == '#':
			j := strings.IndexByte(s[i:], '\n')
			if j < 0 {
				return "", ""
			}
			i += j + 1
		case strings.HasPrefix(s[i:], "/*!") || strings.HasPrefix(s[i:], "/*M!"):
			i += 3
			if s[i-1] == 'M' {
				i++
			}
			for i < len(s) && s[i] >= '0' && s[i] <= '9' {
				i++
			}
		case strings.HasPrefix(s[i:], "*/"):
			i += 2
		case strings.HasPrefix(s[i:], "/*"):
			j := strings.Index(s[i+2:], "*/")
			if j < 0 {
				return "", ""
			}
			i += j + 4
		case c == '\'' || c == '"' || c == '`' || c == '[':
			end := quotedEnd(s, i)
			if end < 0 {
				return "", ""
			}
			if found {
				return readName(s, i)
			}
			i = end
		case isIdentByte(c):
			j := i
			for j < len(s) && isIdentByte(s[j]) {
				j++
			}
			w := s[i:j]
			if found {
				lw := strings.ToLower(w)
				if lw == "if" || lw == "not" || lw == "exists" || lw == "only" {
					i = j
					continue
				}
				return readName(s, i)
			}
			for _, k := range keywords {
				if strings.EqualFold(w, k) {
					found = true
				}
			}
			i = j
		default:
			if found {
				return "", ""
			}
			i++
		}
	}
	return "", ""
}

// readName reads name parts joined by '.' from s[i].
func readName(s string, i int) (raw, name string) {
	start := i
	for {
		if i >= len(s) {
			return "", ""
		}
		var part string
		switch c := s[i]; {
		case c == '"' || c == '`' || c == '[':
			end := quotedEnd(s, i)
			if end < 0 {
				return "", ""
			}
			closer := c
			if c == '[' {
				closer = ']'
			}
			part = strings.ReplaceAll(s[i+1:end-1], string([]byte{closer, closer}), string(closer))
			i = end
		case isIdentByte(c):
			j := i
			for j < len(s) && isIdentByte(s[j]) {
				j++
			}
			part = s[i:j]
			i = j
		default:
			return "", ""
		}
		name = part
		if i < len(s) && s[i] == '.' {
			i++
			continue
		}
		return s[start:i], name
	}
}

// qualifiedName reports whether raw, a name as readName returns it, has
// more than one part (db.t or schema.t).
func qualifiedName(raw string) bool {
	for i := 0; i < len(raw); i++ {
		switch raw[i] {
		case '"', '`', '[':
			end := quotedEnd(raw, i)
			if end < 0 {
				return false
			}
			i = end - 1
		case '.':
			return true
		}
	}
	return false
}

// quotedEnd returns the index after the quoted run starting at s[i], or
// -1 when it never closes. A doubled quote stays inside; a backslash
// escapes in a single-quoted string.
func quotedEnd(s string, i int) int {
	q := s[i]
	if q == '[' {
		j := strings.IndexByte(s[i+1:], ']')
		if j < 0 {
			return -1
		}
		return i + j + 2
	}
	for j := i + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			if q == '\'' {
				j++
			}
		case q:
			if j+1 < len(s) && s[j+1] == q {
				j++
				continue
			}
			return j + 1
		}
	}
	return -1
}

// insertVerb is the start of the INSERT the sink sends for a statement
// with this head: REPLACE INTO, INSERT IGNORE INTO or INSERT INTO.
func insertVerb(head []byte) string {
	h := strings.TrimSpace(string(head))
	if hasWordPrefix(h, "replace") {
		return "REPLACE INTO"
	}
	into := strings.Index(strings.ToLower(h), " into ")
	if into > 0 && strings.Contains(strings.ToLower(h[:into]), "ignore") {
		return "INSERT IGNORE INTO"
	}
	return "INSERT INTO"
}

func dirty(r *core.Row) bool {
	for _, v := range r.Values {
		if v.Dirty {
			return true
		}
	}
	return false
}

// errExpr is a row value written as an expression: the sink loads data
// only, never SQL from a dump.
var errExpr = errors.New("a value is written as an expression, which Roadie loads into a database only as plain data; load this dump with the database's own client")

// argOf is the driver parameter for a value (MySQL, MariaDB, SQLite).
func argOf(d core.Dialect, v core.Value) (any, error) {
	switch v.Kind {
	case core.Null:
		return nil, nil
	case core.String:
		return string(v.Raw), nil
	case core.BinaryString:
		return append([]byte{}, v.Raw...), nil
	case core.Number:
		if d == core.SQLite {
			s := string(v.Raw)
			if n, err := strconv.ParseInt(s, 10, 64); err == nil {
				return n, nil
			}
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return f, nil
			}
		}
		return string(v.Raw), nil
	case core.Hex:
		return hexBytes(v.Raw)
	case core.Bit:
		bits, err := bitDigits(v.Raw)
		if err != nil {
			return nil, err
		}
		if len(bits) > 64 {
			return nil, fmt.Errorf("a BIT value of %d bits is longer than 64", len(bits))
		}
		n := uint64(0)
		if bits != "" {
			if n, err = strconv.ParseUint(bits, 2, 64); err != nil {
				return nil, err
			}
		}
		return n, nil
	case core.Expr:
		switch strings.ToLower(strings.TrimSpace(string(v.Raw))) {
		case "true":
			return int64(1), nil
		case "false":
			return int64(0), nil
		case "null":
			return nil, nil
		}
	}
	return nil, errExpr
}

// hexBytes decodes 0xABCD, X'ABCD' or x'abcd'.
func hexBytes(raw []byte) ([]byte, error) {
	s := string(raw)
	switch {
	case strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X"):
		s = s[2:]
	case len(s) >= 3 && (s[0] == 'x' || s[0] == 'X') && s[1] == '\'' && s[len(s)-1] == '\'':
		s = s[2 : len(s)-1]
	default:
		return nil, errors.New("a hex value isn't a hex literal")
	}
	if len(s)%2 == 1 {
		s = "0" + s
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, errors.New("a hex value isn't a hex literal")
	}
	return b, nil
}

// bitDigits is the 0 and 1 digits of b'0101', B'0101' or 0b0101.
func bitDigits(raw []byte) (string, error) {
	s := string(raw)
	switch {
	case strings.HasPrefix(s, "0b") || strings.HasPrefix(s, "0B"):
		s = s[2:]
	case len(s) >= 3 && (s[0] == 'b' || s[0] == 'B') && s[1] == '\'' && s[len(s)-1] == '\'':
		s = s[2 : len(s)-1]
	default:
		return "", errors.New("a bit value isn't a bit literal")
	}
	if strings.Trim(s, "01") != "" {
		return "", errors.New("a bit value isn't a bit literal")
	}
	return s, nil
}

// reCastLiteral is a PostgreSQL string literal with a cast, as pg_dump
// --inserts writes some values: '2024-01-01'::date.
var reCastLiteral = regexp.MustCompile(`^'((?:[^']|'')*)'::[A-Za-z_][A-Za-z0-9_ ."\[\]()]*$`)

// copyField appends a value as a COPY text field.
func copyField(out []byte, v core.Value) ([]byte, error) {
	switch v.Kind {
	case core.Null, core.String, core.BinaryString, core.Number:
		return pgsql.AppendCopyField(out, v)
	case core.Bit:
		bits, err := bitDigits(v.Raw)
		if err != nil {
			return out, err
		}
		return append(out, bits...), nil
	case core.Hex:
		b, err := hexBytes(v.Raw)
		if err != nil {
			return out, err
		}
		for _, c := range b {
			for i := 7; i >= 0; i-- {
				out = append(out, '0'+(c>>uint(i))&1)
			}
		}
		return out, nil
	}
	t := bytes.TrimSpace(v.Raw)
	switch strings.ToLower(string(t)) {
	case "true":
		return append(out, 't'), nil
	case "false":
		return append(out, 'f'), nil
	case "null":
		return append(out, `\N`...), nil
	}
	if m := reCastLiteral.FindSubmatch(t); m != nil {
		lit := bytes.ReplaceAll(m[1], []byte("''"), []byte("'"))
		return pgsql.AppendCopyField(out, core.Value{Kind: core.String, Raw: lit})
	}
	return out, errExpr
}
