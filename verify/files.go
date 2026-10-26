// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verify

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"net"
	"net/netip"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/keys"
	"github.com/RhythmShahriar/roadie/site/dotenv"
	"github.com/RhythmShahriar/roadie/site/wpconfig"
)

// parsePairs reads wp-config.php or .env into pairs; a hook for tests
// while the site lane's parsers return core.ErrNotBuilt.
var parsePairs = func(name string, b []byte) ([]core.Pair, error) {
	if name == ".env" {
		f, err := dotenv.Parse(b)
		if err != nil {
			return nil, err
		}
		return f.Pairs(), nil
	}
	f, err := wpconfig.Parse(b)
	if err != nil {
		return nil, err
	}
	return f.Pairs(), nil
}

// configFiles are the config files verify reads on a site.
var configFiles = []string{"wp-config.php", ".env"}

// saltNames are the eight WordPress salts and Laravel's APP_KEY.
var saltNames = []string{
	"AUTH_KEY", "SECURE_AUTH_KEY", "LOGGED_IN_KEY", "NONCE_KEY",
	"AUTH_SALT", "SECURE_AUTH_SALT", "LOGGED_IN_SALT", "NONCE_SALT", "APP_KEY",
}

// productionNames are the settings that must not reach production's
// database, cache or storage (config.points-at-production).
var productionNames = []string{
	"DB_HOST", "WP_REDIS_HOST", "AS3CF_SETTINGS", "REDIS_HOST", "MEMCACHED_HOST",
	"AWS_BUCKET", "AWS_ACCESS_KEY_ID",
}

// siteURLNames are the defines and variables that name the site URL.
var siteURLNames = []string{"WP_HOME", "WP_SITEURL", "APP_URL"}

// siteChecks reads the site's config files, code files and uploads and
// runs the Steps' checks.
func (r *run) siteChecks(ctx context.Context, s core.Site) {
	for _, name := range configFiles {
		r.configFile(ctx, s, name)
	}
	if !r.skip.skips("key.") {
		r.codeFiles(ctx, s)
	}
	if !r.skip.skips(core.FindingUploadsPersonal) {
		r.uploads(ctx, s)
	}
	r.steps(ctx, s)
}

// configFile checks one config file: keys, the site URL and the config
// checks that compare with production.
func (r *run) configFile(ctx context.Context, s core.Site, name string) {
	b, err := s.ReadFile(ctx, name)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		r.res.Coverage.Skipped = append(r.res.Coverage.Skipped, core.Skipped{Table: name, Reason: err.Error()})
		return
	}
	pairs, err := parsePairs(name, b)
	if err != nil {
		r.res.Coverage.Skipped = append(r.res.Coverage.Skipped, core.Skipped{Table: name, Reason: "not parsed: " + err.Error()})
		return
	}
	r.countConfig()
	if !r.skip.skips("key.") {
		for _, h := range keys.ScanPairs(name, pairs) {
			var hits []core.KeyHit
			addKeyHit(h, r.sink(), &hits)
			for _, k := range hits {
				r.addKey(k)
			}
		}
	}
	hosts := productionHosts(r.j.Production)
	prod := map[string]string{}
	for _, p := range r.j.ProductionPairs {
		prod[p.Name] = p.Value
	}
	for _, p := range pairs {
		at := core.Location{File: name, Line: p.Line}
		if slices.Contains(siteURLNames, p.Name) && !r.skip.skips(core.FindingURLProductionSiteURL) && matchHost(hosts, hostOf(p.Value)) {
			r.add(core.Finding{ID: core.FindingURLProductionSiteURL, Severity: core.SeverityCritical,
				Title: p.Name + " is still production's", Fix: fixes[core.FindingURLProductionSiteURL],
				Locations: []core.LocationCount{{Location: at}}})
		}
		if r.skip.skips("config.") {
			continue
		}
		pv, inProd := prod[p.Name]
		if slices.Contains(saltNames, p.Name) && inProd && strings.TrimSpace(pv) != "" && strings.TrimSpace(pv) == strings.TrimSpace(p.Value) {
			r.add(core.Finding{ID: core.FindingConfigSalts, Severity: core.SeverityMedium,
				Title: titles[core.FindingConfigSalts], Detail: p.Name, Fix: fixes[core.FindingConfigSalts],
				Locations: []core.LocationCount{{Location: at}}})
		}
		if slices.Contains(productionNames, p.Name) && pointsAtProduction(p, pv, inProd, hosts) {
			r.add(core.Finding{ID: core.FindingConfigPointsAtProduction, Severity: core.SeverityCritical,
				Title: p.Name + " points at production", Detail: p.Name, Fix: fixes[core.FindingConfigPointsAtProduction],
				Locations: []core.LocationCount{{Location: at}}})
		}
	}
}

// countConfig counts one parsed config file for the checks that read it.
func (r *run) countConfig() {
	if !r.skip.skips("key.") {
		for _, id := range []string{CheckKeyLive, core.FindingKeyGeneric, core.FindingKeyUnknownEnv} {
			r.count(id, 1)
		}
	}
	if !r.skip.skips(core.FindingURLProductionSiteURL) && len(productionHosts(r.j.Production)) > 0 {
		r.count(core.FindingURLProductionSiteURL, 1)
	}
	if !r.skip.skips("config.") {
		r.count(core.FindingConfigPointsAtProduction, 1)
		if len(r.j.ProductionPairs) > 0 {
			r.count(core.FindingConfigSalts, 1)
		}
	}
}

// hostNames are the productionNames that name a server, whose value can
// be relative to the machine reading it.
var hostNames = []string{"DB_HOST", "WP_REDIS_HOST", "REDIS_HOST", "MEMCACHED_HOST"}

// pointsAtProduction compares a setting with production's as text and
// its host with the production hosts. No name is ever looked up.
func pointsAtProduction(p core.Pair, prodValue string, inProd bool, hosts []string) bool {
	v := strings.TrimSpace(p.Value)
	if v == "" {
		return false
	}
	// The same machine-local server name on both sides names two servers.
	sameText := inProd && v == strings.TrimSpace(prodValue)
	if sameText && (!slices.Contains(hostNames, p.Name) || !machineLocal(hostOf(v))) {
		return true
	}
	if p.Name == "AS3CF_SETTINGS" {
		if !inProd {
			return false
		}
		for _, k := range []string{"bucket", "access-key-id", "secret-access-key"} {
			if a, b := phpArrayValue(v, k), phpArrayValue(prodValue, k); a != "" && a == b {
				return true
			}
		}
		return false
	}
	return matchHost(hosts, hostOf(v))
}

// machineLocal reports whether host means a different server on each
// machine that reads it: empty (a unix socket path), localhost, a
// loopback, unspecified or link-local address, or a one-label name such
// as a Docker service "db". The same value on staging and production
// then names two servers, not production's. A private address is not
// machine-local: a staging server in production's network reaches the
// same database through it.
func machineLocal(host string) bool {
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if a, err := netip.ParseAddr(host); err == nil {
		a = a.Unmap()
		return a.IsLoopback() || a.IsUnspecified() || a.IsLinkLocalUnicast()
	}
	return !strings.Contains(host, ".") && !strings.Contains(host, ":")
}

// phpArrayValue returns the quoted value of 'key' => 'value' in PHP
// array source, or "".
func phpArrayValue(src, key string) string {
	for _, q := range []string{"'", `"`} {
		i := strings.Index(src, q+key+q)
		if i < 0 {
			continue
		}
		rest := strings.TrimLeft(src[i+len(key)+2:], " \t\r\n")
		rest, ok := strings.CutPrefix(rest, "=>")
		if !ok {
			continue
		}
		rest = strings.TrimLeft(rest, " \t\r\n")
		if rest == "" || rest[0] != '\'' && rest[0] != '"' {
			continue
		}
		if end := strings.IndexByte(rest[1:], rest[0]); end >= 0 {
			return rest[1 : end+1]
		}
	}
	return ""
}

// productionHosts turns production: entries (URLs, hosts, *.globs) into
// lower-case hosts and globs.
func productionHosts(entries []string) []string {
	var out []string
	for _, e := range entries {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if h := hostOf(e); h != "" {
			out = append(out, h)
			if !strings.HasPrefix(h, "*.") && !strings.HasPrefix(h, "www.") {
				out = append(out, "www."+h)
			}
		}
	}
	return out
}

// hostOf returns the lower-case host of a URL, host:port or bare host.
func hostOf(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return ""
	}
	if strings.Contains(v, "://") || strings.HasPrefix(v, "//") {
		u, err := url.Parse(v)
		if err != nil {
			return ""
		}
		return strings.TrimSuffix(u.Hostname(), ".")
	}
	// DB_HOST may be host:port or host:/path/to/socket.
	if h, _, err := net.SplitHostPort(v); err == nil {
		return h
	}
	if i := strings.IndexAny(v, ":/"); i >= 0 && !strings.Contains(v[:i], "[") {
		v = v[:i]
	}
	return strings.Trim(strings.TrimSuffix(v, "."), "[]")
}

// matchHost reports whether host is one of hosts or under a *. glob.
func matchHost(hosts []string, host string) bool {
	if host == "" {
		return false
	}
	for _, h := range hosts {
		if s, ok := strings.CutPrefix(h, "*."); ok {
			if strings.HasSuffix(host, "."+s) {
				return true
			}
			continue
		}
		if h == host {
			return true
		}
	}
	return false
}

// codeRoots are the folders of code files verify reads for keys
// (PRD 12.1), with the depth and size limits.
var codeRoots = []string{"wp-content/themes", "wp-content/plugins"}

const (
	codeDepth   = 6
	codeMaxSize = 2 << 20
)

// codeFiles reads the theme and plugin PHP files of a local site for live
// keys, reported with action manual. Over ssh or an alias the remote
// allowlist has no cat for them, so they are listed as skipped.
func (r *run) codeFiles(ctx context.Context, s core.Site) {
	switch s.Endpoint().Kind {
	case core.LocalSite:
	case core.SSHSite, core.WPAlias:
		r.res.Coverage.Skipped = append(r.res.Coverage.Skipped,
			core.Skipped{Table: "code files", Reason: "not read over ssh (PRD 6.13 allowlist)"})
		return
	default:
		return
	}
	for _, root := range codeRoots {
		list, err := s.List(ctx, root, codeDepth)
		if err != nil {
			continue
		}
		for _, f := range list {
			if f.Dir || f.Size <= 0 || f.Size > codeMaxSize || !strings.HasSuffix(strings.ToLower(f.Rel), ".php") {
				continue
			}
			b, err := s.ReadFile(ctx, f.Rel)
			if err != nil {
				continue
			}
			r.count(CheckKeyLive, 1)
			r.codeFile(f.Rel, b)
		}
	}
}

// codeFile reports each live provider key in one code file.
func (r *run) codeFile(rel string, b []byte) {
	line := 0
	for len(b) > 0 {
		line++
		l := b
		if i := bytes.IndexByte(b[:min(len(b), 1<<16)], '\n'); i >= 0 {
			l, b = b[:i], b[i+1:]
		} else if len(b) > 1<<16 {
			l, b = b[:1<<16], b[1<<16:]
		} else {
			b = nil
		}
		for _, h := range keys.ScanValue(l, core.Location{File: rel, Line: line}, nil) {
			if h.Env != keys.EnvLive || h.Provider == keys.ProviderGeneric {
				continue
			}
			h.Action = "manual"
			if r.add(core.Finding{ID: h.ID, Severity: h.Severity, Title: keyTitle(h), Fix: keyManualMessage(h),
				Locations: []core.LocationCount{{Location: h.At}}, Examples: examples(h.Masked)}) {
				r.addKey(h)
			}
		}
	}
}

// uploadRule is one PRD 15.1 place where personal files collect.
type uploadRule struct {
	dir      string // a folder whose files count
	file     string // or one file
	severity core.Severity
	what     string
}

var uploadRules = []uploadRule{
	{file: "wp-content/debug.log", severity: core.SeverityHigh, what: "a debug log"},
	{dir: "wp-content/uploads/wc-logs", severity: core.SeverityHigh, what: "WooCommerce logs"},
	{dir: "wp-content/uploads/gravity_forms", severity: core.SeverityMedium, what: "form uploads"},
	{dir: "wp-content/updraft", severity: core.SeverityCritical, what: "UpdraftPlus backups"},
	{dir: "wp-content/ai1wm-backups", severity: core.SeverityCritical, what: "All-in-One WP Migration backups"},
}

// uploads lists the PRD 15.1 paths by name and size, never reading them.
func (r *run) uploads(ctx context.Context, s core.Site) {
	add := func(rel, what string, sev core.Severity, n int64) {
		r.add(core.Finding{ID: core.FindingUploadsPersonal, Severity: sev, Count: n,
			Title: "The staging copy holds " + what, Fix: fixes[core.FindingUploadsPersonal],
			Locations: []core.LocationCount{{Location: core.Location{File: rel}, Count: n}}})
	}
	for _, u := range uploadRules {
		if u.file != "" {
			list, err := s.List(ctx, path.Dir(u.file), 1)
			if err != nil {
				continue
			}
			r.count(core.FindingUploadsPersonal, 1)
			for _, f := range list {
				if f.Rel == u.file && !f.Dir && f.Size > 0 {
					add(u.file, u.what, u.severity, 1)
				}
			}
			continue
		}
		list, err := s.List(ctx, u.dir, 0)
		if err != nil {
			continue
		}
		r.count(core.FindingUploadsPersonal, 1)
		var n int64
		for _, f := range list {
			if !f.Dir && f.Size > 0 && path.Base(f.Rel) != "index.php" && path.Base(f.Rel) != ".htaccess" && path.Base(f.Rel) != "index.html" {
				n++
			}
		}
		if n > 0 {
			add(u.dir, u.what, u.severity, n)
		}
	}
	for _, dir := range []string{"", "wp-content"} {
		list, err := s.List(ctx, dir, 1)
		if err != nil {
			continue
		}
		r.count(core.FindingUploadsPersonal, 1)
		for _, f := range list {
			name := strings.ToLower(path.Base(f.Rel))
			dump := strings.HasSuffix(name, ".sql") || strings.HasSuffix(name, ".sql.gz") || strings.HasSuffix(name, ".zip")
			if f.Dir || f.Size <= 0 || !dump {
				continue
			}
			add(f.Rel, "a database dump or archive", core.SeverityCritical, 1)
		}
	}
}

// steps runs each Step's check and records its side effect and findings.
func (r *run) steps(ctx context.Context, s core.Site) {
	for _, st := range r.j.Steps {
		if why := r.skipReason(st.ID()); why != "" {
			r.skipCheck(st.ID(), why)
			continue
		}
		r.count(st.ID(), 1)
		se, found, err := st.Check(ctx, s)
		if err != nil {
			se = core.SideEffect{ID: st.ID(), Status: "unknown", How: "the check failed: " + err.Error()}
		}
		if se.ID == "" {
			se.ID = st.ID()
		}
		r.res.SideEffects = append(r.res.SideEffects, se)
		for _, f := range found {
			r.add(f)
		}
	}
}
