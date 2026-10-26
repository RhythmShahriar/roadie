// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package detect

import (
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/roadie/core"
)

func TestEmailLinearOnAtSigns(t *testing.T) {
	v := repeatTo("@", mib)
	// 100 ms is several times what a linear scan needs and far below a
	// quadratic one (minutes), so a loaded runner under -race still passes.
	if d, ok := within(100*time.Millisecond, func() { Email().Scan(v, core.ScanContext{}, func(core.Hit) {}) }); !ok {
		t.Fatalf("1 MiB of @ took %v, want under %v", d, timeBound(100*time.Millisecond))
	}
}

func TestEmailMatchBounds(t *testing.T) {
	cases := map[string]string{
		"write to maría.lópez@correo.es today":        "maría.lópez@correo.es",
		"<ada@gmail.com>":                             "ada@gmail.com",
		"(ada@gmail.com),":                            "ada@gmail.com",
		"ada@gmail.com.":                              "ada@gmail.com",
		"x " + strings.Repeat("a", 64) + "@gmail.com": strings.Repeat("a", 64) + "@gmail.com",
	}
	for in, want := range cases {
		var got []string
		scanEmails([]byte(in), func(m emailMatch) { got = append(got, in[m.start:m.end]) })
		if len(got) != 1 || got[0] != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
	// A local part longer than 64 bytes is never cut to its last 64.
	long := strings.Repeat("a", 65) + "@gmail.com"
	scanEmails([]byte(long), func(m emailMatch) { t.Errorf("matched %q in an over-long address", long[m.start:m.end]) })
}

func TestWithAllow(t *testing.T) {
	if WithAllow(nil, Allow{Emails: []string{"*"}}) != nil {
		t.Error("WithAllow(nil) is not nil")
	}
	if d := Email(); WithAllow(d, Allow{}) != d {
		t.Error("an empty allow list wraps the detector")
	}
	d := WithAllow(Email(), Allow{Emails: []string{"*@shop.com", "[bad"}, KeptEmails: []string{"Kept@Gmail.com"}})
	if d.ID() != core.FindingPIIEmail {
		t.Errorf("ID %q", d.ID())
	}
	hits := scanAll(d, "a@shop.com b&#64;shop.com kept@gmail.com ada@gmail.com", core.ScanContext{})
	if len(hits) != 1 || hits[0].Masked != core.MaskEmail("ada@gmail.com") {
		t.Errorf("got %v, want only ada@gmail.com", hits)
	}
	// A kept user's row is skipped by every wrapped detector.
	row := core.ScanContext{Column: "notes", Row: func(c string) ([]byte, bool) {
		return []byte("kept@gmail.com"), c == "user_email"
	}}
	if h := scanAll(WithAllow(Email(), Allow{KeptEmails: []string{"kept@gmail.com"}}), "ada@gmail.com", row); len(h) != 0 {
		t.Errorf("kept row flagged: %v", h)
	}
}

func FuzzEmail(f *testing.F) {
	for _, s := range []string{
		"ada.kowalczyk@gmail.com", "ada&#64;gmail.com", "ada%40gmail.com", "maría.lópez@correo.es",
		"git@github.com:org/repo", "logo@2x.png", "@@@@", "a@b.c@d.ee", "noreply@github.com",
		"https://k@o1.ingest.sentry.io/2", "\xff@\xfe.com", strings.Repeat("a", 70) + "@x.com",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, v []byte) {
		start := time.Now()
		hits := scanAll(Email(), string(v), core.ScanContext{})
		if d := time.Since(start); d > timeBound(time.Second) {
			t.Fatalf("%d bytes took %v", len(v), d)
		}
		checkHits(t, v, hits)
		last := 0
		for _, h := range hits {
			if h.Start < last {
				t.Fatalf("hits overlap or go backwards")
			}
			last = h.End
			addr := decodeAt(string(v[h.Start:h.End]))
			if !strings.Contains(addr, "@") || strings.Contains(strings.ToLower(h.Masked), addr) {
				t.Fatalf("hit %q masked %q", addr, h.Masked)
			}
		}
	})
}
