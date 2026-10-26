// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package roadiecfg

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/pseudo"
)

func TestInitTemplateGolden(t *testing.T) {
	for _, s := range []string{"wordpress", "laravel", "sql"} {
		testutil.Golden(t, "init-"+s+".yaml", InitTemplate(s))
	}
	// WooCommerce, auto and "" get the WordPress file byte for byte; only
	// the CLI's first line differs.
	for _, s := range []string{"woocommerce", "auto", ""} {
		if !bytes.Equal(InitTemplate(s), InitTemplate("wordpress")) {
			t.Errorf("%q differs from wordpress", s)
		}
	}
	if InitTemplate("drupal") != nil {
		t.Error("an unknown stack has a template")
	}
	if InitMessage("woocommerce") != "Wrote .roadie/config.yaml for a WooCommerce site." || InitMessage("wordpress") != "Wrote .roadie/config.yaml for a WordPress site." {
		t.Errorf("messages %q %q", InitMessage("woocommerce"), InitMessage("wordpress"))
	}
}

// The file config init writes loads back as the defaults plus its stack.
func TestInitTemplateRoundTrip(t *testing.T) {
	for _, s := range InitStacks() {
		c, err := Load(writeConfig(t, string(InitTemplate(s))), nil)
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		want := Default()
		switch s {
		case "laravel":
			want.Stack = "laravel"
			want.Laravel.Tables = map[string]map[string]string{"users": {"email": "email", "name": "full_name", "password": "password"}}
		case "sql":
			want.Stack = "sql"
			want.Columns = []ColumnRule{{Table: "customers", Column: "email", Rule: "email"}, {Table: "customers", Column: "notes", Rule: "text"}}
		}
		if !reflect.DeepEqual(comparable(c), comparable(want)) {
			t.Errorf("%s: loads as\n%+v\nwant\n%+v", s, comparable(c), comparable(want))
		}
	}
}

func TestSuggest(t *testing.T) {
	block := Suggest([]core.Uncovered{
		{Table: "wp_loyalty_members", Column: "email", Rate: 98},
		{Table: "wp_loyalty_members", Column: "phone", Rate: 41},
		{Table: "crm_contacts", Column: "surname", Rate: 0},
		{Table: "crm_contacts", Column: "remarks", Rate: 23},
	})
	testutil.Golden(t, "suggest.yaml", block)
	if Suggest(nil) != nil {
		t.Error("no columns gives a block")
	}
	full := WithSuggestions(InitTemplate("wordpress"), block)
	testutil.Golden(t, "init-from.yaml", full)
	// Suggestions are comments: the file still loads as the defaults.
	c, err := Load(writeConfig(t, string(full)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Columns) != 0 {
		t.Errorf("a suggestion is active: %v", c.Columns)
	}
	// Uncommenting the suggested entries gives working rules.
	on := strings.Replace(string(full), "#  - table: wp_loyalty_members\n#    column: email\n#    rule: email",
		"  - table: wp_loyalty_members\n    column: email\n    rule: email", 1)
	c, err = Load(writeConfig(t, on), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Columns) != 1 || c.Columns[0].Column != "email" {
		t.Errorf("uncommented entry: %v", c.Columns)
	}
}

// A schema name with a newline can't turn into an active setting: names
// that aren't plain are quoted; uncommenting the entry gives the name
// back exactly.
func TestSuggestQuotesOddNames(t *testing.T) {
	odd := "notes\nsource: ssh://deploy@shop.com/var/www"
	block := Suggest([]core.Uncovered{
		{Table: "crm contacts", Column: odd, Rate: 50},
		{Table: "wp_loyalty$v2", Column: "e-mail", Rate: 98},
	})
	want := "#  - table: \"crm contacts\"\n#    column: \"notes\\nsource: ssh://deploy@shop.com/var/www\"\n"
	if !strings.Contains(string(block), want) {
		t.Fatalf("block:\n%s", block)
	}
	if !strings.Contains(string(block), "#  - table: wp_loyalty$v2\n#    column: e-mail\n") {
		t.Errorf("a plain name was quoted:\n%s", block)
	}
	full := WithSuggestions(InitTemplate("sql"), block)
	c, err := Load(writeConfig(t, string(full)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Source != "" {
		t.Fatalf("a schema name set source: %q", c.Source)
	}
	on := strings.Replace(string(full), "#  - table: \"crm contacts\"\n#    column: ", "  - table: \"crm contacts\"\n    column: ", 1)
	on = strings.Replace(on, "#    rule: text", "    rule: text", 1)
	c, err = Load(writeConfig(t, on), nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(c.Columns); n == 0 || c.Columns[n-1].Column != odd || c.Columns[n-1].Table != "crm contacts" {
		t.Errorf("uncommented entry: %+v", c.Columns)
	}
}

func TestRuleForColumn(t *testing.T) {
	for col, want := range map[string]string{
		"email": "email", "EmailAddress": "email", "e_mail": "email", "mail": "email", "phone_number": "phone", "tel": "phone",
		"mobile": "phone", "first_name": "first_name", "lastname": "last_name", "surname": "last_name", "full_name": "full_name",
		"name": "full_name", "street_1": "street", "billing_address": "address", "city": "city", "zip": "postcode",
		"postcode": "postcode", "ip": "ip", "ip_address": "ip", "dob": "dob", "birth_date": "dob", "iban": "blank",
		"company_name": "company", "notes": "text", "hotel": "text", "zipper_size": "postcode",
	} {
		if got := RuleForColumn(col); got != want {
			t.Errorf("%s: %s, want %s", col, got, want)
		}
	}
}

func TestParseRule(t *testing.T) {
	good := map[string]RuleSpec{
		"email":                    {Action: core.Fake, Fake: core.KEmail},
		"name":                     {Action: core.Fake, Fake: core.KFullName},
		"text":                     {Action: core.Scrub, Fake: core.KText},
		"blank":                    {Action: core.Blank},
		"null":                     {Action: core.SetNull},
		"keep":                     {Action: core.Keep},
		"password":                 {Action: core.ResetPassword},
		"delete-row":               {Action: core.DeleteRow},
		"fixed:paused":             {Action: core.Fixed, Value: "paused"},
		"fixed:":                   {Action: core.Fixed},
		"json:billing.email=email": {Action: core.Fake, Fake: core.KEmail, Path: "json:billing.email"},
		"serialized:secret=blank":  {Action: core.Blank, Path: "serialized:secret"},
		"serialized:ip=ip":         {Action: core.Fake, Fake: core.KIP, Path: "serialized:ip"},
	}
	for in, want := range good {
		got, err := ParseRule(in)
		if err != nil || got != want {
			t.Errorf("%q: %+v %v, want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "emial", "json:", "json:a", "json:=email", "json:a=", "json:a=delete-row", "json:a=json:b=email", "fake"} {
		if _, err := ParseRule(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestValidGlob(t *testing.T) {
	for _, g := range []string{"wp_*", "wp_?_x", "[ab]*", "wp_\\*", "exact"} {
		if ValidGlob(g) != nil {
			t.Errorf("%q refused", g)
		}
	}
	for _, g := range []string{"wp_[", "[]", "a\\", "x[^"} {
		if ValidGlob(g) == nil {
			t.Errorf("%q accepted", g)
		}
	}
}

// LoadKey: ROADIE_KEY wins, then .roadie/key, else a new key not saved.
func TestKeyPrecedence(t *testing.T) {
	var envKey, fileKey pseudo.Key
	for i := range envKey {
		envKey[i], fileKey[i] = byte(i), byte(255-i)
	}
	b64 := func(k pseudo.Key) string { return base64.StdEncoding.EncodeToString(k[:]) }
	cases := []struct {
		name     string
		env      map[string]string
		file     bool
		want     *pseudo.Key
		source   string
		errCode  string
		badFile  bool
		emptyEnv bool
	}{
		{name: "env wins over the file", env: map[string]string{"ROADIE_KEY": b64(envKey)}, file: true, want: &envKey, source: KeyFromEnv},
		{name: "env alone", env: map[string]string{"ROADIE_KEY": b64(envKey)}, want: &envKey, source: KeyFromEnv},
		{name: "file", file: true, want: &fileKey, source: KeyFromFile},
		{name: "empty env falls to the file", env: map[string]string{"ROADIE_KEY": ""}, file: true, want: &fileKey, source: KeyFromFile},
		{name: "new", source: KeyFromNew},
		{name: "bad env", env: map[string]string{"ROADIE_KEY": "c2hvcnQ="}, file: true, errCode: core.ErrConfig},
		{name: "bad file", badFile: true, errCode: core.ErrConfig},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.file {
				if err := SaveKey(dir, fileKey); err != nil {
					t.Fatal(err)
				}
			}
			if tc.badFile {
				if err := os.MkdirAll(filepath.Join(dir, Dir), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, Dir, "key"), []byte("nope\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			k, src, err := LoadKey(dir, envOf(tc.env, nil))
			if tc.errCode != "" {
				var ce *core.Error
				if !errors.As(err, &ce) || ce.Code != tc.errCode || ce.Exit != 2 {
					t.Fatalf("err %v, want code %s", err, tc.errCode)
				}
				if strings.Contains(ce.Msg, tc.env["ROADIE_KEY"]) && tc.env["ROADIE_KEY"] != "" {
					t.Error("the message holds the key")
				}
				return
			}
			if err != nil || src != tc.source {
				t.Fatalf("source %q err %v, want %q", src, err, tc.source)
			}
			if tc.want != nil && k != *tc.want {
				t.Error("wrong key")
			}
			if src == KeyFromNew {
				if k == (pseudo.Key{}) {
					t.Error("new key is zero")
				}
				if _, err := os.Stat(filepath.Join(dir, Dir, "key")); !errors.Is(err, os.ErrNotExist) {
					t.Error("LoadKey saved a new key")
				}
			}
		})
	}
}

func TestSaveAndForgetKey(t *testing.T) {
	dir := t.TempDir()
	var k pseudo.Key
	k[0] = 7
	if err := SaveKey(dir, k); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" { // NTFS ACLs, not mode bits, guard the file there
		for path, want := range map[string]os.FileMode{filepath.Join(dir, Dir): 0o700, filepath.Join(dir, Dir, "key"): 0o600} {
			fi, err := os.Stat(path)
			if err != nil || fi.Mode().Perm() != want {
				t.Errorf("%s: %v %v, want %v", path, fi.Mode().Perm(), err, want)
			}
		}
	}
	got, src, err := LoadKey(dir, nil)
	if err != nil || src != KeyFromFile || got != k {
		t.Fatalf("reload %v %s", err, src)
	}
	if err := ForgetKey(dir); err != nil {
		t.Fatal(err)
	}
	if err := ForgetKey(dir); err != nil {
		t.Errorf("second forget: %v", err)
	}
	if _, src, _ := LoadKey(dir, nil); src != KeyFromNew {
		t.Errorf("after forget: %s", src)
	}
}

func TestRunKey(t *testing.T) {
	dir := t.TempDir()
	c := Default()
	k1, src, stable, err := RunKey(dir, c, false, nil)
	if err != nil || src != KeyFromNew || stable {
		t.Fatalf("per-run: %s %v %v", src, stable, err)
	}
	k2, _, _, _ := RunKey(dir, c, false, nil)
	if k1 == k2 {
		t.Error("per-run gave the same key twice")
	}
	if _, err := os.Stat(filepath.Join(dir, Dir, "key")); !errors.Is(err, os.ErrNotExist) {
		t.Error("per-run saved its key")
	}
	// No config means the defaults: per-run, nothing saved.
	if _, src, stable, err := RunKey(dir, nil, false, nil); err != nil || src != KeyFromNew || stable {
		t.Errorf("nil config: %s %v %v", src, stable, err)
	}
	if _, err := os.Stat(filepath.Join(dir, Dir, "key")); !errors.Is(err, os.ErrNotExist) {
		t.Error("a nil config saved a key")
	}
	c.Pseudonymise.Consistency = "stable"
	s1, src, stable, err := RunKey(dir, c, false, nil)
	if err != nil || src != KeyFromNew || !stable {
		t.Fatalf("stable first: %s %v %v", src, stable, err)
	}
	s2, src, _, _ := RunKey(dir, c, false, nil)
	if src != KeyFromFile || s1 != s2 {
		t.Errorf("stable second: %s", src)
	}
	c.Pseudonymise.Consistency = "per-run"
	s3, _, stable, _ := RunKey(dir, c, true, nil)
	if s3 != s1 || !stable {
		t.Error("--keep-key didn't reuse the saved key")
	}
}

// excludeLines returns the non-comment lines of an exclude file.
func excludeLines(t *testing.T, file string) []string {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out
}

func TestExcludeFromGit(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "info"), 0o750); err != nil {
		t.Fatal(err)
	}
	exclude := filepath.Join(root, ".git", "info", "exclude")
	if err := os.WriteFile(exclude, []byte("# git's own comment\n*.swp"), 0o644); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(root, "sites", "shop")
	if err := os.MkdirAll(project, 0o750); err != nil {
		t.Fatal(err)
	}
	changed, err := ExcludeFromGit(project, GitExcludes())
	if err != nil || !changed {
		t.Fatalf("first: %v %v", changed, err)
	}
	want := []string{"*.swp", "/sites/shop/.roadie/reports/", "/sites/shop/.roadie/key", "/sites/shop/.roadie/backups/", "/sites/shop/.roadie/basic-auth/"}
	if got := excludeLines(t, exclude); !reflect.DeepEqual(got, want) {
		t.Errorf("lines %q, want %q", got, want)
	}
	changed, err = ExcludeFromGit(project, GitExcludes())
	if err != nil || changed {
		t.Errorf("second run changed it: %v %v", changed, err)
	}
	// Outside a work tree nothing happens.
	if changed, err := ExcludeFromGit(t.TempDir(), GitExcludes()); err != nil || changed {
		t.Errorf("outside git: %v %v", changed, err)
	}
}

// gitEscape leaves plain paths alone and escapes glob syntax.
func TestGitEscape(t *testing.T) {
	for in, want := range map[string]string{
		"sites/shop/.roadie/key": "sites/shop/.roadie/key",
		"shop[1]/.roadie/key":    `shop\[1\]/.roadie/key`,
		"a*b?/c\\d":              `a\*b\?/c\\d`,
		"!x/#y":                  "!x/#y",
		"dir /x ":                `dir /x\ `,
	} {
		if got := gitEscape(in); got != want {
			t.Errorf("gitEscape(%q) = %q, want %q", in, got, want)
		}
	}
}

// A linked worktree has a .git file; its excludes live in the main
// repository's info/exclude, through the gitdir's commondir.
func TestExcludeFromGitWorktree(t *testing.T) {
	base := t.TempDir()
	main := filepath.Join(base, "main")
	gitdir := filepath.Join(main, ".git", "worktrees", "wt")
	if err := os.MkdirAll(gitdir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitdir, "commondir"), []byte("../..\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(base, "wt")
	if err := os.MkdirAll(wt, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: ../main/.git/worktrees/wt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := ExcludeFromGit(wt, []string{".roadie/key"})
	if err != nil || !changed {
		t.Fatalf("%v %v", changed, err)
	}
	if got := excludeLines(t, filepath.Join(main, ".git", "info", "exclude")); !reflect.DeepEqual(got, []string{"/.roadie/key"}) {
		t.Errorf("lines %q", got)
	}
}

// With git installed, git itself agrees the paths are ignored.
func TestExcludeFromGitWithGit(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	run := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command(gitBin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	run(root, "init", "-q")
	run(root, "-c", "user.email=t@example.test", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init")
	wt := filepath.Join(t.TempDir(), "wt")
	run(root, "worktree", "add", "-q", wt)
	dirs := []string{root, wt}
	if runtime.GOOS != "windows" {
		// Glob characters in a folder name match only themselves.
		dirs = append(dirs, filepath.Join(root, "shop[1]"), filepath.Join(root, "a*b?"))
	}
	for _, dir := range dirs {
		if _, err := ExcludeFromGit(dir, GitExcludes()); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, ".roadie", "reports"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".roadie", "key"), []byte("k"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".roadie", "reports", "r.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".roadie", "config.yaml"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		out := run(dir, "status", "--porcelain", "--untracked-files=all")
		if strings.Contains(out, "key") || strings.Contains(out, "reports") || !strings.Contains(out, ".roadie/config.yaml") {
			t.Errorf("%s: git status\n%s", dir, out)
		}
	}
}
