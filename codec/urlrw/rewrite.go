// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package urlrw

import (
	"bytes"
	"slices"
	"sort"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
)

// rule is one From host spelling and what it becomes.
type rule struct {
	host []byte // lower case: the ASCII host or the Unicode spelling of an IDN
	from site
	to   site
}

// anchorGroup is the rules whose host has the same anchor byte, searched
// for with one bytes.IndexByte pass.
type anchorGroup struct {
	anchor byte
	rules  []anchoredRule
}

type anchoredRule struct {
	rule   *rule
	offset int // where the anchor byte is in the host
}

// match is one URL found in text.
type match struct {
	start, end int // the bytes replaced: from the scheme, or the host, to the end of the path
	span       int // where the URL starts: its scheme, or its slashes when it has none
	repl       []byte
}

// anchorOf picks the byte Rewrite searches the text for: the first byte
// of the host that isn't a letter (nearly always a dot, which plain text
// has far fewer of than any letter), or the first byte when the host is
// all letters. Letters would need two searches, one per case.
func anchorOf(host []byte) (byte, int) {
	for i, c := range host {
		if c < 'a' || c > 'z' {
			return c, i
		}
	}
	return host[0], 0
}

func lower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}

// equalFold compares text with a lower-case host, ASCII letters folded.
func equalFold(text, host []byte) bool {
	if len(text) != len(host) {
		return false
	}
	for i, c := range host {
		if lower(text[i]) != c {
			return false
		}
	}
	return true
}

// slashBefore reports the length of the form of '/' that ends at i: "/"
// with any backslashes before it (JSON escaping it once or more), "%2F" in
// either case, or an HTML entity such as "&#47;", "&#x2F;" or "&sol;".
func slashBefore(text []byte, i int) int {
	return sepBefore(text, i, '/', "%2f", "&sol;")
}

// colonBefore is slashBefore for ':'.
func colonBefore(text []byte, i int) int {
	return sepBefore(text, i, ':', "%3a", "&colon;")
}

func sepBefore(text []byte, i int, c byte, pct, named string) int {
	if i <= 0 {
		return 0
	}
	if text[i-1] == c {
		n := 1
		for i-n > 0 && text[i-n-1] == '\\' {
			n++
		}
		return n
	}
	if i >= 3 && equalFold(text[i-3:i], []byte(pct)) {
		return 3 + escapesBefore(text, i-3)
	}
	if text[i-1] != ';' {
		return 0
	}
	if i >= len(named) && string(text[i-len(named):i]) == named { // named entities are case-sensitive
		return len(named)
	}
	// a numeric entity: &#47; &#x2F; with any leading zeros
	j := i - 1
	start := j
	for j > 0 && isHex(text[j-1]) {
		j--
	}
	digits := text[j:start]
	if len(digits) == 0 || j < 2 {
		return 0
	}
	v, hex := 0, false
	if lower(text[j-1]) == 'x' && j >= 3 && text[j-2] == '#' && text[j-3] == '&' {
		hex = true
	} else if text[j-1] != '#' || text[j-2] != '&' {
		return 0
	}
	for _, d := range digits {
		if hex {
			v = v*16 + hexVal(d)
		} else {
			if d < '0' || d > '9' {
				return 0
			}
			v = v*10 + int(d-'0')
		}
		if v > 0x10ffff {
			return 0
		}
	}
	if v != int(c) {
		return 0
	}
	if hex {
		return i - (j - 3)
	}
	return i - (j - 2)
}

// escapesBefore is the length of the JSON escapes that end at i before a
// URL-encoded separator. A JSON string URL-encoded whole writes \/ as
// %5C%2F; text decoded once can hold \%2F. Either spelling counts, any
// number of times.
func escapesBefore(text []byte, i int) int {
	n := 0
	for {
		switch {
		case i-n >= 3 && equalFold(text[i-n-3:i-n], []byte("%5c")):
			n += 3
		case i-n >= 1 && text[i-n-1] == '\\':
			n++
		default:
			return n
		}
	}
}

// slashAt is the forward form of slashBefore, for the slashes of a path.
func slashAt(text []byte, i int) int {
	if i >= len(text) {
		return 0
	}
	j := i
	for j < len(text) && text[j] == '\\' {
		j++
	}
	if j < len(text) && text[j] == '/' {
		return j + 1 - i
	}
	for j+3 <= len(text) && equalFold(text[j:j+3], []byte("%5c")) {
		j += 3
	}
	for j < len(text) && text[j] == '\\' {
		j++
	}
	if j+3 <= len(text) && equalFold(text[j:j+3], []byte("%2f")) {
		return j + 3 - i
	}
	if j != i {
		return 0
	}
	if text[i] != '&' {
		return 0
	}
	if end := bytes.IndexByte(text[i:min(len(text), i+12)], ';'); end > 0 {
		if n := slashBefore(text, i+end+1); n == end+1 {
			return n
		}
	}
	return 0
}

// colonAt is the forward form of colonBefore, for the colon before a
// port: ":", "%3A" in either case or an HTML entity such as "&#58;" or
// "&colon;". JSON never escapes a colon, so backslashes don't count.
func colonAt(text []byte, i int) int {
	if i >= len(text) {
		return 0
	}
	switch text[i] {
	case ':':
		return 1
	case '%':
		if i+3 <= len(text) && equalFold(text[i:i+3], []byte("%3a")) {
			return 3
		}
	case '&':
		if end := bytes.IndexByte(text[i:min(len(text), i+12)], ';'); end > 0 {
			if n := colonBefore(text, i+end+1); n == end+1 {
				return n
			}
		}
	}
	return 0
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	}
	return int(c-'A') + 10
}

// hostEnds reports whether a host may end before text[i]: the next byte
// isn't a host byte, nor a dot that starts another label, so shop.com
// doesn't match in shop.com.au or shop.community. A dot spelled %2E or
// as an HTML entity counts too, so shop.com%2Eau and shop.com&#46;au
// stay as they are.
func hostEnds(text []byte, i int) bool {
	if i >= len(text) {
		return true
	}
	n := dotAt(text, i)
	if n > 0 {
		return i+n >= len(text) || !alnum(text[i+n])
	}
	return !hostByte(text[i])
}

// dotSpellings are the encoded dots dotAt knows, matched without case.
var dotSpellings = [][]byte{[]byte("%2e"), []byte("&#46;"), []byte("&#x2e;"), []byte("&period;")}

// dotAt returns the length of the dot at text[i] in any spelling, or 0.
func dotAt(text []byte, i int) int {
	if text[i] == '.' {
		return 1
	}
	if text[i] != '%' && text[i] != '&' {
		return 0
	}
	for _, d := range dotSpellings {
		if i+len(d) <= len(text) && bytes.EqualFold(text[i:i+len(d)], d) {
			return len(d)
		}
	}
	return 0
}

// userinfo reports whether the host that ends at i is the user part of
// another URL's authority, as in https://shop.com@other.example: an '@'
// (or %40) before any byte that ends an authority, a '/', '?' or '#' in
// any spelling among them. That URL points at the other host, so it
// stays.
func userinfo(text []byte, i int) bool {
	for ; i < len(text); i++ {
		c := text[i]
		switch {
		case c == '@':
			return true
		case c == '%':
			if i+2 >= len(text) {
				return false
			}
			a, b := text[i+1], text[i+2]|0x20 // b lower-cased if a letter
			switch {
			case a == '4' && b == '0':
				return true
			case a == '2' && (b == 'f' || b == '3'), a == '3' && b == 'f':
				return false
			}
		case alnum(c), c == '-', c == '.', c == '_', c == '~', c == ':', c == '!', c == '$', c == '*', c == '+', c == '=':
		default:
			return false
		}
	}
	return false
}

func alnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// pathByte is an unreserved byte that continues a path segment, so
// /blog doesn't match in /blogger.
func pathByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~'
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// try matches r at a host found at h and returns what to replace.
func (r *rule) try(text []byte, h int) (match, bool) {
	// Two slashes, in any spelling, right before the host: a URL's
	// authority. A host after anything else (jane@shop.com, myshop.com,
	// plain prose) isn't a URL and stays.
	s2 := slashBefore(text, h)
	if s2 == 0 {
		return match{}, false
	}
	s1 := slashBefore(text, h-s2)
	if s1 == 0 {
		return match{}, false
	}
	slashes := h - s2 - s1
	end := h + len(r.host)
	if !hostEnds(text, end) || userinfo(text, end) {
		return match{}, false
	}
	// The port: From's own must be there; otherwise one in the text goes
	// when To names a port and stays when it doesn't.
	keepPort := -1
	if c := colonAt(text, end); r.from.port != "" {
		p := end + c
		if c == 0 || !bytes.HasPrefix(text[p:], []byte(r.from.port)) || (p+len(r.from.port) < len(text) && isDigit(text[p+len(r.from.port)])) {
			return match{}, false
		}
		end = p + len(r.from.port)
	} else if c > 0 && end+c < len(text) && isDigit(text[end+c]) {
		j := end + c
		for j < len(text) && isDigit(text[j]) {
			j++
		}
		if r.to.port == "" {
			keepPort = end // kept as written, colon spelling and all
		}
		end = j
	}
	// The path prefix, segment by segment in any slash spelling.
	var style []byte
	if r.from.path != "" {
		for _, seg := range strings.Split(r.from.path[1:], "/") {
			n := slashAt(text, end)
			if n == 0 {
				return match{}, false
			}
			if style == nil {
				style = text[end : end+n]
			}
			end += n
			if !bytes.HasPrefix(text[end:], []byte(seg)) {
				return match{}, false
			}
			end += len(seg)
		}
		if end < len(text) && pathByte(text[end]) {
			return match{}, false
		}
	}
	if style == nil {
		style = text[slashes+s1 : h]
	}
	// The scheme before the slashes, when it is http or https and not
	// the tail of a longer word, takes To's scheme.
	start, span := h, slashes
	var repl []byte
	if sStart, sc := schemeBefore(text, slashes); sStart >= 0 {
		span = sStart
		if r.to.scheme != "" && sc != r.to.scheme {
			start = sStart
			repl = append(repl, r.to.scheme...)
			repl = append(repl, text[sStart+len(sc):h]...)
		}
	}
	repl = append(repl, r.to.authority()...)
	if keepPort >= 0 {
		repl = append(repl, text[keepPort:end]...)
	}
	if r.to.path != "" {
		for _, seg := range strings.Split(r.to.path[1:], "/") {
			repl = append(repl, style...)
			repl = append(repl, seg...)
		}
	}
	return match{start: start, end: end, span: span, repl: repl}, true
}

// schemeBefore finds "http" or "https" and a colon in any spelling ending
// at i, not preceded by another scheme byte. It returns where the scheme
// starts (or -1) plus which one it is.
func schemeBefore(text []byte, i int) (int, string) {
	c := colonBefore(text, i)
	if c == 0 {
		return -1, ""
	}
	end := i - c
	for _, sc := range []string{"https", "http"} {
		s := end - len(sc)
		if s >= 0 && equalFold(text[s:end], []byte(sc)) && (s == 0 || !schemeByte(text[s-1])) {
			return s, sc
		}
	}
	return -1, ""
}

func schemeByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'
}

// matches finds every URL of m in text, sorted and without overlaps.
func (m *Map) matches(text []byte) []match {
	var out []match
	for _, g := range m.groups {
		// An all-letter host such as localhost is searched for in either
		// case. Each case's next position is remembered and searched for
		// again only once i has passed it, so text with many hits of one
		// case is scanned once, not once per hit.
		letter := g.anchor >= 'a' && g.anchor <= 'z'
		nextLo, nextUp := -1, -1
		for i := 0; i < len(text); {
			if nextLo < i {
				nextLo = nextIndex(text, i, g.anchor)
			}
			p := nextLo
			if letter {
				if nextUp < i {
					nextUp = nextIndex(text, i, g.anchor-('a'-'A'))
				}
				p = min(p, nextUp)
			}
			if p >= len(text) {
				break
			}
			for _, ar := range g.rules {
				h := p - ar.offset
				if h < 0 || h+len(ar.rule.host) > len(text) || !equalFold(text[h:h+len(ar.rule.host)], ar.rule.host) {
					continue
				}
				if mt, ok := ar.rule.try(text, h); ok {
					out = append(out, mt)
					break
				}
			}
			i = p + 1
		}
	}
	if len(out) < 2 {
		return out
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].start != out[b].start {
			return out[a].start < out[b].start
		}
		return out[a].end > out[b].end
	})
	kept := out[:1]
	for _, mt := range out[1:] {
		if mt.start >= kept[len(kept)-1].end {
			kept = append(kept, mt)
		}
	}
	return kept
}

// nextIndex is the position of the first c in text at or after i, or
// len(text) when there is none.
func nextIndex(text []byte, i int, c byte) int {
	if j := bytes.IndexByte(text[i:], c); j >= 0 {
		return i + j
	}
	return len(text)
}

// rewrite is Rewrite.
func (m *Map) rewrite(text []byte) ([]byte, int) {
	ms := m.matches(text)
	if len(ms) == 0 {
		return text, 0
	}
	out := make([]byte, 0, len(text)+len(ms)*16)
	last := 0
	for _, mt := range ms {
		out = append(out, text[last:mt.start]...)
		out = append(out, mt.repl...)
		last = mt.end
	}
	return append(out, text[last:]...), len(ms)
}

// rewriteHost is RewriteHost.
func (m *Map) rewriteHost(host []byte) ([]byte, bool) {
	h, port := splitPort(string(host))
	for i := range m.rules {
		r := &m.rules[i]
		if r.from.path != "" || !equalFold([]byte(h), r.host) {
			continue
		}
		if r.from.port != "" && port != r.from.port {
			continue
		}
		out := r.to.authority()
		if r.to.port == "" && r.from.port == "" && port != "" {
			out += ":" + port
		}
		return []byte(out), true
	}
	return host, false
}

// find returns the spans of the URLs rewrite would replace.
func (m *Map) find(text []byte) []core.Span {
	ms := m.matches(text)
	spans := make([]core.Span, 0, len(ms))
	for _, mt := range ms {
		spans = append(spans, core.Span{Start: mt.span, End: mt.end, Kind: core.KURL})
	}
	return spans
}

// newMap is NewMap.
func newMap(pairs []Pair) (*Map, error) {
	m := &Map{}
	explicit := map[string]bool{}
	type parsed struct{ from, to site }
	var ps []parsed
	for _, p := range pairs {
		from, err := parseSite(p.From)
		if err != nil {
			return nil, err
		}
		to, err := parseSite(p.To)
		if err != nil {
			return nil, err
		}
		if from.host == to.host && (from.port == to.port || from.port == "" || to.port == "") {
			return nil, core.Errorf(core.ErrConfig, 2, "url map: %s maps to itself; the staging URL needs a host of its own", from.authority())
		}
		explicit[from.host+from.path] = true
		ps = append(ps, parsed{from, to})
	}
	add := func(from, to site) {
		m.rules = append(m.rules, rule{host: []byte(from.host), from: from, to: to})
		if from.uni != "" {
			m.rules = append(m.rules, rule{host: []byte(from.uni), from: from, to: to})
		}
	}
	for _, p := range ps {
		add(p.from, p.to)
		v := p.from
		if rest, ok := strings.CutPrefix(v.host, "www."); ok {
			v.host, v.uni = rest, strings.TrimPrefix(v.uni, "www.")
		} else if !strings.HasPrefix(v.host, "[") {
			v.host = "www." + v.host
			if v.uni != "" {
				v.uni = "www." + v.uni
			}
		} else {
			continue
		}
		if !explicit[v.host+v.path] && v.host != p.to.host {
			explicit[v.host+v.path] = true
			add(v, p.to)
		}
	}
	// Longest From first: a pair with a path, or a longer host, is tried
	// before a shorter one at the same place.
	sort.SliceStable(m.rules, func(a, b int) bool {
		la := len(m.rules[a].host) + len(m.rules[a].from.path) + len(m.rules[a].from.port)
		lb := len(m.rules[b].host) + len(m.rules[b].from.path) + len(m.rules[b].from.port)
		return la > lb
	})
	for i := range m.rules {
		r := &m.rules[i]
		anchor, off := anchorOf(r.host)
		gi := slices.IndexFunc(m.groups, func(g anchorGroup) bool { return g.anchor == anchor })
		if gi < 0 {
			m.groups = append(m.groups, anchorGroup{anchor: anchor})
			gi = len(m.groups) - 1
		}
		m.groups[gi].rules = append(m.groups[gi].rules, anchoredRule{rule: r, offset: off})
	}
	return m, nil
}
