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

// A verify report names the input it checked, so report check --input
// ties it to that file (PRD 16.2, 18.11).
func TestVerifyReportNamesItsInput(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	dump := "CREATE TABLE `wp_options` (`option_id` bigint NOT NULL, `option_name` varchar(191) NOT NULL, `option_value` longtext NOT NULL);\n" +
		"INSERT INTO `wp_options` VALUES (1,'siteurl','https://staging.shop.test'),(2,'home','https://staging.shop.test');\n" +
		"CREATE TABLE `wp_users` (`ID` bigint NOT NULL, `user_login` varchar(60) NOT NULL, `user_email` varchar(100) NOT NULL);\n" +
		"INSERT INTO `wp_users` VALUES (1,'u1','u1@example.invalid');\n"
	in := filepath.Join(dir, "staging.sql")
	other := filepath.Join(dir, "other.sql")
	for p, text := range map[string]string{in: dump, other: dump + "-- changed\n"} {
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, stderr, code := runEnv(t, nil, "verify", in, "--json"); code != 0 {
		t.Fatalf("verify exit %d: %s", code, stderr)
	}
	reports, _ := filepath.Glob(filepath.Join(dir, ".roadie", "reports", "*", "report.json"))
	if len(reports) != 1 {
		t.Fatalf("reports %v", reports)
	}
	if out, stderr, code := runEnv(t, nil, "report", "check", reports[0], "--input", in); code != 0 || !strings.Contains(out, "the sha256 matches") {
		t.Errorf("check --input the checked dump: exit %d\n%s%s", code, out, stderr)
	}
	if _, stderr, code := runEnv(t, nil, "report", "check", reports[0], "--input", other); code == 0 || !strings.Contains(stderr, "another input") {
		t.Errorf("check --input another dump: exit %d %s", code, stderr)
	}
}

// A copy of an app's own schema (stack sql) has no site URL to rewrite
// to, so it runs without --url; a WordPress copy still needs one.
func TestCopyURLOnlyForSites(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	in := filepath.Join(dir, "crm.sql")
	if err := os.WriteFile(in, []byte("CREATE TABLE `contacts` (`id` int, `email` varchar(100));\nINSERT INTO `contacts` VALUES (1,'u1@example.invalid');\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "roadie.yaml")
	if err := os.WriteFile(cfg, []byte("stack: sql\ncolumns:\n  - {table: contacts, column: email, rule: email}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := runEnv(t, nil, "copy", "--from", in, "--to", filepath.Join(dir, "out.sql"), "--config", cfg)
	if code != 0 || strings.Contains(stderr, "needs --url") {
		t.Errorf("stack sql: exit %d %s", code, stderr)
	}
	_, stderr, code = runEnv(t, nil, "copy", "--from", in, "--to", filepath.Join(dir, "out2.sql"))
	if code != 2 || !strings.Contains(stderr, "copy needs --url") {
		t.Errorf("no stack: exit %d %s", code, stderr)
	}
}
