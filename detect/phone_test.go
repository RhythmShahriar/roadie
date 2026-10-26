// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package detect

import (
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/roadie/core"
)

func TestTextSpans(t *testing.T) {
	card := makeLuhn("4539 1488 0343 646?", func(int) string { return "0" })
	text := "Order note: ring Ada on +44 7911 123456 or mail ada.kowalczyk@gmail.com, " +
		"card " + card + ", from 81.2.69.160. Copy to noreply@shop.com and test@example.com; " +
		"version 1.2.3.4 and 10.0.0.1 stay."
	spans := TextSpans().Spans([]byte(text))
	var got []string
	for i, s := range spans {
		if i > 0 && s.Start < spans[i-1].End {
			t.Fatalf("spans overlap: %v", spans)
		}
		got = append(got, string(s.Kind)+":"+text[s.Start:s.End])
	}
	want := []string{"phone:+44 7911 123456", "email:ada.kowalczyk@gmail.com", "card:" + card, "ip:81.2.69.160"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if s := TextSpans().Spans(nil); len(s) != 0 {
		t.Errorf("spans in nothing: %v", s)
	}
}

func TestTextSpansIPv6(t *testing.T) {
	text := "client 2a00:1450:4009:81f::200e at 12:30:45 via ::1"
	spans := TextSpans().Spans([]byte(text))
	if len(spans) != 1 || text[spans[0].Start:spans[0].End] != "2a00:1450:4009:81f::200e" {
		t.Errorf("got %v", spans)
	}
}

func TestLuhnAndIBAN(t *testing.T) {
	if !luhn([]byte("4242424242424242")) || luhn([]byte("4242424242424241")) {
		t.Error("luhn")
	}
	if !ibanValid([]byte("GB82WEST12345698765432")) || ibanValid([]byte("GB83WEST12345698765432")) {
		t.Error("iban mod-97")
	}
}

func FuzzPhone(f *testing.F) {
	for _, s := range []string{
		"+44 7911 123456", "tel:+4479", "(415) 261-2345", "07911 123456", "1234567.89", "1700000000",
		"+٤٤ ٧٧٠٠ ٩٠٠١٢٣", "550e8400-e29b-41d4-a716-446655440000", "v1.2.3", "+44 (0)20 1234 5678",
		"$ 1234567890", "SKU-415-261-2345",
	} {
		f.Add([]byte(s), false)
		f.Add([]byte(s), true)
	}
	f.Fuzz(func(t *testing.T, v []byte, phoneCol bool) {
		sc := core.ScanContext{}
		if phoneCol {
			sc.Column = "phone"
		}
		start := time.Now()
		hits := scanAll(Phone(), string(v), sc)
		if d := time.Since(start); d > timeBound(time.Second) {
			t.Fatalf("%d bytes took %v", len(v), d)
		}
		checkHits(t, v, hits)
		for _, h := range hits {
			n := len(asciiDigits(string(v[h.Start:h.End])))
			if n < 7 || n > 15 {
				t.Fatalf("hit %q has %d digits", v[h.Start:h.End], n)
			}
		}
	})
}

// Found by FuzzPhone: dropping a "(0)" trunk made 16 digits pass as 15.
func TestPhoneTrunkNeverStretchesPastFifteen(t *testing.T) {
	for _, v := range []string{"00(0)0000000000000", "+44 (0)20 1234 5678 9012"} {
		if h := scanAll(Phone(), v, core.ScanContext{Column: "phone"}); len(h) != 0 {
			t.Errorf("%q: %v", v, h)
		}
	}
	if h := scanAll(Phone(), "+44 (0)161 496 1234", core.ScanContext{}); len(h) != 1 {
		t.Errorf("a trunk number with 12 digits: %v", h)
	}
}

// digitsAhead reads over one bracket pair at most: a second bracket ends
// the number, so hostile "(0(0(0" text never runs the patterns.
func TestDigitsAheadBrackets(t *testing.T) {
	for s, want := range map[string]bool{
		"(020) 7946 0958":    true,
		"1 (212) 555-0100":   true,
		"020 7946 0958":      true,
		"(0(0(0(0(0(0(0(0(0": false,
		"(020) (7946) 0958":  false,
		"020) 7946) 0958":    false,
		"0(0(0(0(0(0(0(0(0(": false,
	} {
		if got := digitsAhead([]byte(s), 0, minNational); got != want {
			t.Errorf("digitsAhead(%q) = %v, want %v", s, got, want)
		}
	}
}
