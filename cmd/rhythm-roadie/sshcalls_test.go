// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/internal/demo"
	"github.com/RhythmShahriar/roadie/site/remote/remotetest"
)

// A copy of the demo shop (13 tables) from an ssh source asks the host
// for each file once and runs only commands its plan lists under REMOTE
// COMMANDS: one ssh round trip per profile check each time pass 1 saw a
// new table once made hundreds of connections, which sshd's MaxStartups
// or fail2ban can refuse.
func TestSSHSourceCommandsMatchPlan(t *testing.T) {
	dir, cleanup, err := remotetest.Install()
	if err != nil {
		t.Skip("no ssh shim: ", err)
	}
	t.Cleanup(cleanup)
	t.Chdir(t.TempDir())
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	var shop bytes.Buffer
	if err := demo.Shop(&shop, demo.Options{}); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + os.Getenv("PATH")}
	from := "ssh://deploy@shop.example.com/var/www/shop"

	sh := remotetest.New(t)
	sh.Reply(nil, "", 0)
	sh.ReplyFor("wp --path='/var/www/shop' db export", shop.Bytes(), "", 0)
	stdout, stderr, code := runEnv(t, env, "plan", "--from", from, "--to", "staging.sql", "--url", "https://staging.shop.test", "--json")
	if code != 0 {
		t.Fatalf("plan exit %d: %s", code, stderr)
	}
	var plan struct {
		Data struct {
			RemoteCommands []string `json:"remote_commands"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &plan); err != nil {
		t.Fatal(err)
	}
	// The plan lists the commands it knows in full and the form of the
	// rest: <arg>, <path>, <n> and [<table> ...] stand for words.
	var forms []*regexp.Regexp
	for _, c := range plan.Data.RemoteCommands {
		q := regexp.QuoteMeta(c)
		q = strings.ReplaceAll(q, regexp.QuoteMeta("[<table> ...]"), ".*")
		q = regexp.MustCompile(`<(arg|path|n|table)>`).ReplaceAllString(q, `\S+`)
		q = strings.ReplaceAll(q, regexp.QuoteMeta("<siteurl|home>"), "(siteurl|home)")
		forms = append(forms, regexp.MustCompile("^"+q+"$"))
	}
	listed := func(cmd string) bool {
		return slices.ContainsFunc(forms, func(re *regexp.Regexp) bool { return re.MatchString(cmd) })
	}

	sh = remotetest.New(t)
	sh.Reply(nil, "", 0)
	sh.ReplyFor("wp --path='/var/www/shop' db export", shop.Bytes(), "", 0)
	out := filepath.Join(t.TempDir(), "staging.sql")
	if _, stderr, code := runEnv(t, env, "copy", "--from", from, "--to", out, "--url", "https://staging.shop.test", "--json"); code != 0 {
		t.Fatalf("copy exit %d: %s", code, stderr)
	}
	calls := sh.Calls()
	seen := map[string]int{}
	for _, c := range calls {
		seen[c[len(c)-1]]++
	}
	for cmd, n := range seen {
		if n > 1 && !strings.Contains(cmd, " db export ") {
			t.Errorf("%d times: %s", n, cmd)
		}
		if !listed(cmd) {
			t.Errorf("not in the plan's REMOTE COMMANDS: %s", cmd)
		}
	}
	if len(calls) > len(plan.Data.RemoteCommands) {
		t.Errorf("%d ssh runs for %d listed commands", len(calls), len(plan.Data.RemoteCommands))
	}
}
