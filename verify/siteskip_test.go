// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verify

import (
	"net"
	"net/url"
	"strings"
	"testing"
)

// A --site page that can't be fetched is named in the coverage by the
// whole URL fetched (never by its path alone) without a password.
func TestSiteSkipNamesURL(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close() // nothing listens there now
	res := siteRun(t, Options{SiteURL: "http://" + addr + "/shop/"})
	var got []string
	for _, s := range res.Coverage.Skipped {
		if strings.HasPrefix(s.Table, "--site") {
			got = append(got, s.Table)
		}
	}
	if len(got) != 1 || got[0] != "--site http://"+addr+"/" {
		t.Errorf("skipped %v", got)
	}
	u, _ := url.Parse("https://user:secret@staging.shop.test:8443/x?y=1#z")
	if p := pageURL(u, "/robots.txt"); p != "https://user:xxxxx@staging.shop.test:8443/robots.txt" {
		t.Errorf("pageURL %q", p)
	}
}
