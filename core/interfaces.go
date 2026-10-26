// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package core

import "time"

// FakeOpts constrains one fake.
type FakeOpts struct {
	MaxLen  int  // byte limit of the column; 0 = none
	Unique  bool // the column is unique, so fakes must not collide
	Country string
	Locale  string
}

// Faker turns a personal value into its fake.
type Faker interface {
	// Fake is deterministic for one key: the same kind and original give
	// the same fake in every table and encoding.
	Fake(kind FakeKind, original []byte, o FakeOpts) ([]byte, error)
	// Clone returns a faker for another worker with the same key.
	Clone() Faker
}

// Known is the set of production values Roadie replaced, for exact-mode verify.
type Known interface {
	// Add stores keyed hashes of the normalised value, its md5 hex and its sha256 hex.
	Add(kind FakeKind, original []byte)
	// Seal ends pass 1; Contains is valid after Seal.
	Seal()
	// Contains reports whether token is a known value and in which encoding:
	// "raw", "md5" or "sha256".
	Contains(token []byte) (kind FakeKind, encoding string, ok bool)
	Len() int
}

// Span is a personal value inside free text.
type Span struct {
	Start, End int
	Kind       FakeKind
}

// Spanner finds personal values in free text.
type Spanner interface {
	Spans(text []byte) []Span // non-overlapping, sorted
}

// ScanContext tells a detector where a value comes from.
type ScanContext struct {
	Table, Column string
	ColumnKind    FakeKind                           // from a covering rule, "" when none
	Row           func(column string) ([]byte, bool) // other values on the same row
}

// Hit is one match of a detector inside a value.
type Hit struct {
	ID         string
	Severity   Severity
	Start, End int
	Masked     string
	Encoding   string
}

// Detector looks for one kind of personal data or secret.
type Detector interface {
	ID() string
	// Scan runs in linear time and never retains v.
	Scan(v []byte, sc ScanContext, emit func(Hit))
}

// Progress receives progress from a long run.
type Progress interface {
	Phase(name string, total int64) // "index", "transform", "import", "verify"
	Advance(n int64)
	Table(name string, rows int64)
	Note(msg string)
}

// Clock returns the current time; tests pass a fixed one.
type Clock func() time.Time
