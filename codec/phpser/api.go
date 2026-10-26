// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package phpser reads and rewrites PHP serialized data byte for byte:
// only the changed string leaves differ, with their lengths fixed.
//
// The walker mirrors PHP 8's unserialize grammar without building a tree:
// N; b: i: d: s: a: O: C: (kept opaque) E: r: and R:, with every length
// counted in bytes and checked against the bytes left before use. Objects
// are never instantiated; they are bytes. Two deliberate differences from
// PHP: bytes after the top value make the input invalid (PHP ignores
// them; such a field isn't one serialized value) and the escaped S:
// string form, which no PHP since 6 writes, is invalid.
//
// The exported names in this file are the frozen cross-lane API.
package phpser

import "errors"

// ErrInvalid is returned for bytes that aren't valid PHP serialized data.
var ErrInvalid = errors.New("not valid PHP serialized data")

// LeafFunc sees each string leaf (and each string key when isKey) at its
// path and returns the new bytes and whether they changed.
type LeafFunc func(path string, leaf []byte, isKey bool) (out []byte, changed bool, err error)

// Valid reports whether b is one complete PHP serialized value.
func Valid(b []byte) bool {
	var p parser
	return p.whole(b) == nil
}

// Rewrite returns b with each leaf fn changed, verbatim otherwise, with
// every string length fixed.
//
// fn sees every string value and every string key (isKey true) with its
// path: keys joined by '.', integer keys as [n], object properties by
// their visible name and a '.' inside a key as '\.'. Only a changed leaf
// gets a new s:<len>: prefix; every other byte is copied as it was. When
// nothing changed, out is b itself. Invalid input returns an error that
// wraps ErrInvalid; an error from fn is returned as it is.
func Rewrite(b []byte, fn LeafFunc) (out []byte, changed bool, err error) {
	p := parser{fn: fn}
	if err := p.whole(b); err != nil {
		return nil, false, err
	}
	if p.out == nil {
		return b, false, nil
	}
	return p.out, true, nil
}

// Leaves calls fn for every string leaf and key of b.
//
// It is the read-only walk of Rewrite, with the same paths. Leaves seen
// before an invalid byte have already been passed to fn when the error
// comes back.
func Leaves(b []byte, fn func(path string, leaf []byte, isKey bool)) error {
	p := parser{walk: fn}
	return p.whole(b)
}
