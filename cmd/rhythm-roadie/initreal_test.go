// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/internal/testutil"
)

// config init --from reads a dump no profile matches with the real
// engine: a plain SQL schema (persona C) gets the sql template and its
// personal-looking columns as suggestions, with or without --stack sql;
// --stack laravel reads a Laravel dump with no migrations table.
func TestConfigInitFromRealEngine(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("RHYTHM_CACHE_DIR", t.TempDir())
	old := dep
	t.Cleanup(func() { dep = old })
	dep = realDeps()
	dep.excludeFromGit = func(string, []string) (bool, error) { return false, nil }
	testutil.NoNetwork(t)
	app := "CREATE TABLE `contacts` (\n  `id` int NOT NULL,\n  `email` varchar(190) NOT NULL,\n  `phone` varchar(40) DEFAULT NULL,\n  `notes` text\n);\n" +
		"INSERT INTO `contacts` VALUES (1,'jane.realperson@gmail.com','+44 20 7946 0958','hi'),(2,'li.wei@example.org','+1 415 555 0134','');\n"
	if err := os.WriteFile("app.sql", []byte(app), 0o600); err != nil {
		t.Fatal(err)
	}
	lara := "CREATE TABLE `users` (\n  `id` bigint NOT NULL,\n  `name` varchar(255) NOT NULL,\n  `email` varchar(255) NOT NULL,\n  `password` varchar(255) NOT NULL,\n  `remember_token` varchar(100) DEFAULT NULL\n);\n" +
		"INSERT INTO `users` VALUES (1,'Jane Realperson','jane.realperson@gmail.com','$2y$10$abcdefghijklmnopqrstuv',NULL);\n" +
		"CREATE TABLE `customers` (\n  `id` bigint NOT NULL,\n  `mobile` varchar(40) DEFAULT NULL\n);\n" +
		"INSERT INTO `customers` VALUES (1,'+44 20 7946 0958');\n"
	if err := os.WriteFile("lara.sql", []byte(lara), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		args []string
		says string
		want []string
	}{
		{[]string{"--from", "app.sql"}, "your own SQL schema", []string{"stack: sql", "#  - table: contacts\n#    column: email\n#    rule: email\n", "#    column: phone\n"}},
		{[]string{"--from", "app.sql", "--stack", "sql"}, "your own SQL schema", []string{"stack: sql", "#    column: email\n"}},
		{[]string{"--from", "lara.sql", "--stack", "laravel"}, "a Laravel app", []string{"#  - table: customers\n#    column: mobile\n"}},
	} {
		_ = os.RemoveAll(".roadie")
		out, errOut, code := runCmd(t, "config", append([]string{"init"}, c.args...)...)
		if code != 0 {
			t.Fatalf("%v: exit %d\n%s%s", c.args, code, out, errOut)
		}
		b, _ := os.ReadFile(filepath.Join(".roadie", "config.yaml"))
		if !strings.Contains(out, "for "+c.says) {
			t.Errorf("%v: %s", c.args, out)
		}
		for _, w := range c.want {
			if !strings.Contains(string(b), w) {
				t.Errorf("%v: no %q in\n%s", c.args, w, b)
			}
		}
	}
}
