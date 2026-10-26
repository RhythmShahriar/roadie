// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package wpconfig reads and edits wp-config.php without running it: every
// define() is found by a PHP tokenizer and an edit changes only its bytes.
//
// The exported names in this file are the frozen cross-lane API; the site
// lane replaces the bodies, which return core.ErrNotBuilt until then.
package wpconfig

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

// Set changes a define in place, or inserts it where PRD 13.1 says.
func (f *File) Set(name, value string) error {
	return core.ErrNotBuilt
}

// Bytes returns the file, byte-identical to the input when nothing changed.
func (f *File) Bytes() []byte {
	return nil
}

// Unresolved names the defines set from getenv(), $_ENV or expressions.
func (f *File) Unresolved() []string {
	return nil
}

// TablePrefix returns $table_prefix.
func (f *File) TablePrefix() string {
	return ""
}

// DB returns the database settings when every one is a literal.
func (f *File) DB() (name, user, password, host string, ok bool) {
	return "", "", "", "", false
}
