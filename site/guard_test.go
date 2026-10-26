// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package site

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
)

// stagingSite is a writable WordPress target whose wp-config sets env
// (no define when "") and whose siteurl is siteURL.
func stagingSite(env, siteURL string) *testutil.Site {
	cfg := "<?php\ndefine( 'DB_NAME', 'staging' );\n"
	if env != "" {
		cfg += "define( 'WP_ENVIRONMENT_TYPE', '" + env + "' );\n"
	}
	s := testutil.FakeSite(map[string][]byte{"wp-config.php": []byte(cfg)})
	s.EP = core.Endpoint{Kind: core.LocalSite, Display: "/var/www/staging", Path: "/nonexistent/staging"}
	s.WPFunc = func(args []string) ([]byte, error) {
		if len(args) == 3 && args[2] == "siteurl" {
			return []byte(siteURL + "\n"), nil
		}
		return nil, errors.New("unexpected wp call")
	}
	return s
}

func wantCode(t *testing.T, err error, code string, exit int, msg string) {
	t.Helper()
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != code || ce.Exit != exit {
		t.Fatalf("%v, want %s exit %d", err, code, exit)
	}
	if !core.KnownID(ce.Code) {
		t.Fatalf("unknown code %s", ce.Code)
	}
	if msg != "" && ce.Msg != msg {
		t.Fatalf("message\n got %s\nwant %s", ce.Msg, msg)
	}
	if ce.State != "" {
		t.Fatalf("guard set State %q", ce.State)
	}
}

func dumpEP() core.Endpoint {
	return core.Endpoint{Kind: core.DumpFile, Display: "prod.sql.gz", Path: "/nonexistent/prod.sql.gz"}
}

// TestGuardRefusals has one fixture per refusal (security G1); each
// refuses before anything is written to the target.
func TestGuardRefusals(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name string
		in   func() (GuardInput, *testutil.Site)
		code string
		exit int
		msg  string
	}{
		{"target-is-production by --url", func() (GuardInput, *testutil.Site) {
			s := stagingSite("staging", "https://staging.shop.test")
			return GuardInput{From: dumpEP(), To: s.EP, ToSite: s, Production: []string{"https://shop.com"}, URL: "https://shop.com/", Mode: "copy"}, s
		}, core.ErrTargetIsProduction, 3,
			"target-is-production: /var/www/staging is shop.com, which production: lists. Roadie never writes to production. Point --to at the staging copy."},
		{"target-is-production by siteurl glob", func() (GuardInput, *testutil.Site) {
			s := stagingSite("staging", "https://www.shop.com")
			return GuardInput{From: dumpEP(), To: s.EP, ToSite: s, Production: []string{"*.shop.com"}, URL: "https://staging.shop.test", Mode: "copy"}, s
		}, core.ErrTargetIsProduction, 3, ""},
		{"target-is-production by database host", func() (GuardInput, *testutil.Site) {
			s := stagingSite("staging", "https://staging.shop.test")
			s.Identity.Host = "DB.Shop.com"
			return GuardInput{From: dumpEP(), To: s.EP, ToSite: s, Production: []string{"db.shop.com"}, Mode: "copy"}, s
		}, core.ErrTargetIsProduction, 3,
			"target-is-production: /var/www/staging uses database host db.shop.com, which production: lists. Roadie never writes to production. Point --to at the staging copy."},
		{"target-is-source by database", func() (GuardInput, *testutil.Site) {
			s := stagingSite("staging", "https://staging.shop.test")
			s.Identity = core.DBIdentity{Engine: core.MySQL, Host: "db.shop.com", Port: 3306, Name: "shop"}
			from := core.Endpoint{Kind: core.DSN, Driver: core.MariaDB, Host: "db.shop.com", Path: "shop", Display: "mariadb://db.shop.com/shop"}
			return GuardInput{From: from, To: s.EP, ToSite: s, Mode: "copy"}, s
		}, core.ErrTargetIsSource, 3,
			"target-is-source: --from and --to both reach database shop on db.shop.com. A copy can't replace its own source."},
		{"target-says-production unset", func() (GuardInput, *testutil.Site) {
			s := stagingSite("", "https://staging.shop.test")
			return GuardInput{From: dumpEP(), To: s.EP, ToSite: s, URL: "https://staging.shop.test", Mode: "copy"}, s
		}, core.ErrTargetSaysProduction, 3,
			"target-says-production: /var/www/staging has WP_ENVIRONMENT_TYPE unset, which WordPress reads as production. If it's staging, set WP_ENVIRONMENT_TYPE to staging there, or pass --target-is-staging staging.shop.test."},
		{"target-says-production set", func() (GuardInput, *testutil.Site) {
			s := stagingSite("production", "https://staging.shop.test")
			return GuardInput{From: dumpEP(), To: s.EP, ToSite: s, TargetIsStaging: "staging.shop", Mode: "copy"}, s
		}, core.ErrTargetSaysProduction, 3, ""},
		{"target-says-production laravel", func() (GuardInput, *testutil.Site) {
			s := testutil.FakeSite(map[string][]byte{".env": []byte("APP_ENV=production\nAPP_URL=https://staging.app.test\n")})
			s.StackName = core.StackLaravel
			s.EP = core.Endpoint{Kind: core.LocalSite, Display: "/srv/app", Path: "/nonexistent/app"}
			return GuardInput{From: dumpEP(), To: s.EP, ToSite: s, Mode: "copy"}, s
		}, core.ErrTargetSaysProduction, 3,
			"target-says-production: /srv/app has APP_ENV=production in .env. If it's staging, set APP_ENV=staging there, or pass --target-is-staging staging.app.test."},
		{"target-holds-production", func() (GuardInput, *testutil.Site) {
			s := stagingSite("staging", "https://shop.com")
			return GuardInput{From: s.EP, To: s.EP, FromSite: s, ToSite: s, URL: "https://staging.shop.test", Mode: "prepare"}, s
		}, core.ErrTargetHoldsProduction, 3,
			"target-holds-production: /var/www/staging still calls itself https://shop.com. Pass --production-url https://shop.com so Roadie knows which URL is live."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in, s := tt.in()
			wantCode(t, Guard(ctx, in), tt.code, tt.exit, tt.msg)
			if w := s.Writes(); len(w) != 0 {
				t.Fatalf("guard wrote: %v", w)
			}
			if s.Backups() != 0 {
				t.Fatal("guard backed up")
			}
		})
	}
}

func TestGuardPasses(t *testing.T) {
	ctx := context.Background()
	s := stagingSite("staging", "https://staging.shop.test")
	in := GuardInput{From: dumpEP(), To: s.EP, ToSite: s, Production: []string{"https://shop.com", "*.shop.com"}, URL: "https://staging.shop.test", Mode: "copy"}
	if err := Guard(ctx, in); err != nil {
		t.Fatal(err)
	}
	// prepare: the copy still calls itself production, which prepare
	// fixes, so siteurl alone isn't target-is-production there.
	p := stagingSite("staging", "https://shop.com")
	in = GuardInput{From: p.EP, To: p.EP, FromSite: p, ToSite: p, Production: []string{"shop.com"}, URL: "https://staging.shop.test", Mode: "prepare"}
	if err := Guard(ctx, in); err != nil {
		t.Fatalf("prepare with production known: %v", err)
	}
	in.Production, in.ProductionURL = nil, "https://shop.com"
	if err := Guard(ctx, in); err != nil {
		t.Fatalf("prepare with --production-url: %v", err)
	}
	// The same copy is target-is-production on copy.
	in.Mode, in.Production = "copy", []string{"shop.com"}
	in.From, in.FromSite = dumpEP(), nil
	wantCode(t, Guard(ctx, in), core.ErrTargetIsProduction, 3, "")
	// local and development environments are not production.
	for _, env := range []string{"local", "development"} {
		d := stagingSite(env, "https://staging.shop.test")
		if err := Guard(ctx, GuardInput{From: dumpEP(), To: d.EP, ToSite: d, Mode: "copy"}); err != nil {
			t.Fatalf("%s: %v", env, err)
		}
	}
	// stdio targets get no guard; a fresh folder with no config passes.
	if err := Guard(ctx, GuardInput{From: dumpEP(), To: core.Endpoint{Kind: core.Stdio}}); err != nil {
		t.Fatal(err)
	}
	empty := testutil.FakeSite(nil)
	if err := Guard(ctx, GuardInput{From: dumpEP(), To: empty.EP, ToSite: empty, Mode: "copy"}); err != nil {
		t.Fatal(err)
	}
}

func TestGuardOverride(t *testing.T) {
	ctx := context.Background()
	base := func(override string) GuardInput {
		s := stagingSite("", "https://Staging.Shop.test")
		return GuardInput{From: dumpEP(), To: s.EP, ToSite: s, TargetIsStaging: override, Mode: "copy"}
	}
	if err := Guard(ctx, base("staging.shop.test")); err != nil {
		t.Fatalf("exact host: %v", err)
	}
	for _, bad := range []string{"shop.test", "staging.shop.test.evil", "*.shop.test", "https://staging.shop.test"} {
		wantCode(t, Guard(ctx, base(bad)), core.ErrTargetSaysProduction, 3, "")
	}
	// Never for a production: match, even with the exact host.
	in := base("staging.shop.test")
	in.Production = []string{"staging.shop.test"}
	wantCode(t, Guard(ctx, in), core.ErrTargetIsProduction, 3, "")
	// Never for target-is-source either.
	s := stagingSite("", "https://staging.shop.test")
	s.Identity = core.DBIdentity{Engine: core.MySQL, Host: "127.0.0.1", Port: 3306, Name: "staging"}
	from := core.Endpoint{Kind: core.DSN, Driver: core.MySQL, Host: "localhost", Path: "staging", Display: "mysql://localhost/staging"}
	wantCode(t, Guard(ctx, GuardInput{From: from, To: s.EP, ToSite: s, TargetIsStaging: "staging.shop.test", Mode: "copy"}), core.ErrTargetIsSource, 3, "")
}

func TestGuardSameFolderAndHost(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	from := core.Endpoint{Kind: core.LocalSite, Display: dir, Path: dir}
	to := core.Endpoint{Kind: core.LocalSite, Display: dir + "/.", Path: dir + string(filepath.Separator) + "."}
	wantCode(t, Guard(ctx, GuardInput{From: from, To: to, Mode: "copy"}), core.ErrTargetIsSource, 3,
		"target-is-source: --from and --to are the same folder ("+to.Path+"). A copy can't replace its own source.")
	a := core.Endpoint{Kind: core.SSHSite, Display: "ssh://deploy@Shop.com/var/www/shop/", User: "deploy", Host: "Shop.com", Path: "/var/www/shop/"}
	b := core.Endpoint{Kind: core.SSHSite, Display: "root@shop.com:/var/www/shop", User: "root", Host: "shop.com", Port: 22, Path: "/var/www/shop"}
	wantCode(t, Guard(ctx, GuardInput{From: a, To: b, Mode: "copy"}), core.ErrTargetIsSource, 3,
		"target-is-source: --from and --to are both /var/www/shop on shop.com. A copy can't replace its own source.")
	b.Path = "/var/www/staging"
	if err := Guard(ctx, GuardInput{From: a, To: b, Mode: "copy"}); err != nil {
		t.Fatal(err)
	}
	// Two loopback databases on different machines are different.
	x := core.DBIdentity{Engine: core.MySQL, Server: "shop.com", Host: "localhost", Port: 3306, Name: "wp"}
	y := x
	y.Server = "staging.example"
	if sameDB(x, y) {
		t.Fatal("loopback on two hosts counted as one database")
	}
	y.Server = "shop.com"
	y.Host = "127.0.0.1"
	if !sameDB(x, y) {
		t.Fatal("localhost and 127.0.0.1 on one host")
	}
}

func TestGuardOutputIsInput(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	in := filepath.Join(dir, "prod.sql.gz")
	if err := os.WriteFile(in, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "copy.sql.gz")
	if err := os.Link(in, link); err != nil {
		t.Skip("no hard links here:", err)
	}
	from := core.Endpoint{Kind: core.DumpFile, Display: "prod.sql.gz", Path: in}
	for _, p := range []string{in, link, dir + "/./prod.sql.gz"} {
		to := core.Endpoint{Kind: core.DumpFile, Display: filepath.Base(p), Path: p}
		wantCode(t, Guard(ctx, GuardInput{From: from, To: to, Mode: "copy"}), core.ErrOutputIsInput, 2,
			"output-is-input: --to "+filepath.Base(p)+" is the file Roadie reads. Write the copy to a new file.")
	}
	other := core.Endpoint{Kind: core.DumpFile, Display: "new.sql.gz", Path: filepath.Join(dir, "new.sql.gz")}
	if err := Guard(ctx, GuardInput{From: from, To: other, Production: []string{"*"}, Mode: "copy"}); err != nil {
		t.Fatalf("a new dump file: %v", err)
	}
}

// deadPID returns the pid of a process that has exited.
func deadPID(t *testing.T) int {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^$")
	cmd.Env = append(os.Environ(), "SHIM_DIR=")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func TestLock(t *testing.T) {
	t.Setenv("RHYTHM_CACHE_DIR", t.TempDir())
	dir := t.TempDir()
	target := core.Endpoint{Kind: core.LocalSite, Display: dir, Path: dir}
	unlock, err := Lock(target)
	if err != nil {
		t.Fatal(err)
	}
	// The same target typed another way is the same lock.
	again := core.Endpoint{Kind: core.LocalSite, Display: dir + "/", Path: dir + string(filepath.Separator)}
	_, err = Lock(again)
	wantCode(t, err, core.ErrLock, 3, "")
	if !strings.Contains(err.Error(), "lock: another Roadie run is copying to "+dir+" (pid "+strconv.Itoa(os.Getpid())+", started ") ||
		!strings.Contains(err.Error(), "). Wait for it, or remove ") || !strings.HasSuffix(err.Error(), ".lock if it crashed.") {
		t.Fatalf("message %s", err)
	}
	other := t.TempDir()
	u2, err := Lock(core.Endpoint{Kind: core.LocalSite, Display: other, Path: other})
	if err != nil {
		t.Fatalf("another target: %v", err)
	}
	u2()
	unlock()
	unlock() // a second call is harmless
	u3, err := Lock(target)
	if err != nil {
		t.Fatalf("after unlock: %v", err)
	}
	u3()
	u4, err := Lock(core.Endpoint{Kind: core.Stdio})
	if err != nil {
		t.Fatal(err)
	}
	u4()
}

func TestLockStale(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("RHYTHM_CACHE_DIR", cache)
	target := core.Endpoint{Kind: core.DSN, Driver: core.MySQL, Host: "DB.example", Path: "staging", Display: "mysql://db.example/staging"}
	unlock, err := Lock(target)
	if err != nil {
		t.Fatal(err)
	}
	locks, _ := filepath.Glob(filepath.Join(cache, "roadie", "locks", "*.lock"))
	if len(locks) != 1 {
		t.Fatalf("lock files %v", locks)
	}
	if fi, _ := os.Stat(locks[0]); fi.Mode().Perm() != 0o600 && os.PathSeparator == '/' {
		t.Fatalf("lock mode %v", fi.Mode().Perm())
	}
	pid := deadPID(t)
	started := time.Now().Add(-time.Hour).Format(time.RFC3339)
	if err := os.WriteFile(locks[0], []byte("pid="+strconv.Itoa(pid)+"\nstarted="+started+"\ntarget=mysql://db.example/staging\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var notes []string
	u2, err := LockNotify(target, func(s string) { notes = append(notes, s) })
	if err != nil {
		t.Fatalf("stale lock not replaced: %v", err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "removed a stale lock on mysql://db.example/staging from pid "+strconv.Itoa(pid)) {
		t.Fatalf("notes %v", notes)
	}
	// The first run's unlock leaves the new run's lock alone.
	unlock()
	if _, err := os.Stat(locks[0]); err != nil {
		t.Fatal("unlock removed another run's lock")
	}
	u2()
	if _, err := os.Stat(locks[0]); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("lock not removed")
	}
	// A lock file still empty right after it was made is held.
	if err := os.WriteFile(locks[0], nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Lock(target)
	wantCode(t, err, core.ErrLock, 3, "")
}

func TestLockIdentity(t *testing.T) {
	a := lockIdentity(core.Endpoint{Kind: core.SSHSite, Host: "Shop.com", Path: "/var/www/shop/"})
	b := lockIdentity(core.Endpoint{Kind: core.SSHSite, Host: "shop.com", Port: 22, User: "x", Path: "/var/www/shop"})
	if a != b || a != "ssh:shop.com:22:/var/www/shop" {
		t.Fatalf("%q %q", a, b)
	}
	if got := lockIdentity(core.Endpoint{Kind: core.DSN, Driver: core.MariaDB, Host: "db", Path: "x"}); got != "db:mysql://db:3306/x" {
		t.Fatal(got)
	}
	if lockIdentity(core.Endpoint{Kind: core.Stdio}) != "" {
		t.Fatal("stdio has a lock")
	}
}
