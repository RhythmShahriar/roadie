// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package urlrw

import (
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/RhythmShahriar/roadie/core"
)

// site is one parsed URL or host.
type site struct {
	scheme string // "http", "https" or "" for a bare host
	host   string // lower-case ASCII, punycode for an IDN
	port   string
	path   string // without a trailing slash; "" for the root
	// uni is the Unicode spelling of an IDN host (NFC, lower case), which
	// text may hold as it is; "" for an ASCII host.
	uni string
}

func (s site) authority() string {
	if s.port == "" {
		return s.host
	}
	return s.host + ":" + s.port
}

// errURL is the config error for a bad entry. User info may hold a
// password (https://user:pass@staging.shop.com), so it is masked and
// never quoted back.
func errURL(what, entry string) error {
	masked, user := maskUserinfo(entry)
	if user {
		return core.Errorf(core.ErrConfig, 2, "url map: %q has user info, which isn't allowed; write %s like shop.com or https://shop.com/path", masked, what)
	}
	return core.Errorf(core.ErrConfig, 2, "url map: %q isn't %s like shop.com or https://shop.com/path", masked, what)
}

// maskUserinfo replaces everything between the scheme and the last @ of
// entry with *** and reports whether there was an @. It looks past the
// authority on purpose: a password may hold / ? or # and an entry with
// an @ is refused anyway.
func maskUserinfo(entry string) (string, bool) {
	start := 0
	if i := strings.Index(entry, "://"); i >= 0 {
		start = i + 3
	}
	at := strings.LastIndexByte(entry[start:], '@')
	if at < 0 {
		return entry, false
	}
	return entry[:start] + "***" + entry[start+at:], true
}

// parseSite reads a URL with an http or https scheme, or a bare host with
// an optional port and path. Queries, fragments and user info are refused.
func parseSite(entry string) (site, error) {
	raw := strings.TrimSpace(entry)
	bad := errURL("a URL or a host", entry)
	if raw == "" {
		return site{}, bad
	}
	withScheme := strings.Contains(raw, "://")
	if !withScheme {
		raw = "//" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return site{}, bad
	}
	s := site{scheme: strings.ToLower(u.Scheme), port: u.Port(), path: strings.TrimRight(u.EscapedPath(), "/")}
	if withScheme && s.scheme != "http" && s.scheme != "https" {
		return site{}, bad
	}
	if s.port != "" {
		if n, err := strconv.Atoi(s.port); err != nil || n < 1 || n > 65535 {
			return site{}, bad
		}
	}
	host := strings.TrimSuffix(u.Hostname(), ".")
	if strings.Contains(u.Host, "[") {
		host = "[" + strings.ToLower(host) + "]"
		if strings.Trim(host[1:len(host)-1], "0123456789abcdef:.") != "" {
			return site{}, bad
		}
		s.host = host
		return s, nil
	}
	ascii, uni, ok := asciiHost(host)
	if !ok {
		return site{}, bad
	}
	s.host, s.uni = ascii, uni
	return s, nil
}

// asciiHost lower-cases a host and turns each label with non-ASCII
// letters into punycode, so bücher.de and xn--bcher-kva.de are one host.
// It also returns the Unicode spelling when there was one.
func asciiHost(h string) (ascii, uni string, ok bool) {
	if h == "" || len(h) > 253 {
		return "", "", false
	}
	h = strings.ToLower(norm.NFC.String(h))
	labels := strings.Split(h, ".")
	unicode := false
	for i, l := range labels {
		if l == "" || len(l) > 63 {
			return "", "", false
		}
		if !isASCII(l) {
			unicode = true
			p, ok := punycode(l)
			if !ok {
				return "", "", false
			}
			labels[i] = "xn--" + p
			continue
		}
		for j := 0; j < len(l); j++ {
			if !hostByte(l[j]) || l[j] == '.' {
				return "", "", false
			}
		}
	}
	ascii = strings.Join(labels, ".")
	if unicode {
		uni = h
	}
	return ascii, uni, true
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// hostByte is a byte that can go on a host: [A-Za-z0-9-] and the dot
// between labels.
func hostByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '.'
}

// punycode encodes one label as RFC 3492 says, without the xn-- prefix.
func punycode(label string) (string, bool) {
	const (
		base, tmin, tmax, skew, damp = 36, 1, 26, 38, 700
		initialBias, initialN        = 72, 128
	)
	runes := []rune(label)
	var out []byte
	for _, r := range runes {
		if r < 0x80 {
			if !hostByte(byte(r)) {
				return "", false
			}
			out = append(out, byte(r))
		}
	}
	b := len(out)
	h := b
	if b > 0 {
		out = append(out, '-')
	}
	digit := func(d int) byte { return "abcdefghijklmnopqrstuvwxyz0123456789"[d] }
	adapt := func(delta, points int, first bool) int {
		if first {
			delta /= damp
		} else {
			delta /= 2
		}
		delta += delta / points
		k := 0
		for delta > ((base-tmin)*tmax)/2 {
			delta /= base - tmin
			k += base
		}
		return k + (base-tmin+1)*delta/(delta+skew)
	}
	n, delta, bias := initialN, 0, initialBias
	for h < len(runes) {
		m := int(utf8.MaxRune) + 1
		for _, r := range runes {
			if int(r) >= n && int(r) < m {
				m = int(r)
			}
		}
		delta += (m - n) * (h + 1)
		n = m
		for _, r := range runes {
			if int(r) < n {
				delta++
			}
			if int(r) != n {
				continue
			}
			q := delta
			for k := base; ; k += base {
				t := k - bias
				if t < tmin {
					t = tmin
				} else if t > tmax {
					t = tmax
				}
				if q < t {
					break
				}
				out = append(out, digit(t+(q-t)%(base-t)))
				q = (q - t) / (base - t)
			}
			out = append(out, digit(q))
			bias = adapt(delta, h+1, h == b)
			delta = 0
			h++
		}
		delta++
		n++
	}
	return string(out), true
}

// hosts is Hosts.
func hosts(urls []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	add := func(h string) {
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	for _, e := range urls {
		raw := strings.TrimSpace(e)
		glob := false
		if rest, ok := strings.CutPrefix(raw, "*."); ok {
			glob, raw = true, rest
		} else if i := strings.Index(raw, "://*."); i >= 0 {
			glob, raw = true, raw[:i+3]+raw[i+5:]
		}
		s, err := parseSite(raw)
		if err != nil || strings.Contains(s.host, "*") {
			return nil, errURL("a URL, a host or a *.host glob", e)
		}
		if glob {
			add("*." + s.host)
			continue
		}
		add(s.host)
		if w, ok := strings.CutPrefix(s.host, "www."); ok {
			add(w)
		} else if !strings.HasPrefix(s.host, "[") {
			add("www." + s.host)
		}
	}
	return out, nil
}

// matchHost is MatchHost.
func matchHost(hosts []string, host string) bool {
	host = strings.TrimSuffix(strings.TrimSpace(host), ".")
	host, _ = splitPort(host)
	ascii, _, ok := asciiHost(host)
	if !ok {
		ascii = strings.ToLower(host)
	}
	for _, h := range hosts {
		if rest, glob := strings.CutPrefix(h, "*."); glob {
			if strings.HasSuffix(ascii, "."+rest) {
				return true
			}
		} else if ascii == h {
			return true
		}
	}
	return false
}

// splitPort splits "host:port"; a host without a port comes back whole.
func splitPort(hp string) (host, port string) {
	if strings.HasPrefix(hp, "[") {
		if i := strings.Index(hp, "]"); i > 0 {
			return hp[:i+1], strings.TrimPrefix(hp[i+1:], ":")
		}
		return hp, ""
	}
	if i := strings.LastIndexByte(hp, ':'); i >= 0 && strings.Count(hp, ":") == 1 {
		return hp[:i], hp[i+1:]
	}
	return hp, ""
}
