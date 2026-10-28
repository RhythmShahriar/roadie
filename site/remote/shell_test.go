// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build unix

package remote

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestEveryTemplateRunsInARealShell runs every allowlisted command
// through the shells a host may have, against a temp dir, with stub
// wp, mysql, mysqldump, psql and pg_dump binaries. A shim that never
// runs a shell can't catch a command POSIX sh refuses (test -f -- was
// one: sh exits 2 on it).
func TestEveryTemplateRunsInARealShell(t *testing.T) {
	var shells []string
	for _, sh := range []string{"/bin/sh", "/bin/dash", "/bin/bash", "/usr/bin/zsh", "/bin/busybox"} {
		if _, err := os.Stat(sh); err == nil {
			shells = append(shells, sh)
		}
	}
	if len(shells) == 0 {
		t.Skip("no shell")
	}
	for _, sh := range shells {
		t.Run(filepath.Base(sh), func(t *testing.T) {
			for _, tm := range Templates() {
				runTemplate(t, sh, tm)
			}
		})
	}
}

func runTemplate(t *testing.T, sh string, tm Template) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	home := filepath.Join(dir, "home")
	site := filepath.Join(dir, "site")
	for _, d := range []string{bin, home, filepath.Join(site, "wp-content", "uploads")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stub := "#!/bin/sh\ncat >/dev/null\necho '-- stub'\n"
	for _, b := range []string{"wp", "mysql", "mysqldump", "psql", "pg_dump"} {
		if err := os.WriteFile(filepath.Join(bin, b), []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := filepath.Join(site, "wp-config.php")
	if err := os.WriteFile(cfg, []byte("<?php\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tmp := cfg + ".roadie-tmp"
	uid, gid := strconv.Itoa(os.Getuid()), strconv.Itoa(os.Getgid())
	args := map[Template][]string{
		CoreVersion:    nil,
		OptionGet:      {"siteurl"},
		DBExport:       nil,
		DBExportTables: {"wp_options,wp_users"},
		Cat:            {cfg},
		TestFile:       {cfg},
		Find:           {filepath.Join(site, "wp-content"), "3"},
		MySQLDump:      {"shop", "wp_options"},
		PGDump:         {"127.0.0.1", "5432", "app", "app"},
		DBImport:       nil,
		MySQL:          {"staging"},
		PSQL:           {"127.0.0.1", "5432", "app", "staging"},
		Backup:         {"2026-10-26T14-02-03"},
		Write:          {tmp, tmp, cfg},
		WriteMode:      {tmp, "0640", tmp, tmp, cfg},
		WriteKeep:      {tmp, uid, gid, tmp, gid, tmp, "0644", tmp, tmp, cfg},
		Stat:           {cfg},
		Mkdir:          {filepath.Join(site, "wp-content", "mu-plugins")},
		Remove:         {filepath.Join(site, "robots.txt.roadie-tmp")},
		EvalFile:       nil,
	}
	a, ok := args[tm]
	if !ok {
		t.Errorf("no shell sample for %s", tm)
		return
	}
	if tm == Find && filepath.Base(sh) == "busybox" {
		// busybox find has no -printf; the shell itself is what this checks.
		return
	}
	cmd, err := build(tm, "wp", site, a)
	if err != nil {
		t.Errorf("%s: %v", tm, err)
		return
	}
	argv := []string{"-c", cmd}
	if filepath.Base(sh) == "busybox" {
		argv = append([]string{"sh"}, argv...)
	}
	c := exec.Command(sh, argv...)
	c.Dir = dir
	c.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + home}
	c.Stdin = strings.NewReader("pw\n" + credsEnd + "\n<?php\n")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Errorf("%s via %s: %v\n%s\n%s", tm, sh, err, cmd, out)
	}
	if tm == TestFile {
		// A missing file must exit 1, which the caller reads as "not there".
		cmd, _ := build(TestFile, "", "", []string{filepath.Join(site, "nope.php")})
		argv := []string{"-c", cmd}
		if filepath.Base(sh) == "busybox" {
			argv = append([]string{"sh"}, argv...)
		}
		err := exec.Command(sh, argv...).Run()
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
			t.Errorf("test -f on a missing file via %s: %v, want exit 1", sh, err)
		}
	}
}
