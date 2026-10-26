// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package dotenv

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
)

// variable is one KEY=value assignment and where its value's bytes are.
type variable struct {
	name             string
	value            string
	line             int
	quoted           bool
	refs             bool // holds ${OTHER}
	valStart, valEnd int
}

func (v *variable) pair() core.Pair {
	return core.Pair{Name: v.name, Value: v.value, Line: v.line, Quoted: v.quoted}
}

// errSyntax is wrapped by every parse error.
var errSyntax = errors.New(".env: unreadable")

func parse(src []byte) ([]variable, error) {
	var vars []variable
	line := 1
	i := 0
	for i < len(src) {
		start := i
		end := lineEnd(src, i)
		v, next, ok, err := parseAssignment(src, i, end, line)
		if err != nil {
			return nil, err
		}
		if ok {
			vars = append(vars, v)
			i = next
		} else {
			i = end
		}
		// Skip the rest of the line (a comment after a value) and its end.
		i = lineEnd(src, i)
		i = skipEOL(src, i)
		line += countLines(src[start:i])
	}
	return vars, nil
}

// parseAssignment reads [export ]KEY=value starting at the line start i.
// next is where the value ends; ok is false for a comment, a blank line
// or anything else that isn't an assignment, which is kept as it is.
func parseAssignment(src []byte, i, end, line int) (variable, int, bool, error) {
	i = skipBlanks(src, i, end)
	if bytes.HasPrefix(src[i:end], []byte("export ")) || bytes.HasPrefix(src[i:end], []byte("export\t")) {
		i = skipBlanks(src, i+len("export"), end)
	}
	k := i
	for k < end && isKeyByte(src[k], k == i) {
		k++
	}
	if k == i {
		return variable{}, 0, false, nil
	}
	name := string(src[i:k])
	k = skipBlanks(src, k, end)
	if k >= end || src[k] != '=' {
		return variable{}, 0, false, nil
	}
	k = skipBlanks(src, k+1, end)
	v := variable{name: name, line: line, valStart: k, valEnd: k}
	if k >= len(src) {
		return v, k, true, nil
	}
	switch src[k] {
	case '"':
		var b strings.Builder
		for j := k + 1; j < len(src); j++ {
			c := src[j]
			switch {
			case c == '\\' && j+1 < len(src):
				if r, ok := escapes[src[j+1]]; ok {
					b.WriteByte(r)
					j++
					continue
				}
				b.WriteByte(c)
			case c == '"':
				v.value, v.quoted, v.valEnd = b.String(), true, j+1
				v.refs = strings.Contains(v.value, "${")
				return v, j + 1, true, nil
			default:
				b.WriteByte(c)
			}
		}
		return variable{}, 0, false, fmt.Errorf("%w: the quoted value of %s at line %d never ends", errSyntax, name, line)
	case '\'':
		j := bytes.IndexByte(src[k+1:], '\'')
		if j < 0 {
			return variable{}, 0, false, fmt.Errorf("%w: the quoted value of %s at line %d never ends", errSyntax, name, line)
		}
		v.value, v.quoted, v.valEnd = string(src[k+1:k+1+j]), true, k+2+j
		return v, v.valEnd, true, nil
	}
	// Unquoted: up to a '#' after whitespace, then trailing blanks trimmed.
	j := k
	for j < end && (src[j] != '#' || j == k || src[j-1] != ' ' && src[j-1] != '\t') {
		j++
	}
	for j > k && (src[j-1] == ' ' || src[j-1] == '\t') {
		j--
	}
	v.value, v.valEnd = string(src[k:j]), j
	v.refs = strings.Contains(v.value, "${")
	return v, j, true, nil
}

// escapes are the double quoted escapes Laravel's dotenv decodes.
var escapes = map[byte]byte{'"': '"', '\\': '\\', '$': '$', 'n': '\n', 'r': '\r', 't': '\t', 'f': '\f', 'v': '\v'}

func isKeyByte(c byte, first bool) bool {
	if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '_' {
		return true
	}
	return !first && (c >= '0' && c <= '9' || c == '.')
}

func skipBlanks(src []byte, i, end int) int {
	for i < end && (src[i] == ' ' || src[i] == '\t') {
		i++
	}
	return i
}

// lineEnd is the offset of the line end (\n, \r\n or \r) at or after i.
func lineEnd(src []byte, i int) int {
	for i < len(src) && src[i] != '\n' && src[i] != '\r' {
		i++
	}
	return i
}

func skipEOL(src []byte, i int) int {
	switch {
	case i+1 < len(src) && src[i] == '\r' && src[i+1] == '\n':
		return i + 2
	case i < len(src) && (src[i] == '\n' || src[i] == '\r'):
		return i + 1
	}
	return i
}

func countLines(b []byte) int {
	n := 0
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' || b[i] == '\r' && (i+1 >= len(b) || b[i+1] != '\n') {
			n++
		}
	}
	return n
}
