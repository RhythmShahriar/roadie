// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package detect

import (
	"strings"
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

// columnNames returns the lower-cased names a value is known by: its
// column and, for the value column of a key/value table, the key on the
// same row, so wp_postmeta.meta_value with meta_key _billing_phone reads
// as a phone column.
func columnNames(sc core.ScanContext) []string {
	col := strings.ToLower(sc.Column)
	names := []string{col}
	if key, ok := valueColumns[col]; ok && sc.Row != nil {
		if k, ok := sc.Row(key); ok && len(k) > 0 && len(k) <= 191 {
			names = append(names, strings.ToLower(string(k)))
		}
	}
	return names
}

// anyName reports whether fn holds for one of the names a value is known by.
func anyName(sc core.ScanContext, fn func(name string, tokens []string) bool) bool {
	for _, n := range columnNames(sc) {
		if fn(n, nameTokens(n)) {
			return true
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
