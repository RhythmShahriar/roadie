// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package phpser reads and rewrites PHP serialized data byte for byte:
// only the changed string leaves differ, with their lengths fixed.
//
// The exported names in this file are the frozen cross-lane API; the
// values lane replaces the bodies, which return core.ErrNotBuilt until then.
package phpser

import (
	"errors"

	"github.com/RhythmShahriar/roadie/core"
)

// ErrInvalid is returned for bytes that aren't valid PHP serialized data.
var ErrInvalid = errors.New("not valid PHP serialized data")

// LeafFunc sees each string leaf (and each string key when isKey) at its
// path and returns the new bytes and whether they changed.
type LeafFunc func(path string, leaf []byte, isKey bool) (out []byte, changed bool, err error)

// Valid reports whether b is one complete PHP serialized value.
func Valid(b []byte) bool {
	return false
}

// Rewrite returns b with each leaf fn changed, verbatim otherwise, with
// every string length fixed.
func Rewrite(b []byte, fn LeafFunc) (out []byte, changed bool, err error) {
	return nil, false, core.ErrNotBuilt
}

// Leaves calls fn for every string leaf and key of b.
func Leaves(b []byte, fn func(path string, leaf []byte, isKey bool)) error {
	return core.ErrNotBuilt
}
