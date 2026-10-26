// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package nested walks values that hold other formats: PHP serialized,
// JSON, base64 of serialized data and any nesting of those, within a depth
// and a work budget.
//
// A string leaf that is itself one of those formats is walked too, inside
// out, so the lengths of every level are right after a change: an email in
// JSON in a serialized value changes the JSON and both serialized lengths.
// Paths name each level by its format: "serialized:a.b",
// "json:items[2].email", "base64:serialized:login" and, chained,
// "serialized:data.json:email". Text has no prefix; a text value on its
// own is one leaf with the path "".
//
// The exported names in this file are the frozen cross-lane API.
package nested

// Options bounds the walk.
type Options struct {
	MaxDepth int   // most nested formats; 0 = the default
	Budget   int64 // most bytes decoded for one value; 0 = the default
	// Stats, when set, counts the values the walk treated as text for a
	// reason the caller may want to report.
	Stats *Stats
	// Key, when set, sees each string key of serialized data and each
	// object key of JSON in Rewrite, at the path of the array or object
	// that holds it, and may change it: a plugin may key its data by a
	// customer's address or phone. Leaves never calls it.
	Key func(path string, key []byte) ([]byte, bool)
}

// Stats counts values that were handed to fn whole, as text.
type Stats struct {
	TooDeep    int // nested deeper than MaxDepth
	OverBudget int // the walk ran out of Budget
	Broken     int // looked like serialized data but wasn't valid
}

// Rewrite returns b with each string leaf fn changed, keeping every outer
// format valid.
//
// fn sees the string values of serialized data and JSON (never keys;
// Options.Key sees those) and any text, each at its path. Bytes fn doesn't change are kept as they
// were: serialized lengths are fixed only for changed leaves and a changed
// JSON string is written back in the style of the document, with "\/"
// when it escaped slashes and \uXXXX when it escaped non-ASCII. When
// nothing changed the result is b itself. A value nested deeper than
// MaxDepth, or met after Budget bytes of work, is handed to fn as text.
// The error is always nil today; it is there for callers to keep checking.
func Rewrite(b []byte, fn func(path string, leaf []byte) ([]byte, bool), o Options) ([]byte, bool, error) {
	if fn == nil {
		return b, false, nil
	}
	w := newWalker(b, fn, o)
	w.key = o.Key
	out, changed := w.value(b, "", 1)
	return out, changed, nil
}

// Leaves calls fn for every string leaf of b.
//
// It sees the same leaves at the same paths as Rewrite.
func Leaves(b []byte, fn func(path string, leaf []byte), o Options) error {
	if fn == nil {
		return nil
	}
	w := newWalker(b, func(path string, leaf []byte) ([]byte, bool) {
		fn(path, leaf)
		return nil, false
	}, o)
	w.value(b, "", 1)
	return nil
}

// Format names the outer format of b: "serialized", "json",
// "base64-serialized" or "text".
//
// It tries them in that order. JSON must start with '{' or '[' after any
// white space and parse whole. Base64 must use only the standard alphabet,
// be a multiple of 4 and at least 12 bytes long and decode to valid
// serialized data, as Laravel session payloads do.
func Format(b []byte) string {
	f, _ := format(b)
	return f
}

func newWalker(b []byte, fn func(path string, leaf []byte) ([]byte, bool), o Options) *walker {
	w := &walker{fn: fn, maxDepth: o.MaxDepth, budget: o.Budget, stats: o.Stats}
	if w.maxDepth <= 0 {
		w.maxDepth = DefaultMaxDepth
	}
	if w.budget <= 0 {
		w.budget = DefaultBudgetFactor * int64(len(b))
	}
	return w
}
