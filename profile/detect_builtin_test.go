// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package profile

import (
	"slices"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
)

// schemaWithout copies s without the named tables.
func schemaWithout(s *core.Schema, drop ...string) *core.Schema {
	out := &core.Schema{Dialect: s.Dialect, Prefix: s.Prefix}
	for _, t := range s.Tables {
		if !slices.Contains(drop, t.Name) {
			out.Tables = append(out.Tables, t)
		}
	}
	return out
}

// mergeSchemas joins the tables of several schema files.
func mergeSchemas(t *testing.T, names ...string) *core.Schema {
	t.Helper()
	out := &core.Schema{Dialect: core.MySQL}
	for _, n := range names {
		out.Tables = append(out.Tables, loadSchema(t, n).Tables...)
	}
	return out
}

// TestBuiltinDetection runs the built-in detectors over the committed
// schemas: each profile is found where its tables are (positive) and a
// plain single-site WordPress or a Laravel app picks up nothing else
// (negative), so auto never adds rules for a plugin that isn't there.
func TestBuiltinDetection(t *testing.T) {
	defs, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	core68 := schemaWithout(loadSchema(t, "wordpress-6.8.sql"),
		"wp_blogs", "wp_blogmeta", "wp_registration_log", "wp_site", "wp_sitemeta", "wp_signups")
	if got := Detect(core68, nil, defs); !slices.Equal(got, []string{"wordpress"}) {
		t.Errorf("single-site WordPress: %v", got)
	}
	if got := Detect(loadSchema(t, "wordpress-6.8.sql"), nil, defs); !slices.Equal(got, []string{"multisite", "wordpress"}) {
		t.Errorf("multisite WordPress: %v", got)
	}
	laravel := Detect(loadSchema(t, "laravel-docs.sql"), nil, defs)
	if !slices.Equal(laravel, []string{"laravel", "laravel-cashier"}) {
		t.Errorf("Laravel: %v", laravel)
	}
	// Cashier's customer column alone, with no subscription tables (a
	// shop that charges one-off payments), still detects it.
	noSubs := schemaWithout(loadSchema(t, "laravel-docs.sql"), "subscriptions", "subscription_items")
	if got := Detect(noSubs, nil, defs); !slices.Equal(got, []string{"laravel", "laravel-cashier"}) {
		t.Errorf("Laravel with Cashier's users.stripe_id only: %v", got)
	}
	users := noSubs.Table("users")
	bare := *users
	bare.Columns = slices.DeleteFunc(slices.Clone(users.Columns), func(c core.Column) bool {
		return c.Name == "stripe_id" || c.Name == "pm_type" || c.Name == "pm_last_four" || c.Name == "trial_ends_at"
	})
	noCashier := schemaWithout(noSubs, "users")
	noCashier.Tables = append(noCashier.Tables, &bare)
	if got := Detect(noCashier, nil, defs); !slices.Equal(got, []string{"laravel"}) {
		t.Errorf("Laravel without Cashier: %v", got)
	}
	// A stripe_id column on another table is no Cashier billable.
	other := schemaWithout(noCashier)
	other.Tables = append(other.Tables, &core.Table{Name: "orders", Columns: []core.Column{{Name: "id"}, {Name: "stripe_id"}}})
	if got := Detect(other, nil, defs); !slices.Equal(got, []string{"laravel"}) {
		t.Errorf("orders.stripe_id: %v", got)
	}

	for _, d := range defs {
		files := schemaFiles[d.ID]
		if len(d.Detect.TablesAny) == 0 || len(files) == 0 {
			continue
		}
		t.Run(d.ID, func(t *testing.T) {
			s := mergeSchemas(t, files...)
			s.Prefix = "wp_"
			if !slices.Contains(Detect(s, nil, defs), d.ID) {
				t.Errorf("not detected on %v", files)
			}
		})
	}

	// A plugin found only on a multisite blog's tables still counts. A
	// wp_11_ table never stands for blog 1.
	blog := schemaOf("wp_", "wp_options", "wp_users", "wp_11_options", "wp_11_wc_orders")
	if got := Detect(blog, nil, defs); !slices.Contains(got, "woocommerce-hpos") {
		t.Errorf("blog 11 HPOS table: %v", got)
	}

	// Detection by option name or plugin file for the profiles that leave
	// no table of their own.
	if got := DetectWithOptions(t.Context(), core68, nil, defs, map[string]bool{"wc_subscriptions_siteurl": true}); !slices.Contains(got, "woocommerce-subscriptions") {
		t.Errorf("subscriptions by option: %v", got)
	}
	// WooCommerce from a partial dump: any of its own tables or options.
	for _, tb := range []string{"wp_wc_orders", "wp_woocommerce_sessions", "wp_wc_order_stats", "wp_woocommerce_api_keys"} {
		if got := Detect(schemaOf("wp_", "wp_options", tb), nil, defs); !slices.Contains(got, "woocommerce") {
			t.Errorf("WooCommerce by %s: %v", tb, got)
		}
	}
	for _, opt := range []string{"woocommerce_db_version", "woocommerce_custom_orders_table_enabled"} {
		if got := DetectWithOptions(t.Context(), core68, nil, defs, map[string]bool{opt: true}); !slices.Contains(got, "woocommerce") {
			t.Errorf("WooCommerce by option %s: %v", opt, got)
		}
	}
	if got := Detect(schemaOf("wp_", "wp_options", "wp_12_wc_orders", "wp_shop_orders"), nil, defs); slices.Contains(got, "woocommerce") {
		t.Errorf("WooCommerce from lookalikes: %v", got)
	}
	site := testutil.FakeSite(map[string][]byte{"wp-content/plugins/flamingo/flamingo.php": []byte("<?php")})
	if got := Detect(core68, site, defs); !slices.Contains(got, "contact-form-7-flamingo") {
		t.Errorf("Flamingo by file: %v", got)
	}
	if got := DetectWithOptions(t.Context(), core68, nil, defs, map[string]bool{"blogname": true}); slices.Contains(got, "woocommerce-subscriptions") || slices.Contains(got, "contact-form-7-flamingo") {
		t.Errorf("unrelated option: %v", got)
	}
}
