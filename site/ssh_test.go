// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package site

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/site/remote"
	"github.com/RhythmShahriar/roadie/site/remote/remotetest"
)

// sshEP is an ssh endpoint for the shim.
func sshEP() core.Endpoint {
	return core.Endpoint{Kind: core.SSHSite, Display: "deploy@shop.example:/srv/shop", User: "deploy", Host: "shop.example", Path: "/srv/shop"}
}

func openSSHT(t *testing.T, sh *remotetest.Shim, o Options) core.Site {
	t.Helper()
	sh.Reply(nil, "", 0)
	s, err := Open(context.Background(), sshEP(), o)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// remoteCmd is the remote command string of a shim call.
func remoteCmd(argv []string) string { return argv[len(argv)-1] }

// assertAllowed fails when a recorded command is outside the allowlist.
func assertAllowed(t *testing.T, sh *remotetest.Shim) {
	t.Helper()
	for _, c := range sh.Calls() {
		if _, ok := remote.Allowed(remoteCmd(c)); !ok {
			t.Errorf("not in the allowlist: %s", remoteCmd(c))
		}
		for _, a := range c {
			if strings.Contains(a, "StrictHostKeyChecking") || strings.Contains(a, "UserKnownHostsFile") {
				t.Errorf("host key option in argv: %v", c)
			}
		}
	}
}

// useFakeDump makes dump.Open read the whole stream into one batch.
func useFakeDump(t *testing.T) {
	t.Helper()
	old := openDump
	openDump = func(_ context.Context, r io.Reader, _ dump.Options) (core.Source, error) {
		b, err := io.ReadAll(r)
		if err != nil {
			return nil, err
		}
		return testutil.FakeSource(nil, []*core.Batch{{Seq: 1, Pass: &core.Statement{Kind: core.StmtOther, Text: b}}}), nil
	}
	t.Cleanup(func() { openDump = old })
}

// streamSink stands in for dump.NewStreamSink: it writes every
// passthrough statement's text to w.
type streamSink struct {
	*testutil.Sink
	w io.Writer
}

func (s *streamSink) Write(ctx context.Context, b *core.Batch) error {
	if b.Pass != nil {
		if _, err := s.w.Write(b.Pass.Text); err != nil {
			return err
		}
	}
	return s.Sink.Write(ctx, b)
}

func useFakeStreamSink(t *testing.T) {
	t.Helper()
	old := newStreamSink
	newStreamSink = func(w io.Writer, _ dump.WriterOptions) core.Sink { return &streamSink{Sink: testutil.FakeSink(), w: w} }
	t.Cleanup(func() { newStreamSink = old })
}

func TestSSHSourceStreamsExport(t *testing.T) {
	sh := remotetest.New(t)
	s := openSSHT(t, sh, Options{ReadOnly: true})
	if s.Stack() != core.StackWordPress {
		t.Fatalf("stack %q", s.Stack())
	}
	useFakeDump(t)
	export := []byte("-- MySQL dump\nCREATE TABLE `wp_options` (x int);\n-- Dump completed\n")
	sh.Reply(export, "", 0)
	n := len(sh.Calls())
	src, err := s.Source(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := src.Next(context.Background())
	if err != nil || !bytes.Equal(b.Pass.Text, export) {
		t.Fatalf("streamed %q, %v", b.Pass.Text, err)
	}
	if _, err := src.Next(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("end: %v", err)
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	want := "wp --path='/srv/shop' db export - --skip-plugins --skip-themes --single-transaction --skip-triggers"
	if got := remoteCmd(sh.Calls()[n]); got != want {
		t.Fatalf("command\n got %s\nwant %s", got, want)
	}
	if _, err := s.Source(context.Background(), []string{"wp_options", "wp_users"}); err != nil {
		t.Fatal(err)
	}
	if got := remoteCmd(sh.Calls()[n+1]); !strings.Contains(got, "--tables='wp_options,wp_users'") {
		t.Fatalf("tables command %s", got)
	}
	assertAllowed(t, sh)
	if got := s.RemoteCommands(); len(got) != n+2 || got[n] != want {
		t.Fatalf("RemoteCommands %v", got)
	}
}

func TestSSHSourceExportFails(t *testing.T) {
	sh := remotetest.New(t)
	s := openSSHT(t, sh, Options{ReadOnly: true})
	useFakeDump(t)
	sh.Reply(nil, "bash: wp: command not found\n", 127)
	_, err := s.Source(context.Background(), nil)
	if !errors.Is(err, &core.Error{Code: core.ErrWPCLI}) {
		t.Fatalf("%v, want wp-cli", err)
	}
}

func TestSSHLaravelCredentialsOnStdin(t *testing.T) {
	ctx := context.Background()
	sh := remotetest.New(t)
	s := openSSHT(t, sh, Options{ReadOnly: true})
	pw := "canary-" + "pw-9431"
	sh.Canary(pw)
	ss := s.(*sshSite)
	ss.stack = core.StackLaravel
	sh.Reply([]byte("DB_CONNECTION=mysql\nDB_HOST=127.0.0.1\nDB_DATABASE=app\nDB_USERNAME=app\nDB_PASSWORD="+pw+"\n"), "", 0)
	id, err := s.DBIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if id.Server != "shop.example" || id.Host != "127.0.0.1" || id.Name != "app" {
		t.Fatalf("identity %+v", id)
	}
	useFakeDump(t)
	sh.Reply([]byte("-- dump"), "", 0)
	n := len(sh.Calls())
	src, err := s.Source(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = src.Close()
	if got := remoteCmd(sh.Calls()[n]); !strings.HasPrefix(got, "mysqldump --defaults-extra-file=/dev/stdin") || !strings.HasSuffix(got, " 'app'") {
		t.Fatalf("command %s", got)
	}
	if in := sh.Stdin(n); !bytes.Contains(in, []byte("password=\""+pw+"\"")) {
		t.Fatalf("credentials not on stdin: %q", in)
	}
	for _, c := range sh.Calls() {
		if strings.Contains(strings.Join(c, " "), pw) {
			t.Fatal("password in argv")
		}
	}
	assertAllowed(t, sh)
}

func TestSSHFiles(t *testing.T) {
	ctx := context.Background()
	sh := remotetest.New(t)
	s := openSSHT(t, sh, Options{})
	sh.Reply([]byte("f 120 wp-config.php\nd 4096 wp-content\nf 5 wp-content/a b.txt\nbroken line\n"), "", 0)
	list, err := s.List(ctx, ".", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0] != (core.FileInfo{Rel: "wp-config.php", Size: 120}) || !list[1].Dir || list[2].Rel != "wp-content/a b.txt" {
		t.Fatalf("list %+v", list)
	}
	if got := remoteCmd(sh.Calls()[len(sh.Calls())-1]); got != `find '/srv/shop' -maxdepth 2 -printf '%y %s %P\n'` {
		t.Fatalf("find %s", got)
	}
	sh.Reply(nil, "cat: /srv/shop/x: No such file or directory\n", 1)
	if _, err := s.ReadFile(ctx, "x"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
	// A new file gets the caller's mode.
	sh.Reply(nil, "", 0)
	sh.ReplyFor("ls ", nil, "ls: cannot access '/srv/shop/x': No such file or directory\n", 2)
	n := len(sh.Calls())
	if err := s.WriteFile(ctx, "wp-content/mu-plugins/roadie-staging.php", []byte("<?php // body"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := sh.Calls()[n:]
	if len(calls) != 3 || remoteCmd(calls[0]) != "mkdir -p -- '/srv/shop/wp-content/mu-plugins'" ||
		remoteCmd(calls[1]) != "ls -lnLd -- '/srv/shop/wp-content/mu-plugins/roadie-staging.php'" {
		t.Fatalf("write calls %v", calls)
	}
	w := remoteCmd(calls[2])
	if !strings.HasPrefix(w, "umask 077 && cat > '/srv/shop/wp-content/mu-plugins/.roadie-staging.php.roadie-") ||
		!strings.Contains(w, "&& chmod 0644 -- ") || !strings.HasSuffix(w, " '/srv/shop/wp-content/mu-plugins/roadie-staging.php'") {
		t.Fatalf("write command %s", w)
	}
	if string(sh.Stdin(n+2)) != "<?php // body" {
		t.Fatalf("stdin %q", sh.Stdin(n+2))
	}

	// An existing file keeps its mode, owner and group, whatever the
	// caller asks for (a deploy-owned 0644 wp-config.php that PHP-FPM
	// reads as another user must stay readable).
	sh.ReplyFor("ls ", []byte("-rw-r--r--+ 1 1001 33 3120 Oct 26 10:00 /srv/shop/wp-config.php\n"), "", 0)
	n = len(sh.Calls())
	if err := s.WriteFile(ctx, "wp-config.php", []byte("<?php // new"), 0o640); err != nil {
		t.Fatal(err)
	}
	calls = sh.Calls()[n:]
	if len(calls) != 3 {
		t.Fatalf("write calls %v", calls)
	}
	w = remoteCmd(calls[2])
	if !strings.Contains(w, "&& { chown 1001:33 -- ") || !strings.Contains(w, "|| chgrp 33 -- ") ||
		!strings.Contains(w, "&& chmod 0644 -- ") || strings.Contains(w, "0640") {
		t.Fatalf("write command %s", w)
	}
	if tm, ok := remote.Allowed(w); !ok || tm != remote.WriteKeep {
		t.Fatalf("write command %s is %q in the allowlist", w, tm)
	}

	// ls output Roadie can't read, or an ls that failed for another
	// reason, stops the write rather than guess a mode.
	for _, bad := range []struct {
		out, stderr string
		exit        int
	}{
		{"drwxr-xr-x 2 0 0 4096 Oct 26 10:00 /srv/shop/wp-config.php\n", "", 0},
		{"-rw-r--r-- 1 deploy www 10 x\n", "", 0},
		{"garbage\n", "", 0},
		{"", "ls: cannot access '/srv/shop/wp-config.php': Permission denied\n", 2},
	} {
		sh.ReplyFor("ls ", []byte(bad.out), bad.stderr, bad.exit)
		n = len(sh.Calls())
		if err := s.WriteFile(ctx, "wp-config.php", []byte("x"), 0o640); err == nil {
			t.Errorf("ls %q: wrote anyway", bad.out)
		}
		for _, c := range sh.Calls()[n:] {
			if strings.HasPrefix(remoteCmd(c), "umask") {
				t.Errorf("ls %q: sent %s", bad.out, remoteCmd(c))
			}
		}
	}
	sh.ReplyFor("", nil, "", 0)
	sh.Reply(nil, "", 0)
	if err := s.WriteFile(ctx, "../etc/x", nil, 0o644); err == nil {
		t.Fatal("wrote outside the site")
	}
	// A path with shell characters is sent single-quoted, exactly.
	if _, err := s.ReadFile(ctx, "a$(id)'b"); err != nil {
		t.Fatal(err)
	}
	if got := remoteCmd(sh.Calls()[len(sh.Calls())-1]); got != `cat -- '/srv/shop/a$(id)'\''b'` {
		t.Fatalf("quoted %s", got)
	}
	if err := s.(*sshSite).EvalFile(ctx, []byte("<?php echo 1;")); err != nil {
		t.Fatal(err)
	}
	if got := remoteCmd(sh.Calls()[len(sh.Calls())-1]); got != "wp --path='/srv/shop' eval-file - --skip-plugins --skip-themes" {
		t.Fatalf("eval-file %s", got)
	}
	if _, err := s.WP(ctx, "plugin", "list"); !errors.Is(err, remote.ErrNotAllowed) {
		t.Fatalf("wp plugin list: %v", err)
	}
	assertAllowed(t, sh)
}

func TestSSHBackup(t *testing.T) {
	sh := remotetest.New(t)
	s := openSSHT(t, sh, Options{})
	fixClock(t)
	n := len(sh.Calls())
	p, restore, err := s.Backup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	calls := sh.Calls()[n:]
	want := "set -C && umask 077 && mkdir -p ~/.roadie-backups && { wp --path='/srv/shop' db export - --skip-plugins --skip-themes --single-transaction || echo 'roadie: wp db export failed' >&2; } | gzip > ~/.roadie-backups/2026-10-26T08-02-03.sql.gz"
	if len(calls) != 1 || remoteCmd(calls[0]) != want {
		t.Fatalf("backup calls %v", calls)
	}
	if p != "~/.roadie-backups/2026-10-26T08-02-03.sql.gz" {
		t.Fatalf("path %s", p)
	}
	if restore != "ssh deploy@shop.example 'gunzip -c ~/.roadie-backups/2026-10-26T08-02-03.sql.gz | wp --path=/srv/shop db import -'" {
		t.Fatalf("restore %s", restore)
	}
	sh.Reply(nil, "roadie: wp db export failed\n", 0)
	if _, _, err := s.Backup(context.Background()); err == nil {
		t.Fatal("a failed export passed as a backup")
	}
	// set -C refused to overwrite a backup made in the same second.
	sh.Reply(nil, "sh: 1: cannot create /home/deploy/.roadie-backups/2026-10-26T08-02-03.sql.gz: File exists\n", 2)
	_, _, err = s.Backup(context.Background())
	if err == nil || err.Error() != "backup: ~/.roadie-backups/2026-10-26T08-02-03.sql.gz already exists on deploy@shop.example: another backup started there in the same second. Run again" {
		t.Fatalf("same-second backup: %v", err)
	}
	// Any other failure is not read as a taken name.
	sh.Reply(nil, "gzip: stdout: No space left on device\n", 1)
	if _, _, err = s.Backup(context.Background()); err == nil || strings.Contains(err.Error(), "already exists") {
		t.Fatalf("disk full read as a taken name: %v", err)
	}
	assertAllowed(t, sh)
}

func TestBackupExistsRE(t *testing.T) {
	for _, tt := range []struct {
		stderr string
		want   bool
	}{
		{"sh: 1: cannot create /home/u/.roadie-backups/x.sql.gz: File exists", true},     // dash
		{"sh: can't create /home/u/.roadie-backups/x.sql.gz: File exists", true},         // busybox
		{"bash: /home/u/.roadie-backups/x.sql.gz: cannot overwrite existing file", true}, // bash
		{"zsh:1: file exists: /home/u/.roadie-backups/x.sql.gz", true},                   // zsh
		{"mkdir: cannot create directory '/home/u/.roadie-backups': File exists", false}, // a file in the folder's place
		{"Error: Error establishing a database connection.", false},
		{"gzip: stdout: No space left on device", false},
	} {
		if got := backupExistsRE.MatchString(tt.stderr); got != tt.want {
			t.Errorf("%q: %v, want %v", tt.stderr, got, tt.want)
		}
	}
}

// The backup command run twice in one second by real shells: the
// second refuses to overwrite the first backup.
func TestBackupNoClobberUnderSh(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX sh")
	}
	bin := t.TempDir()
	wp := "#!/bin/sh\nprintf -- '-- dump %s\\n' \"$ROADIE_TEST_RUN\"\n"
	if err := os.WriteFile(filepath.Join(bin, "wp"), []byte(wp), 0o700); err != nil { // #nosec G306 -- a test script that must run
		t.Fatal(err)
	}
	cmd, err := (&remote.Runner{Host: "h", Root: "/srv/shop"}).Command(remote.Backup, "2026-10-26T08-02-03")
	if err != nil {
		t.Fatal(err)
	}
	ran := 0
	for _, name := range []string{"sh", "bash", "zsh"} {
		shell, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		ran++
		home := t.TempDir()
		run := func(n string) (string, error) {
			c := exec.Command(shell, "-c", cmd) // #nosec G204 -- the allowlisted backup command with a fake wp
			c.Env = append(os.Environ(), "HOME="+home, "ROADIE_TEST_RUN="+n, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			out, err := c.CombinedOutput()
			return string(out), err
		}
		if out, err := run("first"); err != nil {
			t.Fatalf("%s: first backup: %v: %s", name, err, out)
		}
		out, err := run("second")
		if err == nil {
			t.Fatalf("%s: second backup in the same second overwrote the first", name)
		}
		if !backupExistsRE.MatchString(out) {
			t.Errorf("%s: stderr %q not read as a taken name", name, out)
		}
		f, err := os.Open(filepath.Join(home, ".roadie-backups", "2026-10-26T08-02-03.sql.gz"))
		if err != nil {
			t.Fatal(err)
		}
		zr, err := gzip.NewReader(f)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(zr)
		_ = f.Close()
		if string(b) != "-- dump first\n" {
			t.Fatalf("%s: backup holds %q", name, b)
		}
		if fi, _ := os.Stat(f.Name()); fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s: backup mode %v", name, fi.Mode().Perm())
		}
	}
	if ran == 0 {
		t.Skip("no POSIX sh")
	}
}

// createTable is a CREATE TABLE batch as the dump writer gets it.
func createTable(seq uint64, name string) *core.Batch {
	return &core.Batch{Seq: seq, Pass: &core.Statement{Kind: core.StmtCreateTable, Text: []byte("CREATE TABLE `" + name + "` (id int);\n")}}
}

// dropTable is a DROP TABLE IF EXISTS batch as the dump writer gets it.
func dropTable(seq uint64, name string) *core.Batch {
	return &core.Batch{Seq: seq, Pass: &core.Statement{Kind: core.StmtDropTable, Text: []byte("DROP TABLE IF EXISTS `" + name + "`;\n")}}
}

func TestSSHSinkImport(t *testing.T) {
	ctx := context.Background()
	useFakeStreamSink(t)
	tests := []struct {
		name   string
		stderr string
		exit   int
		tables int
		code   string
		state  core.TargetState
		msg    string
	}{
		{"ok", "", 0, 2, "", core.Replaced, ""},
		{"collation", "ERROR 1273 (HY000) at line 25: Unknown collation: 'utf8mb4_0900_ai_ci'\n", 1, 1, core.ErrImport, core.Untouched,
			"import: the database on deploy@shop.example:/srv/shop doesn't know collation utf8mb4_0900_ai_ci. Run again with --fix-collation. The target is untouched."},
		{"collation after a drop", "ERROR 1273 (HY000) at line 25: Unknown collation: 'utf8mb4_0900_ai_ci'\n", 1, 1, core.ErrImport, core.Mixed,
			"import: the database on deploy@shop.example:/srv/shop doesn't know collation utf8mb4_0900_ai_ci, after 0 of 61 tables. The target is in a mixed state: some tables are pseudonymised, the rest still hold the earlier copy. Run again with --fix-collation."},
		{"lost after a drop", "Connection to shop.example closed by remote host.\n", 255, 0, core.ErrImportMixed, core.Mixed, ""},
		{"collation later", "ERROR 1273 (HY000) at line 99: Unknown collation: 'utf8mb4_0900_ai_ci'\n", 1, 3, core.ErrImport, core.Mixed, ""},
		{"broken after tables", "ERROR 1062 (23000) at line 52: Duplicate entry 'alice@example.org' for key 'x'\n", 1, 2, core.ErrImportMixed, core.Mixed,
			"import: wp db import on deploy@shop.example:/srv/shop failed (MySQL ERROR 1062) after 2 of 61 tables. The target is in a mixed state: some tables are pseudonymised, the rest still hold the earlier copy. Run the same command again."},
		{"connection lost", "Connection to shop.example closed by remote host.\n", 255, 3, core.ErrImportMixed, core.Mixed, ""},
		{"fails first", "ERROR 1045 (28000): Access denied\n", 1, 0, core.ErrImport, core.Untouched,
			"import: wp db import on deploy@shop.example:/srv/shop failed (MySQL ERROR 1045) before any table was replaced. The target is untouched."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sh := remotetest.New(t)
			s := openSSHT(t, sh, Options{})
			sh.Reply(nil, tt.stderr, tt.exit)
			n := len(sh.Calls())
			sk, err := s.Sink(ctx)
			if err != nil {
				t.Fatal(err)
			}
			sk.(TableCounter).ExpectTables(61)
			// A case named "... drop" writes DROP TABLE IF EXISTS before
			// each CREATE, as most dumps do.
			drops := strings.HasSuffix(tt.name, "drop")
			var werr error
			for i := 0; i < tt.tables && werr == nil; i++ {
				if drops {
					werr = sk.Write(ctx, dropTable(uint64(2*i+1), "wp_t"+string(rune('a'+i))))
				}
				if werr == nil {
					werr = sk.Write(ctx, createTable(uint64(2*i+2), "wp_t"+string(rune('a'+i))))
				}
			}
			if drops && tt.tables == 0 && werr == nil {
				werr = sk.Write(ctx, dropTable(1, "wp_ta"))
			}
			if werr == nil {
				werr = sk.Commit(ctx)
			}
			if remoteCmd(sh.Calls()[n]) != "wp --path='/srv/shop' db import -" {
				t.Fatalf("import command %s", remoteCmd(sh.Calls()[n]))
			}
			if tt.code == "" {
				if werr != nil || sk.State() != core.Replaced {
					t.Fatalf("%v state %s", werr, sk.State())
				}
				if !bytes.Contains(sh.Stdin(n), []byte("CREATE TABLE `wp_tb`")) {
					t.Fatalf("stdin %q", sh.Stdin(n))
				}
				return
			}
			var ce *core.Error
			if !errors.As(werr, &ce) || ce.Code != tt.code || ce.Exit != 3 || ce.State != tt.state || sk.State() != tt.state {
				t.Fatalf("%v (%+v), want %s %s", werr, ce, tt.code, tt.state)
			}
			if !core.KnownID(ce.Code) {
				t.Fatalf("unknown code %s", ce.Code)
			}
			if strings.Contains(ce.Msg, "alice") {
				t.Fatalf("a row value reached the message: %s", ce.Msg)
			}
			if tt.msg != "" && ce.Msg != tt.msg {
				t.Fatalf("message\n got %s\nwant %s", ce.Msg, tt.msg)
			}
		})
	}
}

func TestSSHSinkNotBuilt(t *testing.T) {
	sh := remotetest.New(t)
	s := openSSHT(t, sh, Options{})
	_, err := s.Sink(context.Background())
	if errors.Is(err, core.ErrNotBuilt) {
		t.Skip("needs the io lane")
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestSSHRefusedHosts(t *testing.T) {
	sh := remotetest.New(t)
	for _, h := range []string{"-oProxyCommand=touch /tmp/x", "a b", "a;b"} {
		e := sshEP()
		e.Host = h
		if _, err := Open(context.Background(), e, Options{}); !errors.Is(err, remote.ErrBadHost) {
			t.Errorf("%q: %v", h, err)
		}
	}
	if len(sh.Calls()) != 0 {
		t.Fatalf("ssh ran for a refused host: %v", sh.Calls())
	}
}

func TestParseFind(t *testing.T) {
	got := parseFind([]byte("d 4096 uploads\nf 10 uploads/x.sql\nl 7 link\n\nf x bad\n"), "wp-content")
	if len(got) != 3 || got[0] != (core.FileInfo{Rel: "wp-content/uploads", Dir: true}) || got[1].Size != 10 || got[2].Rel != "wp-content/link" {
		t.Fatalf("%+v", got)
	}
}

func TestParseFindDropsEscapes(t *testing.T) {
	out := []byte("f 1 ..\nf 1 ../x\nd 0 /etc\nf 1 a/../../b\nf 1 .\nf 2 a/./b\n")
	for _, base := range []string{".", "wp-content"} {
		got := parseFind(out, base)
		want := "a/b"
		if base != "." {
			want = base + "/a/b"
		}
		if len(got) != 1 || got[0].Rel != want || got[0].Size != 2 {
			t.Fatalf("base %q: %+v", base, got)
		}
	}
}

// TestSSHRefusedPaths: every remote command single-quotes the site folder,
// so a path that relies on ~ or is relative never reaches ssh.
func TestSSHRefusedPaths(t *testing.T) {
	sh := remotetest.New(t)
	for _, p := range []string{"~/www", "~deploy/www", "~", "www", ""} {
		e := sshEP()
		e.Path = p
		_, err := Open(context.Background(), e, Options{})
		if err == nil || !strings.Contains(err.Error(), "needs the absolute site folder on the host") {
			t.Errorf("%q: %v", p, err)
		}
	}
	if len(sh.Calls()) != 0 {
		t.Fatalf("ssh ran for a refused path: %v", sh.Calls())
	}
}

// The stat and write-keep commands run by a real POSIX sh keep an
// existing file's mode, which parseLs reads from real ls output.
func TestWriteKeepRunsUnderSh(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil || runtime.GOOS == "windows" {
		t.Skip("no POSIX sh")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "wp-config.php")
	if err := os.WriteFile(p, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	r := &remote.Runner{Host: "h"}
	run := func(cmd string, stdin string) []byte {
		t.Helper()
		c := exec.Command(sh, "-c", cmd) // #nosec G204 -- an allowlisted command on temp files
		c.Stdin = strings.NewReader(stdin)
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v: %s", cmd, err, out)
		}
		return out
	}
	statCmd, err := r.Command(remote.Stat, p)
	if err != nil {
		t.Fatal(err)
	}
	st, err := parseLs(run(statCmd, ""))
	if err != nil {
		t.Fatal(err)
	}
	if st.mode != 0o644 || st.uid != strconv.Itoa(os.Getuid()) || st.gid != strconv.Itoa(os.Getgid()) {
		t.Fatalf("parseLs %+v", st)
	}
	tmp := filepath.Join(dir, ".wp-config.php.roadie-t")
	cmd, err := r.Command(remote.WriteKeep, tmp, st.uid, st.gid, tmp, st.gid, tmp, "0644", tmp, tmp, p)
	if err != nil {
		t.Fatal(err)
	}
	run(cmd, "new")
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "new" || fi.Mode().Perm() != 0o644 {
		t.Fatalf("after write: %q %v", b, fi.Mode())
	}
}

func TestParseLs(t *testing.T) {
	tests := []struct {
		in   string
		mode fs.FileMode
		ok   bool
	}{
		{"-rw-r--r-- 1 1001 33 10 Oct 26 10:00 /x\n", 0o644, true},
		{"-rw-r-----. 1 0 0 10 Oct 26 10:00 /x", 0o640, true},
		{"-r-------- 1 1 2 10 Oct 26 10:00 /x", 0o400, true},
		{"-rwsr-xr-T 1 1 2 10 Oct 26 10:00 /x", 0o754, true},
		{"drwxr-xr-x 2 0 0 4096 Oct 26 10:00 /x", 0, false},
		{"lrwxrwxrwx 1 0 0 4 Oct 26 10:00 /x -> /y", 0, false},
		{"-rw-r--r-- 1 deploy www 10 x", 0, false},
		{"-rw-r?-r-- 1 1 2 10 x", 0, false},
		{"-rw-r--r--", 0, false},
		{"", 0, false},
	}
	for _, tt := range tests {
		st, err := parseLs([]byte(tt.in))
		if (err == nil) != tt.ok || tt.ok && st.mode != tt.mode {
			t.Errorf("parseLs(%q) = %+v, %v", tt.in, st, err)
		}
	}
}

// A host whose find lacks -printf (BSD, macOS, busybox) gets a clear
// error, not a bare RunError; other find failures pass through.
func TestSSHFindWithoutPrintf(t *testing.T) {
	ctx := context.Background()
	sh := remotetest.New(t)
	s := openSSHT(t, sh, Options{})
	for _, stderr := range []string{"find: -printf: unknown primary or operator\n", "find: unrecognized: -printf\n"} {
		sh.Reply(nil, stderr, 1)
		_, err := s.List(ctx, ".", 1)
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != core.ErrSSH || ce.Exit != 3 ||
			ce.Msg != "ssh: find on deploy@shop.example has no -printf, so it isn't GNU find. Roadie lists remote files with GNU find; install findutils there." {
			t.Fatalf("List with %q: %v", stderr, err)
		}
	}
	sh.Reply(nil, "find: '/srv/shop': Permission denied\n", 1)
	_, err := s.List(ctx, ".", 1)
	var re *remote.RunError
	if !errors.As(err, &re) {
		t.Fatalf("other find failure: %v", err)
	}
}

// Export hands over the raw export; a failed export is the stream's
// read error.
func TestSSHExport(t *testing.T) {
	sh := remotetest.New(t)
	s := openSSHT(t, sh, Options{ReadOnly: true})
	ex, ok := s.(interface {
		Export(context.Context) (io.ReadCloser, error)
	})
	if !ok {
		t.Fatal("sshSite has no Export")
	}
	export := []byte("-- MySQL dump\nCREATE TABLE `wp_options` (x int);\n")
	sh.Reply(export, "", 0)
	rc, err := ex.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(rc)
	if err != nil || !bytes.Equal(b, export) {
		t.Fatalf("read %q, %v", b, err)
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	sh.Reply(nil, "bash: wp: command not found\n", 127)
	rc, err = ex.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(rc); !errors.Is(err, &core.Error{Code: core.ErrWPCLI}) {
		t.Fatalf("%v, want wp-cli", err)
	}
	_ = rc.Close()
	assertAllowed(t, sh)
}

// Exists asks the host once per path until the site's files change.
func TestSSHExistsCached(t *testing.T) {
	sh := remotetest.New(t)
	s := openSSHT(t, sh, Options{})
	ctx := context.Background()
	count := func() int {
		n := 0
		for _, c := range sh.Calls() {
			if strings.Contains(remoteCmd(c), "wp-content/plugins/x/x.php") {
				n++
			}
		}
		return n
	}
	for range 3 {
		if ok, err := s.Exists(ctx, "wp-content/plugins/x/x.php"); err != nil || !ok {
			t.Fatalf("exists %v %v", ok, err)
		}
	}
	if n := count(); n != 1 {
		t.Errorf("%d asks, want 1", n)
	}
	if err := s.Remove(ctx, "wp-content/plugins/x/x.php"); err != nil {
		t.Fatal(err)
	}
	sh.Reply(nil, "find: '/srv/shop/wp-content/plugins/x/x.php': No such file or directory\n", 1)
	if ok, err := s.Exists(ctx, "wp-content/plugins/x/x.php"); err != nil || ok {
		t.Errorf("after Remove: %v %v", ok, err)
	}
	if n := count(); n != 4 {
		t.Errorf("%d calls, want 4 (the remove, then test and find again)", n)
	}
}
