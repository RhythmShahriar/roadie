// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package roadiecfg

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/keys"
	"github.com/RhythmShahriar/roadie/site"
	"gopkg.in/yaml.v3"
)

// writeConfig puts body at <tmp>/.roadie/config.yaml and returns the path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), Dir, File)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// envOf returns an env func over m that records every name asked for.
func envOf(m map[string]string, asked *[]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		if asked != nil {
			*asked = append(*asked, name)
		}
		v, ok := m[name]
		return v, ok
	}
}

// comparable drops what Load records about the file, so two configs
// compare by their settings alone.
func comparable(c *Config) *Config {
	cp := *c
	cp.Sources, cp.ProductionFrom, cp.Unset = nil, nil, nil
	cp.interp, cp.lines, cp.file = nil, nil, ""
	return &cp
}

func TestDefaultGolden(t *testing.T) {
	b, err := yaml.Marshal(Default())
	if err != nil {
		t.Fatal(err)
	}
	testutil.Golden(t, "default.yaml", b)
}

func TestEmptyFileIsDefault(t *testing.T) {
	for _, body := range []string{"", "# only a comment\n", "---\n"} {
		c, err := Load(writeConfig(t, body), nil)
		if err != nil {
			t.Fatalf("%q: %v", body, err)
		}
		if !reflect.DeepEqual(comparable(c), comparable(Default())) {
			t.Errorf("%q: not the defaults", body)
		}
		if c.Sources["stack"] != SourceDefault || c.Sources["pseudonymise.email_domain"] != SourceDefault {
			t.Errorf("%q: sources %v", body, c.Sources)
		}
	}
}

func TestLoadSetsValuesAndSources(t *testing.T) {
	body := `stack: wordpress
pseudonymise:
  locale: de
  keep_users: ["admin", "*@agency.example"]
columns:
  - table: wp_loyalty_members
    column: email
    rule: email
  - table: wp_postmeta
    where: {meta_key: _billing_vat_number}
    column: meta_value
    rule: blank
keys:
  ignore: [{id: key.generic, location: "wp_options my_flag", reason: "a test value", until: 2027-01-31}]
`
	c, err := Load(writeConfig(t, body), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Stack != "wordpress" || c.Pseudonymise.Locale != "de" || len(c.Columns) != 2 || c.Columns[1].Where["meta_key"] != "_billing_vat_number" {
		t.Fatalf("decoded %+v", c)
	}
	if c.Keys.Ignore[0].Until != "2027-01-31" {
		t.Errorf("until %q: a YAML date must stay as written", c.Keys.Ignore[0].Until)
	}
	for key, want := range map[string]string{
		"stack": SourceFile, "pseudonymise.locale": SourceFile, "pseudonymise.keep_users": SourceFile,
		"columns": SourceFile, "keys.ignore": SourceFile, "pseudonymise.email_domain": SourceDefault, "noindex": SourceDefault,
	} {
		if c.Sources[key] != want {
			t.Errorf("Sources[%s] = %q, want %q", key, c.Sources[key], want)
		}
	}
}

// Each bad file gives exit 2, code config (or the code the check owns)
// and the exact message with the line.
func TestLoadErrors(t *testing.T) {
	cases := []struct {
		name, body, code, msg string
	}{
		{"unknown key with suggestion", "pseudonymise:\n  locale: en\n  emial_domain: example.test\n", core.ErrConfig,
			".roadie/config.yaml line 3: unknown key pseudonymise.emial_domain; did you mean pseudonymise.email_domain?"},
		{"unknown key without suggestion", "stack: auto\nfrobnicate: true\n", core.ErrConfig,
			".roadie/config.yaml line 2: unknown key frobnicate; every key is listed at " + WikiURL},
		{"unknown nested key", "side_effects:\n  echo: {instll: true}\n", core.ErrConfig,
			".roadie/config.yaml line 2: unknown key side_effects.echo.instll; did you mean side_effects.echo.install?"},
		{"wrong type: list for a string", "stack: [auto]\n", core.ErrConfig,
			".roadie/config.yaml line 1: stack is a list; it takes a text value"},
		{"wrong type: string for a bool", "source: \"\"\nnoindex: \"yes\"\n", core.ErrConfig,
			".roadie/config.yaml line 2: noindex is a text value; it takes true or false"},
		{"wrong type: map for a list", "production: {a: b}\n", core.ErrConfig,
			".roadie/config.yaml line 1: production is a map; it takes a list, e.g. [a, b]"},
		{"bad glob", "drop: [wp_ok, \"wp_[\"]\n", core.ErrConfig,
			".roadie/config.yaml line 1: drop[1] is \"wp_[\", which isn't a valid glob (use *, ? and [abc] as in path.Match)"},
		{"bad glob in where", "columns:\n  - table: t\n    column: c\n    where: {k: \"a[\"}\n    rule: blank\n", core.ErrConfig,
			".roadie/config.yaml line 4: columns[0].where.k is \"a[\", which isn't a valid glob (use *, ? and [abc] as in path.Match)"},
		{"non-ROADIE variable", "keys:\n  mode: swap\n  test_keys:\n    stripe: ${STRIPE_TEST}\n", core.ErrConfig,
			".roadie/config.yaml line 4: keys.test_keys.stripe reads ${STRIPE_TEST}; only ROADIE_ variables are read. Rename it ROADIE_STRIPE_TEST."},
		{"non-reserved email domain", "pseudonymise:\n  email_domain: shop.com\n", core.ErrConfig,
			".roadie/config.yaml line 2: pseudonymise.email_domain is \"shop.com\", which can receive real mail. Use a reserved domain: example.invalid, example.com or a name under .test, .example, .invalid or .localhost."},
		{"ignore without reason", "verify:\n  ignore:\n    - {id: pii.phone, location: \"wp_options shop_phone\"}\n", core.ErrConfig,
			".roadie/config.yaml line 3: verify.ignore[0] needs a reason: say why this value may stay"},
		{"ignore with unknown id", "keys:\n  ignore:\n    - {id: pii.phnoe, location: x, reason: y}\n", core.ErrConfig,
			".roadie/config.yaml line 3: keys.ignore[0].id is \"pii.phnoe\", which isn't a finding ID; did you mean pii.phone? rhythm-roadie explain lists them."},
		{"ignore with a bad date", "keys:\n  ignore:\n    - id: pii.phone\n      location: x\n      reason: y\n      until: soon\n", core.ErrConfig,
			".roadie/config.yaml line 6: keys.ignore[0].until is \"soon\"; write a date like 2027-01-31"},
		{"enum with suggestion", "stack: wordpres\n", core.ErrConfig,
			".roadie/config.yaml line 1: stack is \"wordpres\"; did you mean wordpress? It takes auto, wordpress, laravel or sql."},
		{"enum without suggestion", "pseudonymise:\n  consistency: forever\n", core.ErrConfig,
			".roadie/config.yaml line 2: pseudonymise.consistency is \"forever\"; it takes per-run or stable."},
		{"bad rule", "columns:\n  - {table: t, column: c, rule: emial}\n", core.ErrConfig,
			".roadie/config.yaml line 2: columns[0].rule: \"emial\" isn't a rule; did you mean email? Rules: " + strings.Join(RuleNames(), ", ")},
		{"echo url over http to a public host", "side_effects:\n  echo: {url: \"http://echo.example.com:8025\"}\n", core.ErrConfig,
			".roadie/config.yaml line 2: side_effects.echo.url is http://echo.example.com:8025 over plain http to a public host; use https or an address on this machine or a private network"},
		{"key set twice", "stack: auto\nstack: sql\n", core.ErrConfig,
			".roadie/config.yaml line 2: stack is set twice (first on line 1); keep one"},
		{"bad production entry", "production: [\"shop.com\", \"ftp://shop.com\"]\n", core.ErrConfig,
			".roadie/config.yaml line 1: production[1] is \"ftp://shop.com\", which isn't a site like shop.com, *.shop.com or https://shop.com/shop/"},
		{"unknown test key provider", "keys:\n  test_keys: {strpe: x}\n", core.ErrConfig,
			".roadie/config.yaml line 2: keys.test_keys.strpe names \"strpe\", which isn't a provider Roadie swaps keys for; did you mean stripe?"},
		{"not a mapping", "- a\n- b\n", core.ErrConfig,
			".roadie/config.yaml line 1: the file must be a list of settings like \"source:\" and \"stack: auto\""},
		{"syntax", "stack: auto\n  bad: [\n", core.ErrConfig, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tc.body), nil)
			var ce *core.Error
			if !errors.As(err, &ce) {
				t.Fatalf("err = %v, want *core.Error", err)
			}
			if ce.Code != tc.code || ce.Exit != 2 {
				t.Errorf("code %s exit %d, want %s exit 2", ce.Code, ce.Exit, tc.code)
			}
			if tc.msg == "" {
				if !strings.HasPrefix(ce.Msg, ".roadie/config.yaml line ") || !strings.Contains(ce.Msg, "this isn't valid YAML") {
					t.Errorf("msg %q", ce.Msg)
				}
				return
			}
			if ce.Msg != tc.msg {
				t.Errorf("msg\n got %q\nwant %q", ce.Msg, tc.msg)
			}
		})
	}
}

// Every valid value of each enum and a set of good values load.
func TestLoadAcceptsGoodValues(t *testing.T) {
	body := `url: https://staging.shop.test
production: ["shop.com", "*.shop.com", "https://shop.com/shop/", "http://shop.com:8080"]
url_map: {"cdn.shop.com": "cdn.staging.shop.test", "https://img.shop.com": "https://img.staging.shop.test/x"}
profiles: [woocommerce, gravity-forms]
pseudonymise:
  email_domain: staging.example.com
  phone_style: country
  keep: [country, state, city, postcode]
  consistency: stable
  on_unparseable: fail
  passwords: keep
  free_text: blank
drop: ["wp_*_log", "tmp_?", "[ab]_x"]
keys:
  mode: blank
side_effects:
  echo: {install: false, url: "https://echo.example.com", mode: observe, proxy: "http://10.0.0.5:3128"}
  action_scheduler: cancel
verify:
  fail_on: medium
  allow_emails: ["info@shop.com", "*@shop.com"]
laravel:
  env_file: config/.env
  tables: {customers: {email: email, notes: text, meta: "json:billing.email=email"}}
  env: {QUEUE_CONNECTION: sync}
`
	if _, err := Load(writeConfig(t, body), nil); err != nil {
		t.Fatal(err)
	}
}

func TestEchoURL(t *testing.T) {
	good := []string{"http://127.0.0.1:8025", "http://localhost:8025", "http://[::1]:8025", "http://10.1.2.3",
		"http://192.168.1.4:8025", "http://172.16.0.9", "http://echo:8025", "http://echo.internal", "http://box.local",
		"https://echo.example.com", "https://203.0.113.9"}
	bad := []string{"http://echo.example.com", "http://8.8.8.8:8025", "ftp://127.0.0.1", "127.0.0.1:8025",
		"http://user:pw@127.0.0.1:8025", ""}
	for _, u := range good {
		if err := ValidateEchoURL(u); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
	for _, u := range bad {
		if err := ValidateEchoURL(u); err == nil {
			t.Errorf("%s accepted", u)
		}
	}
}

// Interpolation reads only ROADIE_ names and only in string values. An
// unset one waits for config check or the run that uses it.
func TestInterpolation(t *testing.T) {
	var asked []string
	body := `source: "${ROADIE_SOURCE}"
url: "https://${ROADIE_HOST}/"
keys:
  test_keys:
    stripe: "${ROADIE_STRIPE_TEST_SECRET}"
verify:
  allow_emails: ["${ROADIE_SHOP_MAIL}"]
`
	env := envOf(map[string]string{"ROADIE_SOURCE": "prod.sql.gz", "ROADIE_HOST": "staging.shop.test", "ROADIE_SHOP_MAIL": "info@shop.com", "HOME": "/root"}, &asked)
	c, err := Load(writeConfig(t, body), env)
	if err != nil {
		t.Fatal(err)
	}
	if c.Source != "prod.sql.gz" || c.URL != "https://staging.shop.test/" || c.Verify.AllowEmails[0] != "info@shop.com" {
		t.Errorf("interpolated %q %q %v", c.Source, c.URL, c.Verify.AllowEmails)
	}
	for _, n := range asked {
		if !strings.HasPrefix(n, "ROADIE_") {
			t.Errorf("read %s", n)
		}
	}
	if c.Keys.TestKeys["stripe"] != "${ROADIE_STRIPE_TEST_SECRET}" || c.Unset["keys.test_keys.stripe"] != "ROADIE_STRIPE_TEST_SECRET" {
		t.Errorf("unset kept %q %v", c.Keys.TestKeys["stripe"], c.Unset)
	}
	err = c.Need("keys")
	want := ".roadie/config.yaml line 5: keys.test_keys.stripe reads ${ROADIE_STRIPE_TEST_SECRET}, which isn't set. Set it in the environment, or remove the entry."
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Msg != want || ce.Exit != 2 {
		t.Errorf("Need: %v\nwant %s", err, want)
	}
	if err := c.Need("source", "url"); err != nil {
		t.Errorf("Need of set values: %v", err)
	}
	// A value that isn't a string is never interpolated: noindex takes a bool.
	asked = nil
	_, err = Load(writeConfig(t, "noindex: ${ROADIE_A}\n"), envOf(map[string]string{"ROADIE_A": "true"}, &asked))
	if err == nil || len(asked) != 0 {
		t.Errorf("bool interpolated: %v %v", err, asked)
	}
}

// A message about a value read from the environment shows the ${NAME}
// reference, never what the variable held: those values are secrets.
func TestErrorsHideInterpolatedValues(t *testing.T) {
	token := testutil.J("tok", "en-", "9f8e7d6c5b4a")
	env := envOf(map[string]string{
		"ROADIE_ECHO_TOKEN": token,
		"ROADIE_URL":        "ftp://" + token + ".example",
		"ROADIE_SITE":       "ftp://shop.com/" + token,
		"ROADIE_LOCALE":     "xx",
	}, nil)
	cases := []struct{ name, body, want string }{
		{"echo url", "side_effects:\n  echo: {url: \"http://echo.example.com:8025/?t=${ROADIE_ECHO_TOKEN}\"}\n",
			".roadie/config.yaml line 2: side_effects.echo.url is http://echo.example.com:8025/?t=${ROADIE_ECHO_TOKEN} over plain http to a public host; use https or an address on this machine or a private network"},
		{"whole value", "url: \"${ROADIE_URL}\"\n",
			".roadie/config.yaml line 1: url is \"${ROADIE_URL}\", which isn't a URL like https://staging.shop.test"},
		{"production entry", "production: [\"shop.com\", \"${ROADIE_SITE}\"]\n",
			".roadie/config.yaml line 1: production[1] is \"${ROADIE_SITE}\", which isn't a site like shop.com, *.shop.com or https://shop.com/shop/"},
		{"short value only where quoted", "pseudonymise:\n  locale: \"${ROADIE_LOCALE}\"\n",
			".roadie/config.yaml line 2: pseudonymise.locale is \"${ROADIE_LOCALE}\"; it takes en, de, fr, es, nl or it."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, tc.body), env)
			var ce *core.Error
			if !errors.As(err, &ce) || ce.Exit != 2 || ce.Code != core.ErrConfig {
				t.Fatalf("err = %v, want a config error", err)
			}
			if ce.Msg != tc.want {
				t.Errorf("msg\n got %q\nwant %q", ce.Msg, tc.want)
			}
			if strings.Contains(ce.Msg, token) {
				t.Errorf("message shows the variable's value: %q", ce.Msg)
			}
		})
	}
	// A value written in the file itself is shown as it is.
	_, err := Load(writeConfig(t, "url: \"ftp://shop.example\"\n"), env)
	if err == nil || !strings.Contains(err.Error(), `"ftp://shop.example"`) {
		t.Errorf("literal value: %v", err)
	}
}

func TestEndpointsCheckedWithSite(t *testing.T) {
	if _, err := site.ParseEndpoint("prod.sql.gz"); errors.Is(err, core.ErrNotBuilt) {
		t.Skip("needs the site lane")
	}
	_, err := Load(writeConfig(t, "source: \"ssh://-oProxyCommand=x/var/www\"\n"), nil)
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrConfig || !strings.HasPrefix(ce.Msg, ".roadie/config.yaml line 1: source: ") {
		t.Errorf("bad endpoint: %v", err)
	}
	if _, err := Load(writeConfig(t, "source: prod.sql.gz\ntarget: /var/www/staging\n"), nil); err != nil {
		t.Errorf("good endpoints: %v", err)
	}
}

func TestLiveTestKeyRefused(t *testing.T) {
	live := testutil.J("sk_", "live_", "51H8xQ2eZvKYlo2C0aBcDeFgHiJkLmNoPq")
	if err := keys.ValidateTestKey("stripe", live); errors.Is(err, core.ErrNotBuilt) {
		t.Skip("needs the detect lane")
	}
	_, err := Load(writeConfig(t, "keys:\n  test_keys: {stripe: \"${ROADIE_STRIPE}\"}\n"), envOf(map[string]string{"ROADIE_STRIPE": live}, nil))
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrTestKeyIsLive || ce.Exit != 2 || !strings.HasPrefix(ce.Msg, ".roadie/config.yaml line 2: test-key-is-live: ") {
		t.Fatalf("live test key: %v", err)
	}
	if strings.Contains(ce.Msg, live) {
		t.Error("the message holds the key")
	}
}

func TestFind(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Find(deep); !errors.Is(err, ErrNoConfig) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("none: %v", err)
	}
	cfg := filepath.Join(root, Dir, File)
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Find(deep)
	if err != nil || got != cfg {
		t.Fatalf("Find = %q %v, want %q", got, err, cfg)
	}
	// The search stops at the repository root: a config above it is not found.
	inner := filepath.Join(root, "a", "repo")
	if err := os.MkdirAll(filepath.Join(inner, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Find(inner); !errors.Is(err, ErrNoConfig) {
		t.Errorf("past the repo root: %v", err)
	}
	if ProjectDir(cfg) != root {
		t.Errorf("ProjectDir %q", ProjectDir(cfg))
	}
}

func TestMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "none.yaml"), nil)
	var ce *core.Error
	if !errors.As(err, &ce) || ce.Code != core.ErrConfig || ce.Exit != 2 {
		t.Fatalf("%v", err)
	}
}

func TestProductionMergedFromSiblings(t *testing.T) {
	p := writeConfig(t, "production: [shop.com]\n")
	dir := ProjectDir(p)
	write := func(rel, body string) {
		t.Helper()
		f := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(".echo/config.yaml", "mode: seal\nproduction: [\"https://shop.com\", \"api.shop.com\"]\nunknown_echo_key: 1\n")
	write(".soundcheck/config.yaml", "pages: [/]\nproduction: [\"*.shop.com\", \"shop.com\"]\n")
	c, err := Load(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"shop.com", "https://shop.com", "api.shop.com", "*.shop.com"}
	if !slices.Equal(c.Production, want) {
		t.Errorf("production %v, want %v", c.Production, want)
	}
	if c.Sources["production"] != "file, echo, soundcheck" || c.ProductionFrom["api.shop.com"] != SourceEcho || c.ProductionFrom["*.shop.com"] != SourceSoundcheck || c.ProductionFrom["shop.com"] != SourceFile {
		t.Errorf("sources %q %v", c.Sources["production"], c.ProductionFrom)
	}
	// A bad entry in a sibling stops the run instead of being skipped.
	write(".echo/config.yaml", "production: [\"ftp://x\"]\n")
	if _, err := Load(p, nil); err == nil || !strings.Contains(err.Error(), ".echo/config.yaml line 1") {
		t.Errorf("bad sibling: %v", err)
	}
}

func TestShowGolden(t *testing.T) {
	stripe := testutil.J("sk_", "test_", "4eC39HqLyjWDarjtT1zdp7dc")
	body := `source: "mysql://shop:hunter2@db.shop.com/shop"
target: /var/www/staging
url: https://staging.shop.test
production: [shop.com]
stack: wordpress
pseudonymise:
  keep_users: [admin]
keys:
  test_keys:
    stripe: "${ROADIE_STRIPE_TEST_SECRET}"
    paypal: "${ROADIE_PAYPAL_TEST}"
`
	p := writeConfig(t, body)
	c, err := Load(p, envOf(map[string]string{"ROADIE_STRIPE_TEST_SECRET": stripe}, nil))
	if err != nil {
		t.Fatal(err)
	}
	c.MarkFlag("url")
	for _, asJSON := range []bool{false, true} {
		b, err := c.Show(asJSON)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), stripe) || strings.Contains(string(b), "hunter2") {
			t.Fatalf("a secret reached config show:\n%s", b)
		}
		name := "show.yaml"
		if asJSON {
			name = "show.json"
		}
		testutil.Golden(t, name, b)
	}
}

// FuzzLoad feeds random files to the loader: it never panics and every
// failure is a *core.Error with exit 2.
func FuzzLoad(f *testing.F) {
	for _, s := range []string{"", "stack: auto\n", string(initTemplate("wordpress")), string(initTemplate("sql")),
		"columns: [{table: t, column: c, rule: \"json:a=email\"}]\n", "a: &x [1]\nb: *x\n", "keys: {test_keys: {stripe: ${ROADIE_X}}}\n"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		l := &loader{file: "fuzz.yaml", env: envOf(map[string]string{"ROADIE_X": "v"}, nil), lines: map[string]int{},
			present: map[string]bool{}, interp: map[string]string{}, unset: map[string]string{}}
		_, err := l.load(b)
		var ce *core.Error
		if err != nil && (!errors.As(err, &ce) || ce.Exit != 2) {
			t.Fatalf("error %v is not a config error", err)
		}
	})
}
