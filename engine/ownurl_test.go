// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// A staging that already calls itself by its own URL, whose admin's
// user_url is that URL (WordPress often sets it so): prepare must not
// count the site's siteurl and home as a person's URL, or verify reports
// them as survivors. With --url, with --production-url and with both.
func TestPrepareOwnSiteURLNotKnown(t *testing.T) {
	const own = "https://staging.shop.test"
	tables := func() []tableData {
		return []tableData{
			{table("wp_options", "option_id:bigint(20)", "option_name", "option_value"), rows{
				{1, "siteurl", own}, {2, "home", own + "/"}, {3, "blogdescription", "See " + own}}},
			{table("wp_users", "ID:bigint(20)", "user_login", "user_email", "user_url"), rows{
				{1, "admin", "admin@shop.com", own}, {2, "ben", "ben@example.com", "https://ben.example.org"}}},
		}
	}
	for name, o := range map[string]struct {
		url  string
		prod []string
	}{
		"url":            {url: own},
		"production-url": {prod: []string{"https://shop.example.com"}},
		"both":           {url: own, prod: []string{"https://shop.example.com"}},
	} {
		t.Run(name, func(t *testing.T) {
			useFakes(t)
			p, sink, _ := fixture(t, Prepare, "wp_", tables, nil, Options{URL: o.url})
			p.samePlace = true
			p.cfg.Production = o.prod
			out, err := p.run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, tok := range []string{own, own + "/", "HTTPS://STAGING.shop.test", "http://staging.shop.test/"} {
				if kind, _, ok := out.Known.Contains([]byte(tok)); ok {
					t.Errorf("%s is known as %s", tok, kind)
				}
			}
			if _, _, ok := out.Known.Contains([]byte("https://ben.example.org")); !ok {
				t.Error("another user's URL is no longer known")
			}
			if v, _ := valueOf(t, sink, "wp_options", 0, "option_value"); v != own {
				t.Errorf("siteurl %q", v)
			}
		})
	}
	// A copy still knows the URL: it is the source's, not the target's.
	useFakes(t)
	p, _, _ := fixture(t, Copy, "wp_", tables, nil, Options{URL: "https://other.shop.test"})
	out, err := p.run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if kind, _, ok := out.Known.Contains([]byte(own)); !ok || kind != core.KURL {
		t.Errorf("copy: %s not known (%s %v)", own, kind, ok)
	}
}
