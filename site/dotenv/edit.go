// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package dotenv

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
)

func (f *File) last(name string) *variable {
	for i := len(f.vars) - 1; i >= 0; i-- {
		if f.vars[i].name == name {
			return &f.vars[i]
		}
	}
	return nil
}

func (f *File) set(name, value string) error {
	if !validName(name) {
		return fmt.Errorf(".env: %q is not a variable name", name)
	}
	q, err := Quote(value)
	if err != nil {
		return fmt.Errorf(".env: %s: %w", name, err)
	}
	nf, ok := f.try(name, value, q)
	if !ok && q == value {
		// Text after the old value (B="" "") can join or replace an
		// unquoted new one; quoted, the value stands alone.
		nf, ok = f.try(name, value, quoteAlways(value))
	}
	if !ok {
		return fmt.Errorf(".env: %s can't be set safely in this file; set it by hand", name)
	}
	*f = *nf
	return nil
}

// try writes q as name's value and checks the file reads back with the
// value written exactly as q and every other assignment where it was.
func (f *File) try(name, value, q string) (*File, bool) {
	var out []byte
	old := f.last(name)
	if old != nil {
		end := old.valEnd
		if old.value != value {
			end = afterComment(f.src, end)
		}
		out = slices.Concat(f.src[:old.valStart], []byte(q), f.src[end:])
	} else {
		eol := lineEnding(f.src)
		out = bytes.Clone(f.src)
		if len(out) > 0 && out[len(out)-1] != '\n' && out[len(out)-1] != '\r' {
			out = append(out, eol...)
			out = append(out, name+"="+q...)
		} else {
			out = append(out, name+"="+q+eol...)
		}
	}
	vars, err := parse(out)
	if err != nil || len(vars) != len(f.vars)+boolInt(old == nil) {
		return nil, false
	}
	nf := &File{src: out, vars: vars}
	v := nf.last(name)
	if v == nil || v.value != value || string(out[v.valStart:v.valEnd]) != q {
		return nil, false
	}
	return nf, true
}

// afterComment is the end of the line when only blanks and a # comment
// follow the value at end, else end. A comment about the old value
// ("# the production relay") would mislead next to the new one.
func afterComment(src []byte, end int) int {
	eol := len(src)
	if i := bytes.IndexAny(src[end:], "\r\n"); i >= 0 {
		eol = end + i
	}
	if bytes.HasPrefix(bytes.TrimLeft(src[end:eol], " \t"), []byte("#")) {
		return eol
	}
	return end
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Quote writes value for a .env file: as it is when it holds only
// letters, digits and _ . / : @ + -, else in double quotes with \\, \",
// \$, \n, \r, \t, \f and \v escapes. Other control characters can't be
// written.
func Quote(value string) (string, error) {
	plain := true
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c < 0x20 && c != '\n' && c != '\r' && c != '\t' && c != '\f' && c != '\v' || c == 0x7f {
			return "", fmt.Errorf("the value holds control character %#x, which a .env file can't carry", c)
		}
		if !isPlain(c) {
			plain = false
		}
	}
	if plain {
		return value, nil
	}
	return quoteAlways(value), nil
}

func isPlain(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.IndexByte("_./:@+-", c) >= 0
}

// quoteAlways double quotes value with escapes; Quote has checked it
// holds no control character other than the escaped ones.
func quoteAlways(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(value); i++ {
		c := value[i]
		switch c {
		case '\\', '"', '$':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\f':
			b.WriteString(`\f`)
		case '\v':
			b.WriteString(`\v`)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func validName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isKeyByte(s[i], i == 0) {
			return false
		}
	}
	return true
}

// lineEnding is the file's first line ending: "\r\n", "\r" or "\n".
func lineEnding(src []byte) string {
	i := bytes.IndexAny(src, "\r\n")
	switch {
	case i < 0, src[i] == '\n':
		return "\n"
	case i+1 < len(src) && src[i+1] == '\n':
		return "\r\n"
	}
	return "\r"
}
