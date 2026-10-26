// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package killswitch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/codec/phpser"
	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/keys"
)

// TestMain reads serialized options with a small test decoder until the
// values lane lands phpser; after that the real parser runs.
func TestMain(m *testing.M) {
	if err := phpser.Leaves([]byte(`a:0:{}`), func(string, []byte, bool) {}); errors.Is(err, core.ErrNotBuilt) {
		leaves = testLeaves
	}
	os.Exit(m.Run())
}

// testLeaves walks PHP serialized arrays of strings, ints, bools and
// nested arrays; paths join keys with ".".
func testLeaves(b []byte, fn func(path string, leaf []byte, isKey bool)) error {
	p := &serParser{b: b}
	if err := p.value("", fn); err != nil {
		return err
	}
	if p.i != len(p.b) {
		return errors.New("trailing bytes")
	}
	return nil
}

type serParser struct {
	b []byte
	i int
}

func (p *serParser) until(c byte) (string, error) {
	j := bytes.IndexByte(p.b[p.i:], c)
	if j < 0 {
		return "", errors.New("unexpected end")
	}
	s := string(p.b[p.i : p.i+j])
	p.i += j + 1
	return s, nil
}

func (p *serParser) scalar() (string, error) {
	if p.i+2 > len(p.b) {
		return "", errors.New("unexpected end")
	}
	t := p.b[p.i]
	p.i += 2
	switch t {
	case 'N':
		return "", nil
	case 'i', 'b', 'd':
		return p.until(';')
	case 's':
		ns, err := p.until(':')
		if err != nil {
			return "", err
		}
		n, err := strconv.Atoi(ns)
		if err != nil || p.i+n+3 > len(p.b) {
			return "", errors.New("bad string")
		}
		s := string(p.b[p.i+1 : p.i+1+n])
		p.i += n + 3
		return s, nil
	}
	return "", fmt.Errorf("type %c", t)
}

func (p *serParser) value(at string, fn func(string, []byte, bool)) error {
	if p.i < len(p.b) && p.b[p.i] == 'a' {
		p.i += 2
		ns, err := p.until(':')
		if err != nil {
			return err
		}
		n, _ := strconv.Atoi(ns)
		p.i++ // {
		for k := 0; k < n; k++ {
			key, err := p.scalar()
			if err != nil {
				return err
			}
			kp := key
			if at != "" {
				kp = at + "." + key
			}
			fn(kp, []byte(key), true)
			if err := p.value(kp, fn); err != nil {
				return err
			}
		}
		p.i++ // }
		return nil
	}
	if p.i < len(p.b) && p.b[p.i] == 'N' {
		p.i += 2
		fn(at, nil, false)
		return nil
	}
	v, err := p.scalar()
	if err != nil {
		return err
	}
	fn(at, []byte(v), false)
	return nil
}

// phpSer writes dotted keys as nested PHP arrays of strings.
func phpSer(m map[string]string) string {
	tree := map[string]any{}
	for k, v := range m {
		parts := strings.Split(k, ".")
		node := tree
		for _, p := range parts[:len(parts)-1] {
			next, ok := node[p].(map[string]any)
			if !ok {
				next = map[string]any{}
				node[p] = next
			}
			node = next
		}
		node[parts[len(parts)-1]] = v
	}
	var write func(n map[string]any) string
	write = func(n map[string]any) string {
		ks := make([]string, 0, len(n))
		for k := range n {
			ks = append(ks, k)
		}
		slices.Sort(ks)
		var b strings.Builder
		fmt.Fprintf(&b, "a:%d:{", len(n))
		for _, k := range ks {
			fmt.Fprintf(&b, "s:%d:\"%s\";", len(k), k)
			switch v := n[k].(type) {
			case string:
				fmt.Fprintf(&b, "s:%d:\"%s\";", len(v), v)
			case map[string]any:
				b.WriteString(write(v))
			}
		}
		b.WriteString("}")
		return b.String()
	}
	return write(tree)
}

func flat(t *testing.T, b string) map[string]string {
	t.Helper()
	m := map[string]string{}
	if err := testLeaves([]byte(b), func(p string, leaf []byte, isKey bool) {
		if !isKey {
			m[p] = string(leaf)
		}
	}); err != nil {
		t.Fatalf("%v: %q", err, b)
	}
	// Drop the parents of nested leaves, which phpSer rebuilds.
	return m
}

// db is a fixture database: table to rows of column to value.
type db map[string][]map[string]string

// tableColumns gives each fixture table a fixed column order.
var tableColumns = map[string][]string{
	"wp_options":                 {"option_id", "option_name", "option_value", "autoload"},
	"wp_actionscheduler_actions": {"action_id", "hook", "status"},
	"wp_wc_webhooks":             {"webhook_id", "status", "delivery_url", "secret"},
	"wp_automatewoo_queue":       {"id", "workflow_id"},
}

// fixtureSite is a FakeSite whose Source reads d.
func fixtureSite(files map[string]string, d db) *testutil.Site {
	fs := map[string][]byte{}
	for k, v := range files {
		fs[k] = []byte(v)
	}
	s := testutil.FakeSite(fs)
	s.SourceFunc = func(tables []string) (core.Source, error) {
		var batches []*core.Batch
		for i, name := range tables {
			rows, ok := d[name]
			if !ok {
				continue
			}
			tb := &core.Table{Name: name}
			for _, c := range tableColumns[name] {
				tb.Columns = append(tb.Columns, core.Column{Name: c})
			}
			b := &core.Batch{Seq: uint64(i + 1), Table: tb}
			for _, r := range rows {
				var row core.Row
				for _, c := range tableColumns[name] {
					row.Values = append(row.Values, core.Value{Kind: core.String, Raw: []byte(r[c])})
				}
				b.Rows = append(b.Rows, row)
			}
			batches = append(batches, b)
		}
		return testutil.FakeSource(nil, batches), nil
	}
	return s
}

// applyRules does what the engine does with DataRules, for the fixtures.
func applyRules(t *testing.T, d db, rules []core.Rule) {
	t.Helper()
	for _, r := range rules {
		table := strings.ReplaceAll(r.Table, "{prefix}", "wp_")
		if r.Action == core.Truncate {
			delete(d, table)
			continue
		}
		for _, row := range d[table] {
			match := true
			for col, glob := range r.Where {
				if ok, _ := path.Match(glob, row[col]); !ok {
					match = false
				}
			}
			if !match {
				continue
			}
			set := func(old string) string {
				switch r.Action {
				case core.Fixed:
					return r.Value
				case core.Blank, core.SwapKey:
					return ""
				}
				t.Fatalf("rule %s: action %s", r.ID, r.Action)
				return old
			}
			if r.Path == "" {
				row[r.Column] = set(row[r.Column])
				continue
			}
			m := flat(t, row[r.Column])
			if _, ok := m[r.Path]; !ok {
				continue
			}
			m[r.Path] = set(m[r.Path])
			row[r.Column] = phpSer(m)
		}
	}
}

// liveStripe is a live-format secret assembled at runtime.
var liveStripe = testutil.J("sk_", "live_", "51Hx0ROADIEfixture000000000000000000")

// prodConfig is a production wp-config.php with every switch off.
func prodConfig() string {
	return "<?php\r\n" +
		"define( 'DB_NAME', 'shop' );\r\n" +
		"define( 'DB_USER', 'shop' );\r\n" +
		"define( 'DB_PASSWORD', 'x' );\r\n" +
		"define( 'DB_HOST', 'localhost' );\r\n" +
		"define( 'WP_HOME', 'https://shop.com' );\r\n" +
		"define( 'STRIPE_SECRET_KEY', '" + liveStripe + "' );\r\n" +
		"define( 'AUTH_KEY',         'put your unique phrase here' );\r\n" +
		"define( 'SECURE_AUTH_KEY',  'put your unique phrase here' );\r\n" +
		"define( 'LOGGED_IN_KEY',    'put your unique phrase here' );\r\n" +
		"define( 'NONCE_KEY',        'put your unique phrase here' );\r\n" +
		"define( 'AUTH_SALT',        'put your unique phrase here' );\r\n" +
		"define( 'SECURE_AUTH_SALT', 'put your unique phrase here' );\r\n" +
		"define( 'LOGGED_IN_SALT',   'put your unique phrase here' );\r\n" +
		"define( 'NONCE_SALT',       'put your unique phrase here' );\r\n" +
		"$table_prefix = 'wp_';\r\n" +
		"/* That's all, stop editing! Happy publishing. */\r\n" +
		"require_once ABSPATH . 'wp-settings.php';\r\n"
}

// fixtureGateways is the gateway list the tests pass, standing in for
// keys.Gateways until the detect lane lands it.
func fixtureGateways() []keys.Gateway {
	return []keys.Gateway{
		{ID: "stripe", Name: "Stripe", Option: "woocommerce_stripe_settings", EnabledPath: "enabled", ModePath: "testmode", TestValue: "yes", LiveValue: "no",
			LiveSecretPaths: []string{"secret_key", "publishable_key"}, TestSecretPaths: []string{"test_secret_key"}},
		{ID: "ppcp-gateway", Name: "PayPal Payments", Option: "woocommerce-ppcp-settings", EnabledPath: "enabled", ModePath: "sandbox_on", TestValue: "yes", LiveValue: "no",
			LiveSecretPaths: []string{"client_secret_production"}},
		{ID: "woocommerce_payments", Name: "WooPayments", Option: "woocommerce_woocommerce_payments_settings", EnabledPath: "enabled", ModePath: "test_mode", TestValue: "yes", LiveValue: "no"},
	}
}

func fixtureMailers() []keys.Mailer {
	return []keys.Mailer{{ID: "wp-mail-smtp", Option: "wp_mail_smtp", MailerPath: "mail.mailer", DefaultMailer: "mail", SecretPaths: []string{"sendgrid.api_key", "smtp.pass"}}}
}

// prodDB is production's database with every switch off.
func prodDB() db {
	opt := func(id int, name, value string) map[string]string {
		return map[string]string{"option_id": strconv.Itoa(id), "option_name": name, "option_value": value, "autoload": "yes"}
	}
	return db{
		"wp_options": {
			opt(1, "siteurl", "https://shop.com"),
			opt(2, "wc_subscriptions_siteurl", "https://sh_[wc_subscriptions_siteurl]_op.com"),
			opt(3, "woocommerce_gateway_order", `a:4:{s:6:"stripe";i:0;s:12:"ppcp-gateway";i:1;s:20:"woocommerce_payments";i:2;s:8:"fancypay";i:3;}`),
			opt(4, "woocommerce_stripe_settings", phpSer(map[string]string{"enabled": "yes", "testmode": "no", "secret_key": liveStripe, "publishable_key": "pk"})),
			opt(5, "woocommerce-ppcp-settings", phpSer(map[string]string{"enabled": "yes", "sandbox_on": "no", "client_secret_production": "x"})),
			opt(6, "woocommerce_woocommerce_payments_settings", phpSer(map[string]string{"enabled": "yes", "test_mode": "no"})),
			opt(7, "woocommerce_fancypay_settings", phpSer(map[string]string{"enabled": "yes", "mode": "live"})),
			opt(8, "wp_mail_smtp", phpSer(map[string]string{"mail.mailer": "sendgrid", "sendgrid.api_key": "SG.x", "smtp.pass": ""})),
			opt(9, "jetpack_options", phpSer(map[string]string{"id": "123"})),
			opt(10, "jetpack_private_options", phpSer(map[string]string{"blog_token": "tok.en"})),
			opt(11, "woocommerce_new_order_settings", phpSer(map[string]string{"enabled": "yes"})),
		},
		"wp_actionscheduler_actions": {
			{"action_id": "1", "hook": "woocommerce_scheduled_subscription_payment", "status": "pending"},
			{"action_id": "2", "hook": "automatewoo_run", "status": "pending"},
			{"action_id": "3", "hook": "x", "status": "pending"},
			{"action_id": "4", "hook": "x", "status": "complete"},
		},
		"wp_wc_webhooks": {
			{"webhook_id": "1", "status": "active", "delivery_url": "https://erp.example.org/hook", "secret": "s1"},
			{"webhook_id": "2", "status": "active", "delivery_url": "https://crm.example.org/hook", "secret": "s2"},
		},
		"wp_automatewoo_queue": {{"id": "1", "workflow_id": "7"}, {"id": "2", "workflow_id": "8"}},
	}
}

func prodFiles() map[string]string {
	return map[string]string{
		"wp-config.php": prodConfig(),
		"wp-content/plugins/woocommerce-subscriptions/woocommerce-subscriptions.php": "<?php",
		"wp-content/plugins/jetpack/jetpack.php":                                     "<?php",
	}
}

// fixtureOptions are kill-switch options with fixture gateway and mailer lists, a fixed
// salt and a key scanner that finds the live Stripe define.
func fixtureOptions() Options {
	n := 0
	return Options{
		URL: "https://staging.shop.test/", EchoURL: "http://127.0.0.1:8025", Noindex: true, Version: "1.0.0",
		Production: []string{"shop.com"},
		Gateways:   fixtureGateways(), Mailers: fixtureMailers(),
		GatewayIDs: []string{"stripe", "ppcp-gateway", "woocommerce_payments", "fancypay"},
		Salt: func() string {
			n++
			return fmt.Sprintf("fixture-salt-%02d-", n) + strings.Repeat("x", 47)
		},
		Keys: func(pairs []core.Pair) []core.KeyHit {
			var out []core.KeyHit
			for _, p := range pairs {
				if strings.HasPrefix(p.Value, "sk_"+"live_") {
					out = append(out, core.KeyHit{ID: core.KeyLiveID("stripe"), Provider: "stripe", Env: "live", Severity: core.SeverityCritical,
						At: core.Location{File: "wp-config.php", Line: p.Line}, Masked: core.MaskKey(p.Value)})
				}
			}
			return out
		},
		Replace: func(core.KeyHit, []byte) ([]byte, string, error) { return nil, "blanked", nil },
	}
}

// applyAll applies every step and the data rules, as prepare does.
func applyAll(t *testing.T, s *testutil.Site, d db, o Options) {
	t.Helper()
	ctx := context.Background()
	for _, st := range Steps(core.StackWordPress, o) {
		if err := st.Apply(ctx, s); err != nil {
			t.Fatalf("%s: %v", st.ID(), err)
		}
	}
	applyRules(t, d, DataRules(core.StackWordPress, o))
}
