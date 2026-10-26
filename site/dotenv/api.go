// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package dotenv reads and edits Laravel .env files, keeping every other
// byte, comment and line end as it was.
//
// It reads what Laravel's dotenv reads: KEY=value and export KEY=value,
// single quoted values (no escapes), double quoted ones (escapes and line
// breaks), unquoted ones ending at a " #" comment. ${OTHER} references
// are kept as written. For a key set twice the last one wins, as in Laravel.
package dotenv

import (
	"bytes"
	"slices"

	"github.com/RhythmShahriar/roadie/core"
)

// File is one parsed file.
type File struct {
	src  []byte
	vars []variable
}

// Parse reads b. It fails only on a quoted value that never ends, since
// an edit after it would land inside the value.
func Parse(b []byte) (*File, error) {
	src := bytes.Clone(b)
	vars, err := parse(src)
	if err != nil {
		return nil, err
	}
	return &File{src: src, vars: vars}, nil
}

// Pairs returns every assignment in file order, duplicates included.
func (f *File) Pairs() []core.Pair {
	out := make([]core.Pair, len(f.vars))
	for i, v := range f.vars {
		out[i] = v.pair()
	}
	return out
}

// Get returns the last assignment of name, the one Laravel keeps.
func (f *File) Get(name string) (core.Pair, bool) {
	if v := f.last(name); v != nil {
		return v.pair(), true
	}
	return core.Pair{}, false
}

// Set changes the last assignment of name in place, or appends
// NAME=value on a new line. A # comment after a value it changes goes
// too, since it described the old value. The value is written unquoted when it holds
// only letters, digits and _ . / : @ + -, else double quoted with escapes.
func (f *File) Set(name, value string) error {
	return f.set(name, value)
}

// Bytes returns the file, byte-identical to the input when nothing changed.
func (f *File) Bytes() []byte {
	return bytes.Clone(f.src)
}

// Unresolved names variables that refer to other variables (${OTHER} in
// an unquoted or double quoted value), in file order.
func (f *File) Unresolved() []string {
	var out []string
	for _, v := range f.vars {
		if l := f.last(v.name); l != nil && l.refs && !slices.Contains(out, v.name) {
			out = append(out, v.name)
		}
	}
	return out
}
