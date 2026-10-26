// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package detect

import (
	"bytes"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RhythmShahriar/roadie/core"
)

// Phone numbers are found two ways. In a phone column any number of 7 to
// 15 digits counts. Everywhere else only strong forms count, so order
// totals, timestamps, EAN codes and tracking numbers in free text are left
// alone: a + and 8 to 15 digits, a tel: link, or a national format with
// separators for the twelve countries in nationalPatterns.

// phoneSeparators may sit between the digits of a number.
const phoneSeparators = " -.()"

// phoneTokens mark a phone column when a name part equals one of them; a
// part containing "phone" counts too.
var phoneTokens = []string{"tel", "tele", "telefon", "telefono", "telephone", "mobile", "mobil", "cell", "cellular", "fax"}

// notPhoneTokens mark a column whose numbers are codes, amounts or keys.
var notPhoneTokens = []string{
	"sku", "ean", "upc", "isbn", "gtin", "tracking", "total", "price", "amount", "order",
	"id", "timestamp", "postcode", "zip",
}

// phoneColumn reports whether the value sits in a phone column.
func phoneColumn(sc core.ScanContext) bool {
	if sc.ColumnKind == core.KPhone {
		return true
	}
	return anyNameTest(sc, testPhone, func(_ string, tokens []string) bool {
		for _, t := range tokens {
			if strings.Contains(t, "phone") {
				return true
			}
		}
		return hasToken(tokens, phoneTokens...)
	})
}

// notPhoneColumn reports whether the column holds codes or amounts. A
// column that also names a phone is a phone column: the name wins.
func notPhoneColumn(sc core.ScanContext) bool {
	return anyNameTest(sc, testNotPhone, func(_ string, tokens []string) bool { return hasToken(tokens, notPhoneTokens...) })
}

// phoneMatch is one number found.
type phoneMatch struct {
	start, end int
	digits     string // ASCII digits, a "(0)" trunk after the country code dropped
	plus       bool   // written with a leading +
	seps       bool   // has separators between digits
}

// readNumber reads digits and separators from v[i] on and returns where
// the number ends (trailing separators left out), its ASCII digits and
// whether separators sat between them. It stops after max+1 digits so a
// longer run is known to be too long.
func readNumber(v []byte, i, max int) (end int, digits []byte, seps bool) {
	end = i
	digits = make([]byte, 0, 16)
	pending := false
	for i < len(v) && len(digits) <= max {
		d, size := digitAt(v, i)
		switch {
		case d >= 0:
			digits = append(digits, asciiDigit(d))
			i += size
			end = i
			seps = seps || pending
		case strings.IndexByte(phoneSeparators, v[i]) >= 0:
			pending = len(digits) > 0
			i++
		default:
			return end, digits, seps
		}
	}
	return end, digits, seps
}

// dropTrunk removes the "(0)" a UK or German number may carry after its
// country code: "+44 (0)20" dials as +4420. Only a number written with +
// has a country code; one already over 15 digits stays too long.
func dropTrunk(v []byte, digits []byte, plus bool) []byte {
	if plus && len(digits) <= 15 && bytes.Contains(v, []byte("(0)")) && len(digits) > 3 {
		s := string(v)
		head := s[:strings.Index(s, "(0)")]
		n := 0
		for _, r := range head {
			if isDigitRune(r) {
				n++
			}
		}
		if n > 0 && n <= 3 && n < len(digits) && digits[n] == '0' {
			return append(digits[:n:n], digits[n+1:]...)
		}
	}
	return digits
}

// numberStart reports whether a number may start at v[i]: a digit, + or (
// with no letter, digit or + right before it.
func numberStart(v []byte, i int) bool {
	c := v[i]
	if c != '+' && c != '(' {
		if d, _ := digitAt(v, i); d < 0 {
			return false
		}
	}
	p := runeBefore(v, i)
	return !isWordRune(p) && p != '+'
}

// phoneDetector finds phone numbers (pii.phone).
type phoneDetector struct{}

// ID returns pii.phone.
func (phoneDetector) ID() string { return core.FindingPIIPhone }

// Scan emits a hit for each number that is not guarded.
func (phoneDetector) Scan(v []byte, sc core.ScanContext, emit func(core.Hit)) {
	phoneCol := phoneColumn(sc)
	if !phoneCol && notPhoneColumn(sc) {
		return
	}
	scanPhones(v, phoneCol, func(m phoneMatch) {
		emit(core.Hit{
			ID: core.FindingPIIPhone, Severity: core.SeverityHigh, Start: m.start, End: m.end,
			Masked: core.MaskPhone(string(v[m.start:m.end])), Encoding: "raw",
		})
	})
}

// scanPhones calls fn for each number in v that no guard rejects: any
// number of 7 to 15 digits in a phone column, strong forms only elsewhere.
func scanPhones(v []byte, phoneCol bool, fn func(m phoneMatch)) {
	if !hasDigit(v) {
		return
	}
	for i := 0; i < len(v); {
		m, ok, next := phoneAt(v, i, phoneCol)
		if ok && !phoneGuarded(v, m, phoneCol) {
			fn(m)
		}
		i = next
	}
}

// hasDigit is the fast path: most values hold no digit at all.
func hasDigit(v []byte) bool {
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c >= '0' && c <= '9' || c >= utf8.RuneSelf {
			return true
		}
	}
	return false
}

// phoneAt tries every form at v[i] and returns the match, whether there is
// one and where to look next.
func phoneAt(v []byte, i int, phoneCol bool) (phoneMatch, bool, int) {
	if (v[i] == 't' || v[i] == 'T') && i+4 < len(v) && strings.EqualFold(string(v[i:i+4]), "tel:") && !unicode.IsLetter(runeBefore(v, i)) {
		if m, ok := telLink(v, i+4); ok {
			return m, true, m.end
		}
		return phoneMatch{}, false, i + 4
	}
	if !numberStart(v, i) {
		_, size := utf8.DecodeRune(v[i:])
		return phoneMatch{}, false, i + size
	}
	if phoneCol {
		plus := v[i] == '+'
		j := i
		if plus {
			j++
		}
		end, digits, seps := readNumber(v, j, 15)
		if end == j {
			return phoneMatch{}, false, i + 1
		}
		digits = dropTrunk(v[i:end], digits, plus)
		if len(digits) >= 7 && len(digits) <= 15 && !isWordRune(runeAt(v, end)) {
			return phoneMatch{start: i, end: end, digits: string(digits), plus: plus, seps: seps}, true, end
		}
		return phoneMatch{}, false, end
	}
	if v[i] == '+' {
		if d := nextDigit(v, i+1); d >= 0 {
			end, digits, seps := readNumber(v, i+1, 15)
			digits = dropTrunk(v[i:end], digits, true)
			if len(digits) >= 8 && len(digits) <= 15 && !isWordRune(runeAt(v, end)) {
				return phoneMatch{start: i, end: end, digits: string(digits), plus: true, seps: seps}, true, end
			}
			if end > i+1 {
				return phoneMatch{}, false, end
			}
		}
		return phoneMatch{}, false, i + 1
	}
	if m, ok := nationalAt(v, i); ok {
		return m, true, m.end
	}
	// Skip the rest of this digit run: a number never starts inside one.
	j := i + 1
	for j < len(v) {
		d, size := digitAt(v, j)
		if d < 0 {
			break
		}
		j += size
	}
	return phoneMatch{}, false, j
}

// nextDigit returns the digit at v[i], or -1 when there is none.
func nextDigit(v []byte, i int) int {
	if i >= len(v) {
		return -1
	}
	d, _ := digitAt(v, i)
	return d
}

// telLink reads the number of a tel: link starting at v[i].
func telLink(v []byte, i int) (phoneMatch, bool) {
	start := i
	plus := i < len(v) && v[i] == '+'
	if plus {
		i++
	}
	end, digits, seps := readNumber(v, i, 15)
	digits = dropTrunk(v[start:end], digits, plus)
	if len(digits) < 7 || len(digits) > 15 || isWordRune(runeAt(v, end)) {
		return phoneMatch{}, false
	}
	return phoneMatch{start: start, end: end, digits: string(digits), plus: plus, seps: seps}, true
}

// nationalPatterns are the national formats with separators of the
// twelve countries PRD 15.2 names. In a pattern d is any digit, N is 2 to
// 9, S is 1 to 9, M is 6 to 9, x is an optional digit, other digits match
// themselves and every other byte is a literal separator. A match needs
// at least minNational digits.
var nationalPatterns = []string{
	// United States and Canada (NANP)
	"(Ndd) Ndd-dddd", "(Ndd)Ndd-dddd", "(Ndd) Ndd dddd", "Ndd-Ndd-dddd", "Ndd.Ndd.dddd",
	"Ndd Ndd dddd", "1-Ndd-Ndd-dddd", "1 (Ndd) Ndd-dddd", "1 Ndd Ndd dddd",
	// United Kingdom
	"07ddd dddddd", "07ddd ddd ddd", "0Sd dddd dddd", "0Sdd ddd dddd", "0Sddd dddddd",
	"(0Sd) dddd dddd", "(0Sdd) ddd dddd", "(0Sddd) dddddd",
	// Germany
	"0Sxxx dddxxxxx", "0Sxxx/dddxxxxx", "0Sxxx-dddxxxxx", "(0Sxxx) dddxxxxx",
	// France
	"0S dd dd dd dd", "0S.dd.dd.dd.dd", "0S-dd-dd-dd-dd",
	// Spain
	"Mdd ddd ddd", "Mdd dd dd dd",
	// Italy
	"3dd ddd dddd", "3dd dddddxx", "0Sx dddd dddx", "0Sx ddddddxx",
	// Netherlands
	"06-dddddddd", "06 dddddddd", "06 dd dd dd dd", "0Sd-ddddddd", "0Sd ddd dddd",
	"0Sdd-dddddd", "0Sdd dddddd",
	// Australia
	"04dd ddd ddd", "(0S) dddd dddd", "0S dddd dddd",
	// India
	"Mdddd ddddd", "Mdddd-ddddd",
	// Brazil
	"(Sd) ddddd-dddd", "(Sd) dddd-dddd", "Sd ddddd-dddd",
	// Ireland
	"08d ddd dddd", "(0S) ddd dddd", "0S ddd dddd", "(0Sd) ddd dddd", "0Sd ddd dddd",
	// New Zealand
	"02d ddd dddd", "02d ddd ddd",
}

// minNational is the fewest digits a national format may have, so short
// codes and dates never pass for a number.
const minNational = 9

// nationalAt returns the longest national format that matches at v[i].
func nationalAt(v []byte, i int) (phoneMatch, bool) {
	if !digitsAhead(v, i, minNational) {
		return phoneMatch{}, false
	}
	var buf, best [16]byte
	bestEnd, bestN := -1, 0
	first := v[i]
	if d, _ := digitAt(v, i); d >= 0 {
		first = asciiDigit(d)
	}
	for _, p := range patternsByFirst[first] {
		end, n, ok := matchPattern(v, i, p, &buf)
		if ok && n >= minNational && end > bestEnd && !isWordRune(runeAt(v, end)) {
			bestEnd, bestN, best = end, n, buf
		}
	}
	if bestEnd < 0 {
		return phoneMatch{}, false
	}
	return phoneMatch{start: i, end: bestEnd, digits: string(best[:bestN]), seps: true}, true
}

// patternsByFirst lists, for each byte a number can start with, the
// national patterns that can match there.
var patternsByFirst = func() (m [256][]string) {
	for _, c := range []byte("(0123456789") {
		for _, p := range nationalPatterns {
			if firstFits(p[0], c) {
				m[c] = append(m[c], p)
			}
		}
	}
	return m
}()

// digitsAhead reports whether at least n digits follow v[i] before
// anything but a digit or a separator, within the longest pattern's reach.
// A pattern opens and closes at most one bracket, so a second "(" or ")"
// ends the number: on text like "(0(0(0" the patterns are then never
// tried at every digit.
func digitsAhead(v []byte, i, n int) bool {
	got, opened, closed := 0, false, false
	for end := min(len(v), i+20); i < end && got < n; {
		if c := v[i]; c < utf8.RuneSelf {
			switch {
			case c >= '0' && c <= '9':
				got++
			case c == '(' && opened, c == ')' && closed:
				return false
			case !aheadSeparator[c]:
				return false
			}
			opened, closed = opened || c == '(', closed || c == ')'
			i++
			continue
		}
		d, size := digitAt(v, i)
		if d < 0 {
			return false
		}
		got++
		i += size
	}
	return got >= n
}

// aheadSeparator marks the separators digitsAhead reads over.
var aheadSeparator = [256]bool{' ': true, '-': true, '.': true, '(': true, ')': true, '/': true}

// firstFits reports whether pattern byte p can match c, the first byte
// at the start (a digit already turned to ASCII), so most patterns are
// passed over without running them.
func firstFits(p, c byte) bool {
	switch p {
	case 'd', 'x':
		return c >= '0' && c <= '9'
	case 'N':
		return c >= '2' && c <= '9'
	case 'S':
		return c >= '1' && c <= '9'
	case 'M':
		return c >= '6' && c <= '9'
	}
	return p == c
}

// matchPattern matches pattern p at v[i] and returns the end and the
// count of ASCII digits it wrote to digits. Optional digits are greedy, which is right because
// one is only ever followed by a separator or the end of the pattern.
func matchPattern(v []byte, i int, p string, digits *[16]byte) (int, int, bool) {
	n := 0
	for k := 0; k < len(p); k++ {
		c := p[k]
		var d, size int
		if i < len(v) {
			d, size = digitAt(v, i)
		} else {
			d = -1
		}
		ok := false
		switch {
		case c == 'd':
			ok = d >= 0
		case c == 'N':
			ok = d >= 2
		case c == 'S':
			ok = d >= 1
		case c == 'M':
			ok = d >= 6
		case c == 'x':
			if d < 0 {
				continue
			}
			ok = true
		case c >= '0' && c <= '9':
			ok = d == int(c-'0')
		default:
			if i < len(v) && v[i] == c {
				i++
				continue
			}
			return 0, 0, false
		}
		if !ok || n == len(digits) {
			return 0, 0, false
		}
		digits[n] = asciiDigit(d)
		n++
		i += size
	}
	return i, n, true
}

// The guards. Each looks a bounded distance around the match.

// hexReach is how far the hex guard looks on each side of a match.
const hexReach = 80

// phoneGuarded applies the false-alarm guards of PRD 15.2 to a match.
func phoneGuarded(v []byte, m phoneMatch, phoneCol bool) bool {
	text := string(v[m.start:m.end])
	switch {
	case fictional(m):
		return true
	case nextToMoney(v, m.start, m.end):
		return true
	case decimalNumber(text):
		return true
	case versionString(v, m.start, text):
		return true
	case inHexRun(v, m.start, m.end):
		return true
	case partOfCode(v, m.start, m.end):
		return true
	case !phoneCol && !m.plus && !m.seps && unixSeconds(m.digits):
		return true
	}
	return false
}

// fictional reports whether the number is in a range kept for fiction, read
// as E.164 and, without a +, as a national number of the NANP, the UK and
// Germany.
func fictional(m phoneMatch) bool {
	d := m.digits
	if m.plus {
		return fictionalE164(d)
	}
	cands := []string{d}
	switch {
	case strings.HasPrefix(d, "00"):
		cands = append(cands, d[2:])
	case strings.HasPrefix(d, "0"):
		cands = append(cands, "44"+d[1:], "49"+d[1:])
	case len(d) == 10:
		cands = append(cands, "1"+d)
	case len(d) == 7 && strings.HasPrefix(d, "55501"):
		return true // 555-01xx without an area code
	}
	for _, c := range cands {
		if fictionalE164(c) {
			return true
		}
	}
	return false
}

// fictionalE164 reports whether d, E.164 digits without the +, is in a
// range kept for fiction (core.FictionalPhone).
func fictionalE164(d string) bool { return core.FictionalPhone(d) }

// nextToMoney reports whether a currency sign (one space between allowed)
// or a decimal part touches the match.
func nextToMoney(v []byte, start, end int) bool {
	before, after := runeBefore(v, start), runeAt(v, end)
	if unicode.Is(unicode.Sc, before) || unicode.Is(unicode.Sc, after) {
		return true
	}
	if before == ' ' && unicode.Is(unicode.Sc, runeBefore(v, start-1)) ||
		after == ' ' && unicode.Is(unicode.Sc, runeAt(v, end+1)) {
		return true
	}
	if (after == '.' || after == ',') && end+1 < len(v) {
		if d, _ := digitAt(v, end+1); d >= 0 {
			return true
		}
	}
	if (before == '.' || before == ',') && isDigitRune(runeBefore(v, start-1)) {
		return true
	}
	return false
}

// decimalNumber reports whether text is digits with one decimal point and
// one or two decimals, an amount such as 1234567.89.
func decimalNumber(text string) bool {
	whole, frac, ok := strings.Cut(text, ".")
	if !ok || len(frac) == 0 || len(frac) > 2 || strings.ContainsAny(whole, " -()+.") {
		return false
	}
	return allDigits(whole) && allDigits(frac)
}

// allDigits reports whether s is one or more digits of any script.
func allDigits(s string) bool {
	for _, r := range s {
		if !isDigitRune(r) {
			return false
		}
	}
	return s != ""
}

// versionString reports whether text is digits and two or more dots that
// read as a version (1.12.3, v10.4.2) or an IPv4 address.
func versionString(v []byte, start int, text string) bool {
	groups := strings.Split(text, ".")
	if len(groups) < 3 {
		return false
	}
	for _, g := range groups {
		if !allDigits(g) {
			return false
		}
	}
	if b := runeBefore(v, start); b == 'v' || b == 'V' {
		return true
	}
	if len(groups) == 4 {
		ip := true
		for _, g := range groups {
			n, err := strconv.Atoi(g)
			ip = ip && err == nil && len(g) <= 3 && n <= 255
		}
		if ip {
			return true
		}
	}
	first := groups[0]
	return len(first) <= 2 && (first == "0" || first[0] != '0')
}

// inHexRun reports whether the match is part of a UUID or of a run of 32
// or more hex digits (a hash or a token).
func inHexRun(v []byte, start, end int) bool {
	lo := start
	for lo > 0 && start-lo < hexReach && isHexOrDash(v[lo-1]) {
		lo--
	}
	hi := end
	for hi < len(v) && hi-end < hexReach && isHexOrDash(v[hi]) {
		hi++
	}
	if start-lo >= hexReach || hi-end >= hexReach {
		return true
	}
	// Separators inside the match other than dashes break the run.
	for _, c := range v[start:end] {
		if !isHexOrDash(c) {
			return false
		}
	}
	run := v[lo:hi]
	if isUUID(run) {
		return true
	}
	n := 0
	for _, c := range run {
		if c != '-' {
			n++
		}
	}
	return n >= 32
}

func isHexOrDash(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' || c == '-'
}

// isUUID reports whether b is 8-4-4-4-12 hex digits.
func isUUID(b []byte) bool {
	if len(b) != 36 {
		return false
	}
	for i, c := range b {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if c == '-' {
			return false
		}
	}
	return true
}

// partOfCode reports whether the match is glued to letters or digits by a
// joining character, as in SKU-123-456-7890 or 2024/0123/456789.
func partOfCode(v []byte, start, end int) bool {
	joins := func(r rune) bool { return r == '-' || r == '_' || r == '/' || r == '#' }
	if b := runeBefore(v, start); joins(b) {
		if _, size := utf8.DecodeLastRune(v[:start]); isWordRune(runeBefore(v, start-size)) {
			return true
		}
	}
	if a := runeAt(v, end); joins(a) {
		_, size := utf8.DecodeRune(v[end:])
		if isWordRune(runeAt(v, end+size)) {
			return true
		}
	}
	return false
}

// unixSeconds reports whether ten digits read as a Unix time between
// 2001-01-01 and 2100-01-01.
func unixSeconds(d string) bool {
	if len(d) != 10 {
		return false
	}
	n, err := strconv.ParseInt(d, 10, 64)
	return err == nil && n >= 978307200 && n < 4102444800
}
