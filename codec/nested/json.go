// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package nested

import (
	"strconv"
	"unicode/utf16"
	"unicode/utf8"
)

// jsonLeaf is one string value inside a JSON document.
type jsonLeaf struct {
	start, end int // the token, quotes included
	path       string
}

// jsonScan collects the string values of a JSON document already checked
// with json.Valid, plus the escaping style its strings use.
type jsonScan struct {
	b      []byte
	pos    int
	path   []byte
	leaves []jsonLeaf
	// The style a changed string is written back in: PHP's json_encode
	// writes "\/" and "é" by default; JSON_UNESCAPED_SLASHES and
	// JSON_UNESCAPED_UNICODE turn them off. One escape of either kind
	// anywhere in the document sets the style for all of it.
	slash, unicode bool
}

func (s *jsonScan) ws() {
	for s.pos < len(s.b) {
		switch s.b[s.pos] {
		case ' ', '\t', '\n', '\r':
			s.pos++
		default:
			return
		}
	}
}

func (s *jsonScan) value() {
	s.ws()
	if s.pos >= len(s.b) {
		return
	}
	switch s.b[s.pos] {
	case '{':
		s.pos++
		for {
			s.ws()
			if s.pos >= len(s.b) || s.b[s.pos] == '}' {
				s.pos++
				return
			}
			if s.b[s.pos] == ',' {
				s.pos++
				continue
			}
			start := s.pos
			s.str()
			parent := len(s.path)
			if parent > 0 {
				s.path = append(s.path, '.')
			}
			for _, c := range decodeJSON(s.b[start+1 : s.pos-1]) {
				if c == '.' {
					s.path = append(s.path, '\\')
				}
				s.path = append(s.path, c)
			}
			s.ws()
			s.pos++ // ':'
			s.value()
			s.path = s.path[:parent]
		}
	case '[':
		s.pos++
		for i := 0; ; i++ {
			s.ws()
			if s.pos >= len(s.b) || s.b[s.pos] == ']' {
				s.pos++
				return
			}
			if s.b[s.pos] == ',' {
				s.pos++
				i--
				continue
			}
			parent := len(s.path)
			s.path = append(s.path, '[')
			s.path = strconv.AppendInt(s.path, int64(i), 10)
			s.path = append(s.path, ']')
			s.value()
			s.path = s.path[:parent]
		}
	case '"':
		start := s.pos
		s.str()
		s.leaves = append(s.leaves, jsonLeaf{start: start, end: s.pos, path: string(s.path)})
	default:
		// a number, true, false or null
		for s.pos < len(s.b) {
			switch s.b[s.pos] {
			case ',', '}', ']', ' ', '\t', '\n', '\r':
				return
			}
			s.pos++
		}
	}
}

// str moves past one string token and notes its escapes.
func (s *jsonScan) str() {
	s.pos++
	for s.pos < len(s.b) {
		switch s.b[s.pos] {
		case '"':
			s.pos++
			return
		case '\\':
			if s.pos+1 < len(s.b) {
				switch s.b[s.pos+1] {
				case '/':
					s.slash = true
				case 'u':
					if r, ok := hex4(s.b[s.pos+2:]); ok && r >= 0x80 {
						s.unicode = true
					}
				}
			}
			s.pos += 2
		default:
			s.pos++
		}
	}
}

func hex4(b []byte) (rune, bool) {
	if len(b) < 4 {
		return 0, false
	}
	var r rune
	for _, c := range b[:4] {
		switch {
		case c >= '0' && c <= '9':
			r = r<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			r = r<<4 | rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			r = r<<4 | rune(c-'A'+10)
		default:
			return 0, false
		}
	}
	return r, true
}

// decodeJSON returns the bytes a JSON string body stands for. A body
// without escapes is returned as it is.
func decodeJSON(body []byte) []byte {
	i := 0
	for i < len(body) && body[i] != '\\' {
		i++
	}
	if i == len(body) {
		return body
	}
	out := make([]byte, 0, len(body))
	out = append(out, body[:i]...)
	for i < len(body) {
		c := body[i]
		if c != '\\' || i+1 >= len(body) {
			out = append(out, c)
			i++
			continue
		}
		e := body[i+1]
		i += 2
		switch e {
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
		case 'u':
			r, ok := hex4(body[i:])
			if !ok {
				out = append(out, '\\', 'u')
				continue
			}
			i += 4
			// A pair of \u escapes stands for one rune above U+FFFF; a
			// lone surrogate becomes U+FFFD.
			if utf16.IsSurrogate(r) && i+6 <= len(body) && body[i] == '\\' && body[i+1] == 'u' {
				if r2, ok := hex4(body[i+2:]); ok {
					if d := utf16.DecodeRune(r, r2); d != utf8.RuneError {
						r = d
						i += 6
					}
				}
			}
			out = utf8.AppendRune(out, r)
		default: // '"', '\\', '/'
			out = append(out, e)
		}
	}
	return out
}

// encodeJSON writes s as a JSON string body in the document's style.
// Bytes that aren't UTF-8 become U+FFFD, as every JSON encoder does.
func encodeJSON(dst, s []byte, slash, unicode bool) []byte {
	const hexd = "0123456789abcdef"
	for len(s) > 0 {
		c := s[0]
		if c < utf8.RuneSelf {
			switch {
			case c == '"' || c == '\\':
				dst = append(dst, '\\', c)
			case c == '/' && slash:
				dst = append(dst, '\\', '/')
			case c == '\n':
				dst = append(dst, '\\', 'n')
			case c == '\r':
				dst = append(dst, '\\', 'r')
			case c == '\t':
				dst = append(dst, '\\', 't')
			case c < 0x20:
				dst = append(dst, '\\', 'u', '0', '0', hexd[c>>4], hexd[c&0xf])
			default:
				dst = append(dst, c)
			}
			s = s[1:]
			continue
		}
		r, n := utf8.DecodeRune(s)
		if !unicode {
			dst = utf8.AppendRune(dst, r)
			s = s[n:]
			continue
		}
		if r > 0xffff {
			r1, r2 := utf16.EncodeRune(r)
			dst = appendU(dst, r1)
			dst = appendU(dst, r2)
		} else {
			dst = appendU(dst, r)
		}
		s = s[n:]
	}
	return dst
}

func appendU(dst []byte, r rune) []byte {
	const hexd = "0123456789abcdef"
	return append(dst, '\\', 'u', hexd[r>>12&0xf], hexd[r>>8&0xf], hexd[r>>4&0xf], hexd[r&0xf])
}

// json walks the string values of a JSON document; keys, numbers and
// every unchanged string keep their bytes. tag is the path so far ending
// in "json:".
func (w *walker) json(b []byte, tag string, depth int) ([]byte, bool) {
	s := jsonScan{b: b}
	s.value()
	var out []byte
	last := 0
	for _, l := range s.leaves {
		leaf := decodeJSON(b[l.start+1 : l.end-1])
		n, changed := w.value(leaf, tag+l.path, depth+1)
		if !changed {
			continue
		}
		if out == nil {
			out = make([]byte, 0, len(b)+len(n)+16)
		}
		out = append(out, b[last:l.start]...)
		out = append(out, '"')
		out = encodeJSON(out, n, s.slash, s.unicode)
		out = append(out, '"')
		last = l.end
	}
	if out == nil {
		return b, false
	}
	return append(out, b[last:]...), true
}
