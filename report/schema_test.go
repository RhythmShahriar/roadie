// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// validator checks a JSON document against the subset of JSON Schema
// 2020-12 the published schemas use: type, required, properties, items,
// enum, additionalProperties, anyOf, $ref to "#/$defs/...", minimum,
// maximum and pattern. It keeps the module free of a schema dependency;
// anything else in a schema is a test failure, so the subset can't drift
// silently. It is Tuner's validator.
type validator struct {
	root map[string]any
	errs []string
	// strict treats an object schema with properties and no
	// additionalProperties as closed. The published schemas stay open so
	// a release that adds a field doesn't break other people's
	// validators; the tests stay strict so the schema can't fall behind
	// the writer.
	strict bool
}

var supported = []string{"$schema", "$id", "$defs", "title", "description", "format",
	"type", "required", "properties", "items", "enum", "additionalProperties", "anyOf", "$ref",
	"minimum", "maximum", "pattern", "minItems"}

func loadSchema(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return s
}

// validate returns every way doc breaks schema, or nil.
func validate(schema map[string]any, doc []byte) []string { return validateWith(schema, doc, false) }

func validateWith(schema map[string]any, doc []byte, strict bool) []string {
	var v any
	d := json.NewDecoder(bytes.NewReader(doc))
	d.UseNumber()
	if err := d.Decode(&v); err != nil {
		return []string{err.Error()}
	}
	val := &validator{root: schema, strict: strict}
	val.check(schema, v, "$")
	return val.errs
}

func (val *validator) fail(path, format string, a ...any) {
	val.errs = append(val.errs, path+": "+fmt.Sprintf(format, a...))
}

func (val *validator) check(s map[string]any, v any, path string) {
	for k := range s {
		if !slices.Contains(supported, k) {
			val.fail(path, "schema keyword %q is outside the tested subset", k)
		}
	}
	if ref, ok := s["$ref"].(string); ok {
		name, ok := strings.CutPrefix(ref, "#/$defs/")
		def, _ := val.root["$defs"].(map[string]any)[name].(map[string]any)
		if !ok || def == nil {
			val.fail(path, "unresolved $ref %q", ref)
			return
		}
		val.check(def, v, path)
	}
	if alts, ok := s["anyOf"].([]any); ok {
		var all []string
		matched := false
		for _, alt := range alts {
			sub := &validator{root: val.root, strict: val.strict}
			sub.check(alt.(map[string]any), v, path)
			if len(sub.errs) == 0 {
				matched = true
				break
			}
			all = append(all, sub.errs...)
		}
		if !matched {
			val.fail(path, "matches no anyOf branch: %s", strings.Join(all, "; "))
		}
	}
	if t, ok := s["type"]; ok && !typeMatches(t, v) {
		val.fail(path, "want type %v, got %s", t, jsonType(v))
		return
	}
	if enum, ok := s["enum"].([]any); ok && !slices.ContainsFunc(enum, func(e any) bool { return fmt.Sprint(e) == fmt.Sprint(v) }) {
		val.fail(path, "%v is not one of %v", v, enum)
	}
	if n, ok := v.(json.Number); ok {
		f, _ := n.Float64()
		if lo, ok := s["minimum"].(float64); ok && f < lo {
			val.fail(path, "%v is below %v", n, lo)
		}
		if hi, ok := s["maximum"].(float64); ok && f > hi {
			val.fail(path, "%v is above %v", n, hi)
		}
	}
	if p, ok := s["pattern"].(string); ok {
		if str, ok := v.(string); ok && !regexp.MustCompile(p).MatchString(str) {
			val.fail(path, "%q does not match %s", str, p)
		}
	}
	switch v := v.(type) {
	case map[string]any:
		for _, r := range asStrings(s["required"]) {
			if _, ok := v[r]; !ok {
				val.fail(path, "missing required %q", r)
			}
		}
		props, _ := s["properties"].(map[string]any)
		for k, sub := range v {
			if ps, ok := props[k].(map[string]any); ok {
				val.check(ps, sub, path+"."+k)
				continue
			}
			extra, ok := s["additionalProperties"]
			if !ok && val.strict && props != nil {
				extra = false
			}
			switch extra := extra.(type) {
			case bool:
				if !extra {
					val.fail(path, "unexpected property %q", k)
				}
			case map[string]any:
				val.check(extra, sub, path+"."+k)
			}
		}
	case []any:
		if lo, ok := s["minItems"].(float64); ok && float64(len(v)) < lo {
			val.fail(path, "want at least %v items, got %d", lo, len(v))
		}
		if items, ok := s["items"].(map[string]any); ok {
			for i, e := range v {
				val.check(items, e, fmt.Sprintf("%s[%d]", path, i))
			}
		}
	}
}

func asStrings(v any) []string {
	var out []string
	for _, e := range asSlice(v) {
		out = append(out, e.(string))
	}
	return out
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func typeMatches(t, v any) bool {
	if ts, ok := t.([]any); ok {
		return slices.ContainsFunc(ts, func(t any) bool { return typeMatches(t, v) })
	}
	got := jsonType(v)
	if t == "number" && got == "integer" {
		return true
	}
	return t == got
}

func jsonType(v any) string {
	switch v := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case json.Number:
		if f, err := v.Float64(); err == nil && f == math.Trunc(f) && !strings.ContainsAny(v.String(), ".eE") {
			return "integer"
		}
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", v)
}
