// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package dotenv reads and edits Laravel .env files, keeping every other
// byte, comment and line end as it was.
//
// The exported names in this file are the frozen cross-lane API; the site
// lane replaces the bodies, which return core.ErrNotBuilt until then.
package dotenv

import "github.com/RhythmShahriar/roadie/core"

// File is one parsed file.
type File struct{} // its fields are unexported and added by the owning lane

// Parse reads b.
func Parse(b []byte) (*File, error) {
	return nil, core.ErrNotBuilt
}

// Pairs returns every name and value in file order.
func (f *File) Pairs() []core.Pair {
	return nil
}

// Get returns one pair by name.
func (f *File) Get(name string) (core.Pair, bool) {
	return core.Pair{}, false
}

// Set changes a variable in place, or appends it.
func (f *File) Set(name, value string) error {
	return core.ErrNotBuilt
}

// Bytes returns the file, byte-identical to the input when nothing changed.
func (f *File) Bytes() []byte {
	return nil
}

// Unresolved names variables that refer to other variables.
func (f *File) Unresolved() []string {
	return nil
}
