// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package site

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/RhythmShahriar/roadie/codec/urlrw"
	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/site/dotenv"
	"github.com/RhythmShahriar/roadie/site/internal/wpdb"
	"github.com/RhythmShahriar/roadie/site/remote"
	"github.com/RhythmShahriar/roadie/site/wpconfig"
)

// The GuardInput modes; any other value guards like a copy.
const (
	modePrepare = "prepare"
)

// guard runs the PRD 10.4 refusals in order. Each reads the target only;
// nothing is written before every one has passed.
func guard(ctx context.Context, in GuardInput) error {
	switch in.To.Kind {
	case core.Stdio:
		return nil
	case core.DumpFile:
		return outputIsInput(in)
	}
	t, err := readTarget(ctx, in)
	if err != nil {
		return err
	}
	if in.Mode != modePrepare {
		if err := targetIsSource(ctx, in); err != nil {
			return err
		}
	}
	if err := targetIsProduction(in, t); err != nil {
		return err
	}
	if err := targetSaysProduction(in, t); err != nil {
		return err
	}
	if in.Mode == modePrepare {
		return targetHoldsProduction(in, t)
	}
	return nil
}

// outputIsInput refuses a dump-file target that is the input file, a
// hard link to it included.
func outputIsInput(in GuardInput) error {
	if in.From.Kind != core.DumpFile {
		return nil
	}
	a, errA := os.Stat(in.From.Path)
	b, errB := os.Stat(in.To.Path)
	if errA != nil || errB != nil || !os.SameFile(a, b) {
		return nil
	}
	return core.Errorf(core.ErrOutputIsInput, 2,
		"output-is-input: --to %s is the file Roadie reads. Write the copy to a new file.", in.To.Display)
}

// targetFacts is what the guards read from the target.
type targetFacts struct {
	hosts    []hostFact // every host the target answers to or reaches
	siteURL  string     // siteurl, or home, when known
	env      string     // why the target says production, "" when it doesn't
	envFix   string     // the setting to change there
	hasFiles bool
}

// hostFact is one host of the target and where it came from.
type hostFact struct {
	host   string
	what   string // "is", "uses database host"
	byName bool   // a name the target answers to, for --target-is-staging
	site   bool   // from siteurl or home in the database
}

func (t *targetFacts) add(host, what string, byName, site bool) {
	h := normHost(host)
	if h == "" {
		return
	}
	for _, x := range t.hosts {
		if x.host == h && x.what == what {
			return
		}
	}
	t.hosts = append(t.hosts, hostFact{host: h, what: what, byName: byName, site: site})
}

// normHost lower-cases a host and drops a port and a trailing dot.
func normHost(h string) string {
	h = strings.TrimSpace(strings.ToLower(h))
	if hh, _, err := net.SplitHostPort(h); err == nil {
		h = hh
	}
	h = strings.Trim(h, "[]")
	return strings.TrimSuffix(h, ".")
}

// urlHost is the host of a URL, or of a bare host.
func urlHost(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "//" + strings.TrimPrefix(s, "//")
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// loopback reports a database host that means "this machine".
func loopback(h string) bool {
	switch normHost(h) {
	case "", "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// readTarget collects the target's hosts and its environment setting.
func readTarget(ctx context.Context, in GuardInput) (*targetFacts, error) {
	t := &targetFacts{}
	t.add(urlHost(in.URL), "is", true, false)
	e := in.To
	if in.ToSite != nil {
		e = in.ToSite.Endpoint()
	}
	if e.Kind == core.SSHSite || e.Kind == core.WPAlias {
		t.add(e.Host, "is", true, false)
	}
	if e.Kind == core.DSN && !loopback(e.Host) {
		t.add(e.Host, "uses database host", false, false)
	}
	s := in.ToSite
	if s == nil {
		return t, nil
	}
	if id, err := s.DBIdentity(ctx); err == nil && !loopback(id.Host) {
		t.add(id.Host, "uses database host", false, false)
	}
	if b, err := s.ReadFile(ctx, "wp-config.php"); err == nil {
		t.hasFiles = true
		if f, err := wpconfig.Parse(b); err == nil {
			for _, n := range []string{"WP_HOME", "WP_SITEURL"} {
				if p, ok := f.Get(n); ok {
					t.add(urlHost(p.Value), "is", true, false)
				}
			}
			d, ok := f.Lookup("WP_ENVIRONMENT_TYPE")
			switch {
			case !ok:
				t.env = "has WP_ENVIRONMENT_TYPE unset, which WordPress reads as production"
			case !d.Literal:
				t.env = "sets WP_ENVIRONMENT_TYPE from " + exprSource(d.Value) + ", which Roadie can't run, so it counts as production"
			case d.Value == "production":
				t.env = "has WP_ENVIRONMENT_TYPE set to production"
			}
			t.envFix = "set WP_ENVIRONMENT_TYPE to staging there"
		}
	} else if b, err := s.ReadFile(ctx, ".env"); err == nil {
		t.hasFiles = true
		if f, err := dotenv.Parse(b); err == nil {
			if p, ok := f.Get("APP_URL"); ok {
				t.add(urlHost(p.Value), "is", true, false)
			}
			if p, ok := f.Get("APP_ENV"); ok && p.Value == "production" {
				t.env = "has APP_ENV=production in .env"
				t.envFix = "set APP_ENV=staging there"
			}
		}
	}
	t.siteURL = in.SiteURL
	if t.siteURL == "" && s.Stack() == core.StackWordPress {
		t.siteURL = readSiteURL(ctx, s)
	}
	t.add(urlHost(t.siteURL), "is", true, true)
	return t, nil
}

// readSiteURL asks WP-CLI (ssh, alias) or the options table (local) for
// siteurl, else home. A target that can't be read yet (an empty database)
// has none.
func readSiteURL(ctx context.Context, s core.Site) string {
	out, err := s.WP(ctx, "option", "get", "siteurl")
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	if !errors.Is(err, core.ErrNoWPCLI) {
		return ""
	}
	opts, err := wpdb.Options(ctx, s, "siteurl", "home")
	if err != nil {
		return ""
	}
	if v := opts["siteurl"]; len(v) > 0 {
		return string(v)
	}
	return string(opts["home"])
}

// productionHosts turns production: entries into host globs.
func productionHosts(prod []string) []string {
	hosts, err := urlrw.Hosts(prod)
	if err == nil {
		return hosts
	}
	// Until the values lane lands urlrw.Hosts, read the hosts here: a URL,
	// a bare host or a glob such as *.shop.com.
	var out []string
	for _, p := range prod {
		h := p
		if strings.Contains(p, "://") || strings.Contains(p, "/") {
			h = urlHost(p)
		}
		if h = normHost(h); h != "" {
			out = append(out, h)
		}
	}
	return out
}

// matchHost reports the production glob host matches.
func matchHost(host string, globs []string) bool {
	for _, g := range globs {
		g = normHost(g)
		if g == host {
			return true
		}
		if ok, err := path.Match(g, host); err == nil && ok {
			return true
		}
	}
	return false
}

// targetIsProduction refuses a target any of whose hosts production:
// lists. On prepare the copy's own siteurl is still production's, which
// is what prepare fixes, so it doesn't count there.
func targetIsProduction(in GuardInput, t *targetFacts) error {
	globs := productionHosts(in.Production)
	if len(globs) == 0 {
		return nil
	}
	for _, h := range t.hosts {
		if h.site && in.Mode == modePrepare {
			continue
		}
		if matchHost(h.host, globs) {
			return core.Errorf(core.ErrTargetIsProduction, 3,
				"target-is-production: %s %s %s, which production: lists. Roadie never writes to production. Point --to at the staging copy.",
				in.To.Display, h.what, h.host)
		}
	}
	return nil
}

// targetSaysProduction refuses a target whose own config says production,
// unless --target-is-staging names its host exactly.
func targetSaysProduction(in GuardInput, t *targetFacts) error {
	if t.env == "" {
		return nil
	}
	suggest := "<host>"
	for _, h := range t.hosts {
		if !h.byName {
			continue
		}
		if suggest == "<host>" {
			suggest = h.host
		}
		if in.TargetIsStaging != "" && strings.EqualFold(strings.TrimSpace(in.TargetIsStaging), h.host) {
			return nil
		}
	}
	return core.Errorf(core.ErrTargetSaysProduction, 3,
		"target-says-production: %s %s. If it's staging, %s, or pass --target-is-staging %s.",
		in.To.Display, t.env, t.envFix, suggest)
}

// targetHoldsProduction refuses a prepare when the staging still calls
// itself by a URL nothing marks as production or staging, so Roadie can't
// tell which one is live.
func targetHoldsProduction(in GuardInput, t *targetFacts) error {
	if t.siteURL == "" || in.ProductionURL != "" || len(in.Production) > 0 {
		return nil
	}
	if in.URL != "" && sameURL(t.siteURL, in.URL) {
		return nil
	}
	return core.Errorf(core.ErrTargetHoldsProduction, 3,
		"target-holds-production: %s still calls itself %s. Pass --production-url %s so Roadie knows which URL is live.",
		in.To.Display, t.siteURL, t.siteURL)
}

// sameURL compares two site URLs by host and path, ignoring the scheme
// and a trailing slash.
func sameURL(a, b string) bool {
	norm := func(s string) string {
		s = strings.TrimSpace(strings.ToLower(s))
		if i := strings.Index(s, "://"); i >= 0 {
			s = s[i+3:]
		}
		return strings.TrimSuffix(s, "/")
	}
	return norm(a) == norm(b)
}

// place is where an endpoint's files are, for target-is-source.
type place struct {
	local string // a local folder or file
	ssh   string // host:port:path on another machine
	label string // for the message
}

func placeOf(e core.Endpoint, s core.Site) place {
	if s != nil {
		e = s.Endpoint()
	}
	if e.Kind == core.WPAlias && e.Host == "" && e.Path == "" {
		if dir, err := getwd(); err == nil {
			if r, err := remote.ResolveAlias(dir, e.Alias); err == nil {
				e.Host, e.Port, e.Path, e.User = r.Host, r.Port, r.Path, r.User
				if r.Kind == core.LocalSite {
					e.Kind = core.LocalSite
				}
			}
		}
	}
	switch {
	case e.Kind == core.LocalSite || e.Kind == core.DumpFile || e.Kind == core.WPAlias && e.Host == "":
		if e.Path == "" {
			return place{}
		}
		return place{local: e.Path, label: e.Path}
	case e.Kind == core.SSHSite || e.Kind == core.WPAlias:
		port := e.Port
		if port == 0 {
			port = 22
		}
		return place{ssh: fmt.Sprintf("%s:%d:%s", normHost(e.Host), port, path.Clean(e.Path)), label: path.Clean(e.Path) + " on " + normHost(e.Host)}
	}
	return place{}
}

// identityOf is the database an endpoint reaches; ok is false when it
// can't be told without more than the endpoint.
func identityOf(ctx context.Context, e core.Endpoint, s core.Site) (core.DBIdentity, bool) {
	if s == nil && e.Kind == core.DSN {
		s = openDSN(e, Options{ReadOnly: true})
	}
	if s == nil {
		return core.DBIdentity{}, false
	}
	id, err := s.DBIdentity(ctx)
	return id, err == nil && id.Name != ""
}

// family puts MySQL and MariaDB together: one server speaks both.
func family(d core.Dialect) core.Dialect {
	if d == core.MariaDB {
		return core.MySQL
	}
	return d
}

// sameDB compares two identities. Two loopback hosts are one database
// only on the same machine (Server).
func sameDB(a, b core.DBIdentity) bool {
	if family(a.Engine) != family(b.Engine) || a.Name != b.Name {
		return false
	}
	if a.Engine == core.SQLite {
		fa, errA := os.Stat(a.Name)
		fb, errB := os.Stat(b.Name)
		if errA == nil && errB == nil {
			return os.SameFile(fa, fb)
		}
		return filepath.Clean(a.Name) == filepath.Clean(b.Name)
	}
	if a.Port != b.Port {
		return false
	}
	if loopback(a.Host) && loopback(b.Host) {
		return a.Server == b.Server
	}
	return normHost(a.Host) == normHost(b.Host)
}

// targetIsSource refuses a target that is the source: the same folder or
// file, the same ssh host and path, or the same database.
func targetIsSource(ctx context.Context, in GuardInput) error {
	if in.From.Kind == core.Stdio {
		return nil
	}
	from, to := placeOf(in.From, in.FromSite), placeOf(in.To, in.ToSite)
	if from.local != "" && to.local != "" {
		a, errA := os.Stat(from.local)
		b, errB := os.Stat(to.local)
		if errA == nil && errB == nil && os.SameFile(a, b) {
			return core.Errorf(core.ErrTargetIsSource, 3,
				"target-is-source: --from and --to are the same folder (%s). A copy can't replace its own source.", to.label)
		}
	}
	if from.ssh != "" && from.ssh == to.ssh {
		return core.Errorf(core.ErrTargetIsSource, 3,
			"target-is-source: --from and --to are both %s. A copy can't replace its own source.", to.label)
	}
	a, okA := identityOf(ctx, in.From, in.FromSite)
	b, okB := identityOf(ctx, in.To, in.ToSite)
	if okA && okB && sameDB(a, b) {
		where := b.Host
		if b.Engine == core.SQLite {
			where = "this machine"
		} else if loopback(b.Host) && b.Server != "" {
			where = b.Host + " on " + b.Server
		}
		name := b.Name
		if b.Engine == core.SQLite {
			name = filepath.Base(b.Name)
		}
		return core.Errorf(core.ErrTargetIsSource, 3,
			"target-is-source: --from and --to both reach database %s on %s. A copy can't replace its own source.", name, where)
	}
	return nil
}
