// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package detect

import (
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"github.com/RhythmShahriar/roadie/core"
)

// The scanners here read bytes forwards and backwards a bounded number of
// runes from each candidate, so every detector stays linear in its input.

// digitZeros are the code points of zero in the digit blocks the phone and
// card scanners read besides ASCII: Arabic-Indic, Extended Arabic-Indic,
// Devanagari, Bengali and fullwidth. A number written in any of them is
// still a number.
var digitZeros = []rune{0x0660, 0x06F0, 0x0966, 0x09E6, 0xFF10}

// digitAt returns the value of the decimal digit that starts at v[i] and
// its size in bytes, or -1 and the size of whatever rune is there.
func digitAt(v []byte, i int) (int, int) {
	c := v[i]
	if c >= '0' && c <= '9' {
		return int(c - '0'), 1
	}
	if c < utf8.RuneSelf {
		return -1, 1
	}
	r, n := utf8.DecodeRune(v[i:])
	for _, z := range digitZeros {
		if r >= z && r <= z+9 {
			return int(r - z), n
		}
	}
	return -1, n
}

// asciiDigit returns the ASCII digit of a value digitAt returned.
func asciiDigit(d int) byte { return "0123456789"[d] }

// runeBefore returns the rune that ends at v[i], or utf8.RuneError at the start.
func runeBefore(v []byte, i int) rune {
	if i <= 0 {
		return utf8.RuneError
	}
	r, _ := utf8.DecodeLastRune(v[:i])
	return r
}

// runeAt returns the rune that starts at v[i], or utf8.RuneError at the end.
func runeAt(v []byte, i int) rune {
	if i >= len(v) {
		return utf8.RuneError
	}
	r, _ := utf8.DecodeRune(v[i:])
	return r
}

// isDigitRune reports whether r is a decimal digit in any script digitAt reads.
func isDigitRune(r rune) bool {
	if r >= '0' && r <= '9' {
		return true
	}
	for _, z := range digitZeros {
		if r >= z && r <= z+9 {
			return true
		}
	}
	return false
}

// isWordRune reports whether r is a letter or digit, the runes a number
// must not touch on either side.
func isWordRune(r rune) bool {
	return r != utf8.RuneError && (unicode.IsLetter(r) || unicode.IsDigit(r) || isDigitRune(r))
}

// nameTokens splits a lower-cased column name or meta key into its parts:
// "_billing_address_1" gives billing, address, 1.
func nameTokens(name string) []string {
	return strings.FieldsFunc(name, func(r rune) bool {
		return r == '_' || r == '-' || r == '.' || r == ' ' || r == ':'
	})
}

// valueColumns are the columns of key/value tables whose meaning is the
// key on the same row.
var valueColumns = map[string]string{
	"meta_value":   "meta_key",
	"option_value": "option_name",
	"value":        "key",
}

// nameInfo is a column name or row key, lower-cased and split, with the
// answers of the column tests asked about it so far.
type nameInfo struct {
	lower  string
	tokens []string
	// memo holds two bits per test: asked, then the answer. Workers share
	// a nameInfo, so the bits are set atomically; two workers asking at
	// once both compute the same answer.
	memo atomic.Uint64
}

// The column tests memoised on a nameInfo. Each must depend only on the
// name and its tokens.
const (
	testPhone = iota
	testNotPhone
	testCard
	testIP
	testAddress
	testBusiness
)

// test returns fn(ni.lower, ni.tokens), computing it once per name.
func (ni *nameInfo) test(which int, fn func(name string, tokens []string) bool) bool {
	asked, yes := uint64(1)<<(2*which), uint64(1)<<(2*which+1)
	if m := ni.memo.Load(); m&asked != 0 {
		return m&yes != 0
	}
	v := fn(ni.lower, ni.tokens)
	bits := asked
	if v {
		bits |= yes
	}
	ni.memo.Or(bits)
	return v
}

// anyNameTest is anyName with fn memoised as test which.
func anyNameTest(sc core.ScanContext, which int, fn func(name string, tokens []string) bool) bool {
	return eachName(sc, func(ni *nameInfo) bool { return ni.test(which, fn) })
}

// maxNames caps the name cache. Option names are unique per row, so a
// big options table could fill it; the names past the cap are split on
// each use.
const maxNames = 8192

// The name cache holds nameInfo by the name as written. Every detector
// asks about the column of every value, so the split runs once per name
// rather than once per value. Readers look in an immutable snapshot
// without a lock; a new name goes to fresh under namesMu and is published
// in a new snapshot once fresh holds an eighth as many names as the
// snapshot, so copying stays linear in the number of names.
var (
	snapshot atomic.Pointer[map[string]*nameInfo]
	namesMu  sync.Mutex
	fresh    = map[string]*nameInfo{}
)

// cached returns the snapshot's nameInfo of name; m[string(b)] doesn't
// allocate.
func cached(b []byte) (*nameInfo, bool) {
	if m := snapshot.Load(); m != nil {
		ni, ok := (*m)[string(b)]
		return ni, ok
	}
	return nil, false
}

// infoOf returns the cached nameInfo of name, adding it on a miss.
func infoOf(b []byte) *nameInfo {
	if ni, ok := cached(b); ok {
		return ni
	}
	namesMu.Lock()
	defer namesMu.Unlock()
	if ni, ok := fresh[string(b)]; ok {
		return ni
	}
	lower := strings.ToLower(string(b))
	ni := &nameInfo{lower: lower, tokens: nameTokens(lower)}
	old := snapshot.Load()
	n := 0
	if old != nil {
		n = len(*old)
	}
	if n+len(fresh) >= maxNames {
		return ni
	}
	fresh[string(b)] = ni
	if len(fresh) > n/8 {
		next := make(map[string]*nameInfo, n+len(fresh))
		if old != nil {
			for k, v := range *old {
				next[k] = v
			}
		}
		for k, v := range fresh {
			next[k] = v
		}
		snapshot.Store(&next)
		fresh = map[string]*nameInfo{}
	}
	return ni
}

// infoOfString is infoOf for a string.
func infoOfString(s string) *nameInfo {
	if m := snapshot.Load(); m != nil {
		if ni, ok := (*m)[s]; ok {
			return ni
		}
	}
	return infoOf([]byte(s))
}

// anyName reports whether fn holds for one of the lower-cased names a
// value is known by: its column and, for the value column of a key/value
// table, the key on the same row, so wp_postmeta.meta_value with meta_key
// _billing_phone reads as a phone column.
func anyName(sc core.ScanContext, fn func(name string, tokens []string) bool) bool {
	return eachName(sc, func(ni *nameInfo) bool { return fn(ni.lower, ni.tokens) })
}

// eachName reports whether fn holds for the column or the row key, as
// anyName reads them.
func eachName(sc core.ScanContext, fn func(ni *nameInfo) bool) bool {
	col := infoOfString(sc.Column)
	if fn(col) {
		return true
	}
	if key, ok := valueColumns[col.lower]; ok && sc.Row != nil {
		if k, ok := sc.Row(key); ok && len(k) > 0 && len(k) <= 191 {
			return fn(infoOf(k))
		}
	}
	return false
}

// hasToken reports whether one of tokens is in set.
func hasToken(tokens []string, set ...string) bool {
	for _, t := range tokens {
		for _, s := range set {
			if t == s {
				return true
			}
		}
	}
	return false
}

// rowEmailColumns are the columns probed for the email of the row a value
// is on. ScanContext.Row answers for one named column, so the guards that
// look at the row's email ask for the usual names.
var rowEmailColumns = []string{
	"user_email", "email", "billing_email", "comment_author_email", "customer_email",
	"email_address", "user_email_address",
}

// rowEmails returns the emails on the row of sc, trimmed and lower-cased.
func rowEmails(sc core.ScanContext) []string {
	if sc.Row == nil {
		return nil
	}
	var out []string
	for _, c := range rowEmailColumns {
		if v, ok := sc.Row(c); ok {
			if s := strings.ToLower(strings.TrimSpace(string(v))); strings.Contains(s, "@") {
				out = append(out, s)
			}
		}
	}
	return out
}
