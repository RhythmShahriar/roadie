// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package site

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/db"
	"github.com/RhythmShahriar/roadie/dump"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/site/remote"
	"github.com/RhythmShahriar/roadie/site/remote/remotetest"
)

func TestMain(m *testing.M) {
	// No test reaches a real database server to ask who it is.
	instanceOf = func(context.Context, core.Endpoint, core.Site) string { return "" }
	_, cleanup, err := remotetest.Install()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// wpConfig is a wp-config.php with literal settings; pw is the password.
func wpConfig(pw string) string {
	return "<?php\ndefine( 'DB_NAME', 'staging' );\ndefine( 'DB_USER', 'stage' );\ndefine( 'DB_PASSWORD', '" + pw + "' );\n" +
		"define( 'DB_HOST', 'db.internal:3307' );\n$table_prefix = 'wp_';\n/* That's all, stop editing! Happy publishing. */\nrequire_once ABSPATH . 'wp-settings.php';\n"
}

// writeTree writes files (relative paths) under a new temp folder.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func openLocalT(t *testing.T, dir string, o Options) core.Site {
	t.Helper()
	s, err := Open(context.Background(), core.Endpoint{Kind: core.LocalSite, Display: dir, Path: dir}, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// fakeConn stands in for the io lane's database connection.
type fakeConn struct {
	e       core.Endpoint
	mode    db.Mode
	src     *testutil.Source
	sink    *testutil.Sink
	closed  bool
	sinkOpt db.SinkOptions
}

func (c *fakeConn) Identity(context.Context) (core.DBIdentity, error) { return core.DBIdentity{}, nil }
func (c *fakeConn) Source(context.Context, []string) (core.Source, error) {
	return c.src, nil
}
func (c *fakeConn) Sink(_ context.Context, o db.SinkOptions) (core.Sink, error) {
	c.sinkOpt = o
	return c.sink, nil
}
func (c *fakeConn) Close() error { c.closed = true; return nil }

// useFakeDB replaces db.Open for the test and returns the connections made.
func useFakeDB(t *testing.T) *[]*fakeConn {
	t.Helper()
	var conns []*fakeConn
	old := openDB
	openDB = func(_ context.Context, e core.Endpoint, m db.Mode) (dbConn, error) {
		batch := &core.Batch{Seq: 1, Pass: &core.Statement{Kind: core.StmtOther, Text: []byte("-- fake\n")}, First: true, Last: true}
		c := &fakeConn{e: e, mode: m, src: testutil.FakeSource(nil, []*core.Batch{batch}), sink: testutil.FakeSink()}
		conns = append(conns, c)
		return c, nil
	}
	t.Cleanup(func() { openDB = old })
	return &conns
}

func TestLocalWordPressSite(t *testing.T) {
	ctx := context.Background()
	pw := "pw-" + "canary-71"
	dir := writeTree(t, map[string]string{
		"wp-config.php":                 wpConfig(pw),
		"wp-includes/version.php":       "<?php $wp_version = '6.8';",
		"wp-content/uploads/a.jpg":      "12345",
		"wp-content/uploads/2026/b.jpg": "1",
	})
	s := openLocalT(t, dir, Options{})
	if s.Stack() != core.StackWordPress {
		t.Fatalf("stack %q", s.Stack())
	}
	b, err := s.ReadFile(ctx, "wp-config.php")
	if err != nil || !bytes.Contains(b, []byte("DB_NAME")) {
		t.Fatalf("ReadFile: %v", err)
	}
	if _, err := s.ReadFile(ctx, "missing.php"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
	}
	for _, rel := range []string{"../outside", "/etc/passwd", "a/../../b"} {
		if _, err := s.ReadFile(ctx, rel); err == nil {
			t.Fatalf("%s: read outside the site", rel)
		}
	}
	list, err := s.List(ctx, "wp-content", 2)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, fi := range list {
		got = append(got, fmt.Sprintf("%s:%d:%v", fi.Rel, fi.Size, fi.Dir))
	}
	want := "wp-content/uploads:0:true wp-content/uploads/2026:0:true wp-content/uploads/a.jpg:5:false"
	if strings.Join(got, " ") != want {
		t.Fatalf("List:\n got %s\nwant %s", strings.Join(got, " "), want)
	}
	all, _ := s.List(ctx, ".", 0)
	if len(all) != 8 {
		t.Fatalf("List all: %d entries %v", len(all), all)
	}
	id, err := s.DBIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if id != (core.DBIdentity{Engine: core.MySQL, Host: "db.internal", Port: 3307, Name: "staging", User: "stage", Prefix: "wp_"}) {
		t.Fatalf("identity %+v", id)
	}
	conns := useFakeDB(t)
	src, err := s.Source(ctx, []string{"wp_options"})
	if err != nil {
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	c := (*conns)[0]
	if c.mode != db.ReadOnly || !c.closed {
		t.Fatalf("source connection mode %v closed %v", c.mode, c.closed)
	}
	if c.e.Password != pw || strings.Contains(c.e.DSN, pw) || strings.Contains(c.e.Display, pw) {
		t.Fatalf("password handling: DSN %q Display %q", c.e.DSN, c.e.Display)
	}
	if c.e.DSN != "mysql://stage@db.internal:3307/staging" || c.e.Path != "staging" || c.e.Driver != core.MySQL {
		t.Fatalf("endpoint %+v", c.e)
	}
	if _, err := s.WP(ctx, "core", "version"); !errors.Is(err, core.ErrNoWPCLI) {
		t.Fatalf("local WP: %v, want ErrNoWPCLI (no PHP runs on local files)", err)
	}
	sk, err := s.Sink(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := sk.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if c := (*conns)[1]; c.mode != db.ReadWrite || !c.closed {
		t.Fatalf("sink connection mode %v closed %v", c.mode, c.closed)
	}
}

func TestLocalLaravelSQLite(t *testing.T) {
	ctx := context.Background()
	dir := writeTree(t, map[string]string{
		"artisan":                  "#!/usr/bin/env php",
		".env":                     "APP_ENV=staging\nDB_CONNECTION=sqlite\nDB_DATABASE=database/database.sqlite\n",
		"database/database.sqlite": "",
	})
	s := openLocalT(t, dir, Options{FixCollation: true})
	if s.Stack() != core.StackLaravel {
		t.Fatalf("stack %q", s.Stack())
	}
	conns := useFakeDB(t)
	sk, err := s.Sink(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = sk.Abort(ctx)
	c := (*conns)[0]
	wantPath := filepath.Join(dir, "database", "database.sqlite")
	if c.e.Driver != core.SQLite || c.e.Path != wantPath || !strings.HasPrefix(c.e.DSN, "sqlite:///") {
		t.Fatalf("endpoint %+v", c.e)
	}
	if !c.sinkOpt.FixCollation {
		t.Fatal("FixCollation not passed to the import")
	}
	id, _ := s.DBIdentity(ctx)
	if id.Engine != core.SQLite || id.Name != wantPath {
		t.Fatalf("identity %+v", id)
	}
}

// TestLocalSQLiteRealDriver runs the same through the io lane's driver.
func TestLocalSQLiteRealDriver(t *testing.T) {
	dir := writeTree(t, map[string]string{"artisan": "", ".env": "DB_CONNECTION=sqlite\nDB_DATABASE=db.sqlite\n", "db.sqlite": ""})
	s := openLocalT(t, dir, Options{ReadOnly: true})
	src, err := s.Source(context.Background(), nil)
	if errors.Is(err, core.ErrNotBuilt) {
		t.Skip("needs the io lane")
	}
	if err != nil {
		t.Fatal(err)
	}
	_ = src.Close()
}

func TestDetectStack(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		files map[string]string
		want  core.Stack
	}{
		{map[string]string{"wp-config.php": "<?php"}, core.StackWordPress},
		{map[string]string{"wp-includes/version.php": "<?php"}, core.StackWordPress},
		{map[string]string{"artisan": "", ".env": ""}, core.StackLaravel},
		{map[string]string{"artisan": ""}, core.StackSQL},
		{map[string]string{".env": ""}, core.StackSQL},
		{map[string]string{"index.html": ""}, core.StackSQL},
	}
	for _, tt := range tests {
		s := openLocalT(t, writeTree(t, tt.files), Options{ReadOnly: true})
		got, err := DetectStack(ctx, s)
		if err != nil || got != tt.want {
			t.Errorf("%v: %q %v, want %q", tt.files, got, err, tt.want)
		}
	}
	d, _ := Open(ctx, core.Endpoint{Kind: core.DSN, Driver: core.MySQL, Host: "db", Path: "shop"}, Options{})
	if got, _ := DetectStack(ctx, d); got != core.StackSQL {
		t.Errorf("DSN: %q", got)
	}
	if _, err := Open(ctx, core.Endpoint{Kind: core.DumpFile, Path: "x.sql"}, Options{}); !errors.Is(err, errNotSite) {
		t.Errorf("dump file opened as a site: %v", err)
	}
}

func TestSiteConfigUnresolved(t *testing.T) {
	ctx := context.Background()
	cfg := strings.Replace(wpConfig("x"), "'x'", "getenv('DB_PASS')", 1)
	dir := writeTree(t, map[string]string{"wp-config.php": cfg})
	for _, ro := range []bool{false, true} {
		s := openLocalT(t, dir, Options{ReadOnly: ro})
		_, err := s.DBIdentity(ctx)
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != core.ErrSiteConfig || ce.Exit != 3 {
			t.Fatalf("ro=%v: %v, want site-config", ro, err)
		}
		want := "site-config: " + filepath.Join(dir, "wp-config.php") + " sets DB_PASSWORD from getenv('DB_PASS'), which Roadie can't run. Pass the target as a DSN with ROADIE_TO_PASSWORD, or use @staging through WP-CLI."
		if ro {
			want = strings.NewReplacer("the target", "the source", "ROADIE_TO_PASSWORD", "ROADIE_FROM_PASSWORD", "@staging", "@prod").Replace(want)
		}
		if runtime.GOOS == "windows" {
			want = strings.Replace(want, filepath.Join(dir, "wp-config.php"), dir+"\\wp-config.php", 1)
		}
		if ce.Msg != want {
			t.Fatalf("message\n got %s\nwant %s", ce.Msg, want)
		}
		if _, err := s.Source(ctx, nil); !errors.Is(err, &core.Error{Code: core.ErrSiteConfig}) {
			t.Fatalf("Source: %v", err)
		}
	}
	// An expression that could hold the secret itself is never echoed.
	secret := "pa" + "ss-canary"
	cfg = strings.Replace(wpConfig("x"), "'x'", "'pa' . 'ss-canary'", 1)
	s := openLocalT(t, writeTree(t, map[string]string{"wp-config.php": cfg}), Options{})
	_, err := s.DBIdentity(ctx)
	if err == nil || strings.Contains(err.Error(), "canary") || !strings.Contains(err.Error(), "from a PHP expression") {
		t.Fatalf("%v (secret %q must not appear)", err, secret)
	}
	// Fallback and concatenation forms carry a literal: never echoed.
	for _, expr := range []string{
		"getenv('DB_PASSWORD') ?: '" + secret + "'",
		"$_ENV['DB_PASSWORD'] ?? '" + secret + "'",
		"getenv('DB_' . '" + secret + "')",
	} {
		cfg = strings.Replace(wpConfig("x"), "'x'", expr, 1)
		s = openLocalT(t, writeTree(t, map[string]string{"wp-config.php": cfg}), Options{})
		_, err = s.DBIdentity(ctx)
		if err == nil || strings.Contains(err.Error(), "canary") || !strings.Contains(err.Error(), "from a PHP expression") {
			t.Fatalf("%s: %v (secret must not appear)", expr, err)
		}
	}
	// A missing setting and an unknown Laravel driver are site-config too.
	s = openLocalT(t, writeTree(t, map[string]string{"wp-config.php": "<?php define('DB_NAME','x');"}), Options{})
	if _, err := s.DBIdentity(ctx); !errors.Is(err, &core.Error{Code: core.ErrSiteConfig}) || !strings.Contains(err.Error(), "doesn't set DB_USER") {
		t.Fatalf("missing: %v", err)
	}
	s = openLocalT(t, writeTree(t, map[string]string{"artisan": "", ".env": "DB_CONNECTION=sqlsrv\n"}), Options{})
	if _, err := s.DBIdentity(ctx); !errors.Is(err, &core.Error{Code: core.ErrSiteConfig}) {
		t.Fatalf("sqlsrv: %v", err)
	}
	s = openLocalT(t, writeTree(t, map[string]string{"artisan": "", ".env": "DB_CONNECTION=mysql\nDB_DATABASE=app\nDB_PASSWORD=${SECRET}\n"}), Options{})
	if _, err := s.DBIdentity(ctx); !errors.Is(err, &core.Error{Code: core.ErrSiteConfig}) || !strings.Contains(err.Error(), "sets DB_PASSWORD from another variable") {
		t.Fatalf("env reference: %v", err)
	}
}

func TestLaravelMySQLIdentity(t *testing.T) {
	dir := writeTree(t, map[string]string{"artisan": "", ".env": "DB_CONNECTION=pgsql\nDB_HOST=PG.internal\nDB_DATABASE=app\nDB_USERNAME=app\nDB_PASSWORD=\"p w\"\n"})
	s := openLocalT(t, dir, Options{})
	id, err := s.DBIdentity(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if id != (core.DBIdentity{Engine: core.Postgres, Host: "pg.internal", Port: 5432, Name: "app", User: "app"}) {
		t.Fatalf("%+v", id)
	}
}

func TestSplitWPHost(t *testing.T) {
	tests := []struct {
		in, host, socket string
		port             int
	}{
		{"localhost", "localhost", "", 0},
		{"db.internal:3307", "db.internal", "", 3307},
		{"localhost:/run/mysqld/mysqld.sock", "localhost", "/run/mysqld/mysqld.sock", 0},
		{":/tmp/mysql.sock", "localhost", "/tmp/mysql.sock", 0},
		{"[::1]:3308", "::1", "", 3308},
		{"::1", "::1", "", 0},
		{" 127.0.0.1 ", "127.0.0.1", "", 0},
	}
	for _, tt := range tests {
		h, p, s := splitWPHost(tt.in)
		if h != tt.host || p != tt.port || s != tt.socket {
			t.Errorf("%q: %q %d %q", tt.in, h, p, s)
		}
	}
	c := dbConfig{driver: core.MySQL, host: "localhost", socket: "/run/mysqld.sock", name: "wp", user: "u"}
	if e := c.endpoint(""); e.DSN != "mysql://u@localhost:3306/wp?socket=%2Frun%2Fmysqld.sock" {
		t.Errorf("socket DSN %q", e.DSN)
	}
	// The user and the password stay out of Display; DSN and User keep
	// the user for db.Open.
	c = dbConfig{driver: core.MySQL, host: "127.0.0.1", port: 3307, name: "shop", user: "jane.doe", password: "pw"}
	if e := c.endpoint(""); e.Display != "mysql://127.0.0.1:3307/shop" || e.User != "jane.doe" || e.DSN != "mysql://jane.doe@127.0.0.1:3307/shop" || e.Password != "pw" {
		t.Errorf("endpoint %+v", e)
	}
}

func TestReadOnlyEnforced(t *testing.T) {
	ctx := context.Background()
	sh := remotetest.New(t)
	sh.Reply(nil, "", 0)
	local := openLocalT(t, writeTree(t, map[string]string{"wp-config.php": wpConfig("x")}), Options{ReadOnly: true})
	dsn, _ := Open(ctx, core.Endpoint{Kind: core.DSN, Driver: core.MySQL, Host: "db", Path: "shop"}, Options{ReadOnly: true})
	ssh, err := Open(ctx, core.Endpoint{Kind: core.SSHSite, Display: "deploy@shop.example:/srv/shop", User: "deploy", Host: "shop.example", Path: "/srv/shop"}, Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	before := len(sh.Calls())
	for _, s := range []core.Site{local, dsn, ssh} {
		name := string(s.Endpoint().Kind)
		if !s.ReadOnly() {
			t.Fatalf("%s: ReadOnly false", name)
		}
		checks := map[string]error{
			"WriteFile": s.WriteFile(ctx, "wp-config.php", []byte("x"), 0o644),
			"Remove":    s.Remove(ctx, "wp-config.php"),
		}
		_, checks["Sink"] = s.Sink(ctx)
		_, _, checks["Backup"] = s.Backup(ctx)
		_, checks["WP db import"] = s.WP(ctx, "db", "import", "-")
		_, checks["WP eval-file"] = s.WP(ctx, "eval-file", "-")
		for what, err := range checks {
			if !errors.Is(err, core.ErrReadOnly) {
				t.Errorf("%s %s: %v, want ErrReadOnly", name, what, err)
			}
		}
		if _, err := s.WP(ctx, "core", "version"); errors.Is(err, core.ErrReadOnly) {
			t.Errorf("%s: a read WP call was refused as a write", name)
		}
	}
	if ss, ok := ssh.(*sshSite); !ok || !errors.Is(ss.EvalFile(ctx, []byte("<?php")), core.ErrReadOnly) {
		t.Error("ssh EvalFile on a source")
	}
	// Only the read WP call reached the host.
	calls := sh.Calls()[before:]
	if len(calls) != 1 || !strings.Contains(calls[0][len(calls[0])-1], "core version") {
		t.Fatalf("calls on a read-only source: %v", calls)
	}
	b, _ := os.ReadFile(filepath.Join(local.Endpoint().Path, "wp-config.php"))
	if !bytes.Contains(b, []byte("DB_NAME")) {
		t.Fatal("source file changed")
	}
}

func TestAtomicWriteKeepsMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows doesn't keep Unix mode bits")
	}
	ctx := context.Background()
	dir := writeTree(t, map[string]string{"wp-config.php": "old"})
	p := filepath.Join(dir, "wp-config.php")
	if err := os.Chmod(p, 0o640); err != nil {
		t.Fatal(err)
	}
	s := openLocalT(t, dir, Options{})
	if err := s.WriteFile(ctx, "wp-config.php", []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	if b, _ := os.ReadFile(p); string(b) != "new" || fi.Mode().Perm() != 0o640 {
		t.Fatalf("content %q mode %v", b, fi.Mode().Perm())
	}
	if err := s.WriteFile(ctx, "wp-content/mu-plugins/new.php", []byte("<?php"), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, _ = os.Stat(filepath.Join(dir, "wp-content", "mu-plugins", "new.php"))
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("new file mode %v", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".roadie-") {
			t.Fatalf("temp file left: %s", e.Name())
		}
	}
	if err := s.Remove(ctx, "wp-content/mu-plugins/new.php"); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(ctx, "wp-content/mu-plugins/new.php"); err != nil {
		t.Fatalf("removing a missing file: %v", err)
	}
	if ok, _ := s.Exists(ctx, "wp-content/mu-plugins/new.php"); ok {
		t.Fatal("file still there")
	}
}

func TestWriteRefusesEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	ctx := context.Background()
	outside := t.TempDir()
	dir := writeTree(t, map[string]string{"wp-config.php": "x"})
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	s := openLocalT(t, dir, Options{})
	for _, rel := range []string{"../x.php", "link/x.php", "/tmp/x.php"} {
		if err := s.WriteFile(ctx, rel, []byte("x"), 0o644); err == nil {
			t.Errorf("%s: wrote outside the site", rel)
		}
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("files written outside: %v", entries)
	}
}

func TestDSNSite(t *testing.T) {
	ctx := context.Background()
	e, err := ParseEndpoint("postgres://app@PG.example:6432/shop")
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, e, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if s.Stack() != core.StackSQL {
		t.Fatal(s.Stack())
	}
	if _, err := s.ReadFile(ctx, "wp-config.php"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := s.WriteFile(ctx, "x", nil, 0o644); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("WriteFile: %v", err)
	}
	if ok, _ := s.Exists(ctx, "wp-config.php"); ok {
		t.Fatal("Exists")
	}
	id, _ := s.DBIdentity(ctx)
	if id != (core.DBIdentity{Engine: core.Postgres, Host: "pg.example", Port: 6432, Name: "shop", User: "app"}) {
		t.Fatalf("%+v", id)
	}
	if s.RemoteCommands() != nil {
		t.Fatal("remote commands on a DSN")
	}
}

// fakeFileSink stands in for dump.NewFileSink: it gzips every
// passthrough statement into path with mode 0600.
type fakeFileSink struct {
	*testutil.Sink
	path string
	buf  bytes.Buffer
}

func (f *fakeFileSink) Write(ctx context.Context, b *core.Batch) error {
	if b.Pass != nil {
		f.buf.Write(b.Pass.Text)
	}
	return f.Sink.Write(ctx, b)
}

func (f *fakeFileSink) Commit(ctx context.Context) error {
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	_, _ = w.Write(f.buf.Bytes())
	_ = w.Close()
	if err := os.WriteFile(f.path, gz.Bytes(), 0o600); err != nil {
		return err
	}
	return f.Sink.Commit(ctx)
}

func useFakeFileSink(t *testing.T) {
	t.Helper()
	old := newFileSink
	newFileSink = func(p string, _ dump.WriterOptions) (core.Sink, error) {
		return &fakeFileSink{Sink: testutil.FakeSink(), path: p}, nil
	}
	t.Cleanup(func() { newFileSink = old })
}

func fixClock(t *testing.T) {
	t.Helper()
	old := now
	now = func() time.Time { return time.Date(2026, 10, 26, 14, 2, 3, 0, time.FixedZone("x", 6*3600)) }
	t.Cleanup(func() { now = old })
}

func TestLocalBackup(t *testing.T) {
	ctx := context.Background()
	fixClock(t)
	useFakeDB(t)
	useFakeFileSink(t)
	work := t.TempDir()
	old := getwd
	getwd = func() (string, error) { return work, nil }
	t.Cleanup(func() { getwd = old })

	dir := writeTree(t, map[string]string{"wp-config.php": wpConfig("x")})
	s := openLocalT(t, dir, Options{})
	p, restore, err := s.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantPath := filepath.Join(work, ".roadie", "backups", "2026-10-26T08-02-03.sql.gz")
	if p != wantPath {
		t.Fatalf("path %s, want %s", p, wantPath)
	}
	if restore != "gunzip -c "+shellWord(wantPath)+" | wp --path="+shellWord(dir)+" db import -" {
		t.Fatalf("restore %q", restore)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(filepath.Dir(p))
		ff, _ := os.Stat(p)
		if fi.Mode().Perm() != 0o700 || ff.Mode().Perm() != 0o600 {
			t.Fatalf("modes %v %v", fi.Mode().Perm(), ff.Mode().Perm())
		}
	}
	f, _ := os.Open(p)
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(zr); string(b) != "-- fake\n" {
		t.Fatalf("backup body %q", b)
	}

	e, _ := ParseEndpoint("mysql://shop@db.example:3306/shop")
	d, _ := Open(ctx, e, Options{BackupDir: filepath.Join(work, "b")})
	p, restore, err = d.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := "gunzip -c " + shellWord(p) + " | mysql -h db.example -P 3306 -u shop -p shop"; restore != want {
		t.Fatalf("DSN restore %q, want %q", restore, want)
	}
	e, _ = ParseEndpoint("postgres://app@pg.example/shop")
	d, _ = Open(ctx, e, Options{BackupDir: filepath.Join(work, "b")})
	if _, restore, _ = d.Backup(ctx); !strings.HasSuffix(restore, " | psql -h pg.example -U app -d shop") {
		t.Fatalf("pg restore %q", restore)
	}
}

// Two backups in the same second never share a file. A backup that
// fails leaves no file that looks like one.
func TestLocalBackupSameSecond(t *testing.T) {
	ctx := context.Background()
	fixClock(t)
	useFakeDB(t)
	useFakeFileSink(t)
	dir := t.TempDir()
	var paths []string
	for _, dsn := range []string{"mysql://shop@db.example/a", "mysql://shop@db.example/b", "postgres://app@pg.example/c"} {
		e, _ := ParseEndpoint(dsn)
		d, _ := Open(ctx, e, Options{BackupDir: dir})
		p, restore, err := d.Backup(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(restore, shellWord(p)) {
			t.Fatalf("restore %q doesn't name %s", restore, p)
		}
		paths = append(paths, filepath.Base(p))
	}
	want := []string{"2026-10-26T08-02-03.sql.gz", "2026-10-26T08-02-03-2.sql.gz", "2026-10-26T08-02-03-3.sql.gz"}
	if strings.Join(paths, " ") != strings.Join(want, " ") {
		t.Fatalf("paths %v, want %v", paths, want)
	}
	for _, p := range want {
		if fi, err := os.Stat(filepath.Join(dir, p)); err != nil || fi.Size() == 0 {
			t.Fatalf("%s: %v (replaced or empty)", p, err)
		}
	}

	// A source that fails: the reserved name is removed again.
	old := openDB
	openDB = func(context.Context, core.Endpoint, db.Mode) (dbConn, error) {
		return nil, errors.New("connection refused")
	}
	t.Cleanup(func() { openDB = old })
	e, _ := ParseEndpoint("mysql://shop@db.example/d")
	d, _ := Open(ctx, e, Options{BackupDir: dir})
	if _, _, err := d.Backup(ctx); err == nil {
		t.Fatal("backup with a broken source passed")
	}
	left, _ := filepath.Glob(filepath.Join(dir, "*"))
	if len(left) != len(want) {
		t.Fatalf("files after a failed backup: %v", left)
	}
}

// TestLocalBackupRealSink writes through the io lane's file sink.
func TestLocalBackupRealSink(t *testing.T) {
	useFakeDB(t)
	s := openLocalT(t, writeTree(t, map[string]string{"wp-config.php": wpConfig("x")}), Options{BackupDir: t.TempDir()})
	_, _, err := s.Backup(context.Background())
	if errors.Is(err, core.ErrNotBuilt) {
		t.Skip("needs the io lane")
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestAliasSites(t *testing.T) {
	ctx := context.Background()
	sh := remotetest.New(t)
	sh.Reply(nil, "", 0)
	work := writeTree(t, map[string]string{
		"wp-cli.yml":         "@local:\n  path: site\n@prod:\n  ssh: deploy@shop.example:2222/var/www/shop\n",
		"site/wp-config.php": wpConfig("x"),
	})
	old := getwd
	getwd = func() (string, error) { return work, nil }
	t.Cleanup(func() { getwd = old })

	l, err := Open(ctx, core.Endpoint{Kind: core.WPAlias, Alias: "@local", Display: "@local"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	if _, ok := l.(*localSite); !ok || l.Endpoint().Kind != core.WPAlias || l.Endpoint().Path != filepath.Join(work, "site") || l.Stack() != core.StackWordPress {
		t.Fatalf("@local: %T %+v", l, l.Endpoint())
	}
	p, err := Open(ctx, core.Endpoint{Kind: core.WPAlias, Alias: "@prod", Display: "@prod"}, Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	e := p.Endpoint()
	if _, ok := p.(*sshSite); !ok || e.Host != "shop.example" || e.Port != 2222 || e.User != "deploy" || e.Path != "/var/www/shop" {
		t.Fatalf("@prod: %T %+v", p, e)
	}
	calls := sh.Calls()
	if len(calls) == 0 || calls[0][0] != "-p" || calls[0][1] != "2222" {
		t.Fatalf("calls %v", calls)
	}
	if _, err := Open(ctx, core.Endpoint{Kind: core.WPAlias, Alias: "@nope", Display: "@nope"}, Options{}); !errors.Is(err, remote.ErrNoAlias) {
		t.Fatalf("@nope: %v", err)
	}
}

func TestExprSource(t *testing.T) {
	lit := "S3" + "cretPw"
	for in, want := range map[string]string{
		"getenv('DB_PASSWORD')":                  "getenv('DB_PASSWORD')",
		` getenv( "DB_PASSWORD" ) `:              `getenv( "DB_PASSWORD" )`,
		"env('DB_PASSWORD')":                     "env('DB_PASSWORD')",
		"$_ENV['DB_PASSWORD']":                   "$_ENV['DB_PASSWORD']",
		`$_SERVER["DB_PASSWORD"]`:                `$_SERVER["DB_PASSWORD"]`,
		"getenv('DB_PASSWORD') ?: '" + lit + "'": "a PHP expression",
		"$_ENV['DB_PASSWORD'] ?? '" + lit + "'":  "a PHP expression",
		"$_SERVER['X'] ?? '" + lit + "'":         "a PHP expression",
		"getenv('" + lit + "')":                  "getenv('" + lit + "')",
		"'" + lit + "'":                          "a PHP expression",
		"getenv('A') . '" + lit + "'":            "a PHP expression",
		"getenv($name)":                          "a PHP expression",
		"getenv('A')\n?: '" + lit + "'":          "a PHP expression",
		"defined('X') ? X : '" + lit + "'":       "a PHP expression",
	} {
		if got := exprSource(in); got != want {
			t.Errorf("exprSource(%q) = %q, want %q", in, got, want)
		}
	}
}

// A SQLite target is backed up by copying its file (and write-ahead
// log), 0600, with a cp line to restore it; a backup that fails leaves no
// empty backup folder behind.
func TestSQLiteBackup(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "app.db")
	if err := os.WriteFile(db, []byte("SQLite format 3\x00 the pages"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(db+"-wal", []byte("the log"), 0o600); err != nil {
		t.Fatal(err)
	}
	backups := filepath.Join(dir, "b", "backups")
	s := openDSN(core.Endpoint{Kind: core.DSN, Driver: core.SQLite, Path: db}, Options{BackupDir: backups})
	p, restore, err := s.Backup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "SQLite format 3\x00 the pages" || !strings.HasSuffix(p, ".sqlite") {
		t.Errorf("backup %s: %q", p, b)
	}
	if b, _ := os.ReadFile(p + "-wal"); string(b) != "the log" {
		t.Errorf("wal %q", b)
	}
	if fi, err := os.Stat(p); err != nil || runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v %v", fi, err)
	}
	if !strings.HasPrefix(restore, "cp "+shellWord(p)+" "+shellWord(db)) || !strings.Contains(restore, "-wal") {
		t.Errorf("restore %q", restore)
	}
	missing := filepath.Join(dir, "c", "backups")
	s = openDSN(core.Endpoint{Kind: core.DSN, Driver: core.SQLite, Path: filepath.Join(dir, "gone.db")}, Options{BackupDir: missing})
	if _, _, err := s.Backup(context.Background()); err == nil {
		t.Error("a missing database backed up")
	}
	if _, err := os.Stat(missing); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the empty backup folder stayed: %v", err)
	}
}
