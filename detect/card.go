// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package detect

import (
	"strings"

	"github.com/RhythmShahriar/roadie/core"
)

// KCard is the span kind TextSpans gives a card number. It is not a fake
// kind: the scrubber writes a fixed placeholder in its place.
const KCard core.FakeKind = "card"

// cardColumn reports whether the value sits in a card column: a name part
// that contains "card" or equals pan or cc.
func cardColumn(sc core.ScanContext) bool {
	return anyNameTest(sc, testCard, func(_ string, tokens []string) bool {
		for _, t := range tokens {
			if t == "pan" || t == "cc" || strings.Contains(t, "card") {
				return true
			}
		}
		return false
	})
}

// cardMatch is one card-shaped number.
type cardMatch struct {
	start, end int
	digits     string
}

// cardWords are the words that say an ungrouped number in free text is
// a card: "paid with card 4789...", "Visa 4111...".
var cardWords = []string{"card", "visa", "mastercard", "amex", "maestro", "credit", "debit", "karte", "carte", "tarjeta", "kaart"}

// cardWordWindow is how far before a number a card word may stand.
const cardWordWindow = 40

// cardWordBefore reports whether a card word stands in the bytes just
// before v[i].
func cardWordBefore(v []byte, i int) bool {
	lo := max(0, i-cardWordWindow)
	before := strings.ToLower(string(v[lo:i]))
	for _, w := range cardWords {
		if strings.Contains(before, w) {
			return true
		}
	}
	return false
}

// scanCards calls fn for every Luhn-valid card number in v that is not a
// published test card: grouped like a card anywhere, any run of 13 to 19
// digits in a card column, or one in free text with a card word (card,
// Visa, credit) in the 40 bytes before it. Other ungrouped digits in
// free text are order IDs as often as cards and are left alone.
func scanCards(v []byte, cardCol bool, fn func(m cardMatch)) {
	if !hasDigit(v) {
		return
	}
	var buf [20]byte
	for i := 0; i < len(v); {
		d, size := digitAt(v, i)
		if d < 0 || isWordRune(runeBefore(v, i)) {
			i += size
			continue
		}
		end, n, grouped := readCard(v, i, &buf)
		if (grouped || cardCol || cardWordBefore(v, i)) && cardValid(buf[:n]) && !isWordRune(runeAt(v, end)) {
			fn(cardMatch{start: i, end: end, digits: string(buf[:n])})
		}
		i = max(end, i+size)
	}
}

// readCard reads digit groups joined by one space or one dash (the same
// one throughout) from v[i] on into digits, up to one digit more than a
// card has. It returns the end, the count of digits and whether the
// groups have a card's shape: fours with a shorter last group, or Amex
// 4-6-5 and Diners 4-6-4.
func readCard(v []byte, i int, digits *[20]byte) (int, int, bool) {
	var groups [20]int
	ng, n := 0, 0
	var sep byte
	end := i
	for i < len(v) && n < len(digits) {
		g := 0
		for i < len(v) && n < len(digits) {
			d, size := digitAt(v, i)
			if d < 0 {
				break
			}
			digits[n] = asciiDigit(d)
			n++
			i += size
			g++
		}
		if g == 0 {
			break
		}
		groups[ng] = g
		ng++
		end = i
		if i+1 < len(v) && (v[i] == ' ' || v[i] == '-') && (sep == 0 || v[i] == sep) && nextDigit(v, i+1) >= 0 {
			sep = v[i]
			i++
			continue
		}
		break
	}
	return end, n, cardGroups(groups[:ng])
}

// cardGroups reports whether group lengths are a card's.
func cardGroups(g []int) bool {
	switch {
	case len(g) == 3 && g[0] == 4 && g[1] == 6 && (g[2] == 5 || g[2] == 4):
		return true
	case len(g) < 4:
		return false
	}
	for _, n := range g[:len(g)-1] {
		if n != 4 {
			return false
		}
	}
	last := g[len(g)-1]
	return last >= 1 && last <= 4
}

// cardValid reports whether digits is 13 to 19 digits starting 2 to 6,
// passes the Luhn check and is not a published test card.
func cardValid(digits []byte) bool {
	if len(digits) < 13 || len(digits) > 19 || digits[0] < '2' || digits[0] > '6' {
		return false
	}
	return luhn(digits) && !core.TestCard(string(digits))
}

// luhn reports whether the ASCII digits pass the Luhn check.
func luhn(digits []byte) bool {
	sum := 0
	for i := len(digits) - 1; i >= 0; i-- {
		d := int(digits[i] - '0')
		if (len(digits)-1-i)%2 == 1 {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return sum%10 == 0
}

// cardDetector finds payment card numbers (pii.card).
type cardDetector struct{}

// ID returns pii.card.
func (cardDetector) ID() string { return core.FindingPIICard }

// Scan emits a hit for each card number.
func (cardDetector) Scan(v []byte, sc core.ScanContext, emit func(core.Hit)) {
	scanCards(v, cardColumn(sc), func(m cardMatch) {
		emit(core.Hit{ID: core.FindingPIICard, Severity: core.SeverityHigh, Start: m.start, End: m.end,
			Masked: core.MaskKey(m.digits), Encoding: "raw"})
	})
}
