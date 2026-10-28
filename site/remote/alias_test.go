// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package remote

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolveAlias(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "wp-cli.yml"), `
path: web
@prod:
  ssh: deploy@shop.example/var/www/shop
@staging:
  ssh: web@staging.example:2222/srv/staging
@other:
  ssh: other.example:/srv/other
@local:
  path: sites/local
@both:
  - @prod
  - @staging
@docker:
  ssh: docker:wordpress
@evil:
  ssh: deploy@-oProxyCommand=x/srv
@nopath:
  ssh: deploy@shop.example
@home:
  ssh: deploy@shop.example~/www
@homeurl:
  ssh: deploy@shop.example/~/www
@homekey:
  ssh: deploy@shop.example
  path: ~/www
@relative:
  ssh: deploy@shop.example
  path: www
@control:
  ssh: deploy@shop.example
  path: "/srv/a\x01b"
`)
	writeFile(t, filepath.Join(root, "wp-cli.local.yml"), `
@staging:
  ssh: web@staging.example:2200/srv/staging
  path: /srv/override
`)
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		alias string
		want  core.Endpoint
	}{
		{"@prod", core.Endpoint{Kind: core.SSHSite, User: "deploy", Host: "shop.example", Path: "/var/www/shop"}},
		{"@staging", core.Endpoint{Kind: core.SSHSite, User: "web", Host: "staging.example", Port: 2200, Path: "/srv/override"}},
		{"@other", core.Endpoint{Kind: core.SSHSite, Host: "other.example", Path: "/srv/other"}},
		{"@local", core.Endpoint{Kind: core.LocalSite, Path: filepath.Join(root, "sites", "local")}},
	}
	for _, tt := range tests {
		got, err := ResolveAlias(deep, tt.alias)
		if err != nil {
			t.Errorf("%s: %v", tt.alias, err)
			continue
		}
		tt.want.Alias, tt.want.Display, tt.want.Raw = tt.alias, tt.alias, tt.alias
		if got != tt.want {
			t.Errorf("%s:\n got %+v\nwant %+v", tt.alias, got, tt.want)
		}
	}
	for alias, msg := range map[string]string{
		"@both":     "alias group",
		"@docker":   "Roadie runs only ssh aliases",
		"@evil":     "can't start with '-'",
		"@nopath":   "names no site folder",
		"@home":     `not "~/www"; Roadie doesn't expand '~'`,
		"@homeurl":  `not "/~/www"; Roadie doesn't expand '~'`,
		"@homekey":  `not "~/www"; Roadie doesn't expand '~'`,
		"@relative": `such as /home/deploy/www, not "www"`,
		"@control":  "holds a control character",
		"@missing":  "is in no wp-cli.yml",
	} {
		_, err := ResolveAlias(deep, alias)
		if err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%s: %v, want %q", alias, err, msg)
		}
		// Every alias stop has the wp-alias code (PRD 17) and a next step.
		var ce *core.Error
		if !errors.As(err, &ce) || ce.Code != core.ErrWPAlias || ce.Exit != 2 ||
			!strings.HasPrefix(ce.Msg, "wp-alias: ") || !strings.HasSuffix(ce.Msg, "or name the site as ssh://user@host/path.") {
			t.Errorf("%s: %#v", alias, err)
		}
		if errors.Is(err, ErrNoAlias) != (alias == "@missing") {
			t.Errorf("%s: matches ErrNoAlias %v", alias, errors.Is(err, ErrNoAlias))
		}
	}
	_, err := ResolveAlias(deep, "@missing")
	if want := "Add @missing to wp-cli.yml with ssh: user@host/path (or path: for a local site), or name the site as ssh://user@host/path."; !strings.HasSuffix(err.Error(), want) {
		t.Errorf("missing: %v", err)
	}
	if _, err := ResolveAlias(t.TempDir(), "@prod"); !errors.Is(err, ErrNoAlias) {
		t.Errorf("no wp-cli.yml: %v", err)
	}
}

// FuzzAliasSSH feeds WP-CLI ssh values and path keys to the alias
// parser: it never panics and an accepted endpoint has a checked host and
// user and an absolute site folder with no '~' and no control character.
func FuzzAliasSSH(f *testing.F) {
	for _, s := range [][2]string{
		{"deploy@shop.example/var/www/shop", ""},
		{"web@staging.example:2222/srv/staging", "/srv/override"},
		{"deploy@shop.example~/www", ""},
		{"ssh:deploy@[::1]:22/srv", ""},
		{"deploy@-oProxyCommand=x/srv", ""},
		{"deploy@shop.example", "~/www"},
	} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, sshVal, pathVal string) {
		e, err := aliasSSH("x", sshVal, pathVal)
		if err != nil {
			return
		}
		if CheckHost(e.Host) != nil || CheckUser(e.User) != nil {
			t.Fatalf("%q %q: accepted host %q user %q", sshVal, pathVal, e.Host, e.User)
		}
		if !strings.HasPrefix(e.Path, "/") || strings.HasPrefix(e.Path, "/~") {
			t.Fatalf("%q %q: accepted folder %q", sshVal, pathVal, e.Path)
		}
		if strings.ContainsFunc(e.Path, func(r rune) bool { return r < ' ' || r == 0x7f }) {
			t.Fatalf("%q %q: control character in %q", sshVal, pathVal, e.Path)
		}
	})
}
