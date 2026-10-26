// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package remote

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// PRD 18.1: ssh starts only in site/remote; no code starts wp or php
// on this machine. The test walks every .go file of the module.
func TestNoProcessOutsideRemote(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root not found at %s: %v", root, err)
	}
	ssh := regexp.MustCompile(`exec\.Command(?:Context)?\(\s*(?:[A-Za-z_][A-Za-z0-9_.]*\s*,\s*)?"ssh"`)
	local := regexp.MustCompile(`exec\.Command(?:Context)?\(\s*(?:[A-Za-z_][A-Za-z0-9_.]*\s*,\s*)?"(?:wp|php)"`)
	checked := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		checked++
		if ssh.Match(b) && !strings.HasPrefix(rel, "site/remote/") {
			t.Errorf("%s starts ssh; only site/remote may", rel)
		}
		if local.Match(b) && !strings.HasSuffix(rel, "_test.go") {
			t.Errorf("%s starts wp or php on this machine (PRD 18.1)", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 20 {
		t.Fatalf("only %d .go files checked under %s", checked, root)
	}
	// The patterns must catch the forms they forbid.
	for _, bad := range []string{`exec.Command("ssh", a)`, `exec.CommandContext(ctx, "ssh")`, `exec.Command( "wp" )`, `exec.CommandContext(c.ctx, "php", "-r")`} {
		if !ssh.MatchString(bad) && !local.MatchString(bad) {
			t.Errorf("pattern misses %s", bad)
		}
	}
}
