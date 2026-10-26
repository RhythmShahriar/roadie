// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package profile

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/dump"
	"github.com/RhythmShahriar/roadie/roadiecfg"
)

// schemaFiles maps each built-in profile to the captured (or, for paid
// plugins, docs-derived) schema files its tables live in.
var schemaFiles = map[string][]string{
	"wordpress":                 {"wordpress-6.8.sql"},
	"multisite":                 {"wordpress-6.8.sql"},
	"woocommerce":               {"wordpress-6.8.sql", "woocommerce-9.8.0.sql"},
	"woocommerce-hpos":          {"woocommerce-9.8.0.sql"},
	"woocommerce-subscriptions": {"wordpress-6.8.sql", "woocommerce-9.8.0.sql"},
	"action-scheduler":          {"action-scheduler-3.9.2.sql"},
	"automatewoo":               {"automatewoo-docs.sql"},
	"gravity-forms":             {"gravity-forms-docs.sql"},
	"wpforms":                   {"wpforms-docs.sql"},
	"contact-form-7-flamingo":   {"wordpress-6.8.sql"},
	"ninja-forms":               {"wordpress-6.8.sql", "ninja-forms-docs.sql"},
	"fluent-forms":              {"fluent-forms-docs.sql"},
	"mailpoet":                  {"mailpoet-docs.sql"},
	"edd":                       {"edd-docs.sql"},
	"memberpress":               {"wordpress-6.8.sql", "memberpress-docs.sql"},
	"learndash":                 {"learndash-docs.sql"},
	"wp-mail-smtp":              {"mail-logs-docs.sql"},
	"post-smtp":                 {"wordpress-6.8.sql", "mail-logs-docs.sql"},
	"fluent-smtp":               {"mail-logs-docs.sql"},
	"wp-mail-logging":           {"mail-logs-docs.sql"},
	"email-log":                 {"mail-logs-docs.sql"},
	"laravel":                   {"laravel-docs.sql"},
	"laravel-cashier":           {"laravel-docs.sql"},
}

var (
	createTable = regexp.MustCompile("(?i)^CREATE TABLE (?:IF NOT EXISTS )?`?([A-Za-z0-9_]+)`?")
	columnLine  = regexp.MustCompile("^\\s*`([^`]+)`\\s+([a-z]+(?:\\([0-9, ]+\\))?(?: unsigned)?)")
)

// parseSchema reads the CREATE TABLE statements of a mysqldump --no-data
// file: a minimal reader for these committed files until dump.Open can
// read them (the io lane); after that it checks what dump.Open reads.
func parseSchema(r io.Reader) (*core.Schema, error) {
	s := &core.Schema{Dialect: core.MySQL}
	var cur *core.Table
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if m := createTable.FindStringSubmatch(line); m != nil {
			cur = &core.Table{Name: m[1]}
			s.Tables = append(s.Tables, cur)
			continue
		}
		if cur == nil {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), ")") {
			cur = nil
			continue
		}
		if m := columnLine.FindStringSubmatch(line); m != nil {
			cur.Columns = append(cur.Columns, core.Column{Name: m[1], Type: strings.ToLower(m[2])})
		}
	}
	return s, sc.Err()
}

// loadSchema reads one schema file through dump.Open when the io lane has
// built it, else through parseSchema.
func loadSchema(t *testing.T, name string) *core.Schema {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "schemas", name))
	if err != nil {
		t.Fatal(err)
	}
	src, err := dump.Open(context.Background(), bytes.NewReader(b), dump.Options{Name: name})
	if err == nil {
		for {
			if _, err := src.Next(context.Background()); err != nil {
				if !errors.Is(err, io.EOF) {
					t.Fatalf("%s: %v", name, err)
				}
				break
			}
		}
		_ = src.Close()
		return src.Schema()
	}
	if !errors.Is(err, core.ErrNotBuilt) {
		t.Fatalf("%s: %v", name, err)
	}
	s, err := parseSchema(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestProfilesMatchCapturedSchemas: every table and column a built-in
// profile names exists in the schema of the version it was checked
// against. A rule for a column that a plugin renamed would otherwise
// leave the real column untouched without a word.
func TestProfilesMatchCapturedSchemas(t *testing.T) {
	defs, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range defs {
		if d.Stack == core.StackSQL {
			continue // the sql profile has only config-driven tables
		}
		files, ok := schemaFiles[d.ID]
		if !ok {
			t.Errorf("%s: no schema file listed in schemaFiles", d.ID)
			continue
		}
		merged := &core.Schema{Dialect: core.MySQL}
		for _, f := range files {
			merged.Tables = append(merged.Tables, loadSchema(t, f).Tables...)
			// The file of the profile's own plugin carries the version the
			// profile was checked against, from an install or its source.
			if v, ok := strings.CutPrefix(f, d.ID+"-"); ok {
				version := strings.TrimSuffix(v, ".sql")
				if version != "docs" && !strings.Contains(d.CheckedAgainst, version) {
					t.Errorf("%s: checked against %q but the schema file is %s", d.ID, d.CheckedAgainst, f)
				}
			}
		}
		for _, r := range d.Rules {
			for _, name := range Expand(r.Table, "wp_", nil) {
				tb := merged.Table(name)
				if tb == nil {
					t.Errorf("%s: table %s isn't in %v", r.ID, name, files)
					continue
				}
				cols := []string{r.Column}
				for c := range r.Where {
					cols = append(cols, c)
				}
				for _, c := range cols {
					if !slices.ContainsFunc(tb.Columns, func(col core.Column) bool { return col.Name == c }) {
						t.Errorf("%s: column %s.%s isn't in %v", r.ID, name, c, files)
					}
				}
			}
		}
		for _, list := range [][]string{d.Truncate, d.Drop, d.Keep, d.Detect.TablesAny} {
			for _, e := range list {
				for _, name := range Expand(e, "wp_", nil) {
					if !hasTable(merged, name) {
						t.Errorf("%s: table %s isn't in %v", d.ID, name, files)
					}
				}
			}
		}
	}
}

// hasTable reports whether the schema has the table or, for a glob such
// as pulse_*, a table it matches.
func hasTable(s *core.Schema, name string) bool {
	if !hasGlob(name) {
		return s.Table(name) != nil
	}
	return slices.ContainsFunc(s.Tables, func(t *core.Table) bool { ok, _ := path.Match(name, t.Name); return ok })
}

// The schema parser reads what it should and the check catches a
// missing column.
func TestParseSchema(t *testing.T) {
	s, err := parseSchema(strings.NewReader("CREATE TABLE `wp_x` (\n  `id` bigint(20) unsigned NOT NULL,\n  `Email` varchar(100) NOT NULL,\n  PRIMARY KEY (`id`)\n);\n"))
	if err != nil {
		t.Fatal(err)
	}
	tb := s.Table("wp_x")
	if tb == nil || len(tb.Columns) != 2 || tb.Columns[1].Name != "Email" || tb.Columns[0].Type != "bigint(20) unsigned" {
		t.Fatalf("%+v", tb)
	}
}

// The profiles work under a non-default prefix and with blogs: QA asks
// for shop_ and shop_2_ fixtures beside the wp_ ones.
func TestNonDefaultPrefix(t *testing.T) {
	defs, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	s := &core.Schema{Dialect: core.MySQL}
	for _, f := range []string{"wordpress-6.8.sql", "woocommerce-9.8.0.sql"} {
		for _, tb := range loadSchema(t, f).Tables {
			for _, p := range []string{"shop_", "shop_2_"} {
				cp := *tb
				cp.Name = p + strings.TrimPrefix(tb.Name, "wp_")
				s.Tables = append(s.Tables, &cp)
			}
		}
	}
	// No Prefix set: the options table gives it away.
	c, err := Compile(nil, defs, s, roadiecfg.Default())
	if err != nil {
		t.Fatal(err)
	}
	if c.Prefix() != "shop_" || !slices.Equal(c.Sites(), []string{"2_"}) {
		t.Fatalf("prefix %q sites %v", c.Prefix(), c.Sites())
	}
	// wordpress-6.8.sql holds the network tables too, so multisite applies.
	for _, p := range []string{"multisite", "woocommerce", "woocommerce-hpos", "wordpress"} {
		if !slices.Contains(c.Profiles(), p) {
			t.Errorf("%s not detected: %v", p, c.Profiles())
		}
	}
	if slices.Contains(c.Profiles(), "automatewoo") || slices.Contains(c.Profiles(), "action-scheduler") {
		t.Errorf("detected a plugin whose tables aren't there: %v", c.Profiles())
	}
	for table, want := range map[string]string{
		"shop_users": "wordpress.users.user_email", "shop_2_comments": "wordpress.comments.comment_author_email",
		"shop_2_wc_orders": "woocommerce-hpos.wc_orders.billing_email", "shop_wc_order_addresses": "woocommerce-hpos.wc_order_addresses.email",
	} {
		if !slices.Contains(ruleIDs(c.Rules(table)), want) {
			t.Errorf("%s lacks %s: %v", table, want, ruleIDs(c.Rules(table)))
		}
	}
	// shop_2_users isn't a WordPress table (users are network-wide).
	if len(c.Rules("shop_2_users")) != 0 || c.TableAction("shop_2_woocommerce_sessions") != core.Truncate {
		t.Error("per-blog expansion wrong")
	}
}
