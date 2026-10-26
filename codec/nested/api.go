// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package nested walks values that hold other formats: PHP serialized,
// JSON, base64 of serialized data and any nesting of those, within a depth
// and a work budget.
//
// The exported names in this file are the frozen cross-lane API; the
// values lane replaces the bodies, which return core.ErrNotBuilt until then.
package nested

import "github.com/RhythmShahriar/roadie/core"

// Options bounds the walk.
type Options struct {
	MaxDepth int   // most nested formats; 0 = the default
	Budget   int64 // most bytes decoded for one value; 0 = the default
}

// Rewrite returns b with each string leaf fn changed, keeping every outer
// format valid.
func Rewrite(b []byte, fn func(path string, leaf []byte) ([]byte, bool), o Options) ([]byte, bool, error) {
	return nil, false, core.ErrNotBuilt
}

// Leaves calls fn for every string leaf of b.
func Leaves(b []byte, fn func(path string, leaf []byte), o Options) error {
	return core.ErrNotBuilt
}

// Format names the outer format of b: "serialized", "json",
// "base64-serialized" or "text".
func Format(b []byte) string {
	return ""
}
