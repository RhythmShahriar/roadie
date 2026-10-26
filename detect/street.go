// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package detect

import (
	"unicode"
	"unicode/utf8"
)

// streetSuffixes end a street written as a number and a name: "12
// Kingsmead Road", "870 Bukovulir Close". An abbreviation (St, Rd)
// takes its dot along.
var streetSuffixes = map[string]bool{
	"Street": true, "St": true, "Road": true, "Rd": true, "Avenue": true, "Ave": true, "Lane": true, "Ln": true,
	"Drive": true, "Dr": true, "Close": true, "Way": true, "Court": true, "Ct": true, "Place": true, "Pl": true,
	"Terrace": true, "Crescent": true, "Grove": true, "Gardens": true, "Square": true, "Boulevard": true,
	"Blvd": true, "Hill": true, "Row": true, "Mews": true, "Walk": true, "Parade": true, "Gate": true,
	"Vale": true, "Wharf": true, "Highway": true, "Hwy": true, "Parkway": true, "Circle": true, "Trail": true,
	"Alley": true, "Strasse": true, "Straße": true, "Weg": true, "Gasse": true, "Allee": true,
}

// maxStreetWords is the most name words between the number and the
// suffix.
const maxStreetWords = 3

// scanStreets calls fn for each street in v written as a house number (1
// to 5 digits, a letter after it allowed), one to three capitalised name
// words and a street word: what a customer types into an order note or
// a form's message. A street in any other form isn't found.
func scanStreets(v []byte, fn func(start, end int)) {
	for i := 0; i < len(v); i++ {
		if !isASCIIDigit(v[i]) || i > 0 && (isASCIIDigit(v[i-1]) || isWordRune(runeBefore(v, i))) {
			continue
		}
		j := i
		for j < len(v) && isASCIIDigit(v[j]) {
			j++
		}
		if j-i > 5 {
			i = j
			continue
		}
		if j < len(v) && v[j] >= 'a' && v[j] <= 'z' && (j+1 == len(v) || v[j+1] == ' ') {
			j++ // 12a
		}
		if end, ok := streetAfter(v, j); ok {
			fn(i, end)
			i = end - 1
		}
	}
}

// streetAfter reads " Name [Name...] Suffix" from v[i] on and returns the
// end of the suffix.
func streetAfter(v []byte, i int) (int, bool) {
	words := 0
	for {
		if i >= len(v) || v[i] != ' ' {
			return 0, false
		}
		i++
		r, size := utf8.DecodeRune(v[i:])
		if !unicode.IsUpper(r) {
			return 0, false
		}
		j := i + size
		for j < len(v) {
			r, size := utf8.DecodeRune(v[j:])
			if !unicode.IsLetter(r) && r != '\'' && r != '-' {
				break
			}
			j += size
		}
		word := string(v[i:j])
		if words > 0 && streetSuffixes[word] {
			if j < len(v) && v[j] == '.' && len(word) <= 4 {
				j++
			}
			if isWordRune(runeAt(v, j)) {
				return 0, false
			}
			return j, true
		}
		words++
		if words > maxStreetWords {
			return 0, false
		}
		i = j
	}
}

func isASCIIDigit(c byte) bool { return c >= '0' && c <= '9' }
