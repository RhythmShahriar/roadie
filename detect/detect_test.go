// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package detect

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/roadie/core"
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

// The scanners read digits in other scripts too.
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
