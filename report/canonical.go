// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"unicode/utf8"
)

// Canonical returns the canonical JSON of v that signatures cover (PRD
// 16.4): v is marshaled, then written again with object keys sorted
// bytewise, no insignificant whitespace, no HTML escaping and strings as
// UTF-8 with only the escapes JSON requires. Every number must be an
// integer: a float anywhere is an error, as is a key that appears twice
// in one object. Masking happens before this point.
func Canonical(v any) ([]byte, error) {
	b, err := marshal(v)
	if err != nil {
		return nil, fmt.Errorf("canonical JSON: %w", err)
	}
	return canonicalJSON(b)
}

// maxDepth bounds how deeply a report may nest; Roadie's own nest five
// levels at most.
const maxDepth = 256

// canonicalJSON parses one JSON document and writes it canonically.
func canonicalJSON(b []byte) ([]byte, error) {
	v, err := parseJSON(b)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.Grow(len(b))
	writeCanonical(&out, v)
	return out.Bytes(), nil
}

// parseJSON reads exactly one JSON value, refusing invalid UTF-8,
// duplicate keys, non-integer numbers and anything after the value.
func parseJSON(b []byte) (any, error) {
	if !utf8.Valid(b) {
		return nil, errors.New("canonical JSON: not valid UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	v, err := parseValue(d, 0)
	if err != nil {
		return nil, fmt.Errorf("canonical JSON: %w", err)
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("canonical JSON: data after the value")
	}
	return v, nil
}

func parseValue(d *json.Decoder, depth int) (any, error) {
	if depth > maxDepth {
		return nil, errors.New("nested too deeply")
	}
	tok, err := d.Token()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.ErrUnexpectedEOF
		}
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := map[string]any{}
			for d.More() {
				kt, err := d.Token()
				if err != nil {
					return nil, err
				}
				k, ok := kt.(string)
				if !ok {
					return nil, errors.New("an object key isn't a string")
				}
				if _, dup := obj[k]; dup {
					return nil, fmt.Errorf("key %q appears twice in one object", k)
				}
				v, err := parseValue(d, depth+1)
				if err != nil {
					return nil, err
				}
				obj[k] = v
			}
			if _, err := d.Token(); err != nil { // the closing brace
				return nil, err
			}
			return obj, nil
		case '[':
			arr := []any{}
			for d.More() {
				v, err := parseValue(d, depth+1)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			if _, err := d.Token(); err != nil { // the closing bracket
				return nil, err
			}
			return arr, nil
		}
		return nil, fmt.Errorf("unexpected %v", t)
	case json.Number:
		n, err := strconv.ParseInt(t.String(), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("number %s isn't an integer Roadie writes", t)
		}
		return n, nil
	case string, bool, nil:
		return t, nil
	}
	return nil, fmt.Errorf("unexpected token %T", tok)
}

// writeCanonical writes a parsed value canonically.
func writeCanonical(b *bytes.Buffer, v any) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		slices.Sort(keys) // Go strings compare bytewise
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			writeString(b, k)
			b.WriteByte(':')
			writeCanonical(b, t[k])
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			writeCanonical(b, e)
		}
		b.WriteByte(']')
	case string:
		writeString(b, t)
	case int64:
		b.WriteString(strconv.FormatInt(t, 10))
	case bool:
		b.WriteString(strconv.FormatBool(t))
	case nil:
		b.WriteString("null")
	}
}

// writeString writes s with the escapes JSON requires and no others:
// the quote, the backslash and control characters. Everything else,
// "<", "&", U+2028 included, is written as its UTF-8 bytes.
func writeString(b *bytes.Buffer, s string) {
	const hex = "0123456789abcdef"
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			b.WriteString(`\"`)
		case c == '\\':
			b.WriteString(`\\`)
		case c == '\b':
			b.WriteString(`\b`)
		case c == '\f':
			b.WriteString(`\f`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		case c < 0x20:
			b.WriteString(`\u00`)
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0xf])
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
}
