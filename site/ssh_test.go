// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package site

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
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
	sh.Reply(nil, "", 0)
	n := len(sh.Calls())
	if err := s.WriteFile(ctx, "wp-content/mu-plugins/roadie-staging.php", []byte("<?php // body"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := sh.Calls()[n:]
	if len(calls) != 2 || remoteCmd(calls[0]) != "mkdir -p -- '/srv/shop/wp-content/mu-plugins'" {
		t.Fatalf("write calls %v", calls)
	}
	w := remoteCmd(calls[1])
	if !strings.HasPrefix(w, "umask 077 && cat > '/srv/shop/wp-content/mu-plugins/.roadie-staging.php.roadie-") ||
		!strings.Contains(w, "&& chmod 0644 -- ") || !strings.HasSuffix(w, " '/srv/shop/wp-content/mu-plugins/roadie-staging.php'") {
		t.Fatalf("write command %s", w)
	}
	if string(sh.Stdin(n+1)) != "<?php // body" {
		t.Fatalf("stdin %q", sh.Stdin(n+1))
	}
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
	if got := remoteCmd(sh.Calls()[len(sh.Calls())-1]); got != "wp --path='/srv/shop' eval-file -" {
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
	want := "umask 077 && mkdir -p ~/.roadie-backups && { wp --path='/srv/shop' db export - --skip-plugins --skip-themes --single-transaction || echo 'roadie: wp db export failed' >&2; } | gzip > ~/.roadie-backups/2026-10-26T08-02-03.sql.gz"
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
	assertAllowed(t, sh)
}

// createTable is a CREATE TABLE batch as the dump writer gets it.
func createTable(seq uint64, name string) *core.Batch {
	return &core.Batch{Seq: seq, Pass: &core.Statement{Kind: core.StmtCreateTable, Text: []byte("CREATE TABLE `" + name + "` (id int);\n")}}
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
			var werr error
			for i := 0; i < tt.tables && werr == nil; i++ {
				werr = sk.Write(ctx, createTable(uint64(i+1), "wp_t"+string(rune('a'+i))))
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
