// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"bytes"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/RhythmShahriar/roadie/codec/urlrw"
	"github.com/RhythmShahriar/roadie/core"
)

// blogDomain is one row of a multisite's blogs or site table.
type blogDomain struct {
	Domain, Path string
}

// urlSources are the production URLs pass 1 found and the settings that
// name more (PRD 14).
type urlSources struct {
	// SiteURLs are the main site's siteurl and home, or APP_URL.
	SiteURLs []string
	// Blogs are the multisite blogs (wp_blogs) and networks (wp_site).
	Blogs []blogDomain
	// Production holds the production: entries: hosts, URLs or *.host
	// globs.
	Production []string
}

// urlPlan is what the rewrite does: the pairs for urlrw.NewMap, the
// production hosts for the guards and the report plus the hosts url_map
// says to leave alone.
type urlPlan struct {
	Pairs      []urlrw.Pair
	Production []string
	Excluded   []string
	// StagingPath is the path of --url without its last slash ("" for
	// none): a staging site in a subfolder. Multisite path columns get
	// it in front.
	StagingPath string
}

// parseOrigin reads a URL or a bare host into scheme and host (with its
// port). A bare host gets https, as WordPress sites do today.
func parseOrigin(s string) (scheme, host string, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", false
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return "", "", false
	}
	return u.Scheme, strings.ToLower(u.Host), true
}

// bareHost drops the port of a host.
func bareHost(h string) string {
	if i := strings.LastIndexByte(h, ':'); i >= 0 && !strings.Contains(h[i:], "]") {
		return h[:i]
	}
	return h
}

// buildURLPlan turns the production URLs into rewrite pairs to the
// staging URL. Each production origin maps to the staging origin; a
// subdomain multisite's blog whose host ends in the main host maps by
// suffix (fr.shop.com to fr.staging.shop.test); any other blog host
// needs an url_map entry, or the run stops with url-map (exit 2) naming
// each one. An url_map entry with an empty value leaves its host alone.
// With no staging URL there is nothing to rewrite to: no pairs and no
// url-map error.
func buildURLPlan(src urlSources, staging string, urlMap map[string]string) (urlPlan, error) {
	var plan urlPlan
	prod := map[string]bool{}
	for _, p := range src.Production {
		p = strings.ToLower(strings.TrimSpace(p))
		if strings.HasPrefix(p, "*.") {
			prod[p] = true
			continue
		}
		if _, h, ok := parseOrigin(p); ok {
			prod[bareHost(h)] = true
		}
	}
	mainHost := ""
	var origins [][2]string // scheme, host
	addOrigin := func(raw string) {
		if s, h, ok := parseOrigin(raw); ok && !slices.Contains(origins, [2]string{s, h}) {
			origins = append(origins, [2]string{s, h})
			prod[bareHost(h)] = true
			if mainHost == "" {
				mainHost = bareHost(h)
			}
		}
	}
	for _, u := range src.SiteURLs {
		addOrigin(u)
	}
	for _, p := range src.Production {
		if !strings.HasPrefix(strings.TrimSpace(p), "*.") {
			addOrigin(p)
		}
	}
	excluded := map[string]bool{}
	for k, v := range urlMap {
		if strings.TrimSpace(v) == "" {
			if _, h, ok := parseOrigin(k); ok {
				excluded[bareHost(h)] = true
			}
		}
	}
	for _, b := range src.Blogs {
		if h := bareHost(strings.ToLower(strings.TrimSpace(b.Domain))); h != "" {
			prod[h] = true
		}
	}
	for h := range excluded {
		plan.Excluded = append(plan.Excluded, h)
	}
	slices.Sort(plan.Excluded)
	for h := range prod {
		plan.Production = append(plan.Production, h)
	}
	// Sorted, with the main site's host (siteurl or APP_URL) first: the
	// plan's URL line names it as the copy's "from".
	slices.SortFunc(plan.Production, func(a, b string) int {
		switch {
		case a == mainHost:
			return -1
		case b == mainHost:
			return 1
		}
		return strings.Compare(a, b)
	})
	if strings.TrimSpace(staging) == "" {
		return plan, nil
	}
	tScheme, tHost, ok := parseOrigin(staging)
	su, err := url.Parse(strings.TrimSpace(staging))
	if !ok || err != nil || !strings.Contains(staging, "://") || su.RawQuery != "" || su.Fragment != "" {
		return plan, core.Errorf(core.ErrConfig, 2, "url %q isn't a staging URL like https://staging.shop.test", staging)
	}
	plan.StagingPath = strings.TrimRight(su.EscapedPath(), "/")
	to := tScheme + "://" + tHost + plan.StagingPath
	seen := map[string]bool{}
	add := func(from, to string) {
		key := strings.ToLower(from)
		if !seen[key] {
			seen[key] = true
			plan.Pairs = append(plan.Pairs, urlrw.Pair{From: from, To: to})
		}
	}
	mapped := map[string]bool{}
	keys := make([]string, 0, len(urlMap))
	for k := range urlMap {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if v := strings.TrimSpace(urlMap[k]); v != "" {
			add(strings.TrimSpace(k), v)
			if _, h, ok := parseOrigin(k); ok {
				mapped[bareHost(h)] = true
			}
		}
	}
	for _, o := range origins {
		if !excluded[bareHost(o[1])] {
			add(o[0]+"://"+o[1], to)
		}
	}
	var missing []string
	for _, b := range src.Blogs {
		d := strings.ToLower(strings.TrimSpace(b.Domain))
		h := bareHost(d)
		switch {
		case h == "" || h == mainHost || h == "www."+mainHost || mapped[h] || excluded[h]:
		case mainHost != "" && strings.HasSuffix(h, "."+mainHost):
			sub := strings.TrimSuffix(h, "."+mainHost)
			add("https://"+d, tScheme+"://"+sub+"."+tHost+plan.StagingPath)
		default:
			if !slices.Contains(missing, h) {
				missing = append(missing, h)
			}
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return plan, urlMapError(missing, bareHost(tHost))
	}
	return plan, nil
}

// urlMapError is the PRD 17 url-map stop for blog hosts that have no
// staging URL, with a suggested pair for each.
func urlMapError(hosts []string, stagingHost string) *core.Error {
	pairs := make([]string, len(hosts))
	for i, h := range hosts {
		label, _, _ := strings.Cut(h, ".")
		pairs[i] = h + "=" + label + "." + stagingHost
	}
	verb := "has"
	if len(hosts) > 1 {
		verb = "have"
	}
	return &core.Error{Code: core.ErrURLMap, Exit: 2, State: core.Untouched,
		Msg: fmt.Sprintf("url-map: %s %s no staging URL; add --url-map %s.", andList(hosts), verb, strings.Join(pairs, ","))}
}

// isBareHost reports whether a value is a host and nothing else
// (shop.com, fr.shop.com:8443): what a host column holds, as opposed to
// a URL or text that names one.
func isBareHost(b []byte) bool {
	if len(b) == 0 || len(b) > 253 || !bytes.ContainsRune(b, '.') {
		return false
	}
	for _, c := range b {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-', c == ':':
		default:
			return false
		}
	}
	return b[0] != '.' && b[0] != '-'
}

// hostOnly cuts a rewritten host column back to its host: a staging URL
// in a subfolder must not put its path into blogs.domain.
func hostOnly(b []byte) []byte {
	if i := bytes.Index(b, []byte("://")); i >= 0 {
		b = b[i+3:]
	}
	if i := bytes.IndexByte(b, '/'); i >= 0 {
		b = b[:i]
	}
	return b
}

// stagingBlogPath puts the staging subfolder in front of a multisite
// path column ("/fr/" becomes "/staging/fr/"), once.
func stagingBlogPath(stagingPath string, p []byte) ([]byte, bool) {
	if stagingPath == "" || len(p) == 0 || p[0] != '/' {
		return p, false
	}
	if string(p) == stagingPath || bytes.HasPrefix(p, []byte(stagingPath+"/")) {
		return p, false
	}
	return append([]byte(stagingPath), p...), true
}

// andList joins names as "a", "a and b" or "a, b and c".
func andList(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// dotenvValue reads one NAME=value line of a .env file, without quotes;
// enough for APP_URL, which the site lane's full parser would give too.
func dotenvValue(b []byte, name string) (string, bool) {
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) != name {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		} else if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		return v, true
	}
	return "", false
}
