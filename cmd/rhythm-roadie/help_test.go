// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/internal/testutil"
)

// The top help is PRD 6.1's text; each command's is its own block, the
// shared Output and Settings blocks and the exit codes.
func TestHelpGolden(t *testing.T) {
	var out, errb bytes.Buffer
	for _, args := range [][]string{nil, {"help"}, {"-h"}, {"--help"}, {"help", "help"}} {
		out.Reset()
		if code := entry(args, &out, &errb); code != 0 {
			t.Fatalf("%q: exit %d", args, code)
		}
		testutil.Golden(t, "help.txt", out.Bytes())
	}
	for _, c := range tool().Commands {
		stdout, stderr, code := runCmd(t, c.Name, "--help")
		if code != 0 || stderr != "" {
			t.Errorf("%s --help: exit %d, stderr %q", c.Name, code, stderr)
		}
		testutil.Golden(t, "help-"+c.Name+".txt", []byte(stdout))
		if h, _, _ := runCmd(t, c.Name, "-h"); h != stdout {
			t.Errorf("%s -h differs from --help", c.Name)
		}
		if !strings.HasPrefix(stdout, "rhythm roadie "+c.Name+": ") {
			t.Errorf("%s help starts %q", c.Name, strings.SplitN(stdout, "\n", 2)[0])
		}
		for _, block := range []string{"Output:\n", "Settings:\n", "Exit codes (as in Tuner, Soundcheck and Echo):\n"} {
			if !strings.Contains(stdout, block) {
				t.Errorf("%s help lacks %q", c.Name, block)
			}
		}
	}
}

// Command help lines stay within 100 columns, so they read in a 100
// column terminal without wrapping in odd places. The top help is PRD
// 6.1's text as written.
func TestHelpWidth(t *testing.T) {
	texts := map[string]string{}
	for cmd := range commandHelp {
		texts[cmd] = helpFor(cmd)
	}
	for name, text := range texts {
		for _, line := range strings.Split(text, "\n") {
			if n := len([]rune(line)); n > 100 {
				t.Errorf("%s help line is %d columns: %q", name, n, line)
			}
		}
	}
}

// update is kit's command; its help is Roadie's: the same title line
// as every other command, --clear, --json and --pin, by every spelling.
func TestUpdateHelp(t *testing.T) {
	var first string
	for _, args := range [][]string{{"update", "--help"}, {"update", "-h"}, {"help", "update"}, {"update", "--clear", "--help"}} {
		var out, errb bytes.Buffer
		if code := entry(args, &out, &errb); code != 0 || errb.Len() != 0 {
			t.Fatalf("%q: exit %d %s", args, code, errb.String())
		}
		if first == "" {
			first = out.String()
			testutil.Golden(t, "help-update.txt", out.Bytes())
		} else if out.String() != first {
			t.Errorf("%q differs from update --help", args)
		}
	}
	if !strings.HasPrefix(first, "rhythm roadie update: ") {
		t.Errorf("update help starts %q", strings.SplitN(first, "\n", 2)[0])
	}
	for _, w := range []string{"--clear", "--json", "--pin", "Exit codes (as in Tuner, Soundcheck and Echo):\n"} {
		if !strings.Contains(first, w) {
			t.Errorf("update help lacks %q", w)
		}
	}
	for _, line := range strings.Split(first, "\n") {
		if n := len([]rune(line)); n > 100 {
			t.Errorf("update help line is %d columns: %q", n, line)
		}
	}
}
