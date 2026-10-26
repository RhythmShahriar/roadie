// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/site/remote/remotetest"
)

func TestMain(m *testing.M) {
	_, cleanup, err := remotetest.Install()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// sample has arguments that build every template.
var sample = map[Template][]string{
	CoreVersion:    nil,
	OptionGet:      {"siteurl"},
	DBExport:       nil,
	DBExportTables: {"wp_options,wp_users"},
	Cat:            {"/srv/shop/wp-config.php"},
	TestFile:       {"/srv/shop/wp-config.php"},
	Find:           {"/srv/shop/wp-content/uploads", "3"},
	MySQLDump:      {"shop", "wp_options"},
	PGDump:         {"db.internal", "5432", "app", "app"},
	DBImport:       nil,
	MySQL:          {"staging"},
	PSQL:           {"127.0.0.1", "5432", "app", "staging"},
	Backup:         {"2026-10-26T14-02-03"},
	Write:          {"/srv/s/wp-config.php.roadie-tmp", "/srv/s/wp-config.php.roadie-tmp", "/srv/s/wp-config.php"},
	WriteMode:      {"/srv/s/x.tmp", "0640", "/srv/s/x.tmp", "/srv/s/x.tmp", "/srv/s/x"},
	WriteKeep:      {"/srv/s/x.tmp", "1001", "33", "/srv/s/x.tmp", "33", "/srv/s/x.tmp", "0644", "/srv/s/x.tmp", "/srv/s/x.tmp", "/srv/s/x"},
	Stat:           {"/srv/s/wp-config.php"},
	Mkdir:          {"/srv/s/wp-content/mu-plugins"},
	Remove:         {"/srv/s/robots.txt.roadie-tmp"},
	EvalFile:       nil,
}

// checkAllowed fails the test for any recorded command outside the list.
func checkAllowed(t *testing.T, s *remotetest.Shim) {
	t.Helper()
	for _, argv := range s.Calls() {
		cmd := argv[len(argv)-1]
		if _, ok := Allowed(cmd); !ok {
			t.Errorf("recorded command not in the allowlist: %s", cmd)
		}
		if slices.ContainsFunc(argv, func(a string) bool {
			return strings.Contains(a, "StrictHostKeyChecking") || strings.Contains(a, "UserKnownHostsFile")
		}) {
			t.Errorf("host key option in argv %q", argv)
		}
	}
}

func TestEveryTemplateBuildsInsideTheAllowlist(t *testing.T) {
	r := &Runner{Host: "shop.example", Root: "/srv/shop"}
	for _, tm := range Templates() {
		args, ok := sample[tm]
		if !ok {
			t.Errorf("no sample for %s", tm)
			continue
		}
		cmd, err := r.Command(tm, args...)
		if err != nil {
			t.Errorf("%s: %v", tm, err)
			continue
		}
		if got, ok := Allowed(cmd); !ok || got != tm {
			t.Errorf("%s built %q, which matches %q %v", tm, cmd, got, ok)
		}
	}
	if len(sample) != len(Templates()) {
		t.Errorf("sample has %d templates, the allowlist %d", len(sample), len(Templates()))
	}
}

func TestExactCommands(t *testing.T) {
	r := &Runner{Host: "shop.example", Root: "/var/www/shop", WP: "~/bin/wp"}
	tests := []struct {
		t    Template
		args []string
		want string
	}{
		{CoreVersion, nil, "~/bin/wp --path='/var/www/shop' core version --skip-plugins --skip-themes"},
		{DBExport, nil, "~/bin/wp --path='/var/www/shop' db export - --skip-plugins --skip-themes --single-transaction --skip-triggers"},
		{Cat, []string{"/srv/$(id)/wp-config.php"}, "cat -- '/srv/$(id)/wp-config.php'"},
		{Cat, []string{"/srv/it's/.env"}, `cat -- '/srv/it'\''s/.env'`},
		{Find, []string{"/srv/up", "2"}, `find '/srv/up' -maxdepth 2 -printf '%y %s %P\n'`},
		{MySQLDump, []string{"shop"}, "mysqldump --defaults-extra-file=/dev/stdin --single-transaction --skip-triggers --no-tablespaces --hex-blob 'shop'"},
		{Backup, []string{"2026-10-26T14-02-03"}, "set -C && umask 077 && mkdir -p ~/.roadie-backups && { ~/bin/wp --path='/var/www/shop' db export - --skip-plugins --skip-themes --single-transaction || echo 'roadie: wp db export failed' >&2; } | gzip > ~/.roadie-backups/2026-10-26T14-02-03.sql.gz"},
		{EvalFile, nil, "~/bin/wp --path='/var/www/shop' eval-file -"},
		{Stat, []string{"/srv/shop/wp-config.php"}, "ls -lnLd -- '/srv/shop/wp-config.php'"},
		{WriteKeep, []string{"/s/.x.t", "1001", "33", "/s/.x.t", "33", "/s/.x.t", "0644", "/s/.x.t", "/s/.x.t", "/s/x"},
			"umask 077 && cat > '/s/.x.t' && { chown 1001:33 -- '/s/.x.t' 2>/dev/null || chgrp 33 -- '/s/.x.t' 2>/dev/null || :; } && chmod 0644 -- '/s/.x.t' && mv -f -- '/s/.x.t' '/s/x'"},
	}
	for _, tt := range tests {
		got, err := r.Command(tt.t, tt.args...)
		if err != nil || got != tt.want {
			t.Errorf("%s:\n got %s (%v)\nwant %s", tt.t, got, err, tt.want)
		}
	}
	odd := &Runner{Host: "h", WP: "/opt/my wp"}
	if got, _ := odd.Command(CoreVersion); got != "'/opt/my wp' core version --skip-plugins --skip-themes" {
		t.Errorf("a remote.wp with a space is not quoted: %s", got)
	}
}

func TestBadArguments(t *testing.T) {
	r := &Runner{Host: "h"}
	for _, tt := range []struct {
		t    Template
		args []string
	}{
		{OptionGet, []string{"admin_email"}},
		{Find, []string{"/x", "1; id"}},
		{WriteMode, []string{"/a", "777", "/a", "/a", "/b"}},
		{WriteKeep, []string{"/a", "-1", "33", "/a", "33", "/a", "0644", "/a", "/a", "/b"}},
		{WriteKeep, []string{"/a", "1001", "33;id", "/a", "33", "/a", "0644", "/a", "/a", "/b"}},
		{Backup, []string{"../../etc/passwd"}},
		{Cat, []string{"/a\nb"}},
		{Cat, nil},
		{Cat, []string{"/a", "/b"}},
	} {
		if _, err := r.Command(tt.t, tt.args...); !errors.Is(err, ErrNotAllowed) {
			t.Errorf("%s %q: %v, want ErrNotAllowed", tt.t, tt.args, err)
		}
	}
}

func TestUnknownTemplatePanicsInTests(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("no panic for a template outside the allowlist")
		}
	}()
	_, _ = (&Runner{Host: "h"}).Command(Template("wp search-replace"))
}

func TestReadOnlySource(t *testing.T) {
	r := &Runner{Host: "h", Root: "/srv", ReadOnly: true}
	for _, tm := range Templates() {
		_, err := r.Command(tm, sample[tm]...)
		if tm.TargetOnly() != errors.Is(err, ErrNotAllowed) {
			t.Errorf("%s on a source: %v", tm, err)
		}
	}
	for _, p := range []string{"/srv/wp-content/themes/x/functions.php", "/etc/passwd", "/srv/wp-config.php.bak"} {
		if _, err := r.Command(Cat, p); !errors.Is(err, ErrNotAllowed) {
			t.Errorf("source cat of %s allowed", p)
		}
	}
	if _, err := r.Command(Cat, "/srv/.env"); err != nil {
		t.Errorf("source cat of .env: %v", err)
	}
}

func TestHostsRefusedBeforeExec(t *testing.T) {
	s := remotetest.New(t)
	for _, host := range []string{"-oProxyCommand=touch /tmp/x", "-oProxyCommand=x", "a b"} {
		r := &Runner{Host: host}
		err := r.Run(context.Background(), nil, nil, CoreVersion)
		if !errors.Is(err, ErrBadHost) {
			t.Errorf("host %q: %v, want ErrBadHost", host, err)
		}
	}
	r := &Runner{User: "-l", Host: "ok.example"}
	if err := r.Run(context.Background(), nil, nil, CoreVersion); !errors.Is(err, ErrBadHost) {
		t.Errorf("user -l: %v", err)
	}
	if n := len(s.Calls()); n != 0 {
		t.Errorf("ssh ran %d times for refused hosts", n)
	}
}

func TestArgvAndQuotedPath(t *testing.T) {
	s := remotetest.New(t)
	old := stdinIsTerminal
	t.Cleanup(func() { stdinIsTerminal = old })
	stdinIsTerminal = func() bool { return false }
	r := &Runner{User: "deploy", Host: "shop.example", Port: 2222, Root: "/srv/$(id)"}
	if err := r.Run(context.Background(), nil, nil, Cat, "/srv/$(id)/wp-config.php"); err != nil {
		t.Fatal(err)
	}
	stdinIsTerminal = func() bool { return true }
	r2 := &Runner{Host: "shop.example"}
	if err := r2.Run(context.Background(), nil, nil, CoreVersion); err != nil {
		t.Fatal(err)
	}
	r3 := &Runner{Host: "shop.example", BatchMode: true}
	if err := r3.Run(context.Background(), nil, nil, CoreVersion); err != nil {
		t.Fatal(err)
	}
	// An IPv6 host reaches ssh without its brackets.
	r4 := &Runner{User: "deploy", Host: "[2001:db8::1]", Port: 22}
	if err := r4.Run(context.Background(), nil, nil, CoreVersion); err != nil {
		t.Fatal(err)
	}
	if got := r4.Dest(); got != "deploy@[2001:db8::1]" {
		t.Errorf("Dest %q keeps the brackets for messages", got)
	}
	calls := s.Calls()
	want := [][]string{
		{"-p", "2222", "-o", "BatchMode=yes", "--", "deploy@shop.example", "cat -- '/srv/$(id)/wp-config.php'"},
		{"--", "shop.example", "wp core version --skip-plugins --skip-themes"},
		{"-o", "BatchMode=yes", "--", "shop.example", "wp core version --skip-plugins --skip-themes"},
		{"-p", "22", "--", "deploy@2001:db8::1", "wp core version --skip-plugins --skip-themes"},
	}
	if len(calls) != len(want) {
		t.Fatalf("calls %q", calls)
	}
	for i := range want {
		if !slices.Equal(calls[i], want[i]) {
			t.Errorf("call %d:\n got %q\nwant %q", i, calls[i], want[i])
		}
	}
	if got := r.Commands(); !slices.Equal(got, []string{"cat -- '/srv/$(id)/wp-config.php'"}) {
		t.Errorf("Commands %q", got)
	}
	checkAllowed(t, s)
}

// The remote shell reads a quoted word back as exactly the input.
func TestQuoteRoundTrip(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil || runtime.GOOS == "windows" {
		t.Skip("no POSIX sh")
	}
	for _, s := range []string{"", "plain", "it's", "'", "''", "$(id)", "`id`", "a b\tc", `\"`, "-n", "*", "~"} {
		out, err := exec.Command(sh, "-c", "printf '%s' "+Quote(s)).Output()
		if err != nil || string(out) != s {
			t.Errorf("Quote(%q) reads back as %q (%v)", s, out, err)
		}
	}
}

func TestCredentialsOnStdinOnly(t *testing.T) {
	s := remotetest.New(t)
	pw := "Canary-pw-" + "7f3a\"'\\x"
	pw = strings.ReplaceAll(pw, "'", "") // one quote kind, so the option file can carry it
	s.Canary(pw)
	r := &Runner{Host: "db.example"}
	creds := Creds{User: "shop", Password: pw, Host: "127.0.0.1", Port: 3306}
	defaults, err := MySQLDefaults(creds)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Run(context.Background(), WithCreds(defaults, nil), &bytes.Buffer{}, MySQLDump, "shop"); err != nil {
		t.Fatal(err)
	}
	pg, err := PGPassword(creds)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Run(context.Background(), WithCreds(pg, strings.NewReader("COPY x FROM stdin;\n")), nil, PSQL, "127.0.0.1", "5432", "app", "staging"); err != nil {
		t.Fatal(err)
	}
	for i, argv := range s.Calls() {
		if strings.Contains(strings.Join(argv, " "), "Canary-pw") {
			t.Errorf("call %d has the password in argv", i)
		}
	}
	if got := string(s.Stdin(0)); got != "[client]\nuser=\"shop\"\npassword='"+strings.ReplaceAll(pw, `\`, `\\`)+"'\nhost=\"127.0.0.1\"\nport=3306\n#roadie-end\n" {
		t.Errorf("mysqldump stdin %q", got)
	}
	if got := string(s.Stdin(1)); got != pw+"\nCOPY x FROM stdin;\n" {
		t.Errorf("psql stdin %q", got)
	}
	for _, c := range []Creds{{Password: "a\nb"}, {Password: `a'b"c`}} {
		if _, err := MySQLDefaults(c); !errors.Is(err, ErrCredential) {
			t.Errorf("MySQLDefaults(%q) = %v", c.Password, err)
		}
	}
	if _, err := PGPassword(Creds{Password: "a\nb"}); !errors.Is(err, ErrCredential) {
		t.Error("PGPassword took a line break")
	}
	checkAllowed(t, s)
}

// The mysql wrapper turns the stdin defaults into a private file and
// leaves the dump on stdin for mysql.
func TestMySQLWrapper(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil || runtime.GOOS == "windows" {
		t.Skip("no POSIX sh")
	}
	bin := t.TempDir()
	fake := "#!/bin/sh\nfor a; do case $a in --defaults-extra-file=*) cat \"${a#*=}\"; ls -l \"${a#*=}\" | cut -c1-10;; esac; done\necho ---\ncat\n"
	if err := os.WriteFile(filepath.Join(bin, "mysql"), []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd, err := (&Runner{Host: "h"}).Command(MySQL, "staging")
	if err != nil {
		t.Fatal(err)
	}
	defaults, _ := MySQLDefaults(Creds{User: "u", Password: "p"})
	c := exec.Command(sh, "-c", cmd)
	c.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	c.Stdin = WithCreds(defaults, strings.NewReader("INSERT 1;\nINSERT 2;\n"))
	out, err := c.Output()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	want := "[client]\nuser=\"u\"\npassword=\"p\"\n-rw-------\n---\nINSERT 1;\nINSERT 2;\n"
	if string(out) != want {
		t.Errorf("wrapper gave\n%s\nwant\n%s", out, want)
	}
}

func TestErrorMapping(t *testing.T) {
	tests := []struct {
		stderr string
		exit   int
		code   string
		msg    string
	}{
		{"No ED25519 host key is known for shop.example and you have requested strict checking.\nHost key verification failed.\n", 255, core.ErrSSHHostKey,
			"ssh-host-key: deploy@shop.example isn't in known_hosts. Connect once with ssh to check and add it; Roadie never skips host key checks."},
		{"The authenticity of host 'shop.example' can't be established.\nED25519 key fingerprint is SHA256:abcDEF123+/xyz.\nHost key verification failed.\n", 255, core.ErrSSHHostKey,
			"ssh-host-key: deploy@shop.example isn't in known_hosts (ED25519 SHA256:abcDEF123+/xyz). Connect once with ssh to check and add it; Roadie never skips host key checks."},
		{"deploy@shop.example: Permission denied (publickey).\n", 255, core.ErrSSH,
			"ssh: deploy@shop.example refused the key (Permission denied (publickey)). Check ssh deploy@shop.example works on its own; Roadie uses your ssh setup and never skips host key checks."},
		{"bash: line 1: wp: command not found\n", 127, core.ErrWPCLI,
			"wp-cli: wp isn't on PATH for deploy@shop.example. Install WP-CLI there, set remote.wp in .roadie/config.yaml to its path, or export the database yourself and use --from dump.sql.gz."},
		{"sh: 1: wp: not found\n", 127, core.ErrWPCLI, "wp-cli: wp isn't on PATH"},
		{"ssh: Could not resolve hostname shop.example: Name or service not known\n", 255, core.ErrSSH,
			"ssh: couldn't run a command on deploy@shop.example (ssh: Could not resolve hostname shop.example: Name or service not known)."},
	}
	for _, tt := range tests {
		s := remotetest.New(t)
		s.Reply(nil, tt.stderr, tt.exit)
		r := &Runner{User: "deploy", Host: "shop.example"}
		err := r.Run(context.Background(), nil, nil, CoreVersion)
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != tt.code || ce.Exit != 3 || !strings.Contains(ce.Msg, tt.msg) {
			t.Errorf("stderr %q:\n got %v\nwant %s: %s", tt.stderr, err, tt.code, tt.msg)
		}
	}
	// A failure of the command itself is a RunError with its stderr.
	s := remotetest.New(t)
	s.Reply(nil, "ERROR 1273 (HY000) at line 25: Unknown collation: 'utf8mb4_0900_ai_ci'\n", 1)
	r := &Runner{Host: "shop.example"}
	err := r.Run(context.Background(), nil, nil, DBImport)
	var re *RunError
	if !errors.As(err, &re) || re.Exit != 1 || !bytes.Contains(re.Stderr, []byte("ERROR 1273")) {
		t.Errorf("remote failure: %v", err)
	}
	// "cat: x: Permission denied" is the remote command's, not ssh's.
	s.Reply(nil, "cat: /srv/x: Permission denied\n", 1)
	if err := r.Run(context.Background(), nil, nil, Cat, "/srv/x"); !errors.As(err, &re) {
		t.Errorf("remote permission error read as ssh's: %v", err)
	}
	s.Reply(nil, "Error: Error establishing a database connection.\nroadie: wp db export failed\n", 0)
	if err := r.Run(context.Background(), nil, nil, Backup, "2026-10-26T14-02-03"); err == nil {
		t.Error("a failed backup export passed")
	}
}

func TestStreamsStdout(t *testing.T) {
	s := remotetest.New(t)
	dump := bytes.Repeat([]byte("INSERT INTO `wp_options` VALUES (1,'siteurl','https://shop.example','yes');\n"), 2000)
	s.Reply(dump, "", 0)
	out, err := (&Runner{Host: "shop.example", ReadOnly: true}).Output(context.Background(), nil, DBExport)
	if err != nil || !bytes.Equal(out, dump) {
		t.Fatalf("export: %d bytes, %v", len(out), err)
	}
	checkAllowed(t, s)
}

func TestList(t *testing.T) {
	src, all := List(false), List(true)
	if len(src) != 9 || len(all) != len(Templates()) {
		t.Errorf("List: %d source, %d in all", len(src), len(all))
	}
	if !slices.Contains(src, "wp --path=<path> db export - --skip-plugins --skip-themes --single-transaction --skip-triggers") {
		t.Errorf("List(false) = %q", src)
	}
	for _, c := range src {
		if strings.Contains(c, "import") || strings.Contains(c, "rm -f") {
			t.Errorf("source list has a target command: %s", c)
		}
	}
}
