// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package detect

import (
	"github.com/RhythmShahriar/roadie/core"
)

// ibanLengths is the length of an IBAN in each country of the SWIFT IBAN
// registry (swift.com/standards/data-standards/iban). Knowing the exact
// length means an IBAN followed by more letters or digits is never read
// too long or too short.
var ibanLengths = map[string]int{
	"AD": 24, "AE": 23, "AL": 28, "AT": 20, "AZ": 28, "BA": 20, "BE": 16, "BG": 22,
	"BH": 22, "BI": 27, "BR": 29, "BY": 28, "CH": 21, "CR": 22, "CY": 28, "CZ": 24,
	"DE": 22, "DJ": 27, "DK": 18, "DO": 28, "EE": 20, "EG": 29, "ES": 24, "FI": 18,
	"FK": 18, "FO": 18, "FR": 27, "GB": 22, "GE": 22, "GI": 23, "GL": 18, "GR": 27,
	"GT": 28, "HR": 21, "HU": 28, "IE": 22, "IL": 23, "IQ": 23, "IS": 26, "IT": 27,
	"JO": 30, "KW": 30, "KZ": 20, "LB": 28, "LC": 32, "LI": 21, "LT": 20, "LU": 20,
	"LV": 21, "LY": 25, "MC": 27, "MD": 24, "ME": 22, "MK": 19, "MN": 20, "MR": 27,
	"MT": 31, "MU": 30, "NI": 28, "NL": 18, "NO": 15, "OM": 23, "PK": 24, "PL": 28,
	"PS": 29, "QA": 29, "RO": 24, "RS": 22, "RU": 33, "SA": 24, "SC": 31, "SD": 18,
	"SE": 24, "SI": 19, "SK": 24, "SM": 27, "SO": 23, "ST": 25, "SV": 28, "TL": 23,
	"TN": 24, "TR": 26, "UA": 29, "VA": 22, "VG": 24, "XK": 20, "YE": 30,
}

func isUpper(c byte) bool { return c >= 'A' && c <= 'Z' }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// ibanAt reads an IBAN of a registry country at v[i], in the electronic
// form (no spaces) or the printed one (groups of four joined by single
// spaces); it returns its end and its characters.
func ibanAt(v []byte, i int) (int, []byte, bool) {
	if i+4 > len(v) || !isUpper(v[i]) || !isUpper(v[i+1]) || !isDigit(v[i+2]) || !isDigit(v[i+3]) {
		return 0, nil, false
	}
	want, ok := ibanLengths[string(v[i:i+2])]
	if !ok {
		return 0, nil, false
	}
	out := make([]byte, 0, want)
	j := i
	for j < len(v) && len(out) < want {
		c := v[j]
		switch {
		case isUpper(c) || isDigit(c):
			out = append(out, c)
			j++
		case c == ' ' && len(out)%4 == 0 && j+1 < len(v) && (isUpper(v[j+1]) || isDigit(v[j+1])):
			j++
		default:
			return 0, nil, false
		}
	}
	if len(out) != want || j < len(v) && (isWordRune(runeAt(v, j))) {
		return 0, nil, false
	}
	return j, out, true
}

// ibanValid reports whether the ISO 13616 mod-97 check of iban holds.
func ibanValid(iban []byte) bool {
	rem := 0
	add := func(c byte) {
		if isDigit(c) {
			rem = (rem*10 + int(c-'0')) % 97
			return
		}
		n := int(c-'A') + 10
		rem = (rem*100 + n) % 97
	}
	for _, c := range iban[4:] {
		add(c)
	}
	for _, c := range iban[:4] {
		add(c)
	}
	return rem == 1
}

// ibanDetector finds IBANs (pii.iban).
type ibanDetector struct{}

// ID returns pii.iban.
func (ibanDetector) ID() string { return core.FindingPIIIBAN }

// Scan emits a hit for each valid IBAN that is not a documented example.
func (ibanDetector) Scan(v []byte, _ core.ScanContext, emit func(core.Hit)) {
	for i := 0; i+4 <= len(v); i++ {
		if !isUpper(v[i]) || isWordRune(runeBefore(v, i)) {
			continue
		}
		end, iban, ok := ibanAt(v, i)
		if !ok || !ibanValid(iban) || core.ExampleIBAN(string(iban)) {
			continue
		}
		emit(core.Hit{ID: core.FindingPIIIBAN, Severity: core.SeverityMedium, Start: i, End: end,
			Masked: core.MaskKey(string(iban)), Encoding: "raw"})
		i = end - 1
	}
}
