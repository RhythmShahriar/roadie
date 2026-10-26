// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verify

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/detect"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/pseudo"
)

// cleanCase is one file of testdata/clean: a copy with nothing personal
// left in it, mirroring testbed scenarios g01 to g04.
type cleanCase struct {
	Name        string            `yaml:"name"`
	Stack       string            `yaml:"stack"`
	Production  []string          `yaml:"production"`
	AllowEmails []string          `yaml:"allow_emails"`
	Files       map[string]string `yaml:"files"`
	// Rules are the columns the profile covers, so uncovered-column
	// fires only on a column no profile would know.
	Rules []struct {
		Table  string `yaml:"table"`
		Column string `yaml:"column"`
		Fake   string `yaml:"fake"`
	} `yaml:"rules"`
	Tables []struct {
		Name    string   `yaml:"name"`
		Columns []string `yaml:"columns"`
		Rows    [][]any  `yaml:"rows"`
	} `yaml:"tables"`
}

// cleanKeys fills the {{...}} placeholders. Test keys are assembled here
// so no key-shaped literal is committed (push protection).
func cleanKeys() *strings.Replacer {
	return strings.NewReplacer(
		"{{stripe_test_secret}}", testutil.J("sk_", "test_", "51Nq", strings.Repeat("Zx8Kp2", 4)),
		"{{stripe_test_publishable}}", testutil.J("pk_", "test_", "51Nq", strings.Repeat("Wm4Rt7", 4)),
		"{{stripe_test_charge}}", testutil.J("ch_", "3Q", strings.Repeat("aB7", 6)),
	)
}

// cell turns one YAML cell into the stored text: a scalar as written,
// {php: [k, v, ...]} as a PHP serialized array and {json: ...} as JSON.
func cell(t *testing.T, v any, fill *strings.Replacer) string {
	t.Helper()
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return fill.Replace(x)
	case int, float64, bool:
		return fmt.Sprint(x)
	case map[string]any:
		if kv, ok := x["php"].([]any); ok {
			return phpArray(t, kv, fill)
		}
		if j, ok := x["json"]; ok {
			out, err := json.Marshal(j)
			if err != nil {
				t.Fatal(err)
			}
			return fill.Replace(string(out))
		}
	}
	t.Fatalf("cell %#v: want a scalar, {php: [...]} or {json: ...}", v)
	return ""
}

// phpArray serializes [k, v, k, v, ...] as a PHP array; a value may be a
// nested {php: [...]}.
func phpArray(t *testing.T, kv []any, fill *strings.Replacer) string {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "a:%d:{", len(kv)/2)
	for i, v := range kv {
		if m, ok := v.(map[string]any); ok && i%2 == 1 {
			b.WriteString(cell(t, m, fill))
			continue
		}
		s := fill.Replace(fmt.Sprint(v))
		fmt.Fprintf(&b, "s:%d:\"%s\";", len(s), s)
	}
	return b.String() + "}"
}

// fakeFiller fills {{fake:<kind>}} with a fake from the values lane's
// faker, keyed by the row so each row differs. It returns nil while the
// faker returns core.ErrNotBuilt: the rows that need it are left out then.
func fakeFiller(t *testing.T) func(kind, seed string) string {
	t.Helper()
	f, err := pseudo.NewFaker(pseudo.Key{}, pseudo.Options{})
	if errors.Is(err, core.ErrNotBuilt) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return func(kind, seed string) string {
		v, err := f.Fake(core.FakeKind(kind), []byte(seed), core.FakeOpts{})
		if err != nil {
			t.Fatalf("fake %s: %v", kind, err)
		}
		return string(v)
	}
}

// fillFakes replaces each {{fake:<kind>}} in s, reporting false when s
// has one and there is no faker.
func fillFakes(s, seed string, fake func(kind, seed string) string) (string, bool) {
	for {
		i := strings.Index(s, "{{fake:")
		if i < 0 {
			return s, true
		}
		j := strings.Index(s[i:], "}}")
		if j < 0 || fake == nil {
			return s, false
		}
		kind := s[i+len("{{fake:") : i+j]
		s = s[:i] + fake(kind, seed+kind) + s[i+j+2:]
	}
}

// loadClean reads one clean case into a job.
func loadClean(t *testing.T, path string) (cleanCase, Job) {
	t.Helper()
	b, err := os.ReadFile(path) // #nosec G304 -- a test fixture
	if err != nil {
		t.Fatal(err)
	}
	var c cleanCase
	if err := yaml.Unmarshal(b, &c); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	fill := cleanKeys()
	fake := fakeFiller(t)
	var batches []*core.Batch
	for _, tb := range c.Tables {
		var rows [][]string
	row:
		for ri, r := range tb.Rows {
			if len(r) != len(tb.Columns) {
				t.Fatalf("%s %s: row %v has %d cells for %d columns", c.Name, tb.Name, r, len(r), len(tb.Columns))
			}
			var row []string
			for _, v := range r {
				s, ok := fillFakes(cell(t, v, fill), fmt.Sprint(c.Name, tb.Name, ri), fake)
				if !ok {
					t.Logf("%s %s row %d left out: its Roadie fakes need the values lane", c.Name, tb.Name, ri+1)
					continue row
				}
				row = append(row, s)
			}
			rows = append(rows, row)
		}
		batches = append(batches, batch(table(tb.Name, tb.Columns...), rows...))
	}
	j := Job{Source: source(batches...), Production: c.Production, Opts: Options{Allow: detect.Allow{Emails: c.AllowEmails}}}
	for _, r := range c.Rules {
		j.Rules = append(j.Rules, core.Rule{ID: c.Name + "." + r.Table + "." + r.Column, Table: r.Table, Column: r.Column,
			Action: core.Fake, Fake: core.FakeKind(r.Fake)})
	}
	if len(c.Files) > 0 {
		files := map[string][]byte{}
		for name, body := range c.Files {
			files[name] = []byte(fill.Replace(body))
		}
		st := testutil.FakeSite(files)
		st.EP.Kind = core.LocalSite
		st.StackName = core.Stack(c.Stack)
		j.Site = st
	}
	return c, j
}

// TestGoodInputsAreClean runs verify over every clean case and expects
// no finding at or above low (qa.md 6.2 g01 to g04).
func TestGoodInputsAreClean(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("testdata", "clean", "*.yaml"))
	if err != nil || len(paths) < 4 {
		t.Fatalf("clean cases: %v %v", paths, err)
	}
	for _, p := range paths {
		t.Run(filepath.Base(p), func(t *testing.T) {
			c, j := loadClean(t, p)
			res := runJob(t, j)
			if res.Coverage.RowsScanned == 0 {
				t.Fatal("nothing scanned")
			}
			for _, f := range res.Findings {
				if f.Severity >= core.SeverityLow {
					t.Errorf("%s: %s %s ×%d at %v examples %v", c.Name, f.ID, f.Severity, f.Count, f.Locations, f.Examples)
				}
			}
			if j.Site != nil && c.Stack != "" {
				for _, ch := range res.Checks {
					if ch.Status == "fail" && ch.ID != core.FindingUncoveredColumn {
						t.Errorf("%s: check %s failed: %s", c.Name, ch.ID, ch.Detail)
					}
				}
			}
			for _, k := range res.Keys {
				t.Errorf("%s: key %s %s at %v", c.Name, k.ID, k.Env, k.At)
			}
			t.Logf("%s: %s rows, %s bytes", c.Name, strconv.FormatInt(res.Coverage.RowsScanned, 10), strconv.FormatInt(res.Coverage.BytesScanned, 10))
		})
	}
}

// Plugin update caches hold vendor emails, which are public; the same
// email in a plugin's own settings is still flagged; a key in the
// cache is still found.
func TestPublicOptionsSkipPII(t *testing.T) {
	vendor := ser("author", "Plugin Works <support@pluginworks-demo.com>")
	res := runJob(t, Job{Source: source(options(
		[]string{"1", "_site_transient_update_plugins", vendor},
		[]string{"2", "_site_transient_update_themes", ser("key", liveStripe())},
		[]string{"3", "myplugin_settings", vendor},
	))})
	f := find(res, core.FindingPIIEmail)
	if f == nil || f.Count != 1 || f.Locations[0].Row != "option_id=3,option_name=myplugin_settings" {
		t.Errorf("pii.email %+v", f)
	}
	if find(res, core.KeyLiveID("stripe")) == nil {
		t.Errorf("a live key in an update cache was skipped: %v", ids(res))
	}
	for _, tc := range []struct {
		table, column, key, name string
		want                     bool
	}{
		{"wp_options", "option_value", "option_name", "_site_transient_update_plugins", true},
		{"wp_options", "option_value", "option_name", "_transient_feed_ac0b00fe65abe10e0c5b588f3ed8c7ca", true},
		{"wp_options", "option_value", "option_name", "active_plugins", true},
		{"wp_sitemeta", "meta_value", "meta_key", "_site_transient_update_core", true},
		{"wp_options", "option_value", "option_name", "_transient_wc_customer_1", false},
		{"wp_options", "option_value", "option_name", "admin_email", false},
		{"wp_postmeta", "meta_value", "meta_key", "_site_transient_update_plugins", false},
	} {
		sc := core.ScanContext{Table: tc.table, Column: tc.column, Row: func(c string) ([]byte, bool) {
			return []byte(tc.name), c == tc.key
		}}
		if got := publicOption(sc); got != tc.want {
			t.Errorf("publicOption(%s %s) = %v", tc.table, tc.name, got)
		}
	}
}
