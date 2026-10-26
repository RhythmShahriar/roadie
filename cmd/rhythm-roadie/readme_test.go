// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"
)

// docLinks finds the links of a Markdown file: [text](target) and the
// src, srcset and href attributes of its HTML.
var docLinks = regexp.MustCompile(`\]\(([^)\s]+)\)|(?:src|srcset|href)="([^"]+)"`)

// headingSlug is GitHub's anchor for a heading: lower case, spaces to
// dashes, other punctuation dropped.
func headingSlug(h string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(h)) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// brokenLinks returns the relative links of the Markdown file at path
// (under root) that point at nothing: a missing file or a #anchor with no
// heading.
func brokenLinks(t *testing.T, root, path string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, path)) // #nosec G304 -- the repository's own docs
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	anchors := map[string]bool{}
	inCode := false
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "```") {
			inCode = !inCode
		}
		if h, ok := strings.CutPrefix(l, "#"); ok && !inCode {
			anchors[headingSlug(strings.TrimLeft(h, "# "))] = true
		}
	}
	var broken []string
	for _, m := range docLinks.FindAllStringSubmatch(text, -1) {
		link := m[1] + m[2]
		switch {
		case strings.Contains(link, "://"), strings.HasPrefix(link, "mailto:"):
			continue
		case strings.HasPrefix(link, "#"):
			if !anchors[link[1:]] {
				broken = append(broken, link)
			}
			continue
		}
		file, _, _ := strings.Cut(link, "#")
		if _, err := os.Stat(filepath.Join(root, filepath.Dir(path), filepath.FromSlash(file))); err != nil {
			broken = append(broken, link)
		}
	}
	return broken
}

// Every relative link in the README and ROADMAP resolves in the repository.
func TestDocLinks(t *testing.T) {
	for _, f := range []string{"README.md", "ROADMAP.md", "CONTRIBUTING.md"} {
		if broken := brokenLinks(t, "../..", f); len(broken) > 0 {
			t.Errorf("%s links to what isn't there: %q", f, broken)
		}
	}
}

// The checker catches a missing file and a missing anchor and passes good
// links.
func TestBrokenLinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "a.svg"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	doc := "# Top\n\n## Responsible use & disclaimer\n\n```sh\n# not a heading\n```\n" +
		"[ok](docs/a.svg) <img src=\"docs/a.svg\"> [web](https://example.com/x) [here](#responsible-use--disclaimer) [top](#top)\n" +
		"[gone](docs/b.svg) <source srcset=\"missing.svg\"> [nowhere](#not-a-heading) [also](#nope)\n"
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	got := brokenLinks(t, dir, "README.md")
	want := []string{"docs/b.svg", "missing.svg", "#not-a-heading", "#nope"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("broken %q, want %q", got, want)
	}
}
