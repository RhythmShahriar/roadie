// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package site

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// sqliteWP writes a SQLite database whose options tables hold the given
// siteurl per prefix and returns its DSN endpoint.
func sqliteWP(t *testing.T, urls map[string]string) core.Endpoint {
	t.Helper()
	p := filepath.Join(t.TempDir(), "wp.sqlite")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for prefix, u := range urls {
		for _, q := range []string{
			"CREATE TABLE " + prefix + "options (option_id INTEGER PRIMARY KEY, option_name TEXT, option_value TEXT, autoload TEXT)",
			"CREATE TABLE " + prefix + "posts (ID INTEGER PRIMARY KEY, post_title TEXT)",
			"CREATE TABLE " + prefix + "users (ID INTEGER PRIMARY KEY, user_login TEXT)",
			"CREATE TABLE " + prefix + "usermeta (umeta_id INTEGER PRIMARY KEY, meta_key TEXT)",
			"CREATE TABLE " + prefix + "postmeta (meta_id INTEGER PRIMARY KEY, meta_key TEXT)",
		} {
			if _, err := db.Exec(q); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.Exec("INSERT INTO "+prefix+"options (option_name, option_value, autoload) VALUES ('siteurl', ?, 'yes'), ('home', ?, 'yes')", u, u); err != nil {
			t.Fatal(err)
		}
	}
	e, err := ParseEndpoint("sqlite://" + p)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// A database target named by a DSN is checked by the siteurl it holds,
// under any prefix: a tunnel to production's database has a loopback
// host and no wp-config.php to say production.
func TestGuardDSNTargetSiteURL(t *testing.T) {
	ctx := context.Background()
	for _, prefix := range []string{"wp_", "shop_"} {
		to := sqliteWP(t, map[string]string{prefix: "https://www.shop.com"})
		s, err := Open(ctx, to, Options{})
		if err != nil {
			t.Fatal(err)
		}
		in := GuardInput{From: dumpEP(), To: to, ToSite: s, Production: []string{"shop.com", "www.shop.com"}, URL: "https://staging.shop.test", Mode: "copy"}
		wantCode(t, Guard(ctx, in), core.ErrTargetIsProduction, 3,
			"target-is-production: "+to.Display+" is www.shop.com, which production: lists. Roadie never writes to production. Point --to at the staging copy.")
		_ = s.Close()
	}
	// A staging database passes.
	to := sqliteWP(t, map[string]string{"wp_": "https://staging.shop.test"})
	s, err := Open(ctx, to, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if err := Guard(ctx, GuardInput{From: dumpEP(), To: to, ToSite: s, Production: []string{"shop.com"}, Mode: "copy"}); err != nil {
		t.Fatal(err)
	}
}

// A host production: lists matches however the target spells it: an IDN
// in Unicode or punycode, fullwidth letters, an ideographic dot, a
// trailing dot, upper case or a port.
func TestGuardHostSpellings(t *testing.T) {
	globs := productionHosts([]string{"bücher.de", "https://shop.com", "*.example.org", "203.0.113.7", "2001:db8::1"})
	for _, h := range []string{
		"bücher.de", "BÜCHER.DE", "xn--bcher-kva.de", "www.bücher.de", "ｓｈｏｐ.com", "shop。com",
		"SHOP.COM.", "SHOP.COM..", "shop.com:8443", "a.b.example.org",
		"[::ffff:203.0.113.7]", "203.0.113.7.", "[2001:0db8:0000::0001]:8443",
	} {
		if !matchHost(normHost(urlHost(h)), globs) {
			t.Errorf("%q isn't production", h)
		}
	}
	for _, h := range []string{"staging.shop.test", "example.org", "bucher.de", "shop.co"} {
		if matchHost(normHost(urlHost(h)), globs) {
			t.Errorf("%q is production", h)
		}
	}
}

// A copy's target that answers to the source's own site URL is
// production even when production: is empty: the guard reads the
// source's siteurl once pass 1 has it. A prepare (same place), a staging
// refreshed from its own backup (--url is that host) and a source on
// this machine pass.
func TestGuardSourceSiteURL(t *testing.T) {
	ctx := context.Background()
	to := sqliteWP(t, map[string]string{"wp_": "https://www.shop.example.com"})
	s, err := Open(ctx, to, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	in := GuardInput{From: dumpEP(), To: to, ToSite: s, URL: "https://staging.shop.test", Mode: "copy",
		SourceURLs: []string{"https://shop.example.com", "https://shop.example.com/"}}
	wantCode(t, Guard(ctx, in), core.ErrTargetIsProduction, 3,
		"target-is-production: "+to.Display+" is www.shop.example.com, the source's own site URL. Roadie never writes to production. Point --to at the staging copy; if this is a staging that still holds a raw copy of production, run prepare on it instead.")
	in.Mode = "plan"
	wantCode(t, Guard(ctx, in), core.ErrTargetIsProduction, 3, "")
	for name, mod := range map[string]func(*GuardInput){
		"prepare": func(g *GuardInput) { g.Mode = "prepare"; g.URL = "https://www.shop.example.com" },
		"own backup": func(g *GuardInput) {
			g.URL = "https://www.shop.example.com"
			g.SourceURLs = []string{g.URL}
		},
		"local": func(g *GuardInput) { g.SourceURLs = []string{"http://localhost:8080"} },
		"other": func(g *GuardInput) { g.SourceURLs = []string{"https://other.example.net"} },
	} {
		g := in
		g.Mode = "copy"
		mod(&g)
		if err := Guard(ctx, g); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if got := SourceHosts([]string{"https://A.example.com/", "http://127.0.0.1", "https://a.example.com", "https://staging.shop.test"}, "https://staging.shop.test"); len(got) != 1 || got[0] != "a.example.com" {
		t.Errorf("SourceHosts = %v", got)
	}
}

// errReader is a site whose files can't be read.
type errReader struct{ err error }

func (r errReader) ReadFile(context.Context, string) ([]byte, error) { return nil, r.err }

// TestUnreadableConfigHasCode: a wp-config.php or .env Roadie can't read
// stops with site-config and says why, so the stop box names a code.
func TestUnreadableConfigHasCode(t *testing.T) {
	ctx := context.Background()
	perm := &fs.PathError{Op: "openat", Path: "wp-config.php", Err: fs.ErrPermission}
	for _, stack := range []core.Stack{core.StackWordPress, core.StackLaravel} {
		_, err := readDBConfig(ctx, errReader{perm}, stack, "/srv/site", false)
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != core.ErrSiteConfig || ce.Exit != 3 || !strings.Contains(ce.Msg, "can't be read (permission denied)") {
			t.Errorf("%s: %v", stack, err)
		}
	}
}
