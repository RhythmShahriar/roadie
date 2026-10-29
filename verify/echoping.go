// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
)

// echoPingPath is the Echo connector's ping route on a WordPress site.
const echoPingPath = "/wp-json/rhythm-echo/v1/ping"

// echoPing is what the connector's ping route answers that matters here.
type echoPing struct {
	Active bool   `json:"active"`
	Reason string `json:"reason"`
	Mode   string `json:"mode"`
	Echo   struct {
		Reachable bool   `json:"reachable"`
		Mode      string `json:"mode"`
	} `json:"echo"`
}

// wordpressSite reports whether the copy is a WordPress site.
func (r *run) wordpressSite() bool {
	return r.j.Stack.Name == core.StackWordPress || r.site != nil && r.site.Stack() == core.StackWordPress
}

// sitePing asks the staging site's Echo connector, through its ping
// route rhythm-echo/v1/ping, whether it runs and seals mail (PRD 13.1,
// side-effect.mail). A site with no route has no connector running; that
// fails unless the site's files show roadie-staging.php blocking wp_mail
// instead. A route that wants Echo's token, or a site behind basic auth,
// can't be asked and the coverage says so.
func (r *run) sitePing(ctx context.Context) {
	target, err := siteURL(r.j.Opts.SiteURL)
	if err != nil {
		return
	}
	c := siteClient(target, r.j.Opts.InsecureTLS)
	at := core.Location{File: pageURL(target, echoPingPath)}
	skipped := func(why string) {
		r.res.Coverage.Skipped = append(r.res.Coverage.Skipped, core.Skipped{Table: "--site " + at.File, Reason: why})
		r.skipCheck(core.FindingSideEffectMail, why)
	}
	mail := func(title, fix string) {
		r.add(core.Finding{ID: core.FindingSideEffectMail, Severity: core.SeverityCritical, Title: title, Fix: fix,
			Locations: []core.LocationCount{{Location: at}}})
	}
	status, body, err := r.ping(ctx, c, target, false)
	if err == nil && status == http.StatusNotFound {
		// Without pretty permalinks the REST API answers only at
		// ?rest_route=.
		status, body, err = r.ping(ctx, c, target, true)
	}
	if err != nil {
		skipped("the Echo connector's ping route couldn't be fetched: " + fetchReason(err))
		return
	}
	switch {
	case status == http.StatusUnauthorized:
		skipped("the site asks for a password, so the Echo connector's ping route can't be asked")
		return
	case status == http.StatusForbidden:
		skipped("the Echo connector's ping route wants its token; set RHYTHM_ECHO_TOKEN to ask it")
		return
	case status == http.StatusNotFound:
		r.count(core.FindingSideEffectMail, 1)
		if r.mailBlocked() {
			return
		}
		mail("The staging site doesn't answer the Echo connector's ping route, so no connector is known to seal its mail",
			"Install the connector: rhythm-roadie prepare <site> --only side-effects, then run verify again.")
		return
	case status != http.StatusOK:
		skipped("the Echo connector's ping route answered HTTP " + http.StatusText(status))
		return
	}
	var p echoPing
	if err := json.Unmarshal(body, &p); err != nil {
		skipped("the Echo connector's ping route didn't answer in JSON")
		return
	}
	r.count(core.FindingSideEffectMail, 1)
	fix := "Run rhythm-roadie prepare <site> --only side-effects, which installs the connector in seal mode, then run verify again."
	switch {
	case !p.Active:
		why := ""
		if p.Reason != "" {
			why = ": " + printable(p.Reason, 80)
		}
		mail("The Echo connector on the staging site isn't active"+why, fix)
	case !strings.EqualFold(p.Mode, "seal"):
		mail("The Echo connector on the staging site is in "+printable(p.Mode, 40)+" mode, so mail from this copy reaches real people", fix)
	case p.Echo.Reachable && p.Echo.Mode != "" && !strings.EqualFold(p.Echo.Mode, "seal"):
		mail("Echo, as the staging site reaches it, is in "+printable(p.Echo.Mode, 40)+" mode, so mail from this copy reaches real people",
			"Run Echo in seal mode: rhythm echo up --mode seal.")
	}
}

// ping GETs the ping route, at /wp-json/ or with ?rest_route=, sending
// Echo's token when one is set.
func (r *run) ping(ctx context.Context, c *http.Client, target *url.URL, query bool) (int, []byte, error) {
	u := *target
	u.Path, u.RawPath, u.RawQuery, u.Fragment = strings.TrimSuffix(target.Path, "/")+echoPingPath, "", "", ""
	if query {
		u.Path = strings.TrimSuffix(target.Path, "/") + "/"
		u.RawQuery = "rest_route=" + url.QueryEscape(strings.TrimPrefix(echoPingPath, "/wp-json"))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	if r.j.Opts.EchoToken != "" {
		req.Header.Set("X-Echo-Token", r.j.Opts.EchoToken)
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, siteMaxBody))
	return resp.StatusCode, body, err
}

// mailBlocked reports whether the site's own mail check found
// roadie-staging.php blocking wp_mail, which needs no connector.
func (r *run) mailBlocked() bool {
	for _, se := range r.res.SideEffects {
		if se.ID == core.FindingSideEffectMail && se.Status == "off" && strings.Contains(se.How, "roadie-staging.php blocks wp_mail") {
			return true
		}
	}
	return false
}
