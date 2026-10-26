// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pseudo

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
)

// No regexp in any hot path: the codecs and the faker run once per value
// of a dump; a regexp there costs more than the rest of the work.
// This walks the lane's two trees (pseudo and codec) and fails on any
// "regexp." outside a _test.go file.
func TestNoRegexpInHotPaths(t *testing.T) {
	files := 0
	for _, root := range []string{".", filepath.Join("..", "codec")} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == "testdata" {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			files++
			b, err := os.ReadFile(path) // #nosec G304 -- the package's own sources
			if err != nil {
				return err
			}
			for i, line := range bytes.Split(b, []byte("\n")) {
				if bytes.Contains(line, []byte("regexp.")) || bytes.Contains(line, []byte(`"regexp"`)) {
					t.Errorf("%s:%d uses regexp in a hot path: %s", path, i+1, bytes.TrimSpace(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if files < 10 {
		t.Fatalf("walked only %d source files; the test isn't looking where it should", files)
	}
}

// Allocation guards for pseudo: a cached fake allocates only its result,
// a Known lookup of an ASCII token allocates nothing (an IP-shaped one
// may: netip keeps the text in its error) and scrubbing text
// with nothing to replace costs the same few allocations at any length.
func TestAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector drops sync.Pool entries on purpose, which adds allocations")
	}
	f := mustFaker(t, testKey(3), Options{})
	for _, kind := range []core.FakeKind{core.KEmail, core.KFullName, core.KPhone, core.KPostcode, core.KUsername} {
		v := []byte("Jane.Doe@shop.com")
		if _, err := f.Fake(kind, v, core.FakeOpts{}); err != nil {
			t.Fatal(err)
		}
		if n := testing.AllocsPerRun(50, func() { _, _ = f.Fake(kind, v, core.FakeOpts{}) }); n > 2 {
			t.Errorf("cached %s fake: %v allocations, want at most 2", kind, n)
		}
	}
	kn := NewKnown(testKey(3))
	kn.Add(core.KFullName, []byte("Jane Roe"))
	kn.Add(core.KEmail, []byte("jane@shop.com"))
	kn.Seal()
	for _, tok := range []string{"Jane Roe", "roe", "parcel", "JANE@SHOP.COM", "+44 7911 123456"} {
		b := []byte(tok)
		if n := testing.AllocsPerRun(50, func() { kn.Contains(b) }); n != 0 {
			t.Errorf("Contains(%q): %v allocations, want 0", tok, n)
		}
	}
	sp := testutil.FakeSpanner()
	short := []byte(strings.Repeat("The parcel was left at the door as asked. ", 10))
	long := []byte(strings.Repeat("The parcel was left at the door as asked. ", 2000))
	ns := testing.AllocsPerRun(10, func() { Scrub(short, sp, kn, f) })
	nl := testing.AllocsPerRun(10, func() { Scrub(long, sp, kn, f) })
	if ns > 16 || nl > 32 {
		t.Errorf("Scrub with nothing to replace: %v allocations for %d bytes, %v for %d; want at most 16 and 32",
			ns, len(short), nl, len(long))
	}
}

// BenchmarkScrub measures the free-text scrubber on order notes: one
// with nothing to replace and one with an email, a phone and a known
// name in every sentence.
func BenchmarkScrub(b *testing.B) {
	kn := NewKnown(testKey(4))
	kn.Add(core.KFullName, []byte("Jane Roe"))
	kn.Add(core.KEmail, []byte("jane.roe@shop.com"))
	kn.Seal()
	sp := testutil.FakeSpanner()
	clean := []byte(strings.Repeat("The parcel was left at the door as asked; the customer will call back about the invoice tomorrow. ", 1000))
	dirty := []byte(strings.Repeat("Jane Roe (jane.roe@shop.com) asked to leave the parcel with the porter, call back on the number on file. ", 1000))
	for _, c := range []struct {
		name string
		text []byte
	}{{"clean", clean}, {"dirty", dirty}} {
		b.Run(c.name, func(b *testing.B) {
			f := mustFaker(b, testKey(4), Options{})
			b.SetBytes(int64(len(c.text)))
			b.ReportAllocs()
			for b.Loop() {
				Scrub(c.text, sp, kn, f)
			}
		})
	}
}
