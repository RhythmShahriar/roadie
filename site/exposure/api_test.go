// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package exposure

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/rhythm/kit/cli"
	"golang.org/x/crypto/bcrypt"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/site/killswitch"
)

// ownRobots is a site's own robots.txt that lets crawlers in.
const ownRobots = "User-agent: *\nDisallow: /wp-admin/\nAllow: /wp-admin/admin-ajax.php\n\nSitemap: https://shop.example/sitemap.xml\n"

// site is a fake site of stack with files and a blog_public option.
func site(stack core.Stack, files map[string]string, blogPublic string) *testutil.Site {
	fs := map[string][]byte{}
	for k, v := range files {
		fs[k] = []byte(v)
	}
	s := testutil.FakeSite(fs)
	s.StackName = stack
	s.SourceFunc = func(tables []string) (core.Source, error) {
		if blogPublic == "" || !slices.Contains(tables, "wp_options") {
			return testutil.FakeSource(nil, nil), nil
		}
		tb := &core.Table{Name: "wp_options", Columns: []core.Column{{Name: "option_name"}, {Name: "option_value"}}}
		b := &core.Batch{Seq: 1, Table: tb, Rows: []core.Row{{Values: []core.Value{
			{Kind: core.String, Raw: []byte("blog_public")}, {Kind: core.String, Raw: []byte(blogPublic)},
		}}}}
		return testutil.FakeSource(nil, []*core.Batch{b}), nil
	}
	return s
}

func step(t *testing.T, o killswitch.Options) core.Step {
	t.Helper()
	steps := Steps(o)
	if len(steps) != 1 || steps[0].ID() != core.FindingSearchIndexable || steps[0].Phase() != "files" {
		t.Fatalf("steps %v", steps)
	}
	return steps[0]
}

func checkOf(t *testing.T, st core.Step, s core.Site) (core.SideEffect, []core.Finding) {
	t.Helper()
	se, fs, err := st.Check(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if se.ID != core.FindingSearchIndexable {
		t.Errorf("side effect ID %s", se.ID)
	}
	for _, f := range fs {
		if f.ID != core.FindingSearchIndexable || f.Severity != core.SeverityMedium || len(f.Locations) == 0 || f.Fix == "" {
			t.Errorf("finding %+v", f)
		}
	}
	return se, fs
}

// TestRobotsBackedUpOnce: the first run keeps the site's own file as the
// backup; a second run changes nothing and never overwrites that backup.
func TestRobotsBackedUpOnce(t *testing.T) {
	ctx := context.Background()
	st := step(t, killswitch.Options{Noindex: true})
	s := site(core.StackWordPress, map[string]string{"robots.txt": ownRobots}, "1")
	for run := 1; run <= 2; run++ {
		if err := st.Apply(ctx, s); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		files := s.Files()
		if string(files["robots.txt"]) != killswitch.RobotsTxt() {
			t.Fatalf("run %d: robots.txt %q", run, files["robots.txt"])
		}
		if string(files["robots.txt"+RobotsBackupSuffix]) != ownRobots {
			t.Fatalf("run %d: backup %q", run, files["robots.txt"+RobotsBackupSuffix])
		}
	}
	if w := s.Writes(); len(w) != 2 {
		t.Fatalf("writes %v, want the backup and robots.txt once", w)
	}

	// A site file put back by hand after the first run is replaced again,
	// but the first backup stays.
	if err := s.WriteFile(ctx, "robots.txt", []byte("User-agent: *\nAllow: /\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.Apply(ctx, s); err != nil {
		t.Fatal(err)
	}
	if got := string(s.Files()["robots.txt"+RobotsBackupSuffix]); got != ownRobots {
		t.Fatalf("backup overwritten: %q", got)
	}
}

func TestRobotsNoFileWritesNothing(t *testing.T) {
	st := step(t, killswitch.Options{Noindex: true})
	s := site(core.StackWordPress, map[string]string{"wp-config.php": "<?php"}, "1")
	if err := st.Apply(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if w := s.Writes(); len(w) != 0 {
		t.Fatalf("writes %v: the mu-plugin serves robots.txt", w)
	}
	plan, err := st.Plan(context.Background(), s)
	if err != nil || len(plan) != 1 || plan[0].File != killswitch.MuPluginPath {
		t.Fatalf("plan %+v %v", plan, err)
	}
}

func TestNoindexOff(t *testing.T) {
	st := step(t, killswitch.Options{})
	s := site(core.StackWordPress, map[string]string{"robots.txt": ownRobots}, "1")
	if err := st.Apply(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if len(s.Writes()) != 0 {
		t.Fatal("--noindex=off wrote files")
	}
	plan, _ := st.Plan(context.Background(), s)
	if len(plan) != 1 || !strings.HasPrefix(plan[0].What, "left on (--noindex=off)") {
		t.Fatalf("plan %+v", plan)
	}
	// The check still tells the truth.
	if se, fs := checkOf(t, st, s); se.Status != "on" || len(fs) != 1 {
		t.Fatalf("check %+v %v", se, fs)
	}
}

func TestPlan(t *testing.T) {
	ctx := context.Background()
	st := step(t, killswitch.Options{Noindex: true})
	s := site(core.StackWordPress, map[string]string{"robots.txt": ownRobots}, "1")
	plan, err := st.Plan(ctx, s)
	if err != nil || len(plan) != 2 || plan[1].File != "robots.txt" || !strings.Contains(plan[1].What, "backed up to robots.txt.roadie-backup") {
		t.Fatalf("plan %+v %v", plan, err)
	}
	if err := st.Apply(ctx, s); err != nil {
		t.Fatal(err)
	}
	plan, _ = st.Plan(ctx, s)
	if plan[1].What != "already blocks every crawler" {
		t.Fatalf("plan after apply %+v", plan)
	}
	l := site(core.StackLaravel, nil, "")
	plan, _ = st.Plan(ctx, l)
	if len(plan) != 1 || plan[0].File != "public/robots.txt" || !strings.HasPrefix(plan[0].What, "written") {
		t.Fatalf("laravel plan %+v", plan)
	}
	if plan, _ := st.Plan(ctx, site(core.StackSQL, nil, "")); len(plan) != 0 {
		t.Fatalf("sql plan %+v", plan)
	}
}

// TestCheck holds the positive (indexable) and negative fixtures.
func TestCheck(t *testing.T) {
	mu := string(killswitch.MuPlugin(killswitch.Options{Noindex: true}))
	muNoNoindex := string(killswitch.MuPlugin(killswitch.Options{}))
	blocking := killswitch.RobotsTxt()
	cases := []struct {
		name   string
		stack  core.Stack
		files  map[string]string
		public string
		status string
	}{
		{"wordpress bare", core.StackWordPress, nil, "1", "on"},
		{"wordpress no options", core.StackWordPress, nil, "", "on"},
		{"mu-plugin without noindex", core.StackWordPress, map[string]string{killswitch.MuPluginPath: muNoNoindex}, "1", "on"},
		{"robots allows", core.StackWordPress, map[string]string{"robots.txt": ownRobots, killswitch.MuPluginPath: mu}, "0", "on"},
		{"foreign mu-plugin", core.StackWordPress, map[string]string{killswitch.MuPluginPath: "<?php // 'wp_robots'"}, "1", "on"},
		{"laravel no robots", core.StackLaravel, nil, "", "on"},
		{"laravel default robots", core.StackLaravel, map[string]string{"public/robots.txt": "User-agent: *\nDisallow:\n"}, "", "on"},

		{"mu-plugin noindex", core.StackWordPress, map[string]string{killswitch.MuPluginPath: mu}, "1", "off"},
		{"mu-plugin and robots", core.StackWordPress, map[string]string{killswitch.MuPluginPath: mu, "robots.txt": blocking}, "1", "off"},
		{"blocking robots only", core.StackWordPress, map[string]string{"robots.txt": "User-agent: *\nDisallow: /\n"}, "1", "off"},
		{"blog_public 0", core.StackWordPress, nil, "0", "off"},
		{"laravel blocking", core.StackLaravel, map[string]string{"public/robots.txt": blocking}, "", "off"},
		{"sql", core.StackSQL, nil, "", "not-present"},
	}
	st := step(t, killswitch.Options{Noindex: true})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			se, fs := checkOf(t, st, site(c.stack, c.files, c.public))
			if se.Status != c.status {
				t.Fatalf("status %q (%s), want %q", se.Status, se.How, c.status)
			}
			if (c.status == "on") != (len(fs) == 1) {
				t.Fatalf("findings %+v", fs)
			}
			if c.status == "off" && se.Undo == "" {
				t.Error("no undo")
			}
		})
	}
}

// TestApplyThenCheck: a WordPress site with its own robots.txt and a
// Laravel site without one both check clean after Apply.
func TestApplyThenCheck(t *testing.T) {
	ctx := context.Background()
	st := step(t, killswitch.Options{Noindex: true})
	for _, s := range []*testutil.Site{
		site(core.StackWordPress, map[string]string{"robots.txt": ownRobots}, "1"),
		site(core.StackLaravel, map[string]string{"public/robots.txt": "User-agent: *\nDisallow:\n"}, ""),
		site(core.StackLaravel, nil, ""),
	} {
		if se, _ := checkOf(t, st, s); se.Status != "on" {
			t.Fatalf("%s before: %+v", s.StackName, se)
		}
		if err := st.Apply(ctx, s); err != nil {
			t.Fatal(err)
		}
		if se, fs := checkOf(t, st, s); se.Status != "off" || len(fs) != 0 {
			t.Fatalf("%s after: %+v %v", s.StackName, se, fs)
		}
	}
}

func TestApplyErrors(t *testing.T) {
	st := step(t, killswitch.Options{Noindex: true})
	s := site(core.StackWordPress, map[string]string{"robots.txt": ownRobots}, "1")
	s.RO = true
	err := st.Apply(context.Background(), s)
	if !errors.Is(err, core.ErrReadOnly) {
		t.Fatalf("read-only: %v", err)
	}
	failing := &failSite{Site: site(core.StackWordPress, map[string]string{"robots.txt": ownRobots}, "1")}
	err = st.Apply(context.Background(), failing)
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrKillSwitch || ce.Exit != 1 {
		t.Fatalf("write error: %v", err)
	}
	want := "search.indexable: couldn't write robots.txt.roadie-backup (permission denied), so search engines may still crawl this copy. Fix it, then: rhythm-roadie prepare /srv/staging --only noindex."
	if ce.Msg != want {
		t.Fatalf("message\n%s\nwant\n%s", ce.Msg, want)
	}
}

// failSite refuses every write with a permission error.
type failSite struct{ *testutil.Site }

func (f *failSite) WriteFile(_ context.Context, rel string, _ []byte, _ fs.FileMode) error {
	return &fs.PathError{Op: "open", Path: rel, Err: fs.ErrPermission}
}

func TestBlocks(t *testing.T) {
	yes := []string{
		killswitch.RobotsTxt(),
		"User-agent: *\nDisallow: /\n",
		"user-agent: *  # all\r\ndisallow: /\r\n",
		"User-agent: GPTBot\nUser-agent: *\nDisallow: /\n\nUser-agent: Googlebot\nAllow: /\n",
		"User-agent: Googlebot\nDisallow:\n\nUser-agent: *\nDisallow: /\nAllow:\n",
	}
	no := []string{
		"",
		"User-agent: *\nDisallow:\n",
		"User-agent: *\nDisallow: /wp-admin/\n",
		"User-agent: Googlebot\nDisallow: /\n",
		"User-agent: *\nDisallow: /\nAllow: /shop/\n",
		"User-agent: *\nAllow: /\n\nUser-agent: *\nDisallow: /\n",
		"Disallow: /\n",
		"# User-agent: *\n# Disallow: /\n",
	}
	for _, r := range yes {
		if !Blocks([]byte(r)) {
			t.Errorf("Blocks(%q) = false", r)
		}
	}
	for _, r := range no {
		if Blocks([]byte(r)) {
			t.Errorf("Blocks(%q) = true", r)
		}
	}
}

func FuzzBlocks(f *testing.F) {
	f.Add([]byte(killswitch.RobotsTxt()))
	f.Add([]byte(ownRobots))
	f.Add([]byte("User-agent: *\nDisallow: /\nAllow: /x\n"))
	f.Fuzz(func(t *testing.T, b []byte) {
		got := Blocks(b)
		if got != Blocks(b) {
			t.Fatal("not deterministic")
		}
		// Only a Disallow rule can block.
		if got && !strings.Contains(strings.ToLower(string(b)), "disallow") {
			t.Fatalf("blocks without a Disallow rule: %q", b)
		}
	})
}

// fixedAuth makes BasicAuth deterministic for the golden files.
func fixedAuth(t *testing.T) {
	t.Helper()
	pw, hash := newPassword, hashPassword
	t.Cleanup(func() { newPassword, hashPassword = pw, hash })
	newPassword = func() (string, error) { return "Fixture0Password0000", nil }
	hashPassword = func(string) (string, error) {
		return "$2a$05$" + strings.Repeat("A", 22) + strings.Repeat("b", 31), nil
	}
}

func TestBasicAuthGolden(t *testing.T) {
	fixedAuth(t)
	files, pw, err := BasicAuth("staging")
	if err != nil {
		t.Fatal(err)
	}
	if pw != "Fixture0Password0000" {
		t.Fatalf("password %q", pw)
	}
	names := []string{FileApache, FilePasswd, FileNginx, FileCaddy}
	if len(files) != len(names) {
		t.Fatalf("files %v", files)
	}
	for _, n := range names {
		b, ok := files[n]
		if !ok {
			t.Fatalf("no %s", n)
		}
		if strings.Contains(string(b), pw) {
			t.Errorf("%s holds the password", n)
		}
		testutil.Golden(t, strings.TrimPrefix(n, "."), b)
	}
}

func TestBasicAuthReal(t *testing.T) {
	files, pw, err := BasicAuth("staging")
	if err != nil {
		t.Fatal(err)
	}
	if len(pw) != passwordLen || strings.Trim(pw, passwordABC) != "" {
		t.Fatalf("password %q", pw)
	}
	_, pw2, _ := BasicAuth("staging")
	if pw == pw2 {
		t.Fatal("two runs chose the same password")
	}
	user, hash, ok := strings.Cut(strings.TrimSpace(string(files[FilePasswd])), ":")
	if !ok || user != "staging" || !strings.HasPrefix(hash, "$2y$05$") {
		t.Fatalf(".htpasswd %q", files[FilePasswd])
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) != nil {
		t.Fatal(".htpasswd hash doesn't match the password")
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw2)) == nil {
		t.Fatal(".htpasswd hash matches another password")
	}
	caddy := string(files[FileCaddy])
	i := strings.Index(caddy, "staging $2a$")
	if i < 0 {
		t.Fatalf("Caddyfile %q", caddy)
	}
	ch := strings.Fields(caddy[i:])[1]
	if bcrypt.CompareHashAndPassword([]byte(ch), []byte(pw)) != nil {
		t.Fatal("Caddyfile hash doesn't match the password")
	}
	for _, s := range []string{"Require ip 127.0.0.1 ::1", "<RequireAny>", "Require valid-user", "AuthUserFile " + PasswdFile} {
		if !strings.Contains(string(files[FileApache]), s) {
			t.Errorf("apache snippet lacks %q", s)
		}
	}
	for _, s := range []string{"satisfy any;", "allow 127.0.0.1;", "allow ::1;", "deny all;", "auth_basic_user_file " + PasswdFile + ";"} {
		if !strings.Contains(string(files[FileNginx]), s) {
			t.Errorf("nginx snippet lacks %q", s)
		}
	}
}

// TestBasicAuthProxyWarning: every snippet that lets loopback pass warns
// that a reverse proxy on the same host makes every request loopback.
func TestBasicAuthProxyWarning(t *testing.T) {
	files, _, err := BasicAuth("staging")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{FileApache, FileNginx, FileCaddy} {
		if !strings.Contains(string(files[f]), "# Behind a reverse proxy on this host every request is loopback: remove\n# the loopback allow lines there.\n") {
			t.Errorf("%s lacks the reverse proxy warning", f)
		}
	}
	if strings.Contains(string(files[FilePasswd]), "proxy") {
		t.Error(".htpasswd holds a comment")
	}
}

func TestBasicAuthBadUser(t *testing.T) {
	for _, u := range []string{"", "a:b", "a b", "a\nb", `a"b`, "{x}", strings.Repeat("u", 65), "ü"} {
		_, _, err := BasicAuth(u)
		var ue cli.UsageError
		if !errors.As(err, &ue) {
			t.Errorf("BasicAuth(%q): %v, want a usage error", u, err)
		}
	}
	for _, u := range []string{"staging", "qa.team", "first-last_2", "me@agency.example"} {
		if _, _, err := BasicAuth(u); err != nil {
			t.Errorf("BasicAuth(%q): %v", u, err)
		}
	}
}

func TestWriteBasicAuth(t *testing.T) {
	fixedAuth(t)
	files, _, err := BasicAuth("staging")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	out, err := WriteBasicAuth(dir, files)
	if err != nil {
		t.Fatal(err)
	}
	if out != filepath.Join(dir, ".roadie", "basic-auth") {
		t.Fatalf("dir %s", out)
	}
	for n, b := range files {
		got, err := os.ReadFile(filepath.Join(out, n)) // #nosec G304 -- a test temp file
		if err != nil || string(got) != string(b) {
			t.Fatalf("%s: %v", n, err)
		}
		if runtime.GOOS != "windows" {
			if fi, _ := os.Stat(filepath.Join(out, n)); fi.Mode().Perm() != 0o600 {
				t.Errorf("%s mode %v", n, fi.Mode().Perm())
			}
		}
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(out); fi.Mode().Perm() != 0o700 {
			t.Errorf("folder mode %v", fi.Mode().Perm())
		}
	}
	if _, err := WriteBasicAuth(dir, map[string][]byte{"../x": nil}); err == nil {
		t.Fatal("wrote outside the folder")
	}
}
