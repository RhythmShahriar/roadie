// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package pseudo makes the fake values Roadie puts in place of personal
// data: keyed, deterministic within a run, fitted to the column and always
// in a range verify never flags. It also keeps the set of replaced values
// for exact-mode verify and makes passwords, salts and keys for the copy.
//
// The exported names in this file are the frozen cross-lane API; the
// values lane replaces the bodies, which return core.ErrNotBuilt until then.
package pseudo

import "github.com/RhythmShahriar/roadie/core"

// Key is the 32-byte key fakes are derived from.
type Key [32]byte

// Options configures a Faker.
type Options struct {
	EmailDomain string // default example.invalid
	Locale      string // en, de, fr, es, nl, it
	PhoneStyle  string // echo, country
	CacheSize   int
}

// NewKey returns a key from crypto/rand.
func NewKey() (Key, error) {
	return Key{}, core.ErrNotBuilt
}

// ParseKey reads a key written as base64 of 32 bytes.
func ParseKey(s string) (Key, error) {
	return Key{}, core.ErrNotBuilt
}

// String returns the key as base64.
func (k Key) String() string {
	return ""
}

// Fingerprint returns "roadie-key:" and 12 base32 characters, safe to print.
func (k Key) Fingerprint() string {
	return ""
}

// NewFaker returns a Faker keyed by k.
func NewFaker(k Key, o Options) (core.Faker, error) {
	return nil, core.ErrNotBuilt
}

// NewKnown returns an empty known-value set keyed by k.
func NewKnown(k Key) core.Known {
	return nil
}

// Normalise puts a value in the form its fake is keyed on.
func Normalise(kind core.FakeKind, b []byte) []byte {
	return nil
}

// Scrub replaces the personal values sp finds in free text (and the known
// values) with their fakes; it returns the text and the count replaced.
func Scrub(text []byte, sp core.Spanner, known core.Known, f core.Faker) ([]byte, int) {
	return nil, 0
}

// ResetHash returns a password hash no password matches, for the stack.
func ResetHash(stack core.Stack) ([]byte, error) {
	return nil, core.ErrNotBuilt
}

// PasswordHash hashes password for the stack: phpass $P$ or bcrypt.
func PasswordHash(stack core.Stack, password []byte) ([]byte, error) {
	return nil, core.ErrNotBuilt
}

// IsFake reports whether v is a value Roadie's lists or formats produce.
func IsFake(kind core.FakeKind, v []byte) bool {
	return false
}

// Salt returns 64 printable characters from crypto/rand.
func Salt() string {
	return ""
}

// AppKey returns a Laravel APP_KEY: "base64:" and 32 random bytes.
func AppKey() string {
	return ""
}
