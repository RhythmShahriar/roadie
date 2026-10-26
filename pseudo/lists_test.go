// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pseudo

import (
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
)

// The least each locale list holds (lane-values item 6).
var listMinimum = map[string]int{
	listFirst: 500, listLast: 500, listStreets: 200, listCities: 200, listCompanies: 100,
	listStreetSuffixes: 4, listCompanySuffixes: 3,
}

func TestListsWellFormed(t *testing.T) {
	for _, loc := range Locales {
		for name, minimum := range listMinimum {
			raw, err := data.ReadFile("data/" + loc + "/" + name + ".txt")
			if err != nil {
				t.Fatalf("%s/%s: %v", loc, name, err)
			}
			if strings.Contains(string(raw), "\r") || !strings.HasSuffix(string(raw), "\n") {
				t.Errorf("%s/%s: want LF line ends and a final newline", loc, name)
			}
			entries := readList(loc, name)
			if len(entries) < minimum {
				t.Errorf("%s/%s: %d entries, want at least %d", loc, name, len(entries), minimum)
			}
			seen := map[string]bool{}
			for _, e := range entries {
				for i := 0; i < len(e); i++ {
					if c := e[i]; c < 0x20 || c > 0x7e {
						t.Errorf("%s/%s: %q isn't printable ASCII", loc, name, e)
						break
					}
				}
				if e != strings.Join(strings.Fields(e), " ") {
					t.Errorf("%s/%s: %q has stray spaces", loc, name, e)
				}
				if seen[strings.ToLower(e)] {
					t.Errorf("%s/%s: %q twice", loc, name, e)
				}
				seen[strings.ToLower(e)] = true
				if (name == listFirst || name == listLast) && strings.Contains(e, " ") {
					t.Errorf("%s/%s: %q is more than one word", loc, name, e)
				}
			}
		}
	}
}

// A file per list and nothing else, so a stray file can't go unread.
func TestListFiles(t *testing.T) {
	dirs, err := data.ReadDir("data")
	if err != nil {
		t.Fatal(err)
	}
	if len(dirs) != len(Locales) {
		t.Errorf("data has %d locales, want %d", len(dirs), len(Locales))
	}
	for _, d := range dirs {
		if !validLocale(d.Name()) {
			t.Errorf("data/%s isn't a locale", d.Name())
		}
		files, err := data.ReadDir("data/" + d.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if _, ok := listMinimum[strings.TrimSuffix(f.Name(), ".txt")]; !ok {
				t.Errorf("data/%s/%s isn't a list", d.Name(), f.Name())
			}
		}
	}
}

func TestIsFakeOfRealValues(t *testing.T) {
	real := map[core.FakeKind][]string{
		core.KFullName:  {"Ada Kowalczyk", "Kowalczyk", "Jane Q. Public", "Zoë Ångström"},
		core.KFirstName: {"Kowalczyk", "Xyzzy", "J."},
		core.KLastName:  {"Kowalczyk", "Brzęczyszczykiewicz"},
		core.KStreet:    {"12 Baker Street", "Baker Street", "221B Baker Street", "12 Oak", "Oak Street", "1234 Oak Street", "12 Oak Boulevardx"},
		core.KAddress:   {"Flat 2, Real House", "12 Baker Street"},
		core.KCity:      {"Gotham", "Smallville"},
		core.KCompany:   {"Acme Widgets", "Acme Ltd", "Acorn Widgets"},
		core.KEmail: {"jane@gmail.com", "u0123@example.invalid", "u0123456789abcdef@gmail.com",
			"U0123456789ABCDEF@example.invalid", "x0123456789abcdef@example.invalid", "u0123456789abcdef0@example.invalid"},
		core.KUsername:  {"janedoe", "user_123", "user_0123456789abcdefg"},
		core.KURL:       {"https://jane.blog/", "https://u123.example.invalid/", "http://u0123456789ab.example.invalid/", "https://u0123456789ab.example.com/"},
		core.KPhone:     {"+44 7911 123456", "+1 212 555 0000", "+49 30 1234567"},
		core.KIP:        {"81.2.69.142", "8.8.8.8", "2a00:1450:4009:81f::200e", "not an ip"},
		core.KUserAgent: {"Mozilla/5.0 (X11; Linux x86_64)"},
		core.KPostcode:  {"SW1A 1AA"},
		core.KDOB:       {"1984-07-14"},
		core.KText:      {"hello"},
	}
	for kind, vals := range real {
		for _, v := range append(vals, "", "  ") {
			if IsFake(kind, []byte(v)) {
				t.Errorf("IsFake(%s, %q) = true for a real value", kind, v)
			}
		}
	}
	for kind, vals := range testutil.Canaries(3) {
		for _, v := range vals {
			if IsFake(kind, []byte(v)) {
				t.Errorf("IsFake(%s, canary %q) = true", kind, v)
			}
		}
	}
}

func TestIsFakeCaseAndShapes(t *testing.T) {
	yes := map[core.FakeKind][]string{
		core.KFullName: {"OLIVER SMITH", "oliver smith", " Oliver  Smith "},
		core.KStreet:   {"12 Oak Street", "12 oak street", "999 Rue des Lilas", "Lindenstrasse 4", "Kerkstraat 7", "Calle Mayor 12", "Via Roma 3"},
		core.KCompany:  {"Acorn Ltd", "Adler GmbH", "Anker en Zonen"},
		core.KCity:     {"YORK", "Lyon"},
		core.KEmail:    {"u0123456789abcdef@example.invalid", "u0123456789ab@staging.test"},
		core.KUsername: {"user_0123456789ab", "u0123456789abcdef@example.invalid"},
		core.KPhone:    {"+1 555 010 1234", "+44 7700 900123", "+15550101234"},
		core.KIP:       {"192.0.2.10", "2001:db8::1", "::ffff:198.51.100.7"},
	}
	for kind, vals := range yes {
		for _, v := range vals {
			if !IsFake(kind, []byte(v)) {
				t.Errorf("IsFake(%s, %q) = false", kind, v)
			}
		}
	}
	if !IsFake(core.KFullName, []byte("Oliver Smith 0a1b2c3d")) || IsFake(core.KFullName, []byte("Ada Kowalczyk 0a1b2c3d")) {
		t.Error("unique suffix handling")
	}
}
