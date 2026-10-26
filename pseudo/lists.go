// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pseudo

import (
	"bytes"
	"embed"
	"net/netip"
	"strings"
	"sync"

	"github.com/RhythmShahriar/roadie/core"
)

// data holds the locale lists: data/<locale>/<list>.txt, one entry per
// line, plain ASCII so a latin1 table can take every fake.
//
//go:embed data
var data embed.FS

// Locales are the locales the lists cover, in a fixed order.
var Locales = []string{"en", "de", "fr", "es", "nl", "it"}

// The list files of a locale.
const (
	listFirst           = "first"
	listLast            = "last"
	listStreets         = "streets"
	listCities          = "cities"
	listCompanies       = "companies"
	listStreetSuffixes  = "street_suffixes"
	listCompanySuffixes = "company_suffixes"
)

// streetShape is how a locale writes a street address.
type streetShape int

const (
	shapeNumberFirst  streetShape = iota // en: 12 Oak Street
	shapeTypeFirst                       // fr: 12 Rue des Lilas
	shapeCompound                        // de, nl: Lindenstrasse 12
	shapeTypeNumberAt                    // es, it: Calle Mayor 12
)

// locale is one locale's lists.
type locale struct {
	first, last, streets, cities, companies []string
	streetSuffixes, companySuffixes         []string
	shape                                   streetShape
}

// shapes gives each locale the way its streets are written.
var shapes = map[string]streetShape{
	"en": shapeNumberFirst, "fr": shapeTypeFirst, "de": shapeCompound,
	"nl": shapeCompound, "es": shapeTypeNumberAt, "it": shapeTypeNumberAt,
}

// lists is every locale's lists and the lower-case lookup sets IsFake
// uses, all locales together, read from data on first use.
var lists struct {
	once     sync.Once
	byLocale map[string]*locale
	// The lookup sets, lower case, over every locale; read-only once built.
	first, last, streets, cities, companies set
	streetSuffixes, companySuffixes         []string // lower case
}

// readList returns the non-empty lines of one list file.
func readList(loc, name string) []string {
	b, err := data.ReadFile("data/" + loc + "/" + name + ".txt")
	if err != nil {
		panic("pseudo: missing list " + loc + "/" + name + ": " + err.Error()) // the lists are embedded; a test reads each one
	}
	var out []string
	for line := range bytes.SplitSeq(b, []byte("\n")) {
		if s := strings.TrimSpace(string(line)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// loadLists reads the embedded lists once.
func loadLists() {
	lists.once.Do(func() {
		lists.byLocale = map[string]*locale{}
		lists.first, lists.last, lists.streets = set{}, set{}, set{}
		lists.cities, lists.companies = set{}, set{}
		suffixSeen := map[string]bool{}
		companySeen := map[string]bool{}
		for _, loc := range Locales {
			l := &locale{
				first: readList(loc, listFirst), last: readList(loc, listLast),
				streets: readList(loc, listStreets), cities: readList(loc, listCities),
				companies:      readList(loc, listCompanies),
				streetSuffixes: readList(loc, listStreetSuffixes), companySuffixes: readList(loc, listCompanySuffixes),
				shape: shapes[loc],
			}
			lists.byLocale[loc] = l
			lists.first.add(l.first)
			lists.last.add(l.last)
			lists.streets.add(l.streets)
			lists.cities.add(l.cities)
			lists.companies.add(l.companies)
			for _, s := range l.streetSuffixes {
				if s = strings.ToLower(s); !suffixSeen[s] {
					suffixSeen[s] = true
					lists.streetSuffixes = append(lists.streetSuffixes, s)
				}
			}
			for _, s := range l.companySuffixes {
				if s = strings.ToLower(s); !companySeen[s] {
					companySeen[s] = true
					lists.companySuffixes = append(lists.companySuffixes, s)
				}
			}
		}
	})
}

// validLocale reports whether loc names a locale with lists.
func validLocale(loc string) bool {
	for _, l := range Locales {
		if l == loc {
			return true
		}
	}
	return false
}

// set is a lookup set of lower-case list entries.
type set map[string]struct{}

// add stores the lower case of every entry of vals.
func (s set) add(vals []string) {
	for _, v := range vals {
		s[strings.ToLower(v)] = struct{}{}
	}
}

// in reports whether the lower case of v is in s.
func in(s set, v string) bool {
	_, ok := s[strings.ToLower(v)]
	return ok
}

// Hex lengths of the formats.
const (
	hexLen          = 16 // email and login: 64 bits of the hash
	minUniqueHex    = 12 // the shortest a fake for a unique column gets
	urlHexLen       = 12
	minURLHex       = 4
	uniqueSuffixHex = 8 // the suffix a list kind gets in a unique column
	maxNicename     = 50
)

// userAgent is the one fake user agent.
const userAgent = "Mozilla/5.0 (Roadie staging copy)"

// isFake is IsFake.
func isFake(kind core.FakeKind, v []byte) bool {
	s := strings.TrimSpace(string(v))
	if s == "" {
		return false
	}
	switch kind {
	case core.KEmail:
		return isFakeEmail(s)
	case core.KUsername, core.KNicename:
		return isFakeLogin(s) || isFakeEmail(s)
	case core.KURL:
		return isFakeURL(s)
	case core.KFirstName, core.KLastName, core.KFullName:
		return isFakeName(kind, stripUniqueSuffix(s))
	case core.KStreet, core.KAddress:
		return isFakeStreet(stripUniqueSuffix(s))
	case core.KCity:
		loadLists()
		return in(lists.cities, stripUniqueSuffix(s))
	case core.KCompany:
		return isFakeCompany(stripUniqueSuffix(s))
	case core.KPhone:
		return core.FictionalPhone(s)
	case core.KIP:
		ip, err := netip.ParseAddr(s)
		return err == nil && core.DocumentationIP(ip)
	case core.KUserAgent:
		return s == userAgent
	}
	return false
}

// isHex reports whether s is lower-case hex of min to max characters.
func isHex(s string, minLen, maxLen int) bool {
	if len(s) < minLen || len(s) > maxLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// isFakeEmail reports whether s is u<12 to 16 hex>@<reserved domain>.
func isFakeEmail(s string) bool {
	at := strings.LastIndexByte(s, '@')
	if at < 0 || !strings.HasPrefix(s, "u") {
		return false
	}
	return isHex(s[1:at], minUniqueHex, hexLen) && core.ReservedEmailDomain(s[at+1:])
}

// isFakeLogin reports whether s is user_<12 to 16 hex>.
func isFakeLogin(s string) bool {
	rest, ok := strings.CutPrefix(s, "user_")
	return ok && isHex(rest, minUniqueHex, hexLen)
}

// isFakeURL reports whether s is https://u<4 to 12 hex>.example.invalid
// with or without the closing slash.
func isFakeURL(s string) bool {
	rest, ok := strings.CutPrefix(s, "https://u")
	if !ok {
		return false
	}
	rest = strings.TrimSuffix(rest, "/")
	h, ok := strings.CutSuffix(rest, ".example.invalid")
	return ok && isHex(h, minURLHex, urlHexLen)
}

// stripUniqueSuffix drops the " <8 hex>" a fake for a unique column of a
// list kind carries.
func stripUniqueSuffix(s string) string {
	if i := strings.LastIndexByte(s, ' '); i > 0 && isHex(s[i+1:], uniqueSuffixHex, uniqueSuffixHex) {
		return s[:i]
	}
	return s
}

// isFakeName reports whether every word of s is in the name lists.
func isFakeName(kind core.FakeKind, s string) bool {
	loadLists()
	words := strings.Fields(s)
	if len(words) == 0 {
		return false
	}
	for _, w := range words {
		switch {
		case kind == core.KFirstName && in(lists.first, w),
			kind == core.KLastName && in(lists.last, w),
			kind == core.KFullName && (in(lists.first, w) || in(lists.last, w)):
		default:
			return false
		}
	}
	return true
}

// cutNumber removes a house number of 1 to 3 digits from the start or the
// end of s.
func cutNumber(s string) (string, bool) {
	if i := strings.IndexByte(s, ' '); i > 0 && i <= 3 && allDigits(s[:i]) {
		return s[i+1:], true
	}
	if i := strings.LastIndexByte(s, ' '); i > 0 && len(s)-i-1 <= 3 && allDigits(s[i+1:]) {
		return s[:i], true
	}
	return s, false
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// isFakeStreet reports whether s is a street in one of the four shapes
// the locales write, with a name from any street list.
func isFakeStreet(s string) bool {
	loadLists()
	rest, ok := cutNumber(s)
	if !ok {
		return false
	}
	low := strings.ToLower(rest)
	for _, suf := range lists.streetSuffixes {
		if name, ok := strings.CutSuffix(low, " "+suf); ok && in(lists.streets, name) {
			return true // 12 Oak Street
		}
		if name, ok := strings.CutPrefix(low, suf+" "); ok && in(lists.streets, name) {
			return true // 12 Rue des Lilas, Calle Mayor 12
		}
		if name, ok := strings.CutSuffix(low, suf); ok && name != "" && in(lists.streets, name) {
			return true // Lindenstrasse 12
		}
	}
	return false
}

// isFakeCompany reports whether s is a company word and a company suffix.
func isFakeCompany(s string) bool {
	loadLists()
	low := strings.ToLower(s)
	for _, suf := range lists.companySuffixes {
		if word, ok := strings.CutSuffix(low, " "+suf); ok && in(lists.companies, word) {
			return true
		}
	}
	return false
}
