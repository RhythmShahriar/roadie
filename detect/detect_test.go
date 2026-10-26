// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package detect

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
)

// timeBound scales a bound for the race detector.
func timeBound(d time.Duration) time.Duration {
	if raceEnabled {
		return 15 * d
	}
	return d
}

// within runs fn up to three times until one run takes less than bound
// (scaled for the race detector), so a busy machine doesn't fail a bound
// the code meets. It returns the quickest run and whether it was in time.
func within(bound time.Duration, fn func()) (time.Duration, bool) {
	bound = timeBound(bound)
	best := time.Duration(1 << 62)
	for range 3 {
		start := time.Now()
		fn()
		if best = min(best, time.Since(start)); best < bound {
			return best, true
		}
	}
	return best, false
}

const mib = 1 << 20

func repeatTo(s string, n int) []byte { return bytes.Repeat([]byte(s), n/len(s)+1)[:n] }

// Inputs built to make a backtracking scanner slow: every detector and
// the spanner must get through 256 KiB of each within a second, which a
// quadratic scanner can't (it would take minutes).
func TestLinearOnHostileInput(t *testing.T) {
	const size = mib / 4
	inputs := map[string][]byte{
		"at":        repeatTo("@", size),
		"a-at":      repeatTo("a@", size),
		"dots-at":   append(repeatTo("a.", size-1), '@'),
		"pct40":     repeatTo("%40", size),
		"entity":    repeatTo("&#64;", size),
		"long-dom":  append([]byte("a@"), repeatTo("b.", size)...),
		"digits":    repeatTo("1", size),
		"spaced":    repeatTo("1 ", size),
		"dashed":    repeatTo("12-", size),
		"plus":      repeatTo("+1", size),
		"parens":    repeatTo("(0", size),
		"tel":       repeatTo("tel:", size),
		"arabic":    repeatTo("٤ ", size),
		"hex":       repeatTo("a1", size),
		"dotted":    repeatTo("8.", size),
		"colons":    repeatTo("a:", size),
		"iban":      repeatTo("GB82", size),
		"card":      repeatTo("4539 ", size),
		"mixed-utf": repeatTo("é1@ß.", size),
	}
	sc := core.ScanContext{Column: "billing_phone"}
	for name, v := range inputs {
		for _, det := range All() {
			if d, ok := within(time.Second, func() { det.Scan(v, sc, func(core.Hit) {}) }); !ok {
				t.Errorf("%s on %s took %v", det.ID(), name, d)
			}
		}
		if d, ok := within(time.Second, func() { TextSpans().Spans(v) }); !ok {
			t.Errorf("TextSpans on %s took %v", name, d)
		}
	}
}

// The detectors read card and phone digits in other scripts too.
func TestDigitAt(t *testing.T) {
	for _, s := range []string{"7", "٧", "۷", "७", "৭", "７"} {
		if d, n := digitAt([]byte(s), 0); d != 7 || n != len(s) {
			t.Errorf("%q: %d %d", s, d, n)
		}
	}
	if d, _ := digitAt([]byte("x"), 0); d != -1 {
		t.Error("x is a digit")
	}
}

// checkHits asserts the invariants every detector keeps on any input.
func checkHits(t *testing.T, v []byte, hits []core.Hit) {
	t.Helper()
	for _, h := range hits {
		if h.Start < 0 || h.End > len(v) || h.Start >= h.End {
			t.Fatalf("hit %d..%d outside %d bytes", h.Start, h.End, len(v))
		}
		if !core.KnownID(h.ID) {
			t.Fatalf("unknown ID %q", h.ID)
		}
		if txt := v[h.Start:h.End]; len(txt) >= 6 && strings.Contains(h.Masked, string(txt)) {
			t.Fatalf("masked %q shows %q", h.Masked, txt)
		}
	}
}

// Canaries look real, so the detectors flag them where they belong.
func TestCanariesAreFlagged(t *testing.T) {
	c := testutil.Canaries(7)
	for _, e := range c[core.KEmail] {
		if len(scanAll(Email(), e, core.ScanContext{})) != 1 {
			t.Errorf("canary email %q not flagged", e)
		}
	}
	for _, p := range c[core.KPhone] {
		if len(scanAll(Phone(), p, core.ScanContext{})) != 1 {
			t.Errorf("canary phone %q not flagged", p)
		}
	}
	for _, ip := range c[core.KIP] {
		if len(scanAll(IP(), ip, core.ScanContext{Column: "ip_address"})) != 1 {
			t.Errorf("canary IP %q not flagged", ip)
		}
	}
}
