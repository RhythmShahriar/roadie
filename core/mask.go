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
