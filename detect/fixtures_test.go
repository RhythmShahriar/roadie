// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package detect

import (
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/pseudo"
)

// fixtureFile is testdata/<detector>.yaml: values the detector must flag
// (positive) and must leave alone (negative).
type fixtureFile struct {
	Detector string    `yaml:"detector"`
	Positive []fixture `yaml:"positive"`
	Negative []fixture `yaml:"negative"`
}

// fixture is one value and where it sits.
//
// Real-looking numbers are not committed: {dN} in a value is replaced at
// test time by N digits from a seed of the file and case.
type fixture struct {
	Value  string            `yaml:"value"`
	Want   string            `yaml:"want"`  // positive: the hit's ID, default the detector's
	Match  string            `yaml:"match"` // positive: the exact text of the hit
	Table  string            `yaml:"table"`
	Column string            `yaml:"column"`
	Kind   string            `yaml:"kind"`
	Row    map[string]string `yaml:"row"`
	Allow  *struct {
		Emails []string `yaml:"emails"`
		Phones []string `yaml:"phones"`
		Kept   []string `yaml:"kept"`
	} `yaml:"allow"`
	Needs string `yaml:"needs"` // a lane whose real code the case needs
	Note  string `yaml:"note"`
}

var digitsTemplate = regexp.MustCompile(`\{d(\d+)\}`)

// expand fills the templates of a fixture value.
func (f fixture) expand(seed string) string {
	h := fnv.New64a()
	h.Write([]byte(seed))
	r := rand.New(rand.NewPCG(h.Sum64(), 1))
	digits := func(n int) string {
		var b strings.Builder
		for range n {
			b.WriteByte(byte('0' + r.IntN(10)))
		}
		return b.String()
	}
	v := digitsTemplate.ReplaceAllStringFunc(f.Value, func(m string) string {
		n, _ := strconv.Atoi(m[2 : len(m)-1])
		return digits(n)
	})
	return v
}

// context builds the scan context of a fixture.
func (f fixture) context() core.ScanContext {
	sc := core.ScanContext{Table: f.Table, Column: f.Column, ColumnKind: core.FakeKind(f.Kind)}
	if f.Row != nil {
		sc.Row = func(c string) ([]byte, bool) {
			v, ok := f.Row[c]
			return []byte(v), ok
		}
	}
	return sc
}

// detector returns d wrapped with the fixture's allow lists.
func (f fixture) detector(d core.Detector) core.Detector {
	if f.Allow == nil {
		return d
	}
	return WithAllow(d, Allow{Emails: f.Allow.Emails, Phones: f.Allow.Phones, KeptEmails: f.Allow.Kept})
}

// valuesLaneBuilt reports whether pseudo.IsFake is the real one: the stub
// says no to everything, a Roadie fake email included.
func valuesLaneBuilt() bool {
	return pseudo.IsFake(core.KEmail, []byte("u0123456789abcdef@example.invalid"))
}

func loadFixtures(t *testing.T, id string) fixtureFile {
	t.Helper()
	name := strings.TrimPrefix(id, "pii.") + ".yaml"
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("%s has no fixtures: %v", id, err)
	}
	var ff fixtureFile
	if err := yaml.Unmarshal(b, &ff); err != nil {
		t.Fatalf("testdata/%s: %v", name, err)
	}
	if ff.Detector != id {
		t.Fatalf("testdata/%s is for %q, not %q", name, ff.Detector, id)
	}
	return ff
}

func scanAll(d core.Detector, v string, sc core.ScanContext) []core.Hit {
	var hits []core.Hit
	d.Scan([]byte(v), sc, func(h core.Hit) { hits = append(hits, h) })
	return hits
}

// TestEveryDetectorHasFixtures runs every detector over its positive and
// negative fixtures and fails when a detector has none of either.
func TestEveryDetectorHasFixtures(t *testing.T) {
	for _, d := range All() {
		t.Run(d.ID(), func(t *testing.T) {
			ff := loadFixtures(t, d.ID())
			if len(ff.Positive) == 0 || len(ff.Negative) == 0 {
				t.Fatalf("%s needs positive and negative fixtures (%d, %d)", d.ID(), len(ff.Positive), len(ff.Negative))
			}
			run := func(kind string, i int, f fixture, check func(t *testing.T, v string, hits []core.Hit)) {
				t.Run(fmt.Sprintf("%s/%d", kind, i), func(t *testing.T) {
					if f.Needs == "values" && !valuesLaneBuilt() {
						t.Skip("needs the values lane")
					}
					v := f.expand(fmt.Sprintf("%s/%s/%d", d.ID(), kind, i))
					hits := scanAll(f.detector(d), v, f.context())
					for _, h := range hits {
						if !core.KnownID(h.ID) {
							t.Errorf("unknown finding ID %q", h.ID)
						}
						if h.Start < 0 || h.End > len(v) || h.Start >= h.End {
							t.Fatalf("hit %d..%d out of %q", h.Start, h.End, v)
						}
						if txt := v[h.Start:h.End]; len(txt) >= 6 && strings.Contains(h.Masked, txt) {
							t.Errorf("masked %q shows the value %q", h.Masked, txt)
						}
					}
					check(t, v, hits)
				})
			}
			for i, f := range ff.Positive {
				run("positive", i, f, func(t *testing.T, v string, hits []core.Hit) {
					want := cmpOr(f.Want, d.ID())
					for _, h := range hits {
						if h.ID == want && (f.Match == "" || v[h.Start:h.End] == f.Match) {
							return
						}
					}
					t.Errorf("%q (%s): want %s%s, got %s", v, f.Note, want, matchNote(f.Match), describe(v, hits))
				})
			}
			for i, f := range ff.Negative {
				run("negative", i, f, func(t *testing.T, v string, hits []core.Hit) {
					for _, h := range hits {
						if h.Severity >= core.SeverityLow {
							t.Errorf("%q (%s): want no finding, got %s", v, f.Note, describe(v, hits))
							return
						}
					}
				})
			}
		})
	}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func matchNote(m string) string {
	if m == "" {
		return ""
	}
	return " on " + strconv.Quote(m)
}

func describe(v string, hits []core.Hit) string {
	if len(hits) == 0 {
		return "no hit"
	}
	var parts []string
	for _, h := range hits {
		parts = append(parts, fmt.Sprintf("%s %q", h.ID, v[h.Start:h.End]))
	}
	return strings.Join(parts, ", ")
}
