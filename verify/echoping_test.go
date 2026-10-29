// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verify

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// pingSite serves a WordPress home page and, when ping is set, the Echo
// connector's ping route with that body and status.
func pingSite(status int, ping string, prettyLinks bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := prettyLinks && r.URL.Path == echoPingPath || !prettyLinks && r.URL.Query().Get("rest_route") == "/rhythm-echo/v1/ping"
		switch {
		case route && status != 0:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = io.WriteString(w, ping)
		case r.URL.Path == "/" && r.URL.RawQuery == "":
			w.Header().Set("X-Robots-Tag", "noindex")
			_, _ = io.WriteString(w, "<html></html>")
		default:
			http.NotFound(w, r)
		}
	})
}

// verify --site on a WordPress copy asks the Echo connector's ping route
// (PRD 13.1): no connector, one that isn't active, observe mode or an
// Echo not sealing fails side-effect.mail; a sealed active one passes.
func TestSiteEchoPing(t *testing.T) {
	const sealed = `{"ok":true,"active":true,"mode":"seal","echo":{"reachable":true,"mode":"seal"}}`
	for _, c := range []struct {
		name   string
		status int
		body   string
		pretty bool
		fail   bool
		skip   bool
	}{
		{"sealed", 200, sealed, true, false, false},
		{"sealed by rest_route", 200, sealed, false, false, false},
		{"no connector", 0, "", true, true, false},
		{"observe", 200, `{"active":true,"mode":"observe","echo":{"reachable":true,"mode":"seal"}}`, true, true, false},
		{"inactive", 200, `{"active":false,"reason":"production environment","mode":"seal"}`, true, true, false},
		{"echo observes", 200, `{"active":true,"mode":"seal","echo":{"reachable":true,"mode":"observe"}}`, true, true, false},
		{"needs token", 403, `{"code":"rhythm_echo_forbidden"}`, true, false, true},
	} {
		s := serve(t, pingSite(c.status, c.body, c.pretty), false)
		res := runJob(t, Job{Source: source(options([]string{"1", "blogname", "Shop"})), Opts: Options{SiteURL: s.URL},
			Stack: core.StackInfo{Name: core.StackWordPress}})
		f := find(res, core.FindingSideEffectMail)
		if (f != nil) != c.fail || f != nil && f.Severity != core.SeverityCritical {
			t.Errorf("%s: mail finding %+v", c.name, f)
		}
		ck := checkByID(res, core.FindingSideEffectMail)
		if !c.skip && ck.Checked != 1 {
			t.Errorf("%s: mail check %+v", c.name, ck)
		}
		skipped := false
		for _, sk := range res.Coverage.Skipped {
			skipped = skipped || strings.Contains(sk.Reason, "ping route")
		}
		if skipped != c.skip {
			t.Errorf("%s: skipped %v, want %v", c.name, res.Coverage.Skipped, c.skip)
		}
	}
	// Negative: a site that isn't WordPress isn't asked.
	s := serve(t, pingSite(0, "", true), false)
	res := runJob(t, Job{Source: source(options([]string{"1", "blogname", "Shop"})), Opts: Options{SiteURL: s.URL},
		Stack: core.StackInfo{Name: core.StackLaravel}})
	if find(res, core.FindingSideEffectMail) != nil {
		t.Errorf("a Laravel site was asked for the WordPress ping route: %v", ids(res))
	}
}
