// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package pseudo makes the fake values Roadie puts in place of personal
// data: keyed, deterministic within a run, fitted to the column and always
// in a range verify never flags. It also keeps the set of replaced values
// for exact-mode verify and makes passwords, salts and keys for the copy.
//
// A fake is F(kind, HMAC-SHA256(key, kind || 0x00 || Normalise(kind,
// original))): the same person gets the same fake in every table and
// encoding of one run and in every run that uses the same key. Without
// the key a fake can't be linked back to its original; with the key a
// guess can be checked, which is why the copy is pseudonymised and not
// anonymised.
//
// The exported names in this file are the frozen cross-lane API.
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
	return newKey()
}

// ParseKey reads a key written as base64 of 32 bytes.
//
// Surrounding spaces and a trailing newline are ignored, so the content of
// a key file or ROADIE_KEY can be passed as it is. Anything else is an
// error with the code config that never quotes the value.
func ParseKey(s string) (Key, error) {
	return parseKey(s)
}

// String returns the key as base64.
func (k Key) String() string {
	return k.encode()
}

// Fingerprint returns "roadie-key:" and 12 base32 characters, safe to print.
//
// The characters are the first 12 of the lower-case, unpadded base32 of
// SHA-256(key), so the fingerprint names a key without revealing it.
func (k Key) Fingerprint() string {
	return k.fingerprint()
}

// NewFaker returns a Faker keyed by k.
//
// Empty options take their defaults: email domain example.invalid, locale
// en, phone style echo and a cache of 65,536 fakes. An email domain that
// isn't reserved (core.ReservedEmailDomain), an unknown locale or an
// unknown phone style is an error with the code config (exit 2).
//
// A Faker is for one goroutine; Clone gives each worker its own. Clones
// share the key and the per-run set of unique fakes, so a collision
// between two workers is still caught.
func NewFaker(k Key, o Options) (core.Faker, error) {
	return newFaker(k, o)
}

// NewKnown returns an empty known-value set keyed by k.
//
// It keeps 8-byte keyed hashes, never the values. It finds a value again
// in its raw (any case), md5 hex and sha256 hex forms. Names also store
// their words and word pairs, for the free-text scrubber.
func NewKnown(k Key) core.Known {
	return newKnown(k)
}

// Normalise puts a value in the form its fake is keyed on.
//
// Emails are trimmed and lower-cased; phones keep their digits and a
// leading '+'; names, streets, addresses, cities and companies are
// Unicode NFC, case-folded, trimmed and have runs of white space made one
// space; IPs are written in canonical form with IPv4-mapped IPv6 unmapped;
// other kinds are trimmed. The result never shares memory with b.
func Normalise(kind core.FakeKind, b []byte) []byte {
	return normalise(kind, b)
}

// Scrub replaces the personal values sp finds in free text (and the known
// values) with their fakes; it returns the text and the count replaced.
//
// Spans of kind email, phone, ip and the other fake kinds become the
// faker's fake of that kind; any other span (cards, IBANs) becomes
// "XXXX". An email span is first widened over the Unicode letters around
// it, so no part of an address stays behind. Spans that already hold a
// Roadie fake are left as they are. Then every word pair and word of the
// rest of the text that known reports as a name becomes a fake name with
// the same number of words. Text outside the replaced parts is copied
// byte for byte; with nothing to replace, text itself comes back. Names
// Roadie never saw in a structured column can't be found and stay.
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
//
// Emails, usernames, nicenames and URLs are checked by their format and
// reserved domain; names, streets, addresses, cities and companies by
// membership of any locale's lists, ignoring case; phones by
// core.FictionalPhone; IPs by core.DocumentationIP; the user agent by
// equality. Postcodes, dates of birth and text can't be told apart from
// real ones and always report false.
func IsFake(kind core.FakeKind, v []byte) bool {
	return isFake(kind, v)
}

// Salt returns 64 printable characters from crypto/rand.
func Salt() string {
	return ""
}

// AppKey returns a Laravel APP_KEY: "base64:" and 32 random bytes.
func AppKey() string {
	return ""
}
