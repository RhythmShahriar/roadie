// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/roadiecfg"
	"github.com/RhythmShahriar/roadie/site"
)

// The canary tests (security.md M1, M5; PRD 18.9): unique real-looking
// values go in every field type, every command runs with every output
// and none of the values may come out anywhere, in any encoding.

// canaryForms are the ways a value could leak: as it is, lower-cased,
// URL-encoded and JSON-escaped (PHP's json_encode escapes slashes too).
func canaryForms(v string) []string {
	j, _ := json.Marshal(v)
	js := strings.Trim(string(j), `"`)
	forms := []string{v, strings.ToLower(v), url.QueryEscape(v), url.PathEscape(v), js, strings.ReplaceAll(js, "/", `\/`)}
	slices.Sort(forms)
	return slices.Compact(forms)
}

// leakScanner looks for every form of every canary in what a run wrote.
type leakScanner struct {
	forms map[string]string // form -> the kind it came from
}

func newLeakScanner(canaries map[core.FakeKind][]string, extra ...string) *leakScanner {
	s := &leakScanner{forms: map[string]string{}}
	for kind, vs := range canaries {
		for _, v := range vs {
			for _, f := range canaryForms(v) {
				s.forms[f] = string(kind)
			}
		}
	}
	for _, v := range extra {
		for _, f := range canaryForms(v) {
			s.forms[f] = "key"
		}
	}
	return s
}

// find lists the kinds of every canary form in text; the value itself
// stays out of the message, which would leak it into the test log.
func (s *leakScanner) find(text string) []string {
	var out []string
	for f, kind := range s.forms {
		if strings.Contains(text, f) {
			out = append(out, kind)
		}
	}
	slices.Sort(out)
	return out
}

// scan fails the test for every canary found in text, naming where it was.
func (s *leakScanner) scan(t *testing.T, where, text string) {
	t.Helper()
	if kinds := s.find(text); len(kinds) > 0 {
		t.Errorf("%s: canaries leaked (%s)", where, strings.Join(slices.Compact(kinds), ", "))
	}
}

// scanDir scans every file under dir except the ones in skip.
func (s *leakScanner) scanDir(t *testing.T, dir string, skip ...string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || slices.Contains(skip, p) {
			return err
		}
		b, err := os.ReadFile(p) // #nosec G304 -- a file the test's own run wrote
		if err != nil {
			return err
		}
		s.scan(t, p, string(b))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// sqlString quotes s for a MySQL dump.
func sqlString(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`, "\n", `\n`).Replace(s) + "'"
}

// phpString encodes s as PHP's serialize() does.
func phpString(s string) string { return fmt.Sprintf("s:%d:%q;", len(s), s) }

// canaryDump is a WooCommerce (HPOS) dump with a canary in every field
// type Roadie covers: users and their meta, order addresses, order notes,
// Action Scheduler arguments, serialized and JSON options, form entries
// and a mail log, plus a live-format key. Nothing in it is real: the
// canaries are made at run time from a seed.
func canaryDump(c map[core.FakeKind][]string, key string) string {
	v := func(k core.FakeKind, i int) string { return c[k][i%len(c[k])] }
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	w("-- MySQL dump 10.13  Distrib 8.0.36, for Linux (x86_64)\n--\n-- Host: localhost    Database: shop\n")
	w("-- Server version\t8.0.36\n\n/*!40101 SET NAMES utf8mb4 */;\n\n")
	table := func(name, cols string, rows ...string) {
		w("DROP TABLE IF EXISTS `%s`;\nCREATE TABLE `%s` (\n%s\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;\n", name, name, cols)
		if len(rows) > 0 {
			w("INSERT INTO `%s` VALUES %s;\n", name, strings.Join(rows, ","))
		}
		w("\n")
	}
	stripe := "a:3:{" + phpString("enabled") + phpString("yes") + phpString("secret_key") + phpString(key) +
		phpString("notify_email") + phpString(v(core.KEmail, 1)) + "}"
	mods, _ := json.Marshal(map[string]string{"contact_email": v(core.KEmail, 2), "contact_url": v(core.KURL, 0)})
	table("wp_options", "  `option_id` bigint unsigned NOT NULL AUTO_INCREMENT,\n  `option_name` varchar(191) NOT NULL DEFAULT '',\n  `option_value` longtext NOT NULL,\n  `autoload` varchar(20) NOT NULL DEFAULT 'yes',\n  PRIMARY KEY (`option_id`),\n  UNIQUE KEY `option_name` (`option_name`)",
		"(1,'siteurl','https://canary-shop.example','yes')",
		"(2,'home','https://canary-shop.example','yes')",
		"(3,'admin_email',"+sqlString(v(core.KEmail, 0))+",'yes')",
		"(4,'woocommerce_stripe_settings',"+sqlString(stripe)+",'yes')",
		"(5,'theme_mods_storefront',"+sqlString(string(mods))+",'yes')",
		"(6,'blogname','Canary Shop','yes')")
	var users, meta []string
	for i := range 3 {
		id := i + 1
		users = append(users, fmt.Sprintf("(%d,%s,'$P$Bfakehashfakehashfakehash0',%s,%s,%s,'2024-01-0%d 10:00:00','',0,%s)", id,
			sqlString(v(core.KUsername, i)), sqlString(v(core.KNicename, i)), sqlString(v(core.KEmail, i+1)), sqlString(v(core.KURL, i)), id, sqlString(v(core.KFullName, i))))
		for j, kv := range [][2]string{
			{"first_name", v(core.KFirstName, i)}, {"last_name", v(core.KLastName, i)}, {"nickname", v(core.KUsername, i)},
			{"billing_phone", v(core.KPhone, i)}, {"billing_address_1", v(core.KStreet, i)}, {"billing_address_2", v(core.KAddress, i)},
			{"billing_city", v(core.KCity, i)}, {"billing_postcode", v(core.KPostcode, i)}, {"billing_company", v(core.KCompany, i)},
			{"billing_email", v(core.KEmail, i+2)},
		} {
			meta = append(meta, fmt.Sprintf("(%d,%d,%s,%s)", i*20+j+1, id, sqlString(kv[0]), sqlString(kv[1])))
		}
	}
	table("wp_users", "  `ID` bigint unsigned NOT NULL AUTO_INCREMENT,\n  `user_login` varchar(60) NOT NULL DEFAULT '',\n  `user_pass` varchar(255) NOT NULL DEFAULT '',\n  `user_nicename` varchar(50) NOT NULL DEFAULT '',\n  `user_email` varchar(100) NOT NULL DEFAULT '',\n  `user_url` varchar(100) NOT NULL DEFAULT '',\n  `user_registered` datetime NOT NULL DEFAULT '0000-00-00 00:00:00',\n  `user_activation_key` varchar(255) NOT NULL DEFAULT '',\n  `user_status` int NOT NULL DEFAULT '0',\n  `display_name` varchar(250) NOT NULL DEFAULT '',\n  PRIMARY KEY (`ID`)", users...)
	table("wp_usermeta", "  `umeta_id` bigint unsigned NOT NULL AUTO_INCREMENT,\n  `user_id` bigint unsigned NOT NULL DEFAULT '0',\n  `meta_key` varchar(255) DEFAULT NULL,\n  `meta_value` longtext,\n  PRIMARY KEY (`umeta_id`)", meta...)
	var orders, addrs, notes, actions, entries, mails []string
	for i := range 4 {
		id := 100 + i
		orders = append(orders, fmt.Sprintf("(%d,'wc-completed','GBP',42.50000000,%d,%s,'2025-03-0%d 12:00:00',%s,%s,%s)", id, i%3+1,
			sqlString(v(core.KEmail, i)), i+1, sqlString(v(core.KIP, i)), sqlString(v(core.KUserAgent, i)), sqlString(v(core.KText, i))))
		for j, typ := range []string{"billing", "shipping"} {
			addrs = append(addrs, fmt.Sprintf("(%d,%d,'%s',%s,%s,%s,%s,%s,%s,'',%s,'GB',%s,%s)", i*2+j+1, id, typ,
				sqlString(v(core.KFirstName, i)), sqlString(v(core.KLastName, i)), sqlString(v(core.KCompany, i)),
				sqlString(v(core.KStreet, i)), sqlString(v(core.KAddress, i)), sqlString(v(core.KCity, i)), sqlString(v(core.KPostcode, i)),
				sqlString(v(core.KEmail, i)), sqlString(v(core.KPhone, i))))
		}
		notes = append(notes, fmt.Sprintf("(%d,%d,'WooCommerce','woocommerce@canary-shop.example','',%s,'2025-03-0%d 12:01:00',%s,'order_note',0)", i+1, id,
			sqlString(v(core.KIP, i)), i+1, sqlString(v(core.KText, i)+" at "+v(core.KPhone, i))))
		args, _ := json.Marshal([]any{v(core.KEmail, i), map[string]string{"phone": v(core.KPhone, i), "name": v(core.KFullName, i)}})
		actions = append(actions, fmt.Sprintf("(%d,'woocommerce_send_customer_email','pending','2025-03-0%d 12:05:00',%s,0)", i+1, i+1, sqlString(string(args))))
		for j, kv := range [][2]string{{"1", v(core.KFullName, i)}, {"2", v(core.KEmail, i)}, {"3", v(core.KPhone, i)}, {"4", v(core.KDOB, i)}, {"5", v(core.KAddress, i)}} {
			entries = append(entries, fmt.Sprintf("(%d,1,%d,%s,%s)", i*10+j+1, i+1, sqlString(kv[0]), sqlString(kv[1])))
		}
		mails = append(mails, fmt.Sprintf("(%d,'2025-03-0%d 12:06:00',%s,%s,'Your order',%s,'','','',1)", i+1, i+1,
			sqlString(v(core.KIP, i)), sqlString(v(core.KEmail, i)), sqlString("Hello "+v(core.KFirstName, i)+", "+v(core.KText, i))))
	}
	table("wp_wc_orders", "  `id` bigint unsigned NOT NULL,\n  `status` varchar(20) DEFAULT NULL,\n  `currency` varchar(10) DEFAULT NULL,\n  `total_amount` decimal(26,8) DEFAULT NULL,\n  `customer_id` bigint unsigned DEFAULT NULL,\n  `billing_email` varchar(320) DEFAULT NULL,\n  `date_created_gmt` datetime DEFAULT NULL,\n  `ip_address` varchar(100) DEFAULT NULL,\n  `user_agent` text,\n  `customer_note` text,\n  PRIMARY KEY (`id`)", orders...)
	table("wp_wc_order_addresses", "  `id` bigint unsigned NOT NULL AUTO_INCREMENT,\n  `order_id` bigint unsigned NOT NULL,\n  `address_type` varchar(20) DEFAULT NULL,\n  `first_name` text,\n  `last_name` text,\n  `company` text,\n  `address_1` text,\n  `address_2` text,\n  `city` text,\n  `state` text,\n  `postcode` text,\n  `country` text,\n  `email` varchar(320) DEFAULT NULL,\n  `phone` varchar(100) DEFAULT NULL,\n  PRIMARY KEY (`id`)", addrs...)
	table("wp_comments", "  `comment_ID` bigint unsigned NOT NULL AUTO_INCREMENT,\n  `comment_post_ID` bigint unsigned NOT NULL DEFAULT '0',\n  `comment_author` tinytext NOT NULL,\n  `comment_author_email` varchar(100) NOT NULL DEFAULT '',\n  `comment_author_url` varchar(200) NOT NULL DEFAULT '',\n  `comment_author_IP` varchar(100) NOT NULL DEFAULT '',\n  `comment_date` datetime NOT NULL DEFAULT '0000-00-00 00:00:00',\n  `comment_content` text NOT NULL,\n  `comment_type` varchar(20) NOT NULL DEFAULT 'comment',\n  `user_id` bigint unsigned NOT NULL DEFAULT '0',\n  PRIMARY KEY (`comment_ID`)", notes...)
	table("wp_actionscheduler_actions", "  `action_id` bigint unsigned NOT NULL AUTO_INCREMENT,\n  `hook` varchar(191) NOT NULL,\n  `status` varchar(20) NOT NULL,\n  `scheduled_date_gmt` datetime DEFAULT '0000-00-00 00:00:00',\n  `args` varchar(191) DEFAULT NULL,\n  `group_id` bigint unsigned NOT NULL DEFAULT '0',\n  PRIMARY KEY (`action_id`)", actions...)
	// Gravity Forms always has its entry table next to the entry meta;
	// the gravity-forms profile is detected by it.
	table("wp_gf_entry", "  `id` int unsigned NOT NULL AUTO_INCREMENT,\n  `form_id` mediumint unsigned NOT NULL,\n  `ip` varchar(45) NOT NULL,\n  `user_agent` varchar(250) NOT NULL DEFAULT '',\n  `source_url` varchar(200) NOT NULL DEFAULT '',\n  `transaction_id` varchar(50) DEFAULT NULL,\n  PRIMARY KEY (`id`)")
	table("wp_gf_form_meta", "  `form_id` mediumint unsigned NOT NULL,\n  `display_meta` longtext,\n  PRIMARY KEY (`form_id`)",
		`(1,'{"fields":[{"id":1,"type":"name"},{"id":2,"type":"email"},{"id":3,"type":"phone"},{"id":4,"type":"date"},{"id":5,"type":"textarea"}]}')`)
	table("wp_gf_entry_meta", "  `id` bigint unsigned NOT NULL AUTO_INCREMENT,\n  `form_id` mediumint unsigned NOT NULL DEFAULT '0',\n  `entry_id` bigint unsigned NOT NULL,\n  `meta_key` varchar(255) DEFAULT NULL,\n  `meta_value` longtext,\n  PRIMARY KEY (`id`)", entries...)
	table("wp_wpml_mails", "  `mail_id` int NOT NULL AUTO_INCREMENT,\n  `timestamp` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,\n  `host` varchar(200) NOT NULL DEFAULT '0',\n  `receiver` varchar(200) NOT NULL DEFAULT '0',\n  `subject` varchar(200) NOT NULL DEFAULT '0',\n  `message` text,\n  `headers` text,\n  `attachments` text,\n  `error` text,\n  `plugin_version` varchar(200) NOT NULL DEFAULT '0',\n  PRIMARY KEY (`mail_id`)", mails...)
	w("-- Dump completed on 2026-10-26 14:02:11\n")
	return b.String()
}

// TestCanaries runs copy, verify (FAIL on the untouched input) and plan
// on a dump full of canaries with every output, then plan and verify on
// a site whose wp-config.php holds secrets. It asserts that no canary
// reaches stdout, stderr, a report, an -o file or the copy. It
// turns on once the io, values, site, detect and engine lanes merge.
func TestCanaries(t *testing.T) {
	requireLanes(t, "io", "values", "site", "detect", "engine")
	dir := t.TempDir()
	t.Chdir(dir)
	canaries := testutil.Canaries(20261026)
	key := testutil.J("sk_", "live_", "51Cn", "ry0aTq2wN8eLm3VbXq")
	scan := newLeakScanner(canaries, key)
	input := filepath.Join(dir, "shop.sql")
	if err := os.WriteFile(input, []byte(canaryDump(canaries, key)), 0o600); err != nil {
		t.Fatal(err)
	}
	old := dep
	t.Cleanup(func() { dep = old })
	dep = realDeps()
	dep.parseEndpoint = site.ParseEndpoint
	dep.defaultConfig = roadiecfg.Default
	testutil.NoNetwork(t)

	base := map[string][]string{
		"copy":   {"--from", input, "--url", "https://staging.canary-shop.example", "--yes", "--offline", "--no-config", "--debug"},
		"verify": {input, "--production", "canary-shop.example", "--offline", "--no-config", "--debug"},
		"plan":   {"--from", input, "--url", "https://staging.canary-shop.example", "--offline", "--no-config", "--debug"},
	}
	formats := []string{"", "--json", "--md", "--html", "--sarif", "--junit"}
	n := 0
	for _, cmd := range []string{"copy", "verify", "plan"} {
		for _, format := range formats {
			if (format == "--sarif" || format == "--junit") && cmd != "verify" {
				continue
			}
			n++
			args := slices.Clone(base[cmd])
			if cmd == "copy" {
				args = append(args, "--to", filepath.Join(dir, fmt.Sprintf("staging-%d.sql", n)))
			}
			if format != "" {
				args = append(args, format)
			}
			for _, ext := range []string{"json", "md", "html"} {
				args = append(args, "-o", filepath.Join(dir, "out", fmt.Sprintf("%s-%d.%s", cmd, n, ext)))
			}
			if cmd == "verify" {
				args = append(args, "-o", filepath.Join(dir, "out", fmt.Sprintf("verify-%d.sarif", n)), "-o", filepath.Join(dir, "out", fmt.Sprintf("verify-%d.xml", n)))
			}
			out, errOut, code := runCmd(t, cmd, args...)
			where := fmt.Sprintf("%s %s", cmd, format)
			switch {
			case cmd == "verify" && code != 1:
				t.Errorf("%s: exit %d, want 1 (the untouched input is not safe)\n%s", where, code, errOut)
			case cmd != "verify" && code != 0:
				t.Errorf("%s: exit %d\n%s", where, code, errOut)
			}
			scan.scan(t, where+" stdout", out)
			scan.scan(t, where+" stderr", errOut)
		}
	}
	// A WordPress site whose wp-config.php holds the database password, a
	// salt and live keys: plan reads it as a target and verify checks it.
	// Its database is a loopback port nothing listens on, so both stop
	// with an error; the error paths must not leak either.
	secrets := siteSecrets(canaries)
	siteScan := newLeakScanner(canaries, append([]string{key}, secrets...)...)
	site, wpConfig := canarySite(t, dir, canaries, key, secrets)
	for _, args := range [][]string{
		{"plan", "--from", input, "--to", site, "--url", "https://staging.canary-shop.example", "--production-url", "https://canary-shop.example",
			"--offline", "--no-config", "--debug", "--json", "-o", filepath.Join(dir, "out", "site-plan.md")},
		{"verify", site, "--production", "canary-shop.example", "--offline", "--no-config", "--debug", "--md",
			"-o", filepath.Join(dir, "out", "site-verify.json"), "-o", filepath.Join(dir, "out", "site-verify.sarif")},
	} {
		out, errOut, code := runCmd(t, args[0], args[1:]...)
		where := args[0] + " on a site"
		t.Logf("%s: exit %d", where, code)
		siteScan.scan(t, where+" stdout", out)
		siteScan.scan(t, where+" stderr", errOut)
	}
	// Every file the runs wrote: the copies, -o files, saved reports.
	siteScan.scanDir(t, dir, input, wpConfig)
}

// siteSecrets are the values a wp-config.php fixture holds besides the
// live key: the database password, a salt and a SendGrid key, made at
// run time so no literal secret is committed.
func siteSecrets(c map[core.FakeKind][]string) []string {
	last := strings.ToLower(c[core.KLastName][3])
	return []string{
		testutil.J("Cnry-", last, "-pw-", c[core.KPostcode][2][:2], "9q"),
		testutil.J("k#", c[core.KFirstName][4], "|", last, "~salt+", c[core.KIP][4]),
		testutil.J("SG.", "Cnry", last, "Qx7.", "Tz", strings.ToUpper(last), "w0rLd8VbN3mKp2"),
	}
}

// canarySite writes a WordPress folder under dir whose wp-config.php
// holds secrets and key and returns the folder and the file, which the
// leak scan skips: it is the input.
func canarySite(t *testing.T, dir string, c map[core.FakeKind][]string, key string, secrets []string) (string, string) {
	t.Helper()
	site := filepath.Join(dir, "staging-site")
	for _, d := range []string{"wp-content/plugins", "wp-includes"} {
		if err := os.MkdirAll(filepath.Join(site, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	php := func(name, value string) string {
		return fmt.Sprintf("define( '%s', '%s' );\n", name, strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(value))
	}
	conf := "<?php\n" + php("DB_NAME", "shop") + php("DB_USER", c[core.KUsername][4]) + php("DB_PASSWORD", secrets[0]) +
		// 32921 is in the cmd tests' port range and nothing listens on it.
		php("DB_HOST", "127.0.0.1:32921") + php("DB_CHARSET", "utf8mb4") + php("AUTH_KEY", secrets[1]) +
		php("STRIPE_SECRET_KEY", key) + php("SENDGRID_API_KEY", secrets[2]) + php("WP_ENVIRONMENT_TYPE", "staging") +
		php("WP_HOME", "https://staging.canary-shop.example") + php("ADMIN_EMAIL_NOTE", c[core.KEmail][4]) +
		"$table_prefix = 'wp_';\nif ( ! defined( 'ABSPATH' ) ) {\n\tdefine( 'ABSPATH', __DIR__ . '/' );\n}\nrequire_once ABSPATH . 'wp-settings.php';\n"
	files := map[string]string{
		"wp-config.php":           conf,
		"wp-settings.php":         "<?php\n",
		"wp-includes/version.php": "<?php\n$wp_version = '6.6.2';\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(site, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return site, filepath.Join(site, "wp-config.php")
}

// canaryFindings puts canaries into findings the way the engine reports
// them: masked examples and a masked row key.
func canaryFindings(c map[core.FakeKind][]string, fs []core.Finding) {
	for i := range fs {
		fs[i].Examples = []string{core.MaskEmail(c[core.KEmail][i%len(c[core.KEmail])]), core.MaskPhone(c[core.KPhone][0]), core.MaskName(c[core.KFullName][0])}
		for j := range fs[i].Locations {
			fs[i].Locations[j].Row = core.MaskRow(fs[i].Locations[j].Table, map[string]string{"user_email": c[core.KEmail][0]})
		}
	}
}

// TestCanaryViews runs every output of copy, verify and plan with the
// real report renderers on results that carry canaries the way the
// engine reports them: masked examples and row keys, masked keys. It
// checks the rendering path never undoes a mask; TestCanaries covers the
// engine once it merges.
func TestCanaryViews(t *testing.T) {
	canaries := testutil.Canaries(7)
	key := testutil.J("sk_", "live_", "51Vw", "Zx0aTq2wN8eLm3VbYp")
	scan := newLeakScanner(canaries, key)
	keys := []core.KeyHit{{ID: "key.stripe-live", Provider: "Stripe", Env: "live", Severity: core.SeverityCritical,
		At: core.Location{Table: "wp_options", Row: "option_name=woocommerce_stripe_settings", Path: "serialized:secret_key"}, Masked: core.MaskKey(key), Action: "swapped"}}

	copyR := copyFail()
	canaryFindings(canaries, copyR.Verify.Findings)
	copyR.Keys = keys
	copyR.FirstAdmin = "admin-1"
	planR := planResult()
	canaryFindings(canaries, planR.Findings)
	planR.Keys = keys
	verifyR := heuristicFail()
	canaryFindings(canaries, verifyR.Findings)
	verifyR.Keys = keys

	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("ROADIE_SIGNING_KEY", "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=")
	realReports := func(d *deps) {
		real := realDeps()
		d.envelope, d.markdown, d.html, d.sarif, d.junit, d.save = real.envelope, real.markdown, real.html, real.sarif, real.junit, real.save
		d.loadSigner, d.env = real.loadSigner, osEnv
	}
	cases := []struct {
		cmd     string
		args    []string
		formats []string
		exts    []string
		use     func()
	}{
		{"copy", []string{"--from", "prod.sql", "--to", "staging.sql", "--url", "https://staging.shop.test"},
			[]string{"", "--json", "--md", "--html"}, []string{"json", "md", "html"},
			func() { useFixture(t, copyR, nil, realReports) }},
		{"plan", []string{"--from", "prod.sql", "--to", "staging.sql", "--url", "https://staging.shop.test"},
			[]string{"", "--json", "--md", "--html"}, []string{"json", "md", "html"},
			func() { useFixture(t, planR, nil, realReports) }},
		{"verify", []string{"staging.sql", "--production", "shop.com"},
			[]string{"", "--json", "--md", "--html", "--sarif", "--junit"}, []string{"json", "md", "html", "sarif", "xml"},
			func() { useVerify(t, &verifyFixture{results: []*core.VerifyResult{verifyR}}, realReports) }},
	}
	for _, c := range cases {
		for _, format := range c.formats {
			c.use()
			name := c.cmd + strings.TrimPrefix(format, "--")
			args := append(slices.Clone(c.args), "--debug", "-v")
			for _, ext := range c.exts {
				args = append(args, "-o", filepath.Join(dir, "o", name+"."+ext))
			}
			if format != "" {
				args = append(args, format)
			}
			out, errOut, code := runCmd(t, c.cmd, args...)
			if code == 3 || code == 2 {
				t.Errorf("%s %s: exit %d\n%s", c.cmd, format, code, errOut)
			}
			scan.scan(t, c.cmd+" "+format+" stdout", out)
			scan.scan(t, c.cmd+" "+format+" stderr", errOut)
		}
	}
	// The saved reports render again through report and pass report check.
	saved, _ := filepath.Glob(filepath.Join(dir, ".roadie", "reports", "*", "report.json"))
	if len(saved) == 0 {
		t.Fatal("no saved report")
	}
	for i, s := range saved {
		o := filepath.Join(dir, "o", fmt.Sprintf("again-%d", i))
		out, errOut, code := runCmd(t, "report", s, "-o", o+".html", "-o", o+".md", "-o", o+".sarif", "-o", o+".xml")
		if code != 0 {
			t.Fatalf("report: exit %d %s", code, errOut)
		}
		scan.scan(t, "report stdout", out)
		if _, errOut, code := runCmd(t, "report", "check", s); code != 0 {
			t.Errorf("report check %s: exit %d %s", s, code, errOut)
		}
	}
	scan.scanDir(t, dir)
}

// The wp-config.php fixture holds every secret and canary it claims to,
// so the site part of TestCanaries checks something once it turns on.
func TestCanarySiteFixture(t *testing.T) {
	c := testutil.Canaries(5)
	key := testutil.J("sk_", "live_", "51St", "Zx0aTq2wN8eLm3VbQr")
	secrets := siteSecrets(c)
	_, wpConfig := canarySite(t, t.TempDir(), c, key, secrets)
	b, err := os.ReadFile(wpConfig) // #nosec G304 -- the test's own fixture
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range append([]string{key, c[core.KUsername][4], c[core.KEmail][4]}, secrets...) {
		if s := newLeakScanner(nil, v); len(s.find(string(b))) == 0 {
			t.Errorf("wp-config.php lacks one of its secrets")
		}
	}
	if fi, err := os.Stat(wpConfig); err != nil || fi.Mode().Perm()&0o077 != 0 && runtime.GOOS != "windows" {
		t.Errorf("wp-config.php mode: %v %v", fi, err)
	}
}

// The leak scanner itself finds every form of a canary, so a passing
// TestCanaries means something.
func TestLeakScannerFindsEveryForm(t *testing.T) {
	c := map[core.FakeKind][]string{core.KURL: {"https://Bakodi.com/x y"}}
	s := newLeakScanner(c)
	for _, text := range []string{"https://Bakodi.com/x y", "https://bakodi.com/x y", "https%3A%2F%2FBakodi.com%2Fx+y", `https:\/\/Bakodi.com\/x y`, "https:%2F%2FBakodi.com%2Fx%20y"} {
		if len(s.find("a "+text+" b")) == 0 {
			t.Errorf("the scanner missed %q", text)
		}
	}
	if got := s.find("B••• https://staging.shop.test"); len(got) > 0 {
		t.Errorf("the scanner flagged a masked value: %v", got)
	}
}

// TestCanaryKeys seeds canaries as the keys of JSON and serialized
// values: a plugin that keys its data by a customer's name, phone or
// address and WordPress's session tokens keyed by a hash. Finding
// locations name those keys; no canary may reach any output of verify.
func TestCanaryKeys(t *testing.T) {
	requireLanes(t, "io", "values", "site", "detect", "engine")
	dir := t.TempDir()
	t.Chdir(dir)
	c := testutil.Canaries(20261027)
	hash := strings.Repeat(strings.ToLower(c[core.KLastName][1])+"9f3a", 8)[:64]
	scan := newLeakScanner(c, hash)
	members, _ := json.Marshal(map[string]any{
		c[core.KFullName][0]: map[string]string{"phone": c[core.KPhone][0], "tier": "gold"},
		c[core.KPhone][1]:    map[string]string{"tier": "silver"},
		c[core.KEmail][2]:    map[string]string{"tier": "bronze"},
	})
	tokens := "a:1:{" + phpString(hash) + "a:2:{" + phpString("ip") + phpString(c[core.KIP][0]) +
		phpString("ua") + phpString("Mozilla/5.0") + "}}"
	named := "a:1:{" + phpString(c[core.KFullName][1]) + "a:1:{" + phpString("email") + phpString(c[core.KEmail][3]) + "}}"
	dump := "-- MySQL dump 10.13  Distrib 8.0.36, for Linux (x86_64)\n/*!40101 SET NAMES utf8mb4 */;\n" +
		"CREATE TABLE `wp_options` (\n  `option_id` bigint unsigned NOT NULL,\n  `option_name` varchar(191) NOT NULL,\n  `option_value` longtext NOT NULL,\n  `autoload` varchar(20) NOT NULL,\n  PRIMARY KEY (`option_id`)\n);\n" +
		"INSERT INTO `wp_options` VALUES (1,'siteurl','https://canary-shop.example','yes'),(2,'loyalty_members'," + sqlString(string(members)) + ",'no'),(3,'gift_list'," + sqlString(named) + ",'no');\n" +
		"CREATE TABLE `wp_usermeta` (\n  `umeta_id` bigint unsigned NOT NULL,\n  `user_id` bigint unsigned NOT NULL,\n  `meta_key` varchar(255) DEFAULT NULL,\n  `meta_value` longtext,\n  PRIMARY KEY (`umeta_id`)\n);\n" +
		"INSERT INTO `wp_usermeta` VALUES (1,1,'session_tokens'," + sqlString(tokens) + ");\n" +
		"-- Dump completed on 2026-10-26 14:02:11\n"
	input := filepath.Join(dir, "keys.sql")
	if err := os.WriteFile(input, []byte(dump), 0o600); err != nil {
		t.Fatal(err)
	}
	old := dep
	t.Cleanup(func() { dep = old })
	dep = realDeps()
	dep.parseEndpoint = site.ParseEndpoint
	dep.defaultConfig = roadiecfg.Default
	testutil.NoNetwork(t)
	found := false
	for i, format := range []string{"--plain", "--json", "--md", "--html", "--sarif", "--junit"} {
		args := []string{input, "--production", "canary-shop.example", "--offline", "--no-config", "--debug", "-v", format}
		for _, ext := range []string{"json", "md", "html", "sarif", "xml"} {
			args = append(args, "-o", filepath.Join(dir, "out", fmt.Sprintf("keys-%d.%s", i, ext)))
		}
		out, errOut, code := runCmd(t, "verify", args...)
		if code != 1 {
			t.Errorf("verify %s: exit %d, want 1\n%s", format, code, errOut)
		}
		found = found || strings.Contains(out, "<key>")
		scan.scan(t, "verify "+format+" stdout", out)
		scan.scan(t, "verify "+format+" stderr", errOut)
	}
	if !found {
		t.Error("no location names a masked key: the keys weren't found")
	}
	scan.scanDir(t, dir, input)
}

// TestCanaryRowKeys seeds a live-format key and a phone number as row
// keys: a primary key, an option_name and a meta_key, where a finding's
// location names its row. verify must find them and no output, file or
// saved report may hold either value (PRD 15.3: a row key is masked as
// a value is).
func TestCanaryRowKeys(t *testing.T) {
	requireLanes(t, "io", "values", "site", "detect", "engine")
	dir := t.TempDir()
	t.Chdir(dir)
	key := testutil.J("sk_", "live_", "51Hq", "Zx0aTq2wN8eLm3VbYpQrSt", "Uv")
	phone := testutil.J("+44", "7911", "123456")
	spaced := testutil.J("+44 ", "7911 ", "654321")
	scan := newLeakScanner(nil, key, phone, spaced, strings.TrimPrefix(phone, "+"))
	dump := "-- MySQL dump 10.13  Distrib 8.0.36, for Linux (x86_64)\n/*!40101 SET NAMES utf8mb4 */;\n" +
		"CREATE TABLE `api_tokens` (\n  `token` varchar(99) NOT NULL,\n  `label` text,\n  PRIMARY KEY (`token`)\n);\n" +
		"INSERT INTO `api_tokens` VALUES (" + sqlString(key) + ",'stripe');\n" +
		"CREATE TABLE `sms_optins` (\n  `phone` varchar(32) NOT NULL,\n  `note` text,\n  PRIMARY KEY (`phone`)\n);\n" +
		"INSERT INTO `sms_optins` VALUES (" + sqlString(phone) + ",'call " + spaced + "');\n" +
		"CREATE TABLE `wp_options` (\n  `option_id` bigint unsigned NOT NULL,\n  `option_name` varchar(191) NOT NULL,\n  `option_value` longtext NOT NULL,\n  `autoload` varchar(20) NOT NULL,\n  PRIMARY KEY (`option_id`)\n);\n" +
		"INSERT INTO `wp_options` VALUES (1,'siteurl','https://canary-shop.example','yes'),(2," + sqlString(key) + "," + sqlString(key) + ",'no');\n" +
		"CREATE TABLE `wp_usermeta` (\n  `umeta_id` bigint unsigned NOT NULL,\n  `user_id` bigint unsigned NOT NULL,\n  `meta_key` varchar(255) DEFAULT NULL,\n  `meta_value` longtext,\n  PRIMARY KEY (`umeta_id`)\n);\n" +
		"INSERT INTO `wp_usermeta` VALUES (1,1," + sqlString("_stripe_"+key) + "," + sqlString(spaced) + "),(2,1," + sqlString("sms_"+phone) + "," + sqlString(spaced) + ");\n" +
		"-- Dump completed on 2026-10-26 14:02:11\n"
	input := filepath.Join(dir, "rowkeys.sql")
	if err := os.WriteFile(input, []byte(dump), 0o600); err != nil {
		t.Fatal(err)
	}
	old := dep
	t.Cleanup(func() { dep = old })
	dep = realDeps()
	dep.parseEndpoint = site.ParseEndpoint
	dep.defaultConfig = roadiecfg.Default
	testutil.NoNetwork(t)
	masked := false
	for i, format := range []string{"--plain", "--json", "--md", "--html", "--sarif", "--junit"} {
		for _, verbose := range []bool{false, true} {
			args := []string{input, "--production", "canary-shop.example", "--offline", "--no-config", "--debug", format}
			if verbose {
				args = append(args, "-v")
			}
			for _, ext := range []string{"json", "md", "html", "sarif", "xml"} {
				args = append(args, "-o", filepath.Join(dir, "out", fmt.Sprintf("rowkeys-%d-%t.%s", i, verbose, ext)))
			}
			out, errOut, code := runCmd(t, "verify", args...)
			where := fmt.Sprintf("verify %s -v=%t", format, verbose)
			if code != 1 {
				t.Errorf("%s: exit %d, want 1\n%s", where, code, errOut)
			}
			masked = masked || strings.Contains(out, "sk_l•••") || strings.Contains(out, "+44 •••")
			scan.scan(t, where+" stdout", out)
			scan.scan(t, where+" stderr", errOut)
		}
	}
	if !masked {
		t.Error("no output names a masked key or phone: the rows weren't found")
	}
	// The -o files and the signed reports verify saved.
	scan.scanDir(t, dir, input)
}
