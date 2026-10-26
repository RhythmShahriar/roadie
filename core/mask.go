// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package core

import (
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/rhythm/kit/secrets"
)

// The masks are the only way a data value reaches output (PRD 15.3). Each
// keeps at most two characters of any part and never shows how long the
// value was.

const bullets = "•••"

// keepRunes returns at most two leading runes of s and never more than
// half of it, so a short part is never shown whole.
func keepRunes(s string) string {
	r := []rune(s)
	return string(r[:min(2, len(r)/2)])
}

// MaskEmail keeps two leading characters of the local part, two of the
// domain name and the top-level domain: "anna@gmail.com" gives "an•••@gm•••.com".
func MaskEmail(s string) string {
	local, domain, ok := strings.Cut(strings.TrimSpace(s), "@")
	if !ok {
		return bullets
	}
	out := keepRunes(local) + bullets + "@"
	name, tld := domain, ""
	if i := strings.LastIndexByte(domain, '.'); i > 0 {
		name, tld = domain[:i], domain[i:]
	}
	if !isLetters(strings.TrimPrefix(tld, ".")) {
		tld = ""
	}
	return out + keepRunes(name) + bullets + tld
}

func isLetters(s string) bool {
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return s != "" && len(s) <= 24
}

// MaskPhone keeps only the country code: "+44 7700 900123" gives "+44 •••".
// A number without a leading + shows nothing.
func MaskPhone(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "+") {
		return bullets
	}
	var digits []byte
	for i := 1; i < len(s) && len(digits) < 15; i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			digits = append(digits, c)
		case c == ' ' || c == '-' || c == '.' || c == '(' || c == ')':
		default:
			i = len(s)
		}
	}
	n := countryCodeLen(string(digits))
	if n == 0 || n >= len(digits) {
		return bullets
	}
	return "+" + string(digits[:n]) + " " + bullets
}

// countryCodeLen returns the length of the ITU country code digits starts
// with: 1 and 7 are one digit, the zones below two, everything else three.
func countryCodeLen(digits string) int {
	switch {
	case digits == "":
		return 0
	case digits[0] == '1' || digits[0] == '7':
		return 1
	case len(digits) < 2:
		return 0
	case slices.Contains(twoDigitCodes, digits[:2]):
		return 2
	case len(digits) < 3:
		return 0
	}
	return 3
}

// twoDigitCodes are the two-digit country codes of ITU-T E.164 Annex.
var twoDigitCodes = []string{
	"20", "27", "30", "31", "32", "33", "34", "36", "39", "40", "41", "43", "44", "45",
	"46", "47", "48", "49", "51", "52", "53", "54", "55", "56", "57", "58", "60", "61",
	"62", "63", "64", "65", "66", "81", "82", "84", "86", "90", "91", "92", "93", "94",
	"95", "98",
}

// MaskName keeps the initial: "Jane Smith" gives "J•••".
func MaskName(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) < 2 {
		return bullets
	}
	return string(r[0]) + bullets
}

// MaskIP keeps the first octet of an IPv4 address or the first group of
// an IPv6 one: "81.2.69.160" gives "81.•••".
func MaskIP(s string) string {
	a, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return bullets
	}
	a = a.Unmap()
	if a.Is4() {
		return strconv.Itoa(int(a.As4()[0])) + "." + bullets
	}
	first, _, _ := strings.Cut(a.StringExpanded(), ":")
	if first = strings.TrimLeft(first, "0"); first == "" {
		first = "0"
	}
	return first + ":" + bullets
}

// MaskKey masks a key as kit secrets.Mask does with four leading
// characters at most: "sk_l•••••".
func MaskKey(s string) string { return secrets.Mask(s, 4) }

// MaskValue masks a value of the given kind.
func MaskValue(kind FakeKind, s string) string {
	switch kind {
	case KEmail:
		return MaskEmail(s)
	case KPhone:
		return MaskPhone(s)
	case KIP:
		return MaskIP(s)
	case KFirstName, KLastName, KFullName, KUsername, KNicename, KCompany, KCity, KStreet, KAddress:
		return MaskName(s)
	}
	if strings.Contains(s, "@") {
		return MaskEmail(s)
	}
	return bullets
}

// MaskRow builds Location.Row from a row's key columns: "ID=42" or
// "option_name=woocommerce_stripe_settings", sorted by column. Callers
// pass only primary keys or option_name/meta_key, never data. A key value
// that still looks like an email (some tables key on it) is masked.
// table names the table the row is in; it doesn't change the result yet
// and is there so the signature never needs to change when it does.
func MaskRow(table string, pk map[string]string) string {
	_ = table
	cols := make([]string, 0, len(pk))
	for c := range pk {
		cols = append(cols, c)
	}
	slices.Sort(cols)
	parts := make([]string, 0, len(cols))
	for _, c := range cols {
		v := pk[c]
		if strings.Contains(v, "@") {
			v = MaskEmail(v)
		}
		parts = append(parts, c+"="+v)
	}
	return strings.Join(parts, ",")
}

// pathFormats are the prefixes codec/nested gives each format level of a
// leaf path.
var pathFormats = []string{"serialized:", "json:", "base64:"}

// MaskPath masks every key of a leaf path that came from the data rather
// than from a program (PRD 15.3: a location holds row keys, never values).
// A plugin may key its data by an address, a name, a phone or a session
// hash: {"Jane Doe": {...}}. A key that reads as a program's name
// (letters, digits, _ and -, starting with a letter, short and with few
// digits) or a small number stays; an address is masked as MaskEmail
// masks it; any other key becomes <key>. "json:jane@shop\.com.name"
// gives "json:ja•••@sh•••.com.name" and "json:Jane Doe.phone" gives
// "json:<key>.phone".
func MaskPath(p string) string {
	if p == "" {
		return p
	}
	segs := splitPath(p)
	for i, s := range segs {
		prefix, key, idx := pathSegment(s)
		segs[i] = prefix + maskPathKey(key) + idx
	}
	return strings.Join(segs, ".")
}

// PathKeys returns the keys of a leaf path as the data holds them: no
// format prefixes or [n] indexes, a dot inside a key unescaped.
// "json:jane@shop\.com.items[2].email" gives jane@shop.com, items and
// email.
func PathKeys(p string) []string {
	var out []string
	for _, s := range splitPath(p) {
		if _, key, _ := pathSegment(s); key != "" {
			out = append(out, strings.ReplaceAll(key, `\.`, "."))
		}
	}
	return out
}

// pathSegment splits one segment of a leaf path into its format
// prefixes, its key (escaped as in the path) and its [n] indexes.
func pathSegment(s string) (prefix, key, idx string) {
	for again := true; again; {
		again = false
		for _, f := range pathFormats {
			if strings.HasPrefix(s, f) {
				prefix, s, again = prefix+f, s[len(f):], true
			}
		}
	}
	key = s
	for strings.HasSuffix(key, "]") {
		open := strings.LastIndexByte(key, '[')
		if open < 0 || !allDigits(key[open+1:len(key)-1]) {
			break
		}
		key, idx = key[:open], key[open:]+idx
	}
	return prefix, key, idx
}

// splitPath splits a leaf path at each dot that codec/nested didn't
// escape; a dot inside a key is written as \..
func splitPath(p string) []string {
	var out []string
	start := 0
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '\\':
			i++
		case '.':
			out = append(out, p[start:i])
			start = i + 1
		}
	}
	return append(out, p[start:])
}

// maskPathKey masks one key of a leaf path, as MaskPath says.
func maskPathKey(k string) string {
	plain := strings.ReplaceAll(k, `\.`, ".")
	switch {
	case plain == "":
		return k
	case allDigits(plain) && len(plain) <= 6:
		return k
	case programKey(plain):
		return k
	case strings.Count(plain, "@") == 1 && !strings.ContainsAny(plain, " \t"):
		return MaskEmail(plain)
	}
	return "<key>"
}

// programKey reports a key a program chose: an identifier of at most 40
// bytes with at most 4 digits, "billing_address_1" or "sandbox-on".
func programKey(k string) bool {
	if len(k) > 40 {
		return false
	}
	digits := 0
	for i := 0; i < len(k); i++ {
		switch c := k[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		case c >= '0' && c <= '9':
			if i == 0 {
				return false
			}
			digits++
		case c == '-' && i > 0:
		default:
			return false
		}
	}
	return digits <= 4
}

// allDigits reports a non-empty run of ASCII digits.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
