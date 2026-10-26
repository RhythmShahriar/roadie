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
		"@both":    "alias group",
		"@docker":  "Roadie runs only ssh aliases",
		"@evil":    "can't start with '-'",
		"@nopath":  "names no site folder",
		"@missing": "is in no wp-cli.yml",
	} {
		_, err := ResolveAlias(deep, alias)
		if err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%s: %v, want %q", alias, err, msg)
		}
	}
	if _, err := ResolveAlias(t.TempDir(), "@prod"); !errors.Is(err, ErrNoAlias) {
		t.Errorf("no wp-cli.yml: %v", err)
	}
}
