// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keys

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// Settings caps: a settings option is small, so anything deeper or
// larger is not one and is left alone.
const (
	maxSettingsDepth  = 32
	maxSettingsBytes  = 4 << 20
	maxSettingsLeaves = 10000
)

// Settings reads a PHP serialized or JSON settings value, such as
// woocommerce_stripe_settings, into a flat map from path to scalar text.
// Paths follow codec/nested without its format prefix: keys joined by
// '.', integer keys as [n] and a '.' inside a key written as `\.`, so
// "smtp.pass" and "connections[0].provider_settings.password". Unlike the
// string leaves codec/nested walks, booleans, integers and floats are
// read too ("1" and "0" for true and false), since gateway switches such
// as PayPal's sandbox_on are stored as booleans. Objects are read as
// arrays of their visible property names and never instantiated. ok is
// false for anything else, including invalid or oversized input.
func Settings(b []byte) (map[string]string, bool) {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || len(b) > maxSettingsBytes {
		return nil, false
	}
	out := map[string]string{}
	switch b[0] {
	case 'a', 'O':
		p := serReader{b: b, out: out}
		if !p.value("", 0) || p.pos != len(b) {
			return nil, false
		}
		return out, true
	case '{', '[':
		var v any
		d := json.NewDecoder(bytes.NewReader(b))
		d.UseNumber()
		if err := d.Decode(&v); err != nil || d.More() {
			return nil, false
		}
		if !flattenJSON(out, "", v, 0) {
			return nil, false
		}
		return out, true
	}
	return nil, false
}

// joinPath adds one key to a settings path the way codec/nested writes it.
func joinPath(parent, key string, index bool) string {
	if index {
		return parent + "[" + key + "]"
	}
	key = strings.ReplaceAll(key, ".", `\.`)
	if parent == "" {
		return key
	}
	return parent + "." + key
}

// flattenJSON adds the scalars of a decoded JSON value to out.
func flattenJSON(out map[string]string, path string, v any, depth int) bool {
	if depth > maxSettingsDepth || len(out) > maxSettingsLeaves {
		return false
	}
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if !flattenJSON(out, joinPath(path, k, false), x, depth+1) {
				return false
			}
		}
	case []any:
		for i, x := range t {
			if !flattenJSON(out, joinPath(path, strconv.Itoa(i), true), x, depth+1) {
				return false
			}
		}
	case string:
		out[path] = t
	case json.Number:
		out[path] = t.String()
	case bool:
		out[path] = boolText(t)
	case nil:
		out[path] = ""
	}
	return true
}

func boolText(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// serReader reads PHP serialized data. It checks every declared length
// against the bytes left before it uses it and allocates nothing in
// proportion to a declared count, so hostile input costs linear time.
type serReader struct {
	b   []byte
	pos int
	out map[string]string
}

// value reads one value at path into out.
func (p *serReader) value(path string, depth int) bool {
	if depth > maxSettingsDepth || len(p.out) > maxSettingsLeaves || p.pos+1 >= len(p.b) {
		return false
	}
	c := p.b[p.pos]
	if c == 'N' {
		if p.b[p.pos+1] != ';' {
			return false
		}
		p.pos += 2
		p.out[path] = ""
		return true
	}
	if p.b[p.pos+1] != ':' {
		return false
	}
	p.pos += 2
	switch c {
	case 'b', 'i', 'd':
		end := bytes.IndexByte(p.b[p.pos:], ';')
		if end < 0 {
			return false
		}
		text := string(p.b[p.pos : p.pos+end])
		if c == 'b' && text != "0" && text != "1" || text == "" {
			return false
		}
		p.out[path] = text
		p.pos += end + 1
		return true
	case 's':
		s, ok := p.str()
		if !ok || !p.lit(';') {
			return false
		}
		p.out[path] = s
		return true
	case 'a':
		return p.array(path, depth)
	case 'O':
		// O:<len>:"<class>":<n>:{...}: the class name is skipped.
		if _, ok := p.str(); !ok || !p.lit(':') {
			return false
		}
		return p.array(path, depth)
	}
	// C:, E:, r: and R: are not settings.
	return false
}

// array reads <n>:{key value ...}.
func (p *serReader) array(path string, depth int) bool {
	n, ok := p.uint()
	if !ok || !p.lit(':') || !p.lit('{') {
		return false
	}
	// Each entry takes at least four bytes ("i:0;" is half of one).
	if n > (len(p.b)-p.pos)/4 {
		return false
	}
	for range n {
		key, index, ok := p.key()
		if !ok || !p.value(joinPath(path, key, index), depth+1) {
			return false
		}
	}
	return p.lit('}')
}

// key reads an i: or s: key. An object property name loses its
// \0Class\0 or \0*\0 visibility prefix.
func (p *serReader) key() (string, bool, bool) {
	if p.pos+1 >= len(p.b) || p.b[p.pos+1] != ':' {
		return "", false, false
	}
	switch p.b[p.pos] {
	case 'i':
		p.pos += 2
		end := bytes.IndexByte(p.b[p.pos:], ';')
		if end <= 0 {
			return "", false, false
		}
		k := string(p.b[p.pos : p.pos+end])
		if _, err := strconv.ParseInt(k, 10, 64); err != nil {
			return "", false, false
		}
		p.pos += end + 1
		return k, true, true
	case 's':
		p.pos += 2
		k, ok := p.str()
		if !ok || !p.lit(';') {
			return "", false, false
		}
		if strings.HasPrefix(k, "\x00") {
			if i := strings.IndexByte(k[1:], 0); i >= 0 {
				k = k[i+2:]
			}
		}
		return k, false, true
	}
	return "", false, false
}

// str reads <len>:"<bytes>" after the type letter and colon.
func (p *serReader) str() (string, bool) {
	n, ok := p.uint()
	if !ok || !p.lit(':') || !p.lit('"') || n > len(p.b)-p.pos-1 {
		return "", false
	}
	s := string(p.b[p.pos : p.pos+n])
	p.pos += n
	return s, p.lit('"')
}

// uint reads a decimal count of at most nine digits.
func (p *serReader) uint() (int, bool) {
	start, n := p.pos, 0
	for p.pos < len(p.b) && p.b[p.pos] >= '0' && p.b[p.pos] <= '9' && p.pos-start < 9 {
		n = n*10 + int(p.b[p.pos]-'0')
		p.pos++
	}
	return n, p.pos > start
}

// lit consumes the byte c.
func (p *serReader) lit(c byte) bool {
	if p.pos < len(p.b) && p.b[p.pos] == c {
		p.pos++
		return true
	}
	return false
}

// Siblings returns the settings next to the leaf at path: the entries of
// the same parent, keyed by their last segment, then the top-level ones
// that the parent doesn't name. A key such as "testmode" or "host" is
// what Classify and the name rules read.
func Siblings(settings map[string]string, path string) map[string]string {
	if len(settings) == 0 {
		return nil
	}
	parent, _ := splitPath(path)
	out := map[string]string{}
	for p, v := range settings {
		pp, last := splitPath(p)
		if pp == parent {
			out[last] = v
		}
	}
	if parent != "" {
		for p, v := range settings {
			if pp, last := splitPath(p); pp == "" {
				if _, ok := out[last]; !ok {
					out[last] = v
				}
			}
		}
	}
	return out
}

// splitPath splits a path into its parent and last key: "smtp.pass"
// gives "smtp" and "pass", "a[2]" gives "a" and "2". An escaped dot
// stays in the key, unescaped.
func splitPath(path string) (parent, last string) {
	for i := len(path) - 1; i >= 0; i-- {
		switch path[i] {
		case '.':
			if i > 0 && path[i-1] == '\\' {
				continue
			}
			return path[:i], strings.ReplaceAll(path[i+1:], `\.`, ".")
		case '[':
			if strings.HasSuffix(path, "]") {
				return path[:i], path[i+1 : len(path)-1]
			}
		}
	}
	return "", strings.ReplaceAll(path, `\.`, ".")
}

// LeafKey returns the settings path and the last key of a codec/nested
// leaf path: "serialized:smtp.pass" gives "smtp.pass" and "pass"; a
// chained path keeps only its innermost format, the one whose siblings
// share an object with the leaf.
func LeafKey(nestedPath string) (path, key string) {
	path = nestedPath
	for _, f := range []string{"serialized:", "json:"} {
		if i := strings.LastIndex(path, f); i >= 0 {
			path = path[i+len(f):]
		}
	}
	_, key = splitPath(path)
	return path, key
}
