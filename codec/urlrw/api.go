// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package urlrw rewrites production URLs and hosts to staging ones in
// every form a site stores them: plain, scheme-relative, JSON-escaped,
// URL-encoded and HTML-entity encoded.
//
// The matcher is plain byte code with no regular expressions. It anchors
// on the host and reads the bytes before it backwards: a host counts only
// right after two slashes in some spelling ("//", "\/\/", "%2F%2F",
// "&#x2F;&#x2F;" or a mix), so an email domain or a longer host ending in
// the same letters never matches. Callers apply Rewrite to the decoded
// leaves of codec/nested, which fixes serialized lengths through
// codec/phpser.
//
// The exported names in this file are the frozen cross-lane API.
package urlrw

import "github.com/RhythmShahriar/roadie/core"

// Pair maps one production URL to its staging URL.
type Pair struct {
	From string
	To   string
}

// Map holds the compiled URL pairs. A nil *Map changes nothing.
type Map struct {
	rules  []rule        // longest From first
	groups []anchorGroup // the rules by the byte the text is searched for
}

// NewMap compiles pairs.
//
// From and To are each a URL (http or https, with an optional path) or a
// bare host, with an optional port. From's scheme doesn't matter: both
// schemes of its host take To's scheme, or keep their own when To is a
// bare host. The staging host must differ from the production host. Each
// From host also covers its www. variant (www.shop.com for shop.com and
// the other way round) unless a pair names that variant itself. An IDN
// matches in its punycode and its Unicode spelling. Longer From URLs win
// where two match. A bad pair is an error with the code config.
func NewMap(pairs []Pair) (*Map, error) {
	return newMap(pairs)
}

// Rewrite replaces every form of every From URL in text and returns the count.
//
// The forms are https://host and http://host (both take To's scheme),
// //host, the JSON-escaped https:\/\/host and \/\/host, the URL-encoded
// https%3A%2F%2Fhost in any hex case and HTML entities such as
// https:&#x2F;&#x2F;host, each followed by the From path when it has one.
// The slashes keep their spelling; To's path is written in it. Hosts match
// in any ASCII case. A host must end at a boundary: the next byte isn't
// [A-Za-z0-9-] nor a '.' followed by [A-Za-z0-9], so shop.com.au and
// shop.community stay. A path must end at one too: /blog doesn't match in
// /blogger. A port in the text stays unless To names its own. Bare hosts
// in text stay: RewriteHost is for host columns. Text with no match comes
// back as it is.
func (m *Map) Rewrite(text []byte) ([]byte, int) {
	if m == nil {
		return text, 0
	}
	return m.rewrite(text)
}

// RewriteHost rewrites a bare host column.
//
// The whole value must be a From host (or its www. variant) in any ASCII
// case, with an optional port; it becomes To's host and port.
func (m *Map) RewriteHost(host []byte) ([]byte, bool) {
	if m == nil {
		return host, false
	}
	return m.rewriteHost(host)
}

// Find returns the spans of production URLs in text, Kind "url".
//
// It uses Rewrite's matcher: each span runs from the URL's scheme (or its
// slashes) to the end of its host, port and From path. Text Rewrite has
// been through has none.
func (m *Map) Find(text []byte) []core.Span {
	if m == nil {
		return nil
	}
	return m.find(text)
}

// Hosts returns the production hosts named by URLs and production: entries.
//
// Each entry is a URL, a host or a glob: "*.shop.com" stands for every
// subdomain of shop.com but not shop.com itself. Hosts come back lower
// case in their ASCII form, without ports or paths, with the www.
// variant of each plain host added and no duplicates. An entry that is
// none of those is an error with the code config.
func Hosts(urls []string) ([]string, error) {
	return hosts(urls)
}

// MatchHost reports whether host is one of the hosts Hosts returned: the
// same host or, for a "*.shop.com" entry, any subdomain of shop.com. The
// host may carry a port and may be an IDN in Unicode.
func MatchHost(hosts []string, host string) bool {
	return matchHost(hosts, host)
}
