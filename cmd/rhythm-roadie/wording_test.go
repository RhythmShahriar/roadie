// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/report"
)

// The wording rules of PRD 19.2: no output, help, explanation, golden
// file or changes note says Roadie anonymises or makes anything
// compliant or safe to share; every report format says the copy is
// pseudonymised and still personal data.

// bannedWords are PRD 19.2's, matched case-insensitively; "anonymi[sz]e"
// covers anonymised, anonymized, anonymise and anonymize.
var bannedWords = regexp.MustCompile(`(?i)anonymi[sz]e|gdpr[ -]compliant|compliant|safe to share`)

// allowedSentences may hold a banned word: the honesty line of PRD 16.1
// says what Roadie isn't and the README's positioning line of PRD 3 names
// the other tools.
var allowedSentences = []string{
	"Pseudonymised, not anonymised.",
	"Generic anonymisers don't know WordPress.",
}

// bannedIn returns the banned words in text outside the allowed sentences.
func bannedIn(text string) []string {
	for _, s := range allowedSentences {
		text = strings.ReplaceAll(text, s, "")
	}
	return bannedWords.FindAllString(text, -1)
}

// wordingFiles are every file the rule covers: the goldens of this
// package and of report, the explanations, the changes notes, the README,
// the ROADMAP and the Action.
func wordingFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	for _, root := range []string{"testdata/golden", "explain", "../../report/testdata/golden", "../../changes"} {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				files = append(files, p)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return append(files, "../../README.md", "../../ROADMAP.md", "../../action.yml")
}

func TestWording(t *testing.T) {
	files := wordingFiles(t)
	if len(files) < 20 {
		t.Fatalf("only %d files to check; the paths moved?", len(files))
	}
	for _, f := range files {
		b, err := os.ReadFile(f) // #nosec G304 -- the repository's own files
		if err != nil {
			t.Fatal(err)
		}
		if words := bannedIn(string(b)); len(words) > 0 {
			t.Errorf("%s says %q (PRD 19.2)", f, words)
		}
	}
	// Every help text, as it prints.
	texts := map[string]string{"help": topHelp}
	for _, c := range tool().Commands {
		texts["help "+c.Name] = helpFor(c.Name)
	}
	for name, text := range texts {
		if words := bannedIn(text); len(words) > 0 {
			t.Errorf("%s says %q", name, words)
		}
	}
	// The screen's own fixed sentences.
	for _, s := range []string{honestyLine, notCovered, notStopped, cachesLine} {
		if words := bannedIn(s); len(words) > 0 {
			t.Errorf("%q says %q", s, words)
		}
	}
}

// Every report golden says pseudonymised and carries the still personal
// data sentence of PRD 19.1.
func TestReportGoldensSayPseudonymised(t *testing.T) {
	files, _ := filepath.Glob("../../report/testdata/golden/render/*")
	if len(files) < 8 {
		t.Fatalf("only %d report goldens", len(files))
	}
	for _, f := range files {
		b, err := os.ReadFile(f) // #nosec G304 -- the repository's own goldens
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(bytes.ToLower(b), []byte("pseudonymised")) {
			t.Errorf("%s doesn't say pseudonymised", f)
		}
		if !bytes.Contains(b, []byte(report.StillPersonal)) {
			t.Errorf("%s lacks %q", f, report.StillPersonal)
		}
	}
}

// The checker itself: it catches each banned word and lets the honesty
// line through.
func TestBannedWords(t *testing.T) {
	for _, bad := range []string{"fully anonymised", "Anonymized data", "we anonymise it", "GDPR-compliant", "GDPR compliant copies", "compliant", "Safe to share"} {
		if len(bannedIn(bad)) == 0 {
			t.Errorf("%q passed", bad)
		}
	}
	if words := bannedIn(honestyLine); len(words) > 0 {
		t.Errorf("the honesty line is flagged: %q", words)
	}
	if words := bannedIn("Pseudonymised, not anonymised. Then anonymised."); len(words) != 1 {
		t.Errorf("only the allowed sentence may hold the word: %q", words)
	}
}
