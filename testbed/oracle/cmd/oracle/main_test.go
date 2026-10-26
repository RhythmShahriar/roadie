// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	email := "dovarin.kelmas.q81z03@outlook.com"
	canaries := filepath.Join(dir, "canaries.json")
	writeFile(t, canaries, `{"version":1,"seed":1,"canaries":[`+
		`{"id":"r01-email-0001","kind":"email","scenario":"r01","table":"wp_users","column":"user_email","value":"`+email+`"},`+
		`{"id":"r02-email-0001","kind":"email","scenario":"r02","value":"other.person.z11a22@gmail.com"}]}`)
	dump := "CREATE TABLE `wp_users` (\n  `ID` int,\n  `user_email` text,\n  PRIMARY KEY (`ID`)\n);\n"
	in := filepath.Join(dir, "in.sql")
	writeFile(t, in, dump+"INSERT INTO `wp_users` VALUES (1,'"+email+"');\n")
	out := filepath.Join(dir, "out.sql")
	writeFile(t, out, dump+"INSERT INTO `wp_users` VALUES (1,'fake-email-1@example.com');\n")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"-canaries", canaries, "-scenario", "r01", "-dump", out, "-input", in}, &stdout, &stderr); code != 0 {
		t.Fatalf("clean output: exit %d\n%s%s", code, stdout.String(), stderr.String())
	}
	var rep struct {
		Canaries  int `json:"canaries"`
		Survivors []struct{ ID, Where string }
	}
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil || rep.Canaries != 1 {
		t.Fatalf("report %s: %v", stdout.String(), err)
	}

	stdout.Reset()
	if code := run([]string{"-canaries", canaries, "-dump", in}, &stdout, &stderr); code != 1 {
		t.Fatalf("leaky output: exit %d", code)
	}
	if strings.Contains(stdout.String(), email) {
		t.Fatal("the report prints a canary value")
	}
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil || len(rep.Survivors) != 1 || rep.Survivors[0].ID != "r01-email-0001" || rep.Survivors[0].Where != "db:wp_users" {
		t.Errorf("survivors %+v %v", rep.Survivors, err)
	}

	for _, args := range [][]string{{}, {"-dump", out, "-canaries", filepath.Join(dir, "none.json")}, {"-list-serialized"}, {"-bogus"}} {
		if code := run(args, &stdout, &stderr); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

func TestListSerialized(t *testing.T) {
	php, err := exec.LookPath("php")
	if err != nil {
		if os.Getenv("ROADIE_REQUIRE_PHP") == "1" {
			t.Fatal("php not on PATH")
		}
		t.Skip("php not on PATH")
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "in.sql")
	writeFile(t, in, "CREATE TABLE `o` (\n  `id` int,\n  `v` text,\n  PRIMARY KEY (`id`)\n);\nINSERT INTO `o` VALUES (1,'a:0:{}'),(2,'s:3:\\\"ab\\\";'),(3,'plain');\n")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-list-serialized", "-input", in, "-php", php}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if got := strings.Join(strings.Fields(stdout.String()), ""); !strings.Contains(got, `"cells":[{"table":"o","key":"id=1","column":"v"}]`) {
		t.Errorf("serialized.json %s", stdout.String())
	}
}
