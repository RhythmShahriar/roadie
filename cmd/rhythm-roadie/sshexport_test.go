// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/site/remote/remotetest"
)

// A copy from an ssh source exports production once (PRD 10.3): pass 1
// spools the export and pass 2 reads the spool, so both passes see the
// same rows and production carries one export's load. Plugin detection
// asks the host for each plugin file once per run.
func TestSSHSourceExportedOnce(t *testing.T) {
	dir, cleanup, err := remotetest.Install()
	if err != nil {
		t.Skip("no ssh shim: ", err)
	}
	t.Cleanup(cleanup)
	t.Chdir(t.TempDir()) // the copy writes its report under .roadie/
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	sh := remotetest.New(t)
	sh.Reply(nil, "", 0)
	dump := "CREATE TABLE `wp_options` (`option_id` bigint NOT NULL, `option_name` varchar(191) NOT NULL, `option_value` longtext NOT NULL);\n" +
		"INSERT INTO `wp_options` VALUES (1,'siteurl','https://shop.example.com'),(2,'home','https://shop.example.com'),(3,'blogname','Shop');\n" +
		"CREATE TABLE `wp_users` (`ID` bigint NOT NULL, `user_login` varchar(60) NOT NULL, `user_email` varchar(100) NOT NULL, `display_name` varchar(250) NOT NULL);\n" +
		"INSERT INTO `wp_users` VALUES (1,'jdoe','jane.doe@mail.example.com','Jane Doe');\n"
	sh.ReplyFor("wp --path='/var/www/shop' db export", []byte(dump), "", 0)
	out := filepath.Join(t.TempDir(), "out.sql")
	_, stderr, code := runEnv(t, []string{"PATH=" + os.Getenv("PATH")}, "copy", "--from", "ssh://deploy@shop.example.com/var/www/shop",
		"--to", out, "--url", "https://staging.shop.test", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	exports, finds := 0, map[string]int{}
	for _, c := range sh.Calls() {
		cmd := c[len(c)-1]
		if strings.Contains(cmd, " db export ") {
			exports++
		}
		if strings.HasPrefix(cmd, "find ") {
			finds[cmd]++
		}
	}
	if exports != 1 {
		t.Errorf("%d exports, want 1", exports)
	}
	if len(finds) == 0 {
		t.Error("no plugin file was looked for")
	}
	for cmd, n := range finds {
		if n > 1 {
			t.Errorf("%d times: %s", n, cmd)
		}
	}
	b, err := os.ReadFile(out) // #nosec G304 -- the test's own output
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "jane.doe@mail.example.com") || !strings.Contains(string(b), "https://staging.shop.test") {
		t.Errorf("output not pseudonymised:\n%s", b)
	}
}
