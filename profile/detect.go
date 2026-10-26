// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package profile

import (
	"context"
	"path"
	"slices"

	"github.com/RhythmShahriar/roadie/core"
)

// DetectWithOptions is Detect with the option names pass 1 saw in the
// options table, for profiles that a plugin's option gives away before
// its tables exist. options may be nil.
func DetectWithOptions(ctx context.Context, s *core.Schema, st core.Site, defs []Def, options map[string]bool) []string {
	prefix, _ := WordPressPrefix(s)
	sites := Sites(s, prefix)
	var out []string
	for _, d := range defs {
		if detects(ctx, d.Detect, s, st, options, prefix, sites) {
			out = append(out, d.ID)
		}
	}
	slices.Sort(out)
	return out
}

// detects reports whether any of a profile's detectors matches.
func detects(ctx context.Context, dd DetectDef, s *core.Schema, st core.Site, options map[string]bool, prefix string, sites []string) bool {
	for _, t := range dd.TablesAny {
		for _, name := range Expand(t, prefix, sites) {
			if s == nil {
				continue
			}
			if !hasGlob(name) {
				if s.Table(name) != nil {
					return true
				}
				continue
			}
			for _, tb := range s.Tables {
				if ok, _ := path.Match(name, tb.Name); ok {
					return true
				}
			}
		}
	}
	for _, o := range dd.OptionsAny {
		if options[o] {
			return true
		}
	}
	if st != nil {
		for _, f := range dd.Files {
			if ok, err := st.Exists(ctx, f); err == nil && ok {
				return true
			}
		}
	}
	return false
}
