// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package nested

import (
	"bytes"
	"encoding/base64"
	"encoding/json"

	"github.com/RhythmShahriar/roadie/codec/phpser"
)

// Format names: the values Format returns.
const (
	FormatSerialized       = "serialized"
	FormatJSON             = "json"
	FormatBase64Serialized = "base64-serialized"
	FormatText             = "text"
)

// Defaults for Options.
const (
	DefaultMaxDepth = 16 // format levels, the outer value being the first
	// DefaultBudgetFactor times the input size is the work one value may
	// take, in bytes scanned over every level.
	DefaultBudgetFactor = 64
)

// format is Format with the bytes it decoded for base64, so a walk
// doesn't decode twice.
func format(b []byte) (string, []byte) {
	if phpser.Valid(b) {
		return FormatSerialized, nil
	}
	if looksJSON(b) && json.Valid(b) {
		return FormatJSON, nil
	}
	if dec, ok := base64Serialized(b); ok {
		return FormatBase64Serialized, dec
	}
	return FormatText, nil
}

// looksJSON reports whether the first byte after white space opens an
// object or an array: a bare JSON string or number in a column is text.
func looksJSON(b []byte) bool {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		case '{', '[':
			return true
		}
		return false
	}
	return false
}

// base64Serialized decodes b when it is standard base64 (Laravel session
// payloads) of valid serialized data. Short values are left alone: under
// 12 bytes an ordinary word is often valid base64.
func base64Serialized(b []byte) ([]byte, bool) {
	if len(b) < 12 || len(b)%4 != 0 {
		return nil, false
	}
	for _, c := range b {
		if !base64Byte(c) {
			return nil, false
		}
	}
	dec := make([]byte, base64.StdEncoding.DecodedLen(len(b)))
	n, err := base64.StdEncoding.Decode(dec, b)
	if err != nil || !phpser.Valid(dec[:n]) {
		return nil, false
	}
	return dec[:n], true
}

func base64Byte(c byte) bool {
	return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '+' || c == '/' || c == '='
}

// looksSerialized is WordPress's is_serialized test: what a value that
// PHP once wrote looks like. A value that looks serialized but isn't
// valid is a broken one, which Stats counts.
func looksSerialized(b []byte) bool {
	if len(b) < 4 {
		return string(b) == "N;"
	}
	if b[1] != ':' || bytes.IndexByte([]byte("aOsbidCE"), b[0]) < 0 {
		return false
	}
	last := b[len(b)-1]
	return last == ';' || last == '}'
}

// walker carries one Rewrite or Leaves call.
type walker struct {
	fn       func(path string, leaf []byte) ([]byte, bool)
	key      func(path string, key []byte) ([]byte, bool)
	maxDepth int
	budget   int64
	stats    *Stats
}

// join chains a path inside a leaf to the leaf's own path.
func join(prefix, inner string) string {
	if prefix == "" {
		return inner
	}
	return prefix + "." + inner
}

// value walks b, which sits at path, depth formats deep.
func (w *walker) value(b []byte, path string, depth int) ([]byte, bool) {
	if depth > w.maxDepth {
		if w.stats != nil {
			w.stats.TooDeep++
		}
		return w.text(b, path)
	}
	// Detection and the walk each scan b once.
	w.budget -= 2 * int64(len(b))
	if w.budget < 0 {
		if w.stats != nil {
			w.stats.OverBudget++
		}
		return w.text(b, path)
	}
	f, dec := format(b)
	switch f {
	case FormatSerialized:
		return w.serialized(b, join(path, "serialized:"), depth)
	case FormatJSON:
		return w.json(b, join(path, "json:"), depth)
	case FormatBase64Serialized:
		out, changed := w.serialized(dec, join(path, "base64:serialized:"), depth)
		if !changed {
			return b, false
		}
		enc := make([]byte, base64.StdEncoding.EncodedLen(len(out)))
		base64.StdEncoding.Encode(enc, out)
		return enc, true
	}
	if w.stats != nil && looksSerialized(b) {
		w.stats.Broken++
	}
	return w.text(b, path)
}

// text hands b to fn whole.
func (w *walker) text(b []byte, path string) ([]byte, bool) {
	out, changed := w.fn(path, b)
	if !changed {
		return b, false
	}
	return out, true
}

// serialized walks the string values of valid serialized data; keys stay.
// tag is the path so far ending in "serialized:".
func (w *walker) serialized(b []byte, tag string, depth int) ([]byte, bool) {
	out, changed, err := phpser.Rewrite(b, func(p string, leaf []byte, isKey bool) ([]byte, bool, error) {
		if isKey {
			if w.key == nil {
				return nil, false, nil
			}
			// phpser's path for a key already ends in the key; Key gets
			// the path of the array that holds it, as JSON's keys do.
			n, ch := w.key(tag+containerOf(p), leaf)
			return n, ch, nil
		}
		n, ch := w.value(leaf, tag+p, depth+1)
		return n, ch, nil
	})
	if err != nil {
		// Unreachable: b was checked with phpser.Valid and the leaf
		// function never fails.
		return b, false
	}
	return out, changed
}

// containerOf drops the last key from a phpser path: "a.b" gives "a",
// "[3].bob" gives "[3]" and "jane@shop\.com" gives "". A dot after a
// backslash is part of a key.
func containerOf(p string) string {
	cut := 0
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '\\':
			i++
		case '.', '[':
			cut = i
		}
	}
	return p[:cut]
}
