// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verify

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/RhythmShahriar/echo/client"
	"github.com/RhythmShahriar/echo/model"
	"github.com/RhythmShahriar/rhythm/kit/cli"
	"github.com/RhythmShahriar/rhythm/kit/httpx"

	"github.com/RhythmShahriar/roadie/core"
)

// Limits of the --site fetch (PRD 15.1).
const (
	siteTimeout = 10 * time.Second
	siteMaxBody = 2 << 20
)

// DefaultEchoURL is where verify --echo looks for Echo by default.
const DefaultEchoURL = client.DefaultURL

// echoTimeout bounds each call to Echo.
const echoTimeout = 10 * time.Second

// maxEchoPages caps how many pages of messages are counted.
const maxEchoPages = 200

// checkOptions refuses options that could reach the wrong host before
// anything is read: a --site on a production host and an Echo URL that
// would send its token in plain HTTP beyond this machine or network.
func checkOptions(j Job) error {
	if j.Opts.SiteURL != "" {
		u, err := siteURL(j.Opts.SiteURL)
		if err != nil {
			return err
		}
		if matchHost(productionHosts(j.Production), strings.ToLower(u.Hostname())) {
			return core.Errorf(core.ErrTargetIsProduction, 3,
				"target-is-production: --site %s is %s, which production: lists. Roadie never fetches production. Point --site at the staging copy.",
				u.Redacted(), u.Hostname())
		}
	}
	if j.Opts.Echo {
		if _, err := echoURL(j.Opts.EchoURL); err != nil {
			return err
		}
	}
	return nil
}

// siteURL parses --site; a bare host means https.
func siteURL(s string) (*url.URL, error) {
	u, _, err := httpx.ParseTarget(strings.TrimSpace(s))
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, core.Errorf(core.ErrConfig, cli.ExitUsage, "verify --site: %q isn't a URL; give the staging site's address, e.g. https://staging.shop.test", s)
	}
	return u, nil
}

// echoURL parses the Echo URL and checks it is loopback or private
// unless it is https (security N6).
func echoURL(s string) (*url.URL, error) {
	if strings.TrimSpace(s) == "" {
		s = DefaultEchoURL
	}
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, core.Errorf(core.ErrConfig, cli.ExitUsage, "verify --echo: %q isn't a URL; Echo listens on http://127.0.0.1:8025 by default", s)
	}
	if u.Scheme == "https" || localHost(u.Hostname()) {
		return u, nil
	}
	return nil, core.Errorf(core.ErrConfig, cli.ExitUsage,
		"verify --echo: %s is plain http beyond this machine and its network, where anyone on the way could read or fake its answers. Use https:// for an Echo elsewhere.", u.Redacted())
}

// localHost reports whether host is localhost or a loopback, private or
// link-local address.
func localHost(host string) bool {
	h := strings.ToLower(host)
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	a, err := netip.ParseAddr(h)
	return err == nil && httpx.AddrClass(a) != ""
}

// extraChecks runs the checks that talk to services: the staging site
// over HTTP and Echo.
func (r *run) extraChecks(ctx context.Context) {
	if r.j.Opts.SiteURL != "" {
		r.siteHTTP(ctx)
	}
	if r.j.Opts.Echo && !r.skip.skips(core.FindingSideEffectMail) {
		r.echo(ctx)
	}
}

// siteClient is the client for --site: kit httpx with its guard and the
// size and time caps. Redirects go only to the same host and port;
// InsecureTLS applies to that one host only.
func siteClient(target *url.URL, insecureHost string) *http.Client {
	tr := &http.Transport{}
	if dt, ok := http.DefaultTransport.(*http.Transport); ok {
		tr = dt.Clone()
	}
	g := httpx.NewGuard(target.Hostname())
	tr.Proxy, tr.DialContext = g.Proxy, g.DialContext
	// The client only ever reaches the target host (redirects stay on
	// it), so trusting any certificate is scoped to that one host.
	if strings.EqualFold(strings.TrimSpace(insecureHost), target.Hostname()) {
		tr.TLSClientConfig = insecureFor(target.Hostname())
	}
	c := httpx.New(httpx.Options{
		UserAgent: "rhythm-roadie verify",
		Timeout:   siteTimeout,
		MaxBody:   siteMaxBody + 1,
		Target:    target,
		Transport: tr,
	})
	policy := c.CheckRedirect
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if !httpx.SameHost(target, req.URL) {
			return fmt.Errorf("not following a redirect to %s: %w", req.URL.Redacted(), httpx.ErrOffSite)
		}
		return policy(req, via)
	}
	return c
}

// insecureFor accepts any certificate from host and verifies every other
// host as usual, so --insecure-tls can't weaken a connection elsewhere.
// A connection to an IP literal sends no server name and is the target.
func insecureFor(host string) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // #nosec G402 -- VerifyConnection checks every host but the one the user named
		VerifyConnection: func(cs tls.ConnectionState) error {
			if cs.ServerName == "" || strings.EqualFold(cs.ServerName, host) {
				return nil
			}
			if len(cs.PeerCertificates) == 0 {
				return errors.New("no certificate")
			}
			inter := x509.NewCertPool()
			for _, c := range cs.PeerCertificates[1:] {
				inter.AddCert(c)
			}
			_, err := cs.PeerCertificates[0].Verify(x509.VerifyOptions{DNSName: cs.ServerName, Intermediates: inter})
			return err
		},
	}
}

// page is one fetched page.
type page struct {
	status int
	header http.Header
	body   []byte
}

// fetch GETs one path of the target, reading at most siteMaxBody bytes.
func fetch(ctx context.Context, c *http.Client, target *url.URL, p string) (*page, error) {
	u := *target
	u.Path, u.RawQuery, u.Fragment = p, "", ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, siteMaxBody))
	if err != nil {
		return nil, err
	}
	return &page{status: resp.StatusCode, header: resp.Header, body: body}, nil
}

// siteHTTP fetches / and /robots.txt of the staging host for
// search.indexable and auth.none.
func (r *run) siteHTTP(ctx context.Context) {
	target, err := siteURL(r.j.Opts.SiteURL)
	if err != nil {
		return
	}
	c := siteClient(target, r.j.Opts.InsecureTLS)
	home, err := fetch(ctx, c, target, "/")
	if err != nil {
		r.res.Coverage.Skipped = append(r.res.Coverage.Skipped, core.Skipped{Table: "--site /", Reason: fetchReason(err)})
		r.skipCheck(core.FindingSearchIndexable, "the home page couldn't be fetched")
		r.skipCheck(core.FindingAuthNone, "the home page couldn't be fetched")
		return
	}
	robots, err := fetch(ctx, c, target, "/robots.txt")
	if err != nil {
		r.res.Coverage.Skipped = append(r.res.Coverage.Skipped, core.Skipped{Table: "--site /robots.txt", Reason: fetchReason(err)})
	}
	pages := int64(1)
	if robots != nil {
		pages++
	}
	r.count(core.FindingSearchIndexable, pages)
	r.count(core.FindingAuthNone, pages)
	at := core.Location{File: target.Scheme + "://" + target.Host + "/"}
	if home.status == http.StatusUnauthorized && home.header.Get("WWW-Authenticate") != "" {
		// Behind basic auth the pages can't be read, which is the point.
		return
	}
	if !r.skip.skips(core.FindingSearchIndexable) {
		noindex := hasNoindex(home.header.Values("X-Robots-Tag")) || metaNoindex(home.body)
		allows := robots == nil || robots.status != http.StatusOK || robotsAllowAll(robots.body)
		if !noindex || allows {
			why := "the home page has no noindex header or meta tag"
			if noindex {
				why = "robots.txt lets every crawler in"
			}
			r.add(core.Finding{ID: core.FindingSearchIndexable, Severity: core.SeverityMedium,
				Title:     "Search engines can index the staging site: " + why,
				Fix:       "Run rhythm-roadie prepare --only exposure, or serve X-Robots-Tag: noindex.",
				Locations: []core.LocationCount{{Location: at}}})
		}
	}
	if home.status >= 200 && home.status < 300 {
		r.add(core.Finding{ID: core.FindingAuthNone, Severity: core.SeverityLow,
			Title:     "The staging site answers without a password",
			Fix:       "Put it behind basic auth: rhythm-roadie prepare --basic-auth <user> writes the snippets.",
			Locations: []core.LocationCount{{Location: at}}})
	}
}

// fetchReason says why a fetch failed without any page content.
func fetchReason(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	switch {
	case errors.Is(err, httpx.ErrOffSite):
		return "refused a redirect off the staging host: " + err.Error()
	case errors.Is(err, httpx.ErrPrivate):
		return "refused to connect to a private address: " + err.Error()
	}
	return err.Error()
}

// hasNoindex reports whether an X-Robots-Tag value says noindex (or
// none) for every crawler. A rule for one bot ("googlebot: noindex")
// leaves the rest free to index, so it doesn't count.
func hasNoindex(values []string) bool {
	for _, v := range values {
		for d := range strings.SplitSeq(strings.ToLower(v), ",") {
			d = strings.TrimSpace(d)
			if d == "noindex" || d == "none" {
				return true
			}
		}
	}
	return false
}

// metaNoindex reports whether the page has <meta name="robots"
// content="...noindex...">. It reads tags by hand: the page is untrusted
// and only one attribute pair matters.
func metaNoindex(body []byte) bool {
	low := bytes.ToLower(body)
	for off := 0; ; {
		i := bytes.Index(low[off:], []byte("<meta"))
		if i < 0 {
			return false
		}
		start := off + i
		end := bytes.IndexByte(low[start:], '>')
		if end < 0 {
			return false
		}
		tag := string(low[start : start+end])
		off = start + end
		name := attr(tag, "name")
		if name != "robots" && name != "googlebot" {
			continue
		}
		c := attr(tag, "content")
		if strings.Contains(c, "noindex") || strings.Contains(c, "none") {
			return true
		}
	}
}

// attr returns the value of attribute a in a lower-cased tag.
func attr(tag, a string) string {
	for off := 0; ; {
		i := strings.Index(tag[off:], a)
		if i < 0 {
			return ""
		}
		i += off
		off = i + len(a)
		if i > 0 && tag[i-1] != ' ' && tag[i-1] != '\t' && tag[i-1] != '\n' {
			continue
		}
		rest := strings.TrimLeft(tag[off:], " \t\n")
		rest, ok := strings.CutPrefix(rest, "=")
		if !ok {
			continue
		}
		rest = strings.TrimLeft(rest, " \t\n")
		if rest == "" {
			return ""
		}
		if q := rest[0]; q == '"' || q == '\'' {
			if j := strings.IndexByte(rest[1:], q); j >= 0 {
				return rest[1 : j+1]
			}
			return rest[1:]
		}
		if j := strings.IndexAny(rest, " \t\n/"); j >= 0 {
			return rest[:j]
		}
		return rest
	}
}

// robotsAllowAll reports whether robots.txt lets User-agent: * crawl "/":
// no "Disallow: /" in the group that names *.
func robotsAllowAll(body []byte) bool {
	inStar, groupStarted := false, false
	for line := range strings.SplitSeq(string(body), "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch k {
		case "user-agent":
			if groupStarted {
				inStar, groupStarted = false, false
			}
			if v == "*" {
				inStar = true
			}
		case "disallow":
			groupStarted = true
			if inStar && v == "/" {
				return false
			}
		default:
			groupStarted = true
		}
	}
	return true
}

// echo asks Echo whether its seal is on and how many messages it let
// through since the run started (side-effect.mail).
func (r *run) echo(ctx context.Context) {
	u, err := echoURL(r.j.Opts.EchoURL)
	if err != nil {
		return
	}
	c := client.New(u.String(), "")
	c.HTTP = &http.Client{Timeout: echoTimeout}
	r.count(core.FindingSideEffectMail, 1)
	at := core.Location{File: u.Redacted()}
	mail := func(title, fix string) {
		r.add(core.Finding{ID: core.FindingSideEffectMail, Severity: core.SeverityCritical, Title: title, Fix: fix,
			Locations: []core.LocationCount{{Location: at}}})
	}
	h, err := c.Health(ctx)
	if needsToken(err) {
		// Echo asks for its token on every route once it listens beyond
		// loopback; verify has no token to send yet.
		mail(fmt.Sprintf("verify --echo: Echo at %s wants a token, which verify doesn't send", u.Redacted()),
			"Run verify where Echo listens on loopback (http://127.0.0.1:8025), which needs no token.")
		return
	}
	if err != nil {
		mail(fmt.Sprintf("verify --echo: Echo isn't reachable at %s; start it with rhythm echo up.", u.Redacted()),
			"Start Echo (rhythm echo up), then run verify again.")
		return
	}
	if h.Mode != "seal" {
		mail("Echo is in "+h.Mode+" mode, so mail from this copy reaches real people",
			"Run Echo in seal mode: rhythm echo up --mode seal.")
	}
	sent := int64(0)
	f := client.Filter{Since: r.started(), Limit: 200}
	for range maxEchoPages {
		p, err := c.List(ctx, f)
		if err != nil {
			// Without the count the check can't say nothing went out, so
			// it fails closed.
			mail("verify --echo: couldn't count what Echo let through: "+echoReason(err),
				"Check Echo's inbox for sent messages, then run verify again.")
			return
		}
		for _, m := range p.Messages {
			if m.Status == model.Sent {
				sent++
			}
		}
		if p.Next == "" {
			break
		}
		f.Before = p.Next
	}
	if sent > 0 {
		mail(fmt.Sprintf("Echo let %d messages through since the copy", sent),
			"Check who they reached in Echo's inbox, then keep Echo in seal mode.")
	}
}

// needsToken reports whether Echo refused a request for want of a token.
func needsToken(err error) bool {
	var ae *client.APIError
	return errors.As(err, &ae) && (ae.StatusCode == http.StatusUnauthorized || ae.StatusCode == http.StatusForbidden)
}

// echoReason says why an Echo call failed. For an HTTP answer it keeps
// the status alone, since Echo's message body is not ours to print.
func echoReason(err error) string {
	var ae *client.APIError
	if errors.As(err, &ae) {
		return "Echo answered " + ae.Status
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return err.Error()
}

// started is when the run began, from Options.Clock.
func (r *run) started() time.Time { return r.start }
