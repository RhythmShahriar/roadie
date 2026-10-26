// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pseudo

import (
	"bytes"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
)

// needleSpanner adds fixed needles of a kind to the emails
// testutil.FakeSpanner finds, standing in for detect.TextSpans.
type needleSpanner struct {
	needles map[string]core.FakeKind
}

func (n needleSpanner) Spans(text []byte) []core.Span {
	spans := testutil.FakeSpanner().Spans(text)
	for needle, kind := range n.needles {
		for off := 0; ; {
			i := bytes.Index(text[off:], []byte(needle))
			if i < 0 {
				break
			}
			spans = append(spans, core.Span{Start: off + i, End: off + i + len(needle), Kind: kind})
			off += i + len(needle)
		}
	}
	return spans
}

// The order-note case of lane-values item 8: an email, a phone, a known
// full name and a name Roadie never saw. The unknown name stays: free
// text holds names no pattern can find; Roadie only knows the names
// it saw in structured columns.
func TestScrubOrderNote(t *testing.T) {
	c := testutil.Canaries(31)
	email, phone, name := c[core.KEmail][0], c[core.KPhone][0], c[core.KFullName][0]
	unknown := c[core.KFullName][1]
	note := "Order note: " + name + " (" + email + ", " + phone + ") asked to leave it with " + unknown + ". Card 4000 0000 0000 0002 on file, ip 81.2.69.142."
	sp := needleSpanner{map[string]core.FakeKind{phone: core.KPhone, "4000 0000 0000 0002": "card", "81.2.69.142": core.KIP}}
	for _, kc := range []struct {
		name  string
		known core.Known
	}{{"FakeKnown", testutil.FakeKnown()}, {"Known", NewKnown(testKey(8))}} {
		t.Run(kc.name, func(t *testing.T) {
			kc.known.Add(core.KFullName, []byte(name))
			kc.known.Add(core.KEmail, []byte(email))
			kc.known.Seal()
			f := mustFaker(t, testKey(8), Options{})
			got, n := Scrub([]byte(note), sp, kc.known, f)
			want := "Order note: " + fake(t, f, core.KFullName, strings.ToLower(name), core.FakeOpts{}) +
				" (" + fake(t, f, core.KEmail, email, core.FakeOpts{}) + ", " + fake(t, f, core.KPhone, phone, core.FakeOpts{}) +
				") asked to leave it with " + unknown + ". Card XXXX on file, ip " + fake(t, f, core.KIP, "81.2.69.142", core.FakeOpts{}) + "."
			if string(got) != want || n != 5 {
				t.Fatalf("Scrub =\n %s (%d)\nwant\n %s (5)", got, n, want)
			}
			for _, leak := range []string{email, phone, name, "4000 0000"} {
				if strings.Contains(string(got), leak) {
					t.Errorf("%q survived", leak)
				}
			}
			if !strings.Contains(string(got), unknown) {
				t.Error("the unknown name was expected to stay (documented limit)")
			}
			again, n2 := Scrub(got, sp, kc.known, f)
			if !bytes.Equal(again, got) || n2 != 0 {
				t.Errorf("not idempotent: %s (%d)", again, n2)
			}
		})
	}
}

func TestScrubUnicodeEmail(t *testing.T) {
	f := mustFaker(t, testKey(8), Options{})
	in := "Escribe a maría.lópez@correo.example. Gracias, José."
	got, n := Scrub([]byte(in), testutil.FakeSpanner(), nil, f)
	want := "Escribe a " + fake(t, f, core.KEmail, "maría.lópez@correo.example", core.FakeOpts{}) + ". Gracias, José."
	if string(got) != want || n != 1 {
		t.Fatalf("Scrub = %q (%d), want %q", got, n, want)
	}
	for _, part := range []string{"maría", "lópez", "pez@", "correo", "ía."} {
		if strings.Contains(string(got), part) {
			t.Errorf("part of the address survived: %q in %q", part, got)
		}
	}
	for _, s := range []string{"jöhn.dœ@exämple-shop.de", "user+tag@例え.jp", "ali@mañana.es."} {
		out, _ := Scrub([]byte("x "+s+" y"), testutil.FakeSpanner(), nil, f)
		if strings.ContainsAny(string(out), "öœäñ例え") || !strings.HasPrefix(string(out), "x u") {
			t.Errorf("%q became %q", s, out)
		}
	}
}

func TestScrubNamesAndPairs(t *testing.T) {
	k := NewKnown(testKey(8))
	k.Add(core.KFullName, []byte("Zoë Ångström"))
	k.Add(core.KFirstName, []byte("Bartholomew"))
	k.Add(core.KLastName, []byte("O'Sullivan"))
	k.Add(core.KFirstName, []byte("Jean-Luc"))
	k.Add(core.KFullName, []byte("Jean Luc"))
	k.Add(core.KEmail, []byte("someone@shop.com"))
	k.Seal()
	f := mustFaker(t, testKey(8), Options{})
	cases := []struct {
		in    string
		words int // words in the replacement
		n     int
		stays []string
	}{
		{"Called ZOË ÅNGSTRÖM today.", 2, 1, []string{"Called ", " today."}},
		{"Called Ångström today.", 1, 1, []string{"Called ", " today."}},
		{"Ask bartholomew", 1, 1, []string{"Ask "}},
		{"Mr O'Sullivan rang", 1, 1, []string{"Mr ", " rang"}}, // joined by an apostrophe: one name word
		{"Ask Jean-Luc O'Sullivan", 1, 2, []string{"Ask "}},
		{"Ask jean luc", 2, 1, []string{"Ask "}},
		{"someone wrote", 0, 0, []string{"someone wrote"}}, // emails aren't names
		{"no names here at all", 0, 0, []string{"no names here at all"}},
	}
	for _, c := range cases {
		got, n := Scrub([]byte(c.in), nil, k, f)
		if n != c.n {
			t.Errorf("Scrub(%q) = %q, %d replacements, want %d", c.in, got, n, c.n)
		}
		for _, s := range c.stays {
			if !strings.Contains(string(got), s) {
				t.Errorf("Scrub(%q) = %q lost %q", c.in, got, s)
			}
		}
		if c.words > 0 {
			stays := strings.Join(c.stays, "")
			if fakeWords := len(strings.Fields(string(got))) - len(strings.Fields(stays)); fakeWords != c.words*c.n {
				t.Errorf("Scrub(%q) = %q: %d fake words, want %d", c.in, got, fakeWords, c.words*c.n)
			}
		}
		if c.n == 0 && string(got) != c.in {
			t.Errorf("Scrub(%q) changed text with nothing known: %q", c.in, got)
		}
	}
	got, _ := Scrub([]byte("Called Zoë Ångström today."), nil, k, f)
	if name := strings.TrimSuffix(strings.TrimPrefix(string(got), "Called "), " today."); len(strings.Fields(name)) != 2 || !IsFake(core.KFullName, []byte(name)) {
		t.Errorf("pair became %q", got)
	}
}

func TestScrubEdgeCases(t *testing.T) {
	plain := []byte("mail jane at the shop now")
	if out, n := Scrub(plain, nil, nil, nil); &out[0] != &plain[0] || n != 0 {
		t.Error("nothing to do should return the input itself")
	}
	in := []byte("mail jane@shop.com now")
	if out, n := Scrub(in, nil, nil, nil); string(out) != "mail XXXX now" || n != 1 {
		t.Errorf("no spanner, no faker = %q %d: Scrub finds addresses itself", out, n)
	}
	if out, n := Scrub(nil, testutil.FakeSpanner(), testutil.FakeKnown(), nil); out != nil || n != 0 {
		t.Errorf("nil text = %q %d", out, n)
	}
	out, n := Scrub(in, testutil.FakeSpanner(), nil, nil)
	if string(out) != "mail XXXX now" || n != 1 {
		t.Errorf("no faker = %q %d", out, n)
	}
	bad := spannerFunc(func([]byte) []core.Span {
		return []core.Span{{Start: 5, End: 18, Kind: core.KEmail}, {Start: -1, End: 3}, {Start: 10, End: 99}, {Start: 7, End: 7},
			{Start: 6, End: 12, Kind: "card"}, {Start: 0, End: 4, Kind: core.KText}}
	})
	out, n = Scrub(in, bad, nil, mustFaker(t, testKey(8), Options{}))
	if strings.Contains(string(out), "jane") || n != 2 || !strings.HasPrefix(string(out), "XXXX ") || !strings.HasSuffix(string(out), " now") {
		t.Errorf("bad spans = %q %d", out, n)
	}
	fakeOut, n := Scrub([]byte("to u0123456789abcdef@example.invalid ok"), testutil.FakeSpanner(), nil, mustFaker(t, testKey(8), Options{}))
	if string(fakeOut) != "to u0123456789abcdef@example.invalid ok" || n != 0 {
		t.Errorf("a Roadie fake was replaced: %q", fakeOut)
	}
	ff := testutil.FakeFaker()
	out, _ = Scrub([]byte("a b@c.de z"), testutil.FakeSpanner(), nil, ff)
	if string(out) != "a fake-email-1@example.invalid z" {
		t.Errorf("FakeFaker = %q", out)
	}
}

type spannerFunc func([]byte) []core.Span

func (f spannerFunc) Spans(b []byte) []core.Span { return f(b) }

func FuzzScrub(f *testing.F) {
	for _, s := range []string{
		"Order note: Ada Example (ada@shop.com) rang.",
		"maría.lópez@correo.example",
		"a@b.co a@b.co@c.de ..@.. @@ x@y",
		"Zoë Ångström and zoe angstrom",
		"\xff\xfe@\x80.com",
		"",
	} {
		f.Add([]byte(s))
	}
	k := NewKnown(testKey(8))
	k.Add(core.KFullName, []byte("Ada Example"))
	k.Add(core.KFullName, []byte("Zoë Ångström"))
	k.Seal()
	faker := mustFaker(f, testKey(8), Options{})
	sp := testutil.FakeSpanner()
	f.Fuzz(func(t *testing.T, in []byte) {
		out, n := Scrub(in, sp, k, faker)
		if n == 0 && !bytes.Equal(out, in) {
			t.Fatalf("no replacement but the text changed: %q to %q", in, out)
		}
		// No address from the input is left at a place the spanner finds one.
		real := map[string]bool{}
		for _, s := range cleanSpans(in, append(emailSpans(in), sp.Spans(in)...)) {
			if v := in[s.Start:s.End]; !IsFake(s.Kind, v) {
				real[string(v)] = true
			}
		}
		for _, s := range cleanSpans(out, append(emailSpans(out), sp.Spans(out)...)) {
			if v := out[s.Start:s.End]; real[string(v)] {
				t.Fatalf("span %q of the input survived in %q", v, out)
			}
		}
	})
}
