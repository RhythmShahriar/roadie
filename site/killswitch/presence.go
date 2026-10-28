// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package killswitch

import (
	"context"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
)

// Presence is what the source holds, as pass 1 read it. plan sets it in
// Options.Source so a step plans no edit to an option or a table the
// source lacks and reports a side effect the source doesn't have as
// not-present, as the copy's read-back does. nil means unknown: every
// step plans all it could change.
type Presence struct {
	Options map[string]bool  // option names, every blog's
	Rows    map[string]int64 // every table the source holds, by its full name, with its rows
	Prefix  string           // the WordPress table prefix
}

// option reports whether the source holds option name; true when the
// presence is unknown.
func (p *Presence) option(name string) bool {
	return p == nil || p.Options[name]
}

// table returns the rows of the main site's prefix+base table and
// whether the source holds it; (0, true) when the presence is unknown.
func (p *Presence) table(base string) (int64, bool) {
	if p == nil {
		return 0, true
	}
	if n, ok := p.Rows[p.Prefix+base]; ok {
		return n, true
	}
	if p.Prefix == "" {
		for name, n := range p.Rows {
			if strings.HasSuffix(name, "_"+base) {
				return n, true
			}
		}
	}
	return 0, false
}

// Absent reports a side-effect step whose side effect the source
// doesn't hold, with the read-back's words for it. It is false for a
// step that can't tell. With a nil Options.Source it is always false.
func Absent(ctx context.Context, st core.Step, s core.Site) (how string, ok bool) {
	a, can := st.(interface {
		absent(ctx context.Context, s core.Site) (string, bool)
	})
	if !can {
		return "", false
	}
	return a.absent(ctx, s)
}

// absent is the step's own presence test; nil can't tell.
func (st *step) absent(ctx context.Context, s core.Site) (string, bool) {
	if st.missing == nil {
		return "", false
	}
	return st.missing(ctx, s)
}

// pluginOnDisk reports a plugin folder on the target, when there is one.
func pluginOnDisk(ctx context.Context, s core.Site, dir string) bool {
	if s == nil {
		return false
	}
	ok, _ := s.Exists(ctx, "wp-content/plugins/"+dir)
	return ok
}
