// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
	"github.com/RhythmShahriar/roadie/roadiecfg"
)

// A local folder with no host to go by is confirmed by its path; the
// message says it is a path, not a host.
func TestConfirmNamesPathLabel(t *testing.T) {
	cfg := roadiecfg.Default()
	cfg.Production = []string{"https://staging.shop.test"}
	to := core.Endpoint{Kind: core.LocalSite, Raw: "../site", Display: "../site", Path: "../site"}
	r := &runner{j: Job{To: to}, cfg: cfg, mode: Prepare, to: testutil.FakeSite(nil)}
	names, what := r.confirmNames(context.Background(), true)
	if len(names) != 1 || names[0] != "../site" || what != "the site's path" {
		t.Errorf("names %v what %q", names, what)
	}
	r.opts.URL = "https://staging.shop.test"
	if names, what := r.confirmNames(context.Background(), true); names[0] != "staging.shop.test" || what != "the site's host" {
		t.Errorf("with --url: names %v what %q", names, what)
	}
	err := r.confirm(context.Background())
	if err == nil {
		t.Fatal("no confirm without a terminal")
	}
	r.opts.URL = ""
	err = r.confirm(context.Background())
	if want := "Pass --confirm-target ../site (the site's path) to go on."; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("confirm: %v", err)
	}
}
