// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mysql

import "regexp"

// reNumberRun is a number literal: decimal with an exponent, 0x hex or
// 0b binary.
var reNumberRun = regexp.MustCompile(`^(0x[0-9a-fA-F]+|0b[01]+|[0-9]+(\.[0-9]*)?([eE][-+]?[0-9]+)?)$`)

// exprWords are the words a row value may hold outside quotes: literal
// keywords and operators. Anything else is a function, a column or a
// query.
var exprWords = map[string]bool{
	"true": true, "false": true, "null": true, "default": true, "unknown": true,
	"current_timestamp": true, "current_date": true, "current_time": true, "localtime": true, "localtimestamp": true,
	"and": true, "or": true, "not": true, "xor": true, "div": true, "mod": true, "is": true,
}

// SafeExpr reports whether a row value the lexer kept as an expression
// (Kind Expr) is data only: literals (strings, numbers, X'..', b'..'),
// the keywords TRUE, FALSE, NULL and DEFAULT, CURRENT_TIMESTAMP and its
// kin written without parentheses, operators and parentheses.
// A function call (LOAD_FILE, sys_exec), a column or variable name, a
// subquery or a comment (which could hide a versioned statement) is not.
// mysqldump, mariadb-dump and phpMyAdmin write plain literals, so a
// dump holding anything else was not made by them.
func SafeExpr(text []byte, m Mode) bool {
	for i := 0; i < len(text); {
		c := text[i]
		switch {
		case c == '\'' || c == '"' && !m.ANSIQuotes:
			j := skipString(text, i, m)
			if j < 0 {
				return false
			}
			i = j
		case c >= '0' && c <= '9':
			// A number; a run like 1abc is an identifier, which MySQL
			// allows to start with a digit.
			j := i
			for j < len(text) && (isWord(text[j]) || text[j] == '.' || (text[j] == '+' || text[j] == '-') && (text[j-1]|0x20 == 'e')) {
				j++
			}
			if !reNumberRun.Match(text[i:j]) {
				return false
			}
			i = j
		case isWord(c):
			j := i
			for j < len(text) && isWord(text[j]) {
				j++
			}
			w := text[i:j]
			if j < len(text) && text[j] == '\'' && len(w) == 1 && (w[0]|0x20 == 'x' || w[0]|0x20 == 'b' || w[0]|0x20 == 'n') {
				i = j // X'..', b'..', N'..'
				continue
			}
			if !exprWords[lowerWord(w)] {
				return false
			}
			i = j
		case c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '(' || c == ')' || c == ',' || c == '.':
			i++
		case c == '+' || c == '-' || c == '*' || c == '%' || c == '<' || c == '>' || c == '=' || c == '!' || c == '&' || c == '|' || c == '^' || c == '~':
			if c == '-' && i+1 < len(text) && text[i+1] == '-' {
				return false // a comment
			}
			i++
		case c == '/':
			if i+1 < len(text) && text[i+1] == '*' {
				return false
			}
			i++
		default:
			// ` names a column, @ a variable, # starts a comment; any
			// other byte isn't part of a literal expression.
			return false
		}
	}
	return true
}

// plainWords are the keywords a value may be on its own: they carry no
// data.
var plainWords = map[string]bool{
	"true": true, "false": true, "null": true, "default": true, "unknown": true,
	"current_timestamp": true, "current_date": true, "current_time": true, "localtime": true, "localtimestamp": true,
}

// PlainExpr reports whether an expression value (Kind Expr) is one
// keyword such as TRUE, DEFAULT or CURRENT_TIMESTAMP, maybe in
// parentheses. Anything with a literal in it, such as adjacent strings
// ('sk_live_' '51Hx'), which MySQL joins into one, or 1 + 2, is not:
// Roadie would scan its pieces, not the value the server stores. No dump
// tool writes those.
func PlainExpr(text []byte) bool {
	words := 0
	for i := 0; i < len(text); {
		c := text[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '(' || c == ')':
			i++
		case isWord(c) && (c < '0' || c > '9'):
			j := i
			for j < len(text) && isWord(text[j]) {
				j++
			}
			if !plainWords[lowerWord(text[i:j])] {
				return false
			}
			words++
			i = j
		default:
			return false
		}
	}
	return words == 1
}

// BitHex turns a bit literal, b'0111' or 0b0111, into the hex literal of
// the same bytes (0x07): MySQL pads the bits on the left to whole bytes.
func BitHex(raw []byte) ([]byte, bool) {
	var bits []byte
	switch {
	case len(raw) >= 3 && raw[0]|0x20 == 'b' && raw[1] == '\'' && raw[len(raw)-1] == '\'':
		bits = raw[2 : len(raw)-1]
	case len(raw) >= 3 && raw[0] == '0' && raw[1] == 'b':
		bits = raw[2:]
	default:
		return nil, false
	}
	if len(bits) == 0 {
		return []byte("X''"), true
	}
	const digits = "0123456789ABCDEF"
	n := (len(bits) + 7) / 8
	out := make([]byte, 2, 2+2*n)
	out[0], out[1] = '0', 'x'
	pad := n*8 - len(bits)
	var b byte
	for k := 0; k < n*8; k++ {
		bit := byte(0)
		if k >= pad {
			switch bits[k-pad] {
			case '0':
			case '1':
				bit = 1
			default:
				return nil, false
			}
		}
		b = b<<1 | bit
		if k%8 == 7 {
			out = append(out, digits[b>>4], digits[b&15])
			b = 0
		}
	}
	return out, true
}

// skipString returns the index after the quoted string at text[i], or
// -1 when it doesn't close.
func skipString(text []byte, i int, m Mode) int {
	q := text[i]
	for j := i + 1; j < len(text); j++ {
		switch text[j] {
		case '\\':
			if !m.NoBackslashEscapes {
				j++
			}
		case q:
			if j+1 < len(text) && text[j+1] == q {
				j++
				continue
			}
			return j + 1
		}
	}
	return -1
}

// lowerWord is b in lower case, for the keyword lookup.
func lowerWord(b []byte) string {
	out := make([]byte, len(b))
	for i, c := range b {
		out[i] = lower(c)
	}
	return string(out)
}
