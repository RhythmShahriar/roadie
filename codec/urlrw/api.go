// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package urlrw rewrites production URLs and hosts to staging ones in
// every form a site stores them: plain, scheme-relative, JSON-escaped,
// URL-encoded and HTML-entity encoded.
//
// The exported names in this file are the frozen cross-lane API; the
// values lane replaces the bodies, which return core.ErrNotBuilt until then.
package urlrw

import "github.com/RhythmShahriar/roadie/core"

// Pair maps one production URL to its staging URL.
type Pair struct {
	From string
	To   string
}

// Map holds the compiled URL pairs.
type Map struct{} // its fields are unexported and added by the owning lane

// NewMap compiles pairs.
func NewMap(pairs []Pair) (*Map, error) {
	return nil, core.ErrNotBuilt
}

// Rewrite replaces every form of every From URL in text and returns the count.
func (m *Map) Rewrite(text []byte) ([]byte, int) {
	return nil, 0
}

// RewriteHost rewrites a bare host column.
func (m *Map) RewriteHost(host []byte) ([]byte, bool) {
	return nil, false
}

// Find returns the spans of production URLs in text, Kind "url".
func (m *Map) Find(text []byte) []core.Span {
	return nil
}

// Hosts returns the production hosts named by URLs and production: entries.
func Hosts(urls []string) ([]string, error) {
	return nil, core.ErrNotBuilt
}
