// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package site

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/site/remote"
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
		{"target-says-production in an included file", func() (GuardInput, *testutil.Site) {
			st := stagingSite("staging", "https://staging.shop.test")
			s := testutil.FakeSite(map[string][]byte{
				"wp-config.php":       []byte("<?php\nrequire __DIR__ . '/wp-config-local.php';\ndefine( 'WP_ENVIRONMENT_TYPE', 'staging' );\n"),
				"wp-config-local.php": []byte("<?php define( 'WP_ENVIRONMENT_TYPE', 'production' );"),
			})
			s.EP, s.WPFunc = st.EP, st.WPFunc
			return GuardInput{From: dumpEP(), To: s.EP, ToSite: s, URL: "https://staging.shop.test", Mode: "copy"}, s
		}, core.ErrTargetSaysProduction, 3,
			"target-says-production: /var/www/staging has WP_ENVIRONMENT_TYPE set to production in wp-config-local.php, which wp-config.php includes first. If it's staging, set WP_ENVIRONMENT_TYPE to staging there, or pass --target-is-staging staging.shop.test."},
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
			"target-holds-production: /var/www/staging calls itself https://shop.com; Roadie can't tell whether that is the live site. Pass --production-url <production URL> (the live site's URL) so Roadie knows which URL is live, or --url https://shop.com if it is this staging's own URL."},
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
	empty.StackName = ""
	if err := Guard(ctx, GuardInput{From: dumpEP(), To: empty.EP, ToSite: empty, Mode: "copy"}); err != nil {
		t.Fatal(err)
	}
}

func TestGuardFailsClosedWithoutEnv(t *testing.T) {
	ctx := context.Background()
	// WordPress with wp-config.php one folder up: Roadie can't read it.
	wp := testutil.FakeSite(map[string][]byte{"wp-includes/version.php": []byte("<?php\n")})
	wantCode(t, Guard(ctx, GuardInput{From: dumpEP(), To: wp.EP, ToSite: wp, SiteURL: "https://staging.shop.test", Mode: "copy"}),
		core.ErrTargetSaysProduction, 3,
		"target-says-production: /srv/staging has no wp-config.php in the site folder Roadie can read, so it counts as production. If it's staging, move wp-config.php into the site folder and set WP_ENVIRONMENT_TYPE to staging there, or pass --target-is-staging staging.shop.test.")
	if err := Guard(ctx, GuardInput{From: dumpEP(), To: wp.EP, ToSite: wp, SiteURL: "https://staging.shop.test", TargetIsStaging: "staging.shop.test", Mode: "copy"}); err != nil {
		t.Fatalf("override: %v", err)
	}
	laravel := func(env string) *testutil.Site {
		s := testutil.FakeSite(map[string][]byte{".env": []byte("APP_URL=https://staging.shop.test\n" + env)})
		s.StackName = core.StackLaravel
		return s
	}
	tests := []struct{ env, msg string }{
		{"", "has APP_ENV unset in .env, which Laravel reads as production"},
		{"APP_ENV=${DEPLOY_ENV}\n", "sets APP_ENV in .env from another variable, which Roadie can't resolve, so it counts as production"},
		{"APP_ENV=production\n", "has APP_ENV=production in .env"},
	}
	for _, tt := range tests {
		s := laravel(tt.env)
		wantCode(t, Guard(ctx, GuardInput{From: dumpEP(), To: s.EP, ToSite: s, Mode: "copy"}), core.ErrTargetSaysProduction, 3,
			"target-says-production: /srv/staging "+tt.msg+". If it's staging, set APP_ENV=staging there, or pass --target-is-staging staging.shop.test.")
	}
	for _, env := range []string{"APP_ENV=staging\n", "APP_ENV='${X}'\n", "APP_ENV=local\n"} {
		s := laravel(env)
		if err := Guard(ctx, GuardInput{From: dumpEP(), To: s.EP, ToSite: s, Mode: "copy"}); err != nil {
			t.Fatalf("%q: %v", env, err)
		}
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
	// On prepare the copy's siteurl is production's: naming it, or any
	// host production: lists, is refused.
	ps := stagingSite("production", "https://shop.com")
	prep := GuardInput{From: dumpEP(), To: ps.EP, ToSite: ps, Mode: "prepare", Production: []string{"shop.com"}, URL: "https://staging.shop.test"}
	for _, bad := range []string{"shop.com", "SHOP.com"} {
		prep.TargetIsStaging = bad
		wantCode(t, Guard(ctx, prep), core.ErrTargetSaysProduction, 3, "")
	}
	prep.TargetIsStaging = "staging.shop.test"
	if err := Guard(ctx, prep); err != nil {
		t.Fatalf("prepare with the staging host: %v", err)
	}
	prep.Production = []string{"*.shop.test"}
	wantCode(t, Guard(ctx, prep), core.ErrTargetIsProduction, 3, "")
	// Never for target-is-source either.
	s := stagingSite("", "https://staging.shop.test")
	s.Identity = core.DBIdentity{Engine: core.MySQL, Host: "127.0.0.1", Port: 3306, Name: "staging"}
	from := core.Endpoint{Kind: core.DSN, Driver: core.MySQL, Host: "localhost", Path: "staging", Display: "mysql://localhost/staging"}
	wantCode(t, Guard(ctx, GuardInput{From: from, To: s.EP, ToSite: s, TargetIsStaging: "staging.shop.test", Mode: "copy"}), core.ErrTargetIsSource, 3, "")
}

// TestGuardSameServerOtherAddress: a source at a published port and a
// target at the container's own address are one database; the servers
// say so (@@server_uuid, system_identifier) though the hosts differ.
func TestGuardSameServerOtherAddress(t *testing.T) {
	ctx := context.Background()
	old := instanceOf
	t.Cleanup(func() { instanceOf = old })
	ids := map[string]string{"127.0.0.1": "mysql:uuid-1/prod16", "172.17.0.2": "mysql:uuid-1/prod16", "10.0.0.9": "mysql:uuid-2/prod16"}
	var asked []string
	instanceOf = func(_ context.Context, e core.Endpoint, _ core.Site) string {
		asked = append(asked, e.Host)
		return ids[e.Host]
	}
	from := core.Endpoint{Kind: core.DSN, Driver: core.MySQL, Host: "127.0.0.1", Port: 40316, Path: "prod16", Display: "mysql://u@127.0.0.1:40316/prod16"}
	to := core.Endpoint{Kind: core.DSN, Driver: core.MySQL, Host: "172.17.0.2", Port: 3306, Path: "prod16", Display: "mysql://u@172.17.0.2:3306/prod16"}
	wantCode(t, Guard(ctx, GuardInput{From: from, To: to, Mode: "copy"}), core.ErrTargetIsSource, 3,
		"target-is-source: --from and --to both reach database prod16 on the same MySQL server (127.0.0.1:40316 and 172.17.0.2:3306 are two addresses of it). A copy can't replace its own source.")
	// Another server with a database of the same name is not the source.
	to.Host = "10.0.0.9"
	if err := targetIsSource(ctx, GuardInput{From: from, To: to, Mode: "copy"}); err != nil {
		t.Fatal(err)
	}
	// Databases with other names are never asked about.
	asked = nil
	to.Path = "staging16"
	if err := targetIsSource(ctx, GuardInput{From: from, To: to, Mode: "copy"}); err != nil || len(asked) != 0 {
		t.Fatalf("%v, asked %v", err, asked)
	}
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

// Runs that find the same stale lock at once must not remove each
// other's new lock: exactly one of them gets it.
func TestLockStaleRace(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("RHYTHM_CACHE_DIR", cache)
	target := core.Endpoint{Kind: core.DSN, Driver: core.MySQL, Host: "db.example", Path: "race", Display: "mysql://db.example/race"}
	pid := deadPID(t)
	for round := 0; round < 20; round++ {
		unlock, err := Lock(target)
		if err != nil {
			t.Fatal(err)
		}
		locks, _ := filepath.Glob(filepath.Join(cache, "roadie", "locks", "*.lock"))
		if len(locks) != 1 {
			t.Fatalf("lock files %v", locks)
		}
		if err := os.WriteFile(locks[0], []byte("pid="+strconv.Itoa(pid)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		const runs = 8
		var (
			wg      sync.WaitGroup
			mu      sync.Mutex
			got     []func()
			refused int
		)
		for i := 0; i < runs; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				u, err := LockNotify(target, func(string) {})
				mu.Lock()
				defer mu.Unlock()
				if err == nil {
					got = append(got, u)
					return
				}
				var ce *core.Error
				if !errors.As(err, &ce) || ce.Code != core.ErrLock {
					t.Errorf("run: %v", err)
				}
				refused++
			}()
		}
		wg.Wait()
		if len(got) != 1 || refused != runs-1 {
			t.Fatalf("round %d: %d runs got the lock, %d refused", round, len(got), refused)
		}
		got[0]()
		unlock() // the first lock was replaced; its unlock leaves nothing behind
		if left, _ := filepath.Glob(filepath.Join(cache, "roadie", "locks", "*")); len(left) != 0 {
			t.Fatalf("left behind %v", left)
		}
	}
}

// A break file left by a run that died while clearing is itself stale.
func TestLockStaleBreakFile(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("RHYTHM_CACHE_DIR", cache)
	target := core.Endpoint{Kind: core.DSN, Driver: core.MySQL, Host: "db.example", Path: "brk", Display: "mysql://db.example/brk"}
	unlock, err := Lock(target)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	p := filepath.Join(cache, "roadie", "locks", hexSum(lockIdentity(target))+".lock")
	if err := os.WriteFile(p, []byte("pid="+strconv.Itoa(deadPID(t))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.WriteFile(p+".break", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p+".break", old, old); err != nil {
		t.Fatal(err)
	}
	u, err := LockNotify(target, func(string) {})
	if err != nil {
		t.Fatalf("old break file blocked the lock: %v", err)
	}
	u()
	if _, err := os.Stat(p + ".break"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("break file left behind")
	}
	// A fresh break file is another run clearing right now: wait, then
	// give up with a lock error rather than race it.
	if err := os.WriteFile(p, []byte("pid="+strconv.Itoa(deadPID(t))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p+".break", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	defer func(w time.Duration) { breakWait = w }(breakWait)
	breakWait = 100 * time.Millisecond
	_, err = LockNotify(target, func(string) {})
	wantCode(t, err, core.ErrLock, 3, "")
	if !strings.HasPrefix(err.Error(), "lock: another Roadie run is clearing a stale lock in ") {
		t.Fatalf("message %s", err)
	}
}

func hexSum(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
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

// unreadable is a site whose named file can't be read for a reason
// other than "not there".
type unreadable struct {
	*testutil.Site
	name string
	err  error
}

func (u unreadable) ReadFile(ctx context.Context, rel string) ([]byte, error) {
	if rel == u.name {
		return nil, u.err
	}
	return u.Site.ReadFile(ctx, rel)
}

// A config the guard can't read or parse counts as production; a
// missing one does not.
func TestGuardFailsClosedOnUnreadableConfig(t *testing.T) {
	ctx := context.Background()
	denied := &remote.RunError{Template: remote.Cat, Exit: 1, Stderr: []byte("cat: /var/www/staging/wp-config.php: Permission denied\n")}
	tests := []struct {
		name string
		site func() core.Site
		msg  string
	}{
		{"wp-config.php permission denied", func() core.Site {
			return unreadable{stagingSite("staging", "https://staging.shop.test"), "wp-config.php", denied}
		}, "target-says-production: /var/www/staging has a wp-config.php Roadie can't read (permission denied), so it counts as production. If it's staging, make it readable, or pass --target-is-staging staging.shop.test."},
		{"wp-config.php other error", func() core.Site {
			return unreadable{stagingSite("staging", "https://staging.shop.test"), "wp-config.php", errors.New("connection reset")}
		}, "target-says-production: /var/www/staging has a wp-config.php Roadie can't read (the read failed), so it counts as production. If it's staging, make it readable, or pass --target-is-staging staging.shop.test."},
		{".env permission denied", func() core.Site {
			s := testutil.FakeSite(nil)
			s.StackName = core.StackLaravel
			s.EP = core.Endpoint{Kind: core.LocalSite, Display: "/srv/app", Path: "/nonexistent/app"}
			return unreadable{s, ".env", &fs.PathError{Op: "read", Path: ".env", Err: fs.ErrPermission}}
		}, "target-says-production: /srv/app has a .env Roadie can't read (permission denied), so it counts as production. If it's staging, make it readable, or pass --target-is-staging staging.shop.test."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := tt.site()
			err := Guard(ctx, GuardInput{From: dumpEP(), To: s.Endpoint(), ToSite: s, URL: "https://staging.shop.test", Mode: "copy"})
			wantCode(t, err, core.ErrTargetSaysProduction, 3, tt.msg)
			if strings.Contains(err.Error(), "connection reset") {
				t.Errorf("error echoes the remote stderr: %v", err)
			}
		})
	}
	// A wp-config.php that doesn't parse can't say staging either.
	bad := testutil.FakeSite(map[string][]byte{"wp-config.php": []byte("<?php\ndefine( 'WP_ENVIRONMENT_TYPE', 'staging );\n")})
	bad.EP = core.Endpoint{Kind: core.LocalSite, Display: "/var/www/staging", Path: "/nonexistent/staging"}
	wantCode(t, Guard(ctx, GuardInput{From: dumpEP(), To: bad.EP, ToSite: bad, URL: "https://staging.shop.test", Mode: "copy"}),
		core.ErrTargetSaysProduction, 3,
		"target-says-production: /var/www/staging has a wp-config.php Roadie can't parse, so it counts as production. If it's staging, set WP_ENVIRONMENT_TYPE to staging there, or pass --target-is-staging staging.shop.test.")
	// --target-is-staging still lets the run through.
	s := unreadable{stagingSite("staging", "https://staging.shop.test"), "wp-config.php", denied}
	if err := Guard(ctx, GuardInput{From: dumpEP(), To: s.Endpoint(), ToSite: s, URL: "https://staging.shop.test", TargetIsStaging: "staging.shop.test", Mode: "copy"}); err != nil {
		t.Fatal(err)
	}
	// A Laravel site with no wp-config.php and no .env passes.
	l := testutil.FakeSite(nil)
	l.StackName = core.StackLaravel
	l.EP = core.Endpoint{Kind: core.LocalSite, Display: "/srv/app", Path: "/nonexistent/app"}
	if err := Guard(ctx, GuardInput{From: dumpEP(), To: l.EP, ToSite: l, URL: "https://staging.app.test", Mode: "copy"}); err != nil {
		t.Fatal(err)
	}
}

// A siteurl with escape sequences or bidi overrides reaches the message
// without them.
func TestPrintableURL(t *testing.T) {
	esc := string(rune(0x1b))
	for in, want := range map[string]string{
		"https://shop.com\n":                                "https://shop.com",
		"https://sh" + esc + "]0;pwned\a" + "op.com":        "https://sh]0;pwnedop.com",
		"https://" + string(rune(0x202e)) + "moc.pohs":      "https://moc.pohs",
		"https://shop.com/" + string([]byte{0xff}) + "path": "https://shop.com/path",
		"https://staging.shop.test":                         "https://staging.shop.test",
	} {
		if got := printableURL(in); got != want {
			t.Errorf("printableURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// A target whose database can't be reached fails closed: the guard can't
// read siteurl, so it can't tell the target isn't production. A database
// that answers without an options table (an empty one) still passes.
func TestGuardUnreachableDatabase(t *testing.T) {
	ctx := context.Background()
	s := stagingSite("staging", "")
	s.WPFunc = nil
	s.SourceFunc = func([]string) (core.Source, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")}
	}
	in := GuardInput{From: dumpEP(), To: s.EP, ToSite: s, Production: []string{"shop.com"}, URL: "https://staging.shop.test", Mode: "copy"}
	wantCode(t, Guard(ctx, in), core.ErrUnreachable, 3, "")
	if w := s.Writes(); len(w) != 0 {
		t.Errorf("writes %v", w)
	}
	s.SourceFunc = func([]string) (core.Source, error) { return nil, errors.New("table staging.wp_options doesn't exist") }
	if err := Guard(ctx, in); err != nil {
		t.Errorf("empty database: %v", err)
	}
}

// A staging that already calls itself by its own URL is never told to pass
// that URL as production's; --url with it lets prepare go on.
func TestGuardHoldsProductionAdvice(t *testing.T) {
	ctx := context.Background()
	s := stagingSite("staging", "https://staging.shop.test")
	in := GuardInput{From: s.EP, To: s.EP, FromSite: s, ToSite: s, Mode: "prepare"}
	err := Guard(ctx, in)
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrTargetHoldsProduction {
		t.Fatalf("err %v", err)
	}
	if strings.Contains(ce.Msg, "--production-url https://staging.shop.test") || !strings.Contains(ce.Msg, "--production-url <production URL>") ||
		!strings.Contains(ce.Msg, "--url https://staging.shop.test") {
		t.Errorf("advice %q", ce.Msg)
	}
	in.URL = "https://staging.shop.test"
	if err := Guard(ctx, in); err != nil {
		t.Errorf("with --url: %v", err)
	}
}
