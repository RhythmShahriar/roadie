// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"testing"

	"github.com/RhythmShahriar/roadie/roadiecfg"
)

// A kept user's personal values stay as they are; production's own
// URL in the row (user_url set to the shop's URL, as WordPress does for
// the first admin) becomes the staging URL, in a plain column and inside
// serialized meta (a nested leaf), so verify reports no url.production for it.
func TestKeptUserURLRewritten(t *testing.T) {
	useFakes(t)
	tables := func() []tableData {
		return []tableData{
			wpBase(),
			{table("wp_users", "ID:bigint(20)", "user_login", "user_email", "user_url"), rows{
				{1, "anna", "anna.lee@example.com", "https://shop.com"}, {2, "ben", "ben@example.com", "https://ben.example.org"}}},
			{table("wp_usermeta", "umeta_id:bigint(20)", "user_id:bigint(20)", "meta_key", "meta_value:longtext"), rows{
				{1, 1, "links", `a:1:{s:4:"shop";s:16:"https://shop.com";}`}, {2, 1, "nickname", "Anna"}}},
		}
	}
	cfg := roadiecfg.Default()
	cfg.Pseudonymise.KeepUsers = []string{"anna"}
	p, sink, _ := fixture(t, Copy, "wp_", tables, cfg, Options{URL: "https://staging.shop.test"})
	p.cfg.Production = []string{"shop.com"}
	run(t, p)
	// The fakes' nested rewrite leaves the lengths alone; the real one
	// fixes them.
	if got, _ := valueOf(t, sink, "wp_usermeta", 0, "meta_value"); got != `a:1:{s:4:"shop";s:16:"https://staging.shop.test";}` {
		t.Errorf("kept serialized meta: %q", got)
	}
	for _, c := range []struct {
		table string
		row   int
		col   string
		want  string
	}{
		{"wp_users", 0, "user_url", "https://staging.shop.test"},
		{"wp_users", 0, "user_email", "anna.lee@example.com"},
		{"wp_users", 0, "user_login", "anna"},
		{"wp_usermeta", 1, "meta_value", "Anna"},
	} {
		if got, _ := valueOf(t, sink, c.table, c.row, c.col); got != c.want {
			t.Errorf("kept %s.%s: %q, want %q", c.table, c.col, got, c.want)
		}
	}
}
