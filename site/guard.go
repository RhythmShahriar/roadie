// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package site

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RhythmShahriar/roadie/codec/urlrw"
	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/db"
	"github.com/RhythmShahriar/roadie/site/dotenv"
	"github.com/RhythmShahriar/roadie/site/internal/wpdb"
	"github.com/RhythmShahriar/roadie/site/remote"
	"github.com/RhythmShahriar/roadie/site/wpconfig"
	"golang.org/x/text/unicode/norm"
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
	if in.Mode != modePrepare {
		if err := targetIsSourceSite(in, t); err != nil {
			return err
		}
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
	h = strings.TrimRight(h, ".")
	// One spelling per IP address: ::ffff:127.0.0.1 is 127.0.0.1 and an
	// IPv6 literal is written short.
	if ip := net.ParseIP(h); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			ip = v4
		}
		h = ip.String()
	}
	return h
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
	// A config file that is there but can't be read or parsed counts as
	// production: the guard can't tell, so it fails closed.
	b, err := s.ReadFile(ctx, "wp-config.php")
	switch {
	case err == nil:
		t.hasFiles = true
		f, err := wpconfig.Parse(b)
		if err != nil {
			t.env = "has a wp-config.php Roadie can't parse, so it counts as production"
			t.envFix = "set WP_ENVIRONMENT_TYPE to staging there"
			break
		}
		for _, n := range []string{"WP_HOME", "WP_SITEURL"} {
			if p, ok := f.Get(n); ok {
				t.add(urlHost(p.Value), "is", true, false)
			}
		}
		// A file wp-config.php includes first wins: PHP keeps the
		// first define.
		r := f.Resolve("WP_ENVIRONMENT_TYPE", func(rel string) ([]byte, error) { return s.ReadFile(ctx, rel) })
		d, ok := r.Define, r.Found
		in := ""
		if r.File != "wp-config.php" {
			in = " in " + r.File + ", which wp-config.php includes first"
		}
		switch {
		case !ok:
			t.env = "has WP_ENVIRONMENT_TYPE unset, which WordPress reads as production"
		case !d.Literal:
			t.env = "sets WP_ENVIRONMENT_TYPE from " + exprSource(d.Value) + in + ", which Roadie can't run, so it counts as production"
		case d.Value == "production":
			t.env = "has WP_ENVIRONMENT_TYPE set to production" + in
		}
		t.envFix = "set WP_ENVIRONMENT_TYPE to staging there"
	case !errors.Is(err, fs.ErrNotExist):
		t.env = "has a wp-config.php Roadie can't read (" + readReason(err) + "), so it counts as production"
		t.envFix = "make it readable"
	default:
		b, err := s.ReadFile(ctx, ".env")
		switch {
		case err == nil:
			t.hasFiles = true
			f, err := dotenv.Parse(b)
			if err != nil {
				t.env = "has a .env Roadie can't parse, so it counts as production"
				t.envFix = "set APP_ENV=staging there"
				break
			}
			if p, ok := f.Get("APP_URL"); ok {
				t.add(urlHost(p.Value), "is", true, false)
			}
			// Laravel reads a missing APP_ENV as production. Roadie
			// can't resolve ${...}, so both fail closed like WordPress.
			p, ok := f.Get("APP_ENV")
			switch {
			case !ok:
				t.env = "has APP_ENV unset in .env, which Laravel reads as production"
			case slices.Contains(f.Unresolved(), "APP_ENV"):
				t.env = "sets APP_ENV in .env from another variable, which Roadie can't resolve, so it counts as production"
			case p.Value == "production":
				t.env = "has APP_ENV=production in .env"
			}
			t.envFix = "set APP_ENV=staging there"
		case !errors.Is(err, fs.ErrNotExist):
			t.env = "has a .env Roadie can't read (" + readReason(err) + "), so it counts as production"
			t.envFix = "make it readable"
		case s.Stack() == core.StackWordPress:
			// WordPress also loads wp-config.php from one folder up, which
			// Roadie can't read, so the environment can't be told.
			t.env = "has no wp-config.php in the site folder Roadie can read, so it counts as production"
			t.envFix = "move wp-config.php into the site folder and set WP_ENVIRONMENT_TYPE to staging there"
		}
	}
	t.siteURL = in.SiteURL
	if t.siteURL == "" && s.Stack() == core.StackWordPress {
		u, err := readSiteURL(ctx, s)
		if err != nil {
			return nil, err
		}
		t.siteURL = u
	}
	if s.Stack() == core.StackSQL {
		// A database named by a DSN may hold a WordPress site under any
		// prefix (an ssh tunnel to production's database looks like a
		// loopback host), so every options table's siteurl and home and
		// every network host count.
		urls, err := dbSiteURLs(ctx, s)
		if err != nil {
			return nil, err
		}
		for _, u := range urls {
			if t.siteURL == "" {
				t.siteURL = u
			}
			t.add(urlHost(u), "is", true, true)
		}
	}
	t.add(urlHost(t.siteURL), "is", true, true)
	return t, nil
}

// dbSiteURLs reads the site URLs a database holds: siteurl and home of
// every table named *options with option_name and option_value (the main
// prefix's first) plus the domains of the network's blogs and site
// tables. A database that can't be reached is an error, as in
// readSiteURL.
func dbSiteURLs(ctx context.Context, s core.Site) ([]string, error) {
	src, err := s.Source(ctx, []string{})
	if err != nil {
		return nil, netErr(s, err)
	}
	if src == nil {
		return nil, nil
	}
	sc := src.Schema()
	_ = src.Close()
	if sc == nil {
		return nil, nil
	}
	var urls []string
	read := func(tb *core.Table) error {
		name := strings.ToLower(tb.Name)
		var cols []string
		switch {
		case strings.HasSuffix(name, "options") && tb.Col("option_name") >= 0 && tb.Col("option_value") >= 0:
			cols = []string{"option_name", "option_value"}
		case sc.Prefix != "" && (strings.EqualFold(name, sc.Prefix+"blogs") || strings.EqualFold(name, sc.Prefix+"site")) && tb.Col("domain") >= 0:
			cols = []string{"domain"}
		default:
			return nil
		}
		return wpdb.Rows(ctx, s, tb.Name, cols, func(v [][]byte) error {
			switch {
			case len(cols) == 1 && len(v[0]) > 0:
				urls = append(urls, printableURL(string(v[0])))
			case len(cols) == 2 && (string(v[0]) == "siteurl" || string(v[0]) == "home") && len(v[1]) > 0:
				urls = append(urls, printableURL(string(v[1])))
			}
			return nil
		})
	}
	tables := slices.Clone(sc.Tables)
	if main := sc.Table(sc.Prefix + "options"); sc.Prefix != "" && main != nil {
		tables = slices.DeleteFunc(tables, func(tb *core.Table) bool { return tb == main })
		tables = append([]*core.Table{main}, tables...)
	}
	for _, tb := range tables {
		if err := read(tb); err != nil {
			if e := netErr(s, err); e != nil {
				return nil, e
			}
		}
	}
	return urls, nil
}

// netErr turns a network error into the unreachable stop and drops any
// other: a database that answers without the tables has no URL to check.
func netErr(s core.Site, err error) error {
	var oe *net.OpError
	var de *net.DNSError
	switch {
	case errors.As(err, &oe):
		return unreachableErr(s, oe)
	case errors.As(err, &de):
		return unreachableErr(s, de)
	}
	return nil
}

// readReason says why a config file couldn't be read without echoing
// the remote stderr, which is not Roadie's to print.
func readReason(err error) string {
	var re *remote.RunError
	switch {
	case errors.Is(err, fs.ErrPermission),
		errors.As(err, &re) && bytes.Contains(re.Stderr, []byte("Permission denied")):
		return "permission denied"
	}
	return "the read failed"
}

// readSiteURL asks WP-CLI (ssh, alias) or the options table (local) for
// siteurl, else home. A target that can't be read yet (an empty database)
// has none. A database that can't be reached is an error: the guard
// fails closed, since it can't tell whether the target is production.
func readSiteURL(ctx context.Context, s core.Site) (string, error) {
	out, err := s.WP(ctx, "option", "get", "siteurl")
	if err == nil {
		return printableURL(string(out)), nil
	}
	if !errors.Is(err, core.ErrNoWPCLI) {
		return "", nil
	}
	opts, err := wpdb.Options(ctx, s, "siteurl", "home")
	if err != nil {
		var oe *net.OpError
		var de *net.DNSError
		switch {
		case errors.As(err, &oe):
			return "", unreachableErr(s, oe)
		case errors.As(err, &de):
			return "", unreachableErr(s, de)
		}
		return "", nil
	}
	if v := opts["siteurl"]; len(v) > 0 {
		return printableURL(string(v)), nil
	}
	return printableURL(string(opts["home"])), nil
}

// unreachableErr is the stop for a target whose database can't be
// reached. Only the network error is quoted: it names the host and port,
// never the password.
func unreachableErr(s core.Site, err error) error {
	where := "DB_HOST"
	if s.Endpoint().Kind == core.DSN {
		// A DSN has no wp-config.php or .env to hold DB_HOST.
		where = "the host and port in the DSN"
	}
	return core.Errorf(core.ErrUnreachable, 3,
		"unreachable: the database of %s: %v. Check that the database is running and reachable from here; then check %s.",
		s.Endpoint().Display, err, where)
}

// printableURL drops control and format characters (escape sequences,
// bidi overrides) from a URL read from the target's own data: the
// target-holds-production message quotes it. A crafted siteurl must
// not drive the terminal or a CI log that shows it.
func printableURL(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, s))
}

// productionHosts turns production: entries into host globs. Each entry
// is read on its own, so one urlrw.Hosts can't read (a bare IPv6
// address) doesn't lose the punycode and www. forms of the others.
func productionHosts(prod []string) []string {
	var out []string
	for _, p := range prod {
		if hosts, err := urlrw.Hosts([]string{p}); err == nil {
			out = append(out, hosts...)
			continue
		}
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

// idnDots are the dots IDNA reads as a label separator besides '.'
// (NFKC already folds the fullwidth one).
var idnDots = strings.NewReplacer("\u3002", ".", "\uff61", ".")

// matchHost reports the production glob host matches. The host is
// compared as IDNA reads it too: fullwidth letters folded (NFKC) and an
// IDN in Unicode matched with its punycode, so a target that spells
// production's host another way is still production.
func matchHost(host string, globs []string) bool {
	if urlrw.MatchHost(globs, idnDots.Replace(norm.NFKC.String(host))) {
		return true
	}
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

// SourceHosts are the hosts of the source's site URLs that a copy's
// target must not answer to: production's own, so not one on this
// machine and not the host --url names (a staging refreshed from its own
// backup).
func SourceHosts(urls []string, staging string) []string {
	own := normHost(urlHost(staging))
	var out []string
	for _, u := range urls {
		h := normHost(urlHost(u))
		if h == "" || loopback(h) || h == own || slices.Contains(out, h) {
			continue
		}
		out = append(out, h)
	}
	return out
}

// targetIsSourceSite refuses a copy whose target answers to the source's
// own site URL: the source is production, so a target that calls itself
// by production's URL is production (an ssh tunnel to its database looks
// like 127.0.0.1), whatever production: says.
func targetIsSourceSite(in GuardInput, t *targetFacts) error {
	hosts := SourceHosts(in.SourceURLs, in.URL)
	if len(hosts) == 0 {
		return nil
	}
	globs := productionHosts(hosts)
	for _, h := range t.hosts {
		if matchHost(h.host, globs) {
			return core.Errorf(core.ErrTargetIsProduction, 3,
				"target-is-production: %s %s %s, the source's own site URL. Roadie never writes to production. Point --to at the staging copy; if this is a staging that still holds a raw copy of production, run prepare on it instead.",
				in.To.Display, h.what, h.host)
		}
	}
	return nil
}

// targetSaysProduction refuses a target whose own config says production,
// unless --target-is-staging names its host exactly. A host production:
// lists is never accepted. On prepare neither is the copy's siteurl:
// targetIsProduction lets that one through because it is production's.
func targetSaysProduction(in GuardInput, t *targetFacts) error {
	if t.env == "" {
		return nil
	}
	override := strings.TrimSpace(in.TargetIsStaging)
	if override != "" && matchHost(normHost(override), productionHosts(in.Production)) {
		override = ""
	}
	suggest := "<host>"
	for _, h := range t.hosts {
		if !h.byName || h.site && in.Mode == modePrepare {
			continue
		}
		if suggest == "<host>" {
			suggest = h.host
		}
		if override != "" && strings.EqualFold(override, h.host) {
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
	// The advice never names siteurl as production's: on a staging that
	// already calls itself by its own URL that would be wrong.
	return core.Errorf(core.ErrTargetHoldsProduction, 3,
		"target-holds-production: %s calls itself %s; Roadie can't tell whether that is the live site. Pass --production-url <production URL> (the live site's URL) so Roadie knows which URL is live, or --url %s if it is this staging's own URL.",
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

// instanceOf names the database endpoint e (or site s) reaches on its
// server, whatever address reached it (db.Conn.Instance); "" when it
// can't tell. A variable so tests never connect.
var instanceOf = func(ctx context.Context, e core.Endpoint, s core.Site) string {
	ep := e
	if e.Kind != core.DSN {
		d, ok := s.(interface {
			dbEndpoint(context.Context) (core.Endpoint, error)
		})
		if !ok {
			return ""
		}
		var err error
		if ep, err = d.dbEndpoint(ctx); err != nil {
			return ""
		}
	}
	c, err := openDB(ctx, ep, db.ReadOnly)
	if err != nil {
		return ""
	}
	defer func() { _ = c.Close() }()
	if i, ok := c.(interface{ Instance() string }); ok {
		return i.Instance()
	}
	return ""
}

// sameServerDB asks both servers who they are when the text of the two
// endpoints names the same database on different addresses: a source
// reached at 127.0.0.1:40316 and a target at the container's bridge
// address are one database.
func sameServerDB(ctx context.Context, in GuardInput, a, b core.DBIdentity) bool {
	if family(a.Engine) != family(b.Engine) || a.Engine == core.SQLite || !strings.EqualFold(a.Name, b.Name) {
		return false
	}
	x := instanceOf(ctx, in.From, in.FromSite)
	if x == "" {
		return false
	}
	return x == instanceOf(ctx, in.To, in.ToSite)
}

func serverName(d core.Dialect) string {
	switch family(d) {
	case core.Postgres:
		return "PostgreSQL"
	}
	return "MySQL"
}

func hostPort(id core.DBIdentity) string {
	return net.JoinHostPort(id.Host, strconv.Itoa(id.Port))
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
	if okA && okB && !sameDB(a, b) && sameServerDB(ctx, in, a, b) {
		// Two addresses (a published port and the container's own
		// address, a host name and its IP) of one server.
		return core.Errorf(core.ErrTargetIsSource, 3,
			"target-is-source: --from and --to both reach database %s on the same %s server (%s and %s are two addresses of it). A copy can't replace its own source.",
			b.Name, serverName(b.Engine), hostPort(a), hostPort(b))
	}
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
